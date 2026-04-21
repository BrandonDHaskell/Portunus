package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

// handleAdminLogin handles POST /admin/v1/login.
// Body: {"username": "...", "password": "..."}
func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "username and password are required")
		return
	}

	sessionID, err := s.authService.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
			return
		}
		s.logger.Printf("login error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "login failed")
		return
	}

	setSessionCookie(w, sessionID, s.tlsEnabled)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminLogout handles POST /admin/v1/logout.
// Requires a valid session (enforced by requireSession in the route).
func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	if err := s.authService.Logout(r.Context(), cookie.Value); err != nil {
		s.logger.Printf("logout error: %v", err)
	}

	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAdminChangePassword handles POST /admin/v1/change-password.
// Requires a valid session. Does NOT require a specific permission — any
// authenticated admin can change their own password.
// Body: {"current_password": "...", "new_password": "..."}
func (s *Server) handleAdminChangePassword(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())
	if sess == nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "valid session required")
		return
	}

	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}
	if body.CurrentPassword == "" || body.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "current_password and new_password are required")
		return
	}

	err := s.authService.ChangePassword(r.Context(), sess.AdminUUID, body.CurrentPassword, body.NewPassword)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "current password is incorrect")
			return
		}
		if errors.Is(err, service.ErrPasswordChangeFailed) {
			writeError(w, http.StatusBadRequest, "password_change_failed", err.Error())
			return
		}
		s.logger.Printf("change-password error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "password change failed")
		return
	}

	s.logger.Printf("admin: password changed for user %q", sess.Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
