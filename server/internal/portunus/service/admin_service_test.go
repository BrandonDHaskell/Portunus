package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// errDB is a sentinel that stands in for a real database failure.
var errDB = errors.New("simulated disk I/O error")

// ── fake stores ───────────────────────────────────────────────────────────────

type fakeModuleAdminStore struct {
	modules   map[string]*store.ModuleRecord
	doors     map[string]*store.DoorRecord
	returnErr error // injected into every mutating call when non-nil
}

func newFakeModuleStore() *fakeModuleAdminStore {
	return &fakeModuleAdminStore{
		modules: make(map[string]*store.ModuleRecord),
		doors:   make(map[string]*store.DoorRecord),
	}
}

func (f *fakeModuleAdminStore) CommissionModule(_ context.Context, moduleID, doorID, displayName string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	f.modules[moduleID] = &store.ModuleRecord{
		ModuleID:    moduleID,
		DoorID:      doorID,
		DisplayName: displayName,
		Enabled:     true,
		CreatedAt:   time.Now().UTC(),
	}
	return nil
}

func (f *fakeModuleAdminStore) RevokeModule(_ context.Context, moduleID string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	if _, ok := f.modules[moduleID]; !ok {
		return store.ErrNotFound
	}
	f.modules[moduleID].Enabled = false
	return nil
}

func (f *fakeModuleAdminStore) DeleteModule(_ context.Context, moduleID string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	if _, ok := f.modules[moduleID]; !ok {
		return store.ErrNotFound
	}
	delete(f.modules, moduleID)
	return nil
}

func (f *fakeModuleAdminStore) GetModule(_ context.Context, moduleID string) (*store.ModuleRecord, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	rec, ok := f.modules[moduleID]
	if !ok {
		return nil, nil
	}
	return rec, nil
}

func (f *fakeModuleAdminStore) ListModules(_ context.Context) ([]store.ModuleRecord, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	recs := make([]store.ModuleRecord, 0, len(f.modules))
	for _, r := range f.modules {
		recs = append(recs, *r)
	}
	return recs, nil
}

func (f *fakeModuleAdminStore) RegisterDoor(_ context.Context, doorID, name, location string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	f.doors[doorID] = &store.DoorRecord{
		DoorID:    doorID,
		Name:      name,
		Location:  location,
		CreatedAt: time.Now().UTC(),
	}
	return nil
}

func (f *fakeModuleAdminStore) ListDoors(_ context.Context) ([]store.DoorRecord, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	recs := make([]store.DoorRecord, 0, len(f.doors))
	for _, r := range f.doors {
		recs = append(recs, *r)
	}
	return recs, nil
}

func (f *fakeModuleAdminStore) DeleteDoor(_ context.Context, doorID string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	if _, ok := f.doors[doorID]; !ok {
		return store.ErrNotFound
	}
	delete(f.doors, doorID)
	return nil
}

type fakeCredentialStore struct {
	credentials map[string]*store.CredentialRecord // key: string(credentialHash)
	returnErr   error
}

func newFakeCredentialStore() *fakeCredentialStore {
	return &fakeCredentialStore{credentials: make(map[string]*store.CredentialRecord)}
}

func (f *fakeCredentialStore) RegisterCredential(_ context.Context, credentialHash []byte, tag string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	key := string(credentialHash)
	if _, ok := f.credentials[key]; ok {
		return store.ErrCredentialAlreadyExists
	}
	f.credentials[key] = &store.CredentialRecord{CredentialHash: credentialHash, Tag: tag, Status: "active"}
	return nil
}

func (f *fakeCredentialStore) ListCredentials(_ context.Context) ([]store.CredentialRecord, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	recs := make([]store.CredentialRecord, 0, len(f.credentials))
	for _, r := range f.credentials {
		recs = append(recs, *r)
	}
	return recs, nil
}

func (f *fakeCredentialStore) SetCredentialStatus(_ context.Context, credentialHash []byte, status string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	key := string(credentialHash)
	rec, ok := f.credentials[key]
	if !ok {
		return store.ErrNotFound
	}
	rec.Status = status
	return nil
}

func (f *fakeCredentialStore) DeleteCredential(_ context.Context, credentialHash []byte) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	key := string(credentialHash)
	if _, ok := f.credentials[key]; !ok {
		return store.ErrNotFound
	}
	delete(f.credentials, key)
	return nil
}

func (f *fakeCredentialStore) IsCredentialAllowed(_ context.Context, credentialHash []byte) (bool, error) {
	if f.returnErr != nil {
		return false, f.returnErr
	}
	rec, ok := f.credentials[string(credentialHash)]
	if !ok {
		return false, nil
	}
	return rec.Status == "active", nil
}

// ── helper ────────────────────────────────────────────────────────────────────

func newTestAdminService(ms *fakeModuleAdminStore, cs *fakeCredentialStore) *service.AdminService {
	return service.NewAdminService(ms, cs, nil)
}

// 64-char hex = valid 32-byte credential hash for service-layer calls.
const validHashHex = "deadbeef00000000000000000000000000000000000000000000000000000000"

// ── B12: not-found vs DB-error propagation ────────────────────────────────────

func TestRevokeModule_NotFound_ReturnsErrModuleNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCredentialStore())
	err := svc.RevokeModule(context.Background(), "missing")
	if !errors.Is(err, service.ErrModuleNotFound) {
		t.Fatalf("expected ErrModuleNotFound, got: %v", err)
	}
}

func TestRevokeModule_DBError_Propagated(t *testing.T) {
	ms := newFakeModuleStore()
	ms.returnErr = errDB
	svc := newTestAdminService(ms, newFakeCredentialStore())
	err := svc.RevokeModule(context.Background(), "any")
	if errors.Is(err, service.ErrModuleNotFound) {
		t.Fatal("DB error must not be masked as ErrModuleNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestDeleteModule_NotFound_ReturnsErrModuleNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCredentialStore())
	err := svc.DeleteModule(context.Background(), "missing")
	if !errors.Is(err, service.ErrModuleNotFound) {
		t.Fatalf("expected ErrModuleNotFound, got: %v", err)
	}
}

func TestDeleteModule_DBError_Propagated(t *testing.T) {
	ms := newFakeModuleStore()
	ms.returnErr = errDB
	svc := newTestAdminService(ms, newFakeCredentialStore())
	err := svc.DeleteModule(context.Background(), "any")
	if errors.Is(err, service.ErrModuleNotFound) {
		t.Fatal("DB error must not be masked as ErrModuleNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestSetCredentialStatus_NotFound_ReturnsErrCredentialNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCredentialStore())
	err := svc.SetCredentialStatus(context.Background(), validHashHex, "disabled")
	if !errors.Is(err, service.ErrCredentialNotFound) {
		t.Fatalf("expected ErrCredentialNotFound, got: %v", err)
	}
}

func TestSetCredentialStatus_DBError_Propagated(t *testing.T) {
	cs := newFakeCredentialStore()
	cs.returnErr = errDB
	svc := newTestAdminService(newFakeModuleStore(), cs)
	err := svc.SetCredentialStatus(context.Background(), validHashHex, "disabled")
	if errors.Is(err, service.ErrCredentialNotFound) {
		t.Fatal("DB error must not be masked as ErrCredentialNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestDeleteCredential_NotFound_ReturnsErrCredentialNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCredentialStore())
	err := svc.DeleteCredential(context.Background(), validHashHex)
	if !errors.Is(err, service.ErrCredentialNotFound) {
		t.Fatalf("expected ErrCredentialNotFound, got: %v", err)
	}
}

func TestDeleteCredential_DBError_Propagated(t *testing.T) {
	cs := newFakeCredentialStore()
	cs.returnErr = errDB
	svc := newTestAdminService(newFakeModuleStore(), cs)
	err := svc.DeleteCredential(context.Background(), validHashHex)
	if errors.Is(err, service.ErrCredentialNotFound) {
		t.Fatal("DB error must not be masked as ErrCredentialNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestDeleteDoor_NotFound_ReturnsErrDoorNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCredentialStore())
	err := svc.DeleteDoor(context.Background(), "missing")
	if !errors.Is(err, service.ErrDoorNotFound) {
		t.Fatalf("expected ErrDoorNotFound, got: %v", err)
	}
}

func TestDeleteDoor_DBError_Propagated(t *testing.T) {
	ms := newFakeModuleStore()
	ms.returnErr = errDB
	svc := newTestAdminService(ms, newFakeCredentialStore())
	err := svc.DeleteDoor(context.Background(), "any")
	if errors.Is(err, service.ErrDoorNotFound) {
		t.Fatal("DB error must not be masked as ErrDoorNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

// ── register → mutate lifecycle ───────────────────────────────────────────────

func TestAdminService_RegisterRevokeDeleteModule(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCredentialStore())
	ctx := context.Background()

	info, err := svc.RegisterModule(ctx, types.RegisterModuleRequest{ModuleID: "door-001"})
	if err != nil {
		t.Fatalf("RegisterModule: %v", err)
	}
	if info.ModuleID != "door-001" {
		t.Errorf("expected module_id=door-001, got %q", info.ModuleID)
	}

	if err := svc.RevokeModule(ctx, "door-001"); err != nil {
		t.Fatalf("RevokeModule: %v", err)
	}
	if err := svc.DeleteModule(ctx, "door-001"); err != nil {
		t.Fatalf("DeleteModule: %v", err)
	}

	// Second delete must return not-found, not a panic.
	if err := svc.DeleteModule(ctx, "door-001"); !errors.Is(err, service.ErrModuleNotFound) {
		t.Errorf("expected ErrModuleNotFound on second delete, got: %v", err)
	}
}

func TestAdminService_RegisterUpdateDeleteCredential(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCredentialStore())
	ctx := context.Background()

	info, err := svc.RegisterCredential(ctx, types.RegisterCredentialRequest{CredentialID: "AABBCCDD", Tag: "test"})
	if err != nil {
		t.Fatalf("RegisterCredential: %v", err)
	}
	if info.Status != "active" {
		t.Errorf("expected status=active after register, got %q", info.Status)
	}

	if err := svc.SetCredentialStatus(ctx, info.CredentialHash, "disabled"); err != nil {
		t.Fatalf("SetCredentialStatus: %v", err)
	}
	if err := svc.DeleteCredential(ctx, info.CredentialHash); err != nil {
		t.Fatalf("DeleteCredential: %v", err)
	}

	// Second delete must return not-found.
	if err := svc.DeleteCredential(ctx, info.CredentialHash); !errors.Is(err, service.ErrCredentialNotFound) {
		t.Errorf("expected ErrCredentialNotFound on second delete, got: %v", err)
	}
}
