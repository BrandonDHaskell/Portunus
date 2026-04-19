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

type fakeCardStore struct {
	cards     map[string]*store.CardRecord // key: string(cardIDHash)
	returnErr error
}

func newFakeCardStore() *fakeCardStore {
	return &fakeCardStore{cards: make(map[string]*store.CardRecord)}
}

func (f *fakeCardStore) RegisterCard(_ context.Context, cardIDHash []byte, tag string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	key := string(cardIDHash)
	if _, ok := f.cards[key]; ok {
		return store.ErrCardAlreadyExists
	}
	f.cards[key] = &store.CardRecord{CardIDHash: cardIDHash, Tag: tag, Status: "active"}
	return nil
}

func (f *fakeCardStore) ListCards(_ context.Context) ([]store.CardRecord, error) {
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	recs := make([]store.CardRecord, 0, len(f.cards))
	for _, r := range f.cards {
		recs = append(recs, *r)
	}
	return recs, nil
}

func (f *fakeCardStore) SetCardStatus(_ context.Context, cardIDHash []byte, status string) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	key := string(cardIDHash)
	rec, ok := f.cards[key]
	if !ok {
		return store.ErrNotFound
	}
	rec.Status = status
	return nil
}

func (f *fakeCardStore) DeleteCard(_ context.Context, cardIDHash []byte) error {
	if f.returnErr != nil {
		return f.returnErr
	}
	key := string(cardIDHash)
	if _, ok := f.cards[key]; !ok {
		return store.ErrNotFound
	}
	delete(f.cards, key)
	return nil
}

func (f *fakeCardStore) IsCardAllowed(_ context.Context, cardIDHash []byte) (bool, error) {
	if f.returnErr != nil {
		return false, f.returnErr
	}
	rec, ok := f.cards[string(cardIDHash)]
	if !ok {
		return false, nil
	}
	return rec.Status == "active", nil
}

// ── helper ────────────────────────────────────────────────────────────────────

func newTestAdminService(ms *fakeModuleAdminStore, cs *fakeCardStore) *service.AdminService {
	return service.NewAdminService(ms, cs, nil)
}

// 64-char hex = valid 32-byte card hash for service-layer calls.
const validHashHex = "deadbeef00000000000000000000000000000000000000000000000000000000"

// ── B12: not-found vs DB-error propagation ────────────────────────────────────

func TestRevokeModule_NotFound_ReturnsErrModuleNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCardStore())
	err := svc.RevokeModule(context.Background(), "missing")
	if !errors.Is(err, service.ErrModuleNotFound) {
		t.Fatalf("expected ErrModuleNotFound, got: %v", err)
	}
}

func TestRevokeModule_DBError_Propagated(t *testing.T) {
	ms := newFakeModuleStore()
	ms.returnErr = errDB
	svc := newTestAdminService(ms, newFakeCardStore())
	err := svc.RevokeModule(context.Background(), "any")
	if errors.Is(err, service.ErrModuleNotFound) {
		t.Fatal("DB error must not be masked as ErrModuleNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestDeleteModule_NotFound_ReturnsErrModuleNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCardStore())
	err := svc.DeleteModule(context.Background(), "missing")
	if !errors.Is(err, service.ErrModuleNotFound) {
		t.Fatalf("expected ErrModuleNotFound, got: %v", err)
	}
}

func TestDeleteModule_DBError_Propagated(t *testing.T) {
	ms := newFakeModuleStore()
	ms.returnErr = errDB
	svc := newTestAdminService(ms, newFakeCardStore())
	err := svc.DeleteModule(context.Background(), "any")
	if errors.Is(err, service.ErrModuleNotFound) {
		t.Fatal("DB error must not be masked as ErrModuleNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestSetCardStatus_NotFound_ReturnsErrCardNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCardStore())
	err := svc.SetCardStatus(context.Background(), validHashHex, "disabled")
	if !errors.Is(err, service.ErrCardNotFound) {
		t.Fatalf("expected ErrCardNotFound, got: %v", err)
	}
}

func TestSetCardStatus_DBError_Propagated(t *testing.T) {
	cs := newFakeCardStore()
	cs.returnErr = errDB
	svc := newTestAdminService(newFakeModuleStore(), cs)
	err := svc.SetCardStatus(context.Background(), validHashHex, "disabled")
	if errors.Is(err, service.ErrCardNotFound) {
		t.Fatal("DB error must not be masked as ErrCardNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestDeleteCard_NotFound_ReturnsErrCardNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCardStore())
	err := svc.DeleteCard(context.Background(), validHashHex)
	if !errors.Is(err, service.ErrCardNotFound) {
		t.Fatalf("expected ErrCardNotFound, got: %v", err)
	}
}

func TestDeleteCard_DBError_Propagated(t *testing.T) {
	cs := newFakeCardStore()
	cs.returnErr = errDB
	svc := newTestAdminService(newFakeModuleStore(), cs)
	err := svc.DeleteCard(context.Background(), validHashHex)
	if errors.Is(err, service.ErrCardNotFound) {
		t.Fatal("DB error must not be masked as ErrCardNotFound")
	}
	if !errors.Is(err, errDB) {
		t.Fatalf("expected wrapped errDB, got: %v", err)
	}
}

func TestDeleteDoor_NotFound_ReturnsErrDoorNotFound(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCardStore())
	err := svc.DeleteDoor(context.Background(), "missing")
	if !errors.Is(err, service.ErrDoorNotFound) {
		t.Fatalf("expected ErrDoorNotFound, got: %v", err)
	}
}

func TestDeleteDoor_DBError_Propagated(t *testing.T) {
	ms := newFakeModuleStore()
	ms.returnErr = errDB
	svc := newTestAdminService(ms, newFakeCardStore())
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
	svc := newTestAdminService(newFakeModuleStore(), newFakeCardStore())
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

func TestAdminService_RegisterUpdateDeleteCard(t *testing.T) {
	svc := newTestAdminService(newFakeModuleStore(), newFakeCardStore())
	ctx := context.Background()

	info, err := svc.RegisterCard(ctx, types.RegisterCardRequest{CardID: "AABBCCDD", Tag: "test"})
	if err != nil {
		t.Fatalf("RegisterCard: %v", err)
	}
	if info.Status != "active" {
		t.Errorf("expected status=active after register, got %q", info.Status)
	}

	if err := svc.SetCardStatus(ctx, info.CardIDHash, "disabled"); err != nil {
		t.Fatalf("SetCardStatus: %v", err)
	}
	if err := svc.DeleteCard(ctx, info.CardIDHash); err != nil {
		t.Fatalf("DeleteCard: %v", err)
	}

	// Second delete must return not-found.
	if err := svc.DeleteCard(ctx, info.CardIDHash); !errors.Is(err, service.ErrCardNotFound) {
		t.Errorf("expected ErrCardNotFound on second delete, got: %v", err)
	}
}
