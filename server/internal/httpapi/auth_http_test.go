package httpapi_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (h *harness) restLogin(username, password string) *http.Response {
	h.t.Helper()
	resp := do(h.t, adminReq(h.t, http.MethodPost, h.TS.URL+"/admin/v1/login",
		map[string]string{"username": username, "password": password}, nil))
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func sessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == "portunus_session" {
			return c
		}
	}
	return nil
}

// ── login rate limiting ──────────────────────────────────────────────────────

func TestLoginLimiter_REST_LocksAfterFiveFailures(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for i := 0; i < 5; i++ {
		if resp := h.restLogin("admin", "wrong-password-x"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failure %d: status %d, want 401", i+1, resp.StatusCode)
		}
	}
	// Locked: even the correct password is refused until the window passes.
	resp := h.restLogin("admin", testAdminPassword)
	if resp.StatusCode != http.StatusTooManyRequests || errCode(t, resp) != "too_many_attempts" {
		t.Fatalf("6th attempt: status %d, want 429 too_many_attempts", resp.StatusCode)
	}
	if sessionCookie(resp) != nil {
		t.Fatal("a session cookie was issued while locked out")
	}
}

func TestLoginLimiter_REST_SuccessResetsCount(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	for i := 0; i < 4; i++ {
		h.restLogin("admin", "wrong-password-x")
	}
	if resp := h.restLogin("admin", testAdminPassword); resp.StatusCode != http.StatusOK {
		t.Fatalf("correct password: status %d", resp.StatusCode)
	}
	for i := 0; i < 4; i++ {
		if resp := h.restLogin("admin", "wrong-password-x"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("post-reset failure %d: status %d, want 401 (count should have reset)", i+1, resp.StatusCode)
		}
	}
}

func TestLoginLimiter_UI_ShowsLockout(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	form := url.Values{"username": {"admin"}, "password": {"wrong-password-x"}}
	for i := 0; i < 5; i++ {
		resp := h.postForm("/admin/ui/login", form, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), "Invalid username or password") {
			t.Fatalf("failure %d: no invalid-credentials message", i+1)
		}
	}
	resp := h.postForm("/admin/ui/login", url.Values{"username": {"admin"}, "password": {testAdminPassword}}, nil)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Too many failed login attempts") || sessionCookie(resp) != nil {
		t.Fatalf("6th UI attempt: status %d, no lockout message or a cookie was issued", resp.StatusCode)
	}
}

// ── session cookie + HSTS ────────────────────────────────────────────────────

func TestSessionCookie_Flags(t *testing.T) {
	for _, tls := range []bool{false, true} {
		name := "plain"
		if tls {
			name = "tls"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{TLSEnabled: tls})
			resp := h.restLogin("admin", testAdminPassword)
			c := sessionCookie(resp)
			if c == nil {
				t.Fatal("no session cookie")
			}
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/admin/" || c.Secure != tls {
				t.Errorf("cookie HttpOnly=%v SameSite=%v Path=%q Secure=%v", c.HttpOnly, c.SameSite, c.Path, c.Secure)
			}
			if got := resp.Header.Get("Strict-Transport-Security"); (got != "") != tls {
				t.Errorf("Strict-Transport-Security = %q", got)
			}
		})
	}
}

func TestLogout_ClearsCookieAndSession(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	resp := do(t, adminReq(t, http.MethodPost, h.TS.URL+"/admin/v1/logout", nil, h.Admin))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	if c := sessionCookie(resp); c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("logout did not expire the cookie: %+v", c)
	}
	resp = do(t, adminReq(t, http.MethodGet, h.TS.URL+"/admin/v1/health", nil, h.Admin))
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session still works after logout: %d", resp.StatusCode)
	}
}

func TestChangePassword_REST(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	post := func(body map[string]string, c *http.Cookie) *http.Response {
		resp := do(t, adminReq(t, http.MethodPost, h.TS.URL+"/admin/v1/change-password", body, c))
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	if resp := post(map[string]string{"current_password": "wrong-password-x", "new_password": "a-new-valid-password"}, h.Admin); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong current: %d, want 401", resp.StatusCode)
	}
	if resp := post(map[string]string{"current_password": testAdminPassword, "new_password": "short"}, h.Admin); resp.StatusCode != http.StatusBadRequest || errCode(t, resp) != "password_change_failed" {
		t.Errorf("short new password: %d, want 400 password_change_failed", resp.StatusCode)
	}
	if resp := post(map[string]string{"current_password": testAdminPassword}, h.Admin); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing field: %d, want 400", resp.StatusCode)
	}

	resp := post(map[string]string{"current_password": testAdminPassword, "new_password": "a-new-valid-password"}, h.Admin)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change: %d", resp.StatusCode)
	}
	if c := sessionCookie(resp); c == nil || c.MaxAge >= 0 {
		t.Errorf("change-password did not clear the cookie: %+v", c)
	}
	health := do(t, adminReq(t, http.MethodGet, h.TS.URL+"/admin/v1/health", nil, h.Admin))
	health.Body.Close()
	if health.StatusCode != http.StatusUnauthorized {
		t.Errorf("session survived a password change: %d", health.StatusCode)
	}
	if resp := h.restLogin("admin", "a-new-valid-password"); resp.StatusCode != http.StatusOK {
		t.Errorf("new password login: %d", resp.StatusCode)
	}
}

// A must-change-password session can still reach change-password itself.
func TestChangePassword_AllowedWhileMustChange(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	c := h.sessionWith(nil, true)
	resp := do(t, adminReq(t, http.MethodPost, h.TS.URL+"/admin/v1/change-password",
		map[string]string{"current_password": testAdminPassword, "new_password": "a-new-valid-password"}, c))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("must-change session blocked from changing password: %d", resp.StatusCode)
	}
}
