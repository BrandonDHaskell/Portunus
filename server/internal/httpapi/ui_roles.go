package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

// handleUIRolesList serves GET /admin/ui/roles.
func (s *Server) handleUIRolesList(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "roles")

	roles, err := s.roleService.ListRoles(r.Context())
	if err != nil {
		s.logger.Printf("ui list roles: %v", err)
		d.Flash = "Failed to load roles."
		d.FlashType = "error"
	} else {
		d.Roles = roles
	}

	render.render(w, "roles_list", d)
}

// handleUIRolesNew serves GET /admin/ui/roles/new.
func (s *Server) handleUIRolesNew(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "roles")
	d.Form = map[string]string{}
	render.render(w, "roles_create", d)
}

// handleUIRolesCreate handles POST /admin/ui/roles.
func (s *Server) handleUIRolesCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/roles/new", "Invalid form submission.", "error")
		return
	}

	name := r.FormValue("name")
	description := r.FormValue("description")
	expiryDays := parseOptionalInt(r.FormValue("default_expiry_days"))
	inactivityDays := parseOptionalInt(r.FormValue("default_inactivity_days"))

	role, err := s.roleService.CreateRole(r.Context(), name, description, expiryDays, inactivityDays)
	if err != nil {
		s.logger.Printf("ui create role: %v", err)
		d := newUIPageData(r, "roles")
		d.Form = map[string]string{
			"Name":        name,
			"Description": description,
		}
		d.Flash = err.Error()
		d.FlashType = "error"
		render.render(w, "roles_create", d)
		return
	}

	s.logger.Printf("admin ui: created role %q", role.RoleID)
	// Redirect to edit page to assign permissions immediately.
	flashRedirect(w, r, "/admin/ui/roles/"+role.RoleID, "Role created. Assign permissions below.", "success")
}

// handleUIRolesEdit serves GET /admin/ui/roles/{role_id}.
func (s *Server) handleUIRolesEdit(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("role_id")
	d := newUIPageData(r, "roles")

	role, err := s.roleService.GetRole(r.Context(), roleID)
	if err != nil {
		if errors.Is(err, service.ErrRoleNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui get role: %v", err)
		flashRedirect(w, r, "/admin/ui/roles", "Failed to load role.", "error")
		return
	}
	d.Role = role
	d.PermGroups = permGroups()

	render.render(w, "roles_edit", d)
}

// handleUIRolesUpdate handles POST /admin/ui/roles/{role_id} (metadata update).
func (s *Server) handleUIRolesUpdate(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("role_id")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/roles/"+roleID, "Invalid form submission.", "error")
		return
	}

	name := r.FormValue("name")
	description := r.FormValue("description")
	expiryDays := parseOptionalInt(r.FormValue("default_expiry_days"))
	inactivityDays := parseOptionalInt(r.FormValue("default_inactivity_days"))

	if err := s.roleService.UpdateRole(r.Context(), roleID, name, description, expiryDays, inactivityDays); err != nil {
		if errors.Is(err, service.ErrRoleNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, store.ErrRoleIsSystem) {
			flashRedirect(w, r, "/admin/ui/roles/"+roleID, "System roles cannot be modified.", "error")
			return
		}
		s.logger.Printf("ui update role: %v", err)
		flashRedirect(w, r, "/admin/ui/roles/"+roleID, err.Error(), "error")
		return
	}

	s.logger.Printf("admin ui: updated role %q", roleID)
	flashRedirect(w, r, "/admin/ui/roles/"+roleID, "Role updated.", "success")
}

// handleUIRolesSetPermissions handles POST /admin/ui/roles/{role_id}/permissions.
func (s *Server) handleUIRolesSetPermissions(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("role_id")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/roles/"+roleID, "Invalid form submission.", "error")
		return
	}

	// All checked permission checkboxes come in as repeated "permission" values.
	perms := r.Form["permission"]
	if perms == nil {
		perms = []string{}
	}

	// Validate: only accept known permission strings to prevent injection.
	allPerms := make(map[string]bool, len(permissions.All()))
	for _, p := range permissions.All() {
		allPerms[p] = true
	}
	valid := make([]string, 0, len(perms))
	for _, p := range perms {
		if allPerms[p] {
			valid = append(valid, p)
		}
	}

	if err := s.roleService.SetPermissions(r.Context(), roleID, valid); err != nil {
		s.logger.Printf("ui set permissions: %v", err)
		flashRedirect(w, r, "/admin/ui/roles/"+roleID, "Failed to save permissions.", "error")
		return
	}

	s.logger.Printf("admin ui: set %d permissions for role %q", len(valid), roleID)
	flashRedirect(w, r, "/admin/ui/roles/"+roleID, "Permissions saved.", "success")
}

// handleUIRolesDelete handles POST /admin/ui/roles/{role_id}/delete.
func (s *Server) handleUIRolesDelete(w http.ResponseWriter, r *http.Request) {
	roleID := r.PathValue("role_id")

	if err := s.roleService.DeleteRole(r.Context(), roleID); err != nil {
		if errors.Is(err, service.ErrRoleNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, store.ErrRoleIsSystem) {
			flashRedirect(w, r, "/admin/ui/roles", "System roles cannot be deleted.", "error")
			return
		}
		s.logger.Printf("ui delete role: %v", err)
		flashRedirect(w, r, "/admin/ui/roles", "Failed to delete role.", "error")
		return
	}

	s.logger.Printf("admin ui: deleted role %q", roleID)
	flashRedirect(w, r, "/admin/ui/roles", "Role "+roleID+" deleted.", "success")
}

// uiRoleRoutes registers all /admin/ui/roles/* routes.
func (s *Server) uiRoleRoutes(mux *http.ServeMux) {
	perm := requireUIPermission
	mux.HandleFunc("GET /admin/ui/roles",
		perm(permissions.RoleList, s.handleUIRolesList))
	mux.HandleFunc("GET /admin/ui/roles/new",
		perm(permissions.RoleCreate, s.handleUIRolesNew))
	mux.HandleFunc("POST /admin/ui/roles",
		perm(permissions.RoleCreate, s.handleUIRolesCreate))
	mux.HandleFunc("GET /admin/ui/roles/{role_id}",
		perm(permissions.RoleList, s.handleUIRolesEdit))
	mux.HandleFunc("POST /admin/ui/roles/{role_id}",
		perm(permissions.RoleEdit, s.handleUIRolesUpdate))
	mux.HandleFunc("POST /admin/ui/roles/{role_id}/permissions",
		perm(permissions.RoleAssignPermission, s.handleUIRolesSetPermissions))
	mux.HandleFunc("POST /admin/ui/roles/{role_id}/delete",
		perm(permissions.RoleDelete, s.handleUIRolesDelete))
}

// parseOptionalInt converts a form value to *int, returning nil on empty/invalid.
func parseOptionalInt(s string) *int {
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}
