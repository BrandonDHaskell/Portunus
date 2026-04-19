package httpapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/httpapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

const adminTestKey = "test-admin-api-key"

// newAdminTestServer wires up a full HTTP server with an admin service backed
// by an in-memory SQLite database.
func newAdminTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	conn := openAdminDB(t)
	writer := db.NewWorker(conn)
	t.Cleanup(func() { writer.Close() })

	adminSvc := service.NewAdminService(
		sqlitestore.NewModuleAdminStore(conn, writer),
		sqlitestore.NewCardStore(conn, writer),
		nil, // no card-hash secret
	)

	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:       log.New(io.Discard, "", 0),
		Addr:         ":0",
		AdminService: adminSvc,
		AdminAPIKey:  adminTestKey,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func openAdminDB(t *testing.T) *sql.DB {
	t.Helper()
	// Sanitise the test name so it is safe as a URI parameter.
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	dsn := fmt.Sprintf(
		"file:admin_%s?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		safe,
	)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("openAdminDB: sql.Open: %v", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := db.Migrate(context.Background(), conn); err != nil {
		conn.Close()
		t.Fatalf("openAdminDB: migrate: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// adminReq builds a request with the admin Bearer token and JSON body.
func adminReq(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("adminReq marshal: %v", err)
		}
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("adminReq: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminTestKey)
	return req
}

func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

// ── auth ─────────────────────────────────────────────────────────────────────

func TestAdminAuth_MissingToken_401(t *testing.T) {
	ts := newAdminTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/modules", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAdminAuth_WrongToken_403(t *testing.T) {
	ts := newAdminTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/modules", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestAdminAuth_ValidToken_200(t *testing.T) {
	ts := newAdminTestServer(t)
	req := adminReq(t, http.MethodGet, ts.URL+"/admin/v1/modules", nil)
	resp := do(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ── module lifecycle ──────────────────────────────────────────────────────────

func TestAdminModules_RegisterRevokeDelete(t *testing.T) {
	ts := newAdminTestServer(t)
	base := ts.URL

	// Register
	resp := do(t, adminReq(t, http.MethodPost, base+"/admin/v1/modules",
		map[string]string{"module_id": "door-001"}))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d", resp.StatusCode)
	}

	// Revoke
	resp = do(t, adminReq(t, http.MethodPost, base+"/admin/v1/modules/door-001/revoke", nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d", resp.StatusCode)
	}

	// Delete
	resp = do(t, adminReq(t, http.MethodDelete, base+"/admin/v1/modules/door-001", nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", resp.StatusCode)
	}

	// Get after delete → 404
	resp = do(t, adminReq(t, http.MethodGet, base+"/admin/v1/modules/door-001", nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestAdminModules_DeleteNotFound_404(t *testing.T) {
	ts := newAdminTestServer(t)
	resp := do(t, adminReq(t, http.MethodDelete, ts.URL+"/admin/v1/modules/nonexistent", nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAdminModules_RevokeNotFound_404(t *testing.T) {
	ts := newAdminTestServer(t)
	resp := do(t, adminReq(t, http.MethodPost, ts.URL+"/admin/v1/modules/nonexistent/revoke", nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// ── card lifecycle ────────────────────────────────────────────────────────────

func TestAdminCards_RegisterUpdateDelete(t *testing.T) {
	ts := newAdminTestServer(t)
	base := ts.URL

	// Register
	resp := do(t, adminReq(t, http.MethodPost, base+"/admin/v1/cards",
		map[string]string{"card_id": "AABBCCDD", "tag": "test-card"}))
	var regBody map[string]any
	json.NewDecoder(resp.Body).Decode(&regBody)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register card: expected 201, got %d", resp.StatusCode)
	}

	cardInfo, _ := regBody["card"].(map[string]any)
	cardHash, _ := cardInfo["card_id_hash"].(string)
	if cardHash == "" {
		t.Fatal("expected non-empty card_id_hash in response")
	}

	// Update status
	resp = do(t, adminReq(t, http.MethodPatch, base+"/admin/v1/cards/"+cardHash,
		map[string]string{"status": "disabled"}))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status: expected 200, got %d", resp.StatusCode)
	}

	// Delete
	resp = do(t, adminReq(t, http.MethodDelete, base+"/admin/v1/cards/"+cardHash, nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete card: expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminCards_DuplicateRegister_409(t *testing.T) {
	ts := newAdminTestServer(t)
	body := map[string]string{"card_id": "AABBCCDD"}

	resp := do(t, adminReq(t, http.MethodPost, ts.URL+"/admin/v1/cards", body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first register: expected 201, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodPost, ts.URL+"/admin/v1/cards", body))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate: expected 409, got %d", resp.StatusCode)
	}
}

func TestAdminCards_DeleteNotFound_404(t *testing.T) {
	ts := newAdminTestServer(t)
	// Valid 64-char hex that doesn't exist in the DB.
	hashHex := "deadbeef00000000000000000000000000000000000000000000000000000000"
	resp := do(t, adminReq(t, http.MethodDelete, ts.URL+"/admin/v1/cards/"+hashHex, nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// ── door lifecycle ────────────────────────────────────────────────────────────

func TestAdminDoors_RegisterDelete(t *testing.T) {
	ts := newAdminTestServer(t)
	base := ts.URL

	resp := do(t, adminReq(t, http.MethodPost, base+"/admin/v1/doors",
		map[string]string{"door_id": "d-1", "name": "Front Door"}))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register door: expected 201, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodDelete, base+"/admin/v1/doors/d-1", nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete door: expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminDoors_DeleteNotFound_404(t *testing.T) {
	ts := newAdminTestServer(t)
	resp := do(t, adminReq(t, http.MethodDelete, ts.URL+"/admin/v1/doors/nonexistent", nil))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
