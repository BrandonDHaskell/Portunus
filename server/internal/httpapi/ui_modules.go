package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ── Module list ───────────────────────────────────────────────────────────────

func (s *Server) handleUIModulesList(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "modules")

	mods, err := s.adminService.ListModules(r.Context())
	if err != nil {
		s.logger.Printf("ui list modules: %v", err)
		d.Flash = "Failed to load modules."
		d.FlashType = "error"
	} else {
		d.Modules = mods
	}

	render.render(w, "modules_list", d)
}

// ── Module detail ─────────────────────────────────────────────────────────────

func (s *Server) handleUIModulesDetail(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	d := newUIPageData(r, "modules")

	mod, err := s.adminService.GetModule(r.Context(), moduleID)
	if err != nil {
		if errors.Is(err, service.ErrModuleNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui get module %s: %v", moduleID, err)
		flashRedirect(w, r, "/admin/ui/modules", "Failed to load module.", "error")
		return
	}
	d.Module = mod

	// Authorizations for this module.
	if s.moduleAuthService != nil {
		authRecs, err := s.moduleAuthService.ListByModule(r.Context(), moduleID)
		if err != nil {
			s.logger.Printf("ui list authorizations for module %s: %v", moduleID, err)
		} else {
			d.Authorizations = make([]types.ModuleAuthorizationInfo, len(authRecs))
			for i := range authRecs {
				d.Authorizations[i] = moduleAuthRecordToInfo(&authRecs[i])
			}
		}
	}

	// All members — used by the grant-authorization form.
	if s.memberAccessService != nil {
		members, err := s.memberAccessService.ListMembers(r.Context())
		if err != nil {
			s.logger.Printf("ui list members for grant form: %v", err)
		} else {
			d.Members = memberRecsToInfos(members)
		}
	}

	// All doors — used by the assign-door form.
	doors, err := s.adminService.ListDoors(r.Context())
	if err != nil {
		s.logger.Printf("ui list doors for module detail: %v", err)
	} else {
		d.Doors = doors
	}

	render.render(w, "modules_detail", d)
}

// ── Module register ───────────────────────────────────────────────────────────

func (s *Server) handleUIModulesNew(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "modules")

	doors, err := s.adminService.ListDoors(r.Context())
	if err != nil {
		s.logger.Printf("ui list doors for module register: %v", err)
		d.Flash = "Failed to load doors."
		d.FlashType = "error"
	} else {
		d.Doors = doors
	}

	render.render(w, "modules_register", d)
}

func (s *Server) handleUIModulesRegister(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/modules/new", "Invalid form submission.", "error")
		return
	}

	req := types.RegisterModuleRequest{
		ModuleID:    r.FormValue("module_id"),
		DoorID:      r.FormValue("door_id"),
		DisplayName: r.FormValue("display_name"),
		ModuleType:  r.FormValue("module_type"),
	}

	mod, err := s.adminService.RegisterModule(r.Context(), req)
	if err != nil {
		msg := "Failed to register module."
		switch {
		case errors.Is(err, service.ErrModuleIDRequired):
			msg = "Module ID is required."
		case errors.Is(err, service.ErrModuleDoorRequired):
			msg = "Select a door to commission this module."
		case errors.Is(err, service.ErrDoorNotFound):
			msg = "The selected door no longer exists."
		case errors.Is(err, service.ErrInvalidModuleType):
			msg = "Invalid module type."
		default:
			s.logger.Printf("ui register module: %v", err)
		}
		flashRedirect(w, r, "/admin/ui/modules/new", msg, "error")
		return
	}

	s.logger.Printf("admin ui: registered module %s", mod.ModuleID)
	flashRedirect(w, r, "/admin/ui/modules/"+mod.ModuleID, "Module registered.", "success")
}

// ── Module revoke / delete ────────────────────────────────────────────────────

func (s *Server) handleUIModulesRevoke(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")

	if err := s.adminService.RevokeModule(r.Context(), moduleID); err != nil {
		if errors.Is(err, service.ErrModuleNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui revoke module %s: %v", moduleID, err)
		flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Failed to revoke module.", "error")
		return
	}

	s.logger.Printf("admin ui: revoked module %s", moduleID)
	flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Module revoked.", "success")
}

func (s *Server) handleUIModulesDelete(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")

	if err := s.adminService.DeleteModule(r.Context(), moduleID); err != nil {
		if errors.Is(err, service.ErrModuleNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui delete module %s: %v", moduleID, err)
		flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Failed to delete module.", "error")
		return
	}

	s.logger.Printf("admin ui: deleted module %s", moduleID)
	flashRedirect(w, r, "/admin/ui/modules", "Module deleted.", "success")
}

// ── Module authorizations (module-centric) ────────────────────────────────────

func (s *Server) handleUIModulesGrantAuthorization(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Invalid form submission.", "error")
		return
	}

	memberUUID := r.FormValue("member_uuid")
	expiresAtStr := r.FormValue("expires_at")

	var expiresAt *time.Time
	if expiresAtStr != "" {
		t, err := time.Parse("2006-01-02", expiresAtStr)
		if err != nil {
			flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Invalid expiry date format (use YYYY-MM-DD).", "error")
			return
		}
		u := t.UTC()
		expiresAt = &u
	}

	sess := sessionFromContext(r.Context())
	grantedBy := ""
	if sess != nil {
		grantedBy = sess.AdminUUID
	}

	if err := s.moduleAuthService.GrantAuthorization(r.Context(), memberUUID, moduleID, grantedBy, expiresAt, ""); err != nil {
		msg := "Failed to grant authorization."
		switch {
		case errors.Is(err, service.ErrAuthorizationAlreadyExists):
			msg = "An active authorization already exists for that member."
		case errors.Is(err, service.ErrMemberUUIDRequired), errors.Is(err, service.ErrModuleIDRequired):
			msg = "Member and module are required."
		default:
			s.logger.Printf("ui grant authorization module=%s member=%s: %v", moduleID, memberUUID, err)
		}
		flashRedirect(w, r, "/admin/ui/modules/"+moduleID, msg, "error")
		return
	}

	s.logger.Printf("admin ui: granted authorization module=%s member=%s by=%s", moduleID, memberUUID, grantedBy)
	flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Authorization granted.", "success")
}

func (s *Server) handleUIModulesRevokeAuthorization(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	memberUUID := r.PathValue("member_uuid")

	sess := sessionFromContext(r.Context())
	revokedBy := ""
	if sess != nil {
		revokedBy = sess.AdminUUID
	}

	if err := s.moduleAuthService.RevokeAuthorization(r.Context(), memberUUID, moduleID, revokedBy); err != nil {
		if errors.Is(err, service.ErrAuthorizationNotFound) {
			flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "No active authorization found for that member.", "error")
			return
		}
		s.logger.Printf("ui revoke authorization module=%s member=%s: %v", moduleID, memberUUID, err)
		flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Failed to revoke authorization.", "error")
		return
	}

	s.logger.Printf("admin ui: revoked authorization module=%s member=%s by=%s", moduleID, memberUUID, revokedBy)
	flashRedirect(w, r, "/admin/ui/modules/"+moduleID, "Authorization revoked.", "success")
}

// ── Doors list ────────────────────────────────────────────────────────────────

func (s *Server) handleUIDoorsList(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "doors")

	doors, err := s.adminService.ListDoors(r.Context())
	if err != nil {
		s.logger.Printf("ui list doors: %v", err)
		d.Flash = "Failed to load doors."
		d.FlashType = "error"
	} else {
		d.Doors = doors
	}

	render.render(w, "doors_list", d)
}

// ── Door register ─────────────────────────────────────────────────────────────

func (s *Server) handleUIDoorsNew(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "doors")
	render.render(w, "doors_register", d)
}

func (s *Server) handleUIDoorsCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/doors/new", "Invalid form submission.", "error")
		return
	}

	req := types.RegisterDoorRequest{
		DoorID:   r.FormValue("door_id"),
		Name:     r.FormValue("name"),
		Location: r.FormValue("location"),
	}

	_, err := s.adminService.RegisterDoor(r.Context(), req)
	if err != nil {
		msg := "Failed to register door."
		switch {
		case errors.Is(err, service.ErrDoorIDRequired):
			msg = "Door ID is required."
		case errors.Is(err, service.ErrDoorNameRequired):
			msg = "Door name is required."
		default:
			s.logger.Printf("ui register door: %v", err)
		}
		flashRedirect(w, r, "/admin/ui/doors/new", msg, "error")
		return
	}

	s.logger.Printf("admin ui: registered door %s", req.DoorID)
	flashRedirect(w, r, "/admin/ui/doors", "Door registered.", "success")
}

// ── Door delete ───────────────────────────────────────────────────────────────

func (s *Server) handleUIDoorsDelete(w http.ResponseWriter, r *http.Request) {
	doorID := r.PathValue("door_id")

	if err := s.adminService.DeleteDoor(r.Context(), doorID); err != nil {
		if errors.Is(err, service.ErrDoorNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui delete door %s: %v", doorID, err)
		flashRedirect(w, r, "/admin/ui/doors", "Failed to delete door.", "error")
		return
	}

	s.logger.Printf("admin ui: deleted door %s", doorID)
	flashRedirect(w, r, "/admin/ui/doors", "Door deleted.", "success")
}

// ── Route registration ────────────────────────────────────────────────────────

func (s *Server) uiModuleRoutes(mux *http.ServeMux) {
	perm := requireUIPermission

	// Module list + register
	mux.HandleFunc("GET /admin/ui/modules",
		perm(permissions.ModuleList, s.handleUIModulesList))
	mux.HandleFunc("GET /admin/ui/modules/new",
		perm(permissions.ModuleRegister, s.handleUIModulesNew))
	mux.HandleFunc("POST /admin/ui/modules",
		perm(permissions.ModuleRegister, s.handleUIModulesRegister))

	// Module detail + actions
	mux.HandleFunc("GET /admin/ui/modules/{module_id}",
		perm(permissions.ModuleGet, s.handleUIModulesDetail))
	mux.HandleFunc("POST /admin/ui/modules/{module_id}/revoke",
		perm(permissions.ModuleRevoke, s.handleUIModulesRevoke))
	mux.HandleFunc("POST /admin/ui/modules/{module_id}/delete",
		perm(permissions.ModuleDelete, s.handleUIModulesDelete))

	// Module authorizations (module-centric)
	mux.HandleFunc("POST /admin/ui/modules/{module_id}/authorizations",
		perm(permissions.ModuleAuthGrant, s.handleUIModulesGrantAuthorization))
	mux.HandleFunc("POST /admin/ui/modules/{module_id}/authorizations/{member_uuid}/revoke",
		perm(permissions.ModuleAuthRevoke, s.handleUIModulesRevokeAuthorization))

	// Doors
	mux.HandleFunc("GET /admin/ui/doors",
		perm(permissions.DoorList, s.handleUIDoorsList))
	mux.HandleFunc("GET /admin/ui/doors/new",
		perm(permissions.DoorRegister, s.handleUIDoorsNew))
	mux.HandleFunc("POST /admin/ui/doors",
		perm(permissions.DoorRegister, s.handleUIDoorsCreate))
	mux.HandleFunc("POST /admin/ui/doors/{door_id}/delete",
		perm(permissions.DoorDelete, s.handleUIDoorsDelete))
}
