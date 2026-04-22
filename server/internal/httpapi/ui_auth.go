package httpapi

import (
	"errors"
	"net/http"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
)

var render = uiRenderer{}

// handleUILogin serves GET /admin/ui/login (form) and POST /admin/ui/login
// (submit).
func (s *Server) handleUILogin(w http.ResponseWriter, r *http.Request) {
	// Already logged in — go to dashboard.
	if sessionFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/admin/ui/", http.StatusSeeOther)
		return
	}

	d := &UIPageData{Form: map[string]string{}}

	if r.Method == http.MethodGet {
		// Surface a flash from redirect (e.g. "session expired").
		if f := r.URL.Query().Get("flash"); f != "" {
			d.Flash = f
			d.FlashType = r.URL.Query().Get("ft")
			if d.FlashType == "" {
				d.FlashType = "error"
			}
		}
		render.render(w, "login", d)
		return
	}

	// POST
	if err := r.ParseForm(); err != nil {
		d.Flash = "Invalid form submission."
		d.FlashType = "error"
		render.render(w, "login", d)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")
	d.Form["username"] = username

	sessionID, err := s.authService.Login(r.Context(), username, password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) || errors.Is(err, service.ErrAccountDisabled) {
			d.Flash = "Invalid username or password."
			d.FlashType = "error"
			render.render(w, "login", d)
			return
		}
		s.logger.Printf("ui login error: %v", err)
		d.Flash = "Login failed. Please try again."
		d.FlashType = "error"
		render.render(w, "login", d)
		return
	}

	setSessionCookie(w, sessionID, s.tlsEnabled)
	http.Redirect(w, r, "/admin/ui/", http.StatusSeeOther)
}

// handleUILogout handles POST /admin/ui/logout.
func (s *Server) handleUILogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil && cookie.Value != "" {
		_ = s.authService.Logout(r.Context(), cookie.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/admin/ui/login?flash=Signed+out&ft=success", http.StatusSeeOther)
}

// handleUIChangePassword serves GET /admin/ui/change-password and POST.
func (s *Server) handleUIChangePassword(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/admin/ui/login", http.StatusSeeOther)
		return
	}

	d := newUIPageData(r, "")
	d.MustChange = sess.MustChangePW

	if r.Method == http.MethodGet {
		render.render(w, "change_password", d)
		return
	}

	// POST
	if err := r.ParseForm(); err != nil {
		d.Flash = "Invalid form submission."
		d.FlashType = "error"
		render.render(w, "change_password", d)
		return
	}

	current := r.FormValue("current_password")
	newPW := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	if newPW != confirm {
		d.Flash = "New passwords do not match."
		d.FlashType = "error"
		render.render(w, "change_password", d)
		return
	}

	if err := s.authService.ChangePassword(r.Context(), sess.AdminUUID, current, newPW); err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			d.Flash = "Current password is incorrect."
			d.FlashType = "error"
			render.render(w, "change_password", d)
			return
		}
		if errors.Is(err, service.ErrPasswordChangeFailed) {
			d.Flash = err.Error()
			d.FlashType = "error"
			render.render(w, "change_password", d)
			return
		}
		s.logger.Printf("ui change-password error: %v", err)
		d.Flash = "Password change failed. Please try again."
		d.FlashType = "error"
		render.render(w, "change_password", d)
		return
	}

	http.Redirect(w, r, "/admin/ui/?flash=Password+changed+successfully&ft=success", http.StatusSeeOther)
}

// handleUIDashboard serves GET /admin/ui/.
func (s *Server) handleUIDashboard(w http.ResponseWriter, r *http.Request) {
	// Redirect sub-paths to the right place (e.g. /admin/ui → /admin/ui/).
	if r.URL.Path != "/admin/ui/" {
		http.Redirect(w, r, "/admin/ui/", http.StatusMovedPermanently)
		return
	}

	sess := sessionFromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/admin/ui/login", http.StatusSeeOther)
		return
	}
	if sess.MustChangePW {
		http.Redirect(w, r, "/admin/ui/change-password", http.StatusSeeOther)
		return
	}

	d := newUIPageData(r, "dashboard")

	// Best-effort counts for the dashboard tiles.
	if s.adminUserService != nil {
		if users, err := s.adminUserService.ListUsers(r.Context()); err == nil {
			d.UserCount = len(users)
		}
	}
	if s.roleService != nil {
		if roles, err := s.roleService.ListRoles(r.Context()); err == nil {
			d.RoleCount = len(roles)
		}
	}

	render.render(w, "dashboard", d)
}
