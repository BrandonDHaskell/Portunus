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

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"

	"github.com/BrandonDHaskell/Portunus/server/internal/db"
	"github.com/BrandonDHaskell/Portunus/server/internal/httpapi"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

const testAdminPassword = "test-password-1234"

// newAdminTestServer wires up a full HTTP server with admin + auth services
// backed by an in-memory SQLite database. Returns the test server and a
// valid session cookie for the bootstrapped admin account.
func newAdminTestServer(t *testing.T) (*httptest.Server, *http.Cookie) {
	t.Helper()

	conn := openAdminDB(t)
	writer := db.NewWorker(conn)
	t.Cleanup(func() { writer.Close() })

	silentLogger := log.New(io.Discard, "", 0)

	adminUserStore := sqlitestore.NewAdminUserStore(conn, writer)
	sessionStore := sqlitestore.NewSessionStore(conn, writer)
	roleStore := sqlitestore.NewRoleStore(conn, writer)
	credentialStore := sqlitestore.NewCredentialStore(conn, writer)
	moduleAdminStore := sqlitestore.NewModuleAdminStore(conn, writer)

	authSvc := service.NewAuthService(adminUserStore, sessionStore, roleStore, silentLogger)

	if err := authSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Bootstrap sets a random password. Overwrite it with a known test value
	// so we can log in deterministically.
	user, err := adminUserStore.GetAdminUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("get bootstrap user: %v", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(testAdminPassword), 4) // cost 4 = fast for tests
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	if err := adminUserStore.UpdatePasswordHash(context.Background(), user.UUID, string(hash)); err != nil {
		t.Fatalf("reset password: %v", err)
	}

	sessionID, err := authSvc.Login(context.Background(), "admin", testAdminPassword)
	if err != nil {
		t.Fatalf("test login: %v", err)
	}

	adminSvc := service.NewAdminService(moduleAdminStore, credentialStore, nil)

	srv := httpapi.NewServer(httpapi.Dependencies{
		Logger:       silentLogger,
		Addr:         ":0",
		AdminService: adminSvc,
		AuthService:  authSvc,
	})

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	cookie := &http.Cookie{Name: "portunus_session", Value: sessionID}
	return ts, cookie
}

func openAdminDB(t *testing.T) *sql.DB {
	t.Helper()
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

// adminReq builds a request with a session cookie and JSON body.
func adminReq(t *testing.T, method, url string, body any, cookie *http.Cookie) *http.Request {
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
	if cookie != nil {
		req.AddCookie(cookie)
	}
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

func TestAdminAuth_NoSession_401(t *testing.T) {
	ts, _ := newAdminTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/modules", nil)
	resp := do(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAdminAuth_InvalidSession_401(t *testing.T) {
	ts, _ := newAdminTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/admin/v1/modules", nil)
	req.AddCookie(&http.Cookie{Name: "portunus_session", Value: "not-a-real-session-id"})
	resp := do(t, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAdminAuth_ValidSession_200(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	resp := do(t, adminReq(t, http.MethodGet, ts.URL+"/admin/v1/modules", nil, cookie))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ── module lifecycle ──────────────────────────────────────────────────────────

func TestAdminModules_RegisterRevokeDelete(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	base := ts.URL

	resp := do(t, adminReq(t, http.MethodPost, base+"/admin/v1/modules",
		map[string]string{"module_id": "door-001"}, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodPost, base+"/admin/v1/modules/door-001/revoke", nil, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodDelete, base+"/admin/v1/modules/door-001", nil, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: expected 200, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodGet, base+"/admin/v1/modules/door-001", nil, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", resp.StatusCode)
	}
}

func TestAdminModules_DeleteNotFound_404(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	resp := do(t, adminReq(t, http.MethodDelete, ts.URL+"/admin/v1/modules/nonexistent", nil, cookie))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAdminModules_RevokeNotFound_404(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	resp := do(t, adminReq(t, http.MethodPost, ts.URL+"/admin/v1/modules/nonexistent/revoke", nil, cookie))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// ── credential lifecycle ──────────────────────────────────────────────────────

func TestAdminCredentials_RegisterUpdateDelete(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	base := ts.URL

	resp := do(t, adminReq(t, http.MethodPost, base+"/admin/v1/credentials",
		map[string]string{"credential_id": "AABBCCDD", "tag": "test-credential"}, cookie))
	var regBody map[string]any
	json.NewDecoder(resp.Body).Decode(&regBody)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register credential: expected 201, got %d", resp.StatusCode)
	}

	credInfo, _ := regBody["credential"].(map[string]any)
	credHash, _ := credInfo["credential_hash"].(string)
	if credHash == "" {
		t.Fatal("expected non-empty credential_hash in response")
	}

	resp = do(t, adminReq(t, http.MethodPatch, base+"/admin/v1/credentials/"+credHash,
		map[string]string{"status": "disabled"}, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status: expected 200, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodDelete, base+"/admin/v1/credentials/"+credHash, nil, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete credential: expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminCredentials_DuplicateRegister_409(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	body := map[string]string{"credential_id": "AABBCCDD"}

	resp := do(t, adminReq(t, http.MethodPost, ts.URL+"/admin/v1/credentials", body, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first register: expected 201, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodPost, ts.URL+"/admin/v1/credentials", body, cookie))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate: expected 409, got %d", resp.StatusCode)
	}
}

func TestAdminCredentials_DeleteNotFound_404(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	hashHex := "deadbeef00000000000000000000000000000000000000000000000000000000"
	resp := do(t, adminReq(t, http.MethodDelete, ts.URL+"/admin/v1/credentials/"+hashHex, nil, cookie))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// ── door lifecycle ────────────────────────────────────────────────────────────

func TestAdminDoors_RegisterDelete(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	base := ts.URL

	resp := do(t, adminReq(t, http.MethodPost, base+"/admin/v1/doors",
		map[string]string{"door_id": "d-1", "name": "Front Door"}, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register door: expected 201, got %d", resp.StatusCode)
	}

	resp = do(t, adminReq(t, http.MethodDelete, base+"/admin/v1/doors/d-1", nil, cookie))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete door: expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminDoors_DeleteNotFound_404(t *testing.T) {
	ts, cookie := newAdminTestServer(t)
	resp := do(t, adminReq(t, http.MethodDelete, ts.URL+"/admin/v1/doors/nonexistent", nil, cookie))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
