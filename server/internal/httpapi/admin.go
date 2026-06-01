package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// maxAdminBody is the body size limit for admin requests (16 KiB).
const maxAdminBody = 16384

// ── Modules ─────────────────────────────────────────────────────────────────

func (s *Server) handleAdminListModules(w http.ResponseWriter, r *http.Request) {
	modules, err := s.adminService.ListModules(r.Context())
	if err != nil {
		s.logger.Printf("admin list modules: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list modules")
		return
	}
	if modules == nil {
		modules = []types.ModuleInfo{}
	}
	writeJSON(w, http.StatusOK, types.ListModulesResponse{OK: true, Modules: modules})
}

func (s *Server) handleAdminGetModule(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	if moduleID == "" {
		writeError(w, http.StatusBadRequest, "missing_module_id", "module_id path parameter is required")
		return
	}

	info, err := s.adminService.GetModule(r.Context(), moduleID)
	if err != nil {
		if errors.Is(err, service.ErrModuleNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "module not found")
			return
		}
		s.logger.Printf("admin get module: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get module")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "module": info})
}

func (s *Server) handleAdminRegisterModule(w http.ResponseWriter, r *http.Request) {
	var req types.RegisterModuleRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}

	info, err := s.adminService.RegisterModule(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrModuleIDRequired) {
			writeError(w, http.StatusBadRequest, "missing_module_id", err.Error())
			return
		}
		s.logger.Printf("admin register module: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to register module")
		return
	}

	s.logger.Printf("admin: commissioned module %q", info.ModuleID)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "module": info})
}

func (s *Server) handleAdminRevokeModule(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	if moduleID == "" {
		writeError(w, http.StatusBadRequest, "missing_module_id", "module_id path parameter is required")
		return
	}

	if err := s.adminService.RevokeModule(r.Context(), moduleID); err != nil {
		if errors.Is(err, service.ErrModuleNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "module not found")
			return
		}
		s.logger.Printf("admin revoke module: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to revoke module")
		return
	}

	s.logger.Printf("admin: revoked module %q", moduleID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "module_id": moduleID, "status": "revoked"})
}

func (s *Server) handleAdminDeleteModule(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	if moduleID == "" {
		writeError(w, http.StatusBadRequest, "missing_module_id", "module_id path parameter is required")
		return
	}

	if err := s.adminService.DeleteModule(r.Context(), moduleID); err != nil {
		if errors.Is(err, service.ErrModuleNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "module not found")
			return
		}
		s.logger.Printf("admin delete module: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete module")
		return
	}

	s.logger.Printf("admin: deleted module %q", moduleID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "module_id": moduleID, "deleted": true})
}

// ── Doors ───────────────────────────────────────────────────────────────────

func (s *Server) handleAdminListDoors(w http.ResponseWriter, r *http.Request) {
	doors, err := s.adminService.ListDoors(r.Context())
	if err != nil {
		s.logger.Printf("admin list doors: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list doors")
		return
	}
	if doors == nil {
		doors = []types.DoorInfo{}
	}
	writeJSON(w, http.StatusOK, types.ListDoorsResponse{OK: true, Doors: doors})
}

func (s *Server) handleAdminRegisterDoor(w http.ResponseWriter, r *http.Request) {
	var req types.RegisterDoorRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}

	info, err := s.adminService.RegisterDoor(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrDoorIDRequired) || errors.Is(err, service.ErrDoorNameRequired) {
			writeError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		s.logger.Printf("admin register door: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to register door")
		return
	}

	s.logger.Printf("admin: registered door %q", info.DoorID)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "door": info})
}

func (s *Server) handleAdminDeleteDoor(w http.ResponseWriter, r *http.Request) {
	doorID := r.PathValue("door_id")
	if doorID == "" {
		writeError(w, http.StatusBadRequest, "missing_door_id", "door_id path parameter is required")
		return
	}

	if err := s.adminService.DeleteDoor(r.Context(), doorID); err != nil {
		if errors.Is(err, service.ErrDoorNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "door not found")
			return
		}
		s.logger.Printf("admin delete door: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete door")
		return
	}

	s.logger.Printf("admin: deleted door %q", doorID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "door_id": doorID, "deleted": true})
}

// ── Admin user credentials ───────────────────────────────────────────────────

// handleAdminRegisterAdminCredential registers an RFID badge for an admin user.
// The badge is identified at provisioning time via scan-1 (operator tap).
func (s *Server) handleAdminRegisterAdminCredential(w http.ResponseWriter, r *http.Request) {
	adminUUID := r.PathValue("admin_uuid")
	if adminUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_admin_uuid", "admin_uuid path parameter is required")
		return
	}

	var body struct {
		CredentialID string `json:"credential_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}

	rawUID, err := service.ParseCredentialUID(body.CredentialID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_credential_id", "credential_id must be a colon-separated hex UID (e.g. \"04:A3:2B:1C\")")
		return
	}
	credHash := service.HashCredentialID(rawUID, s.credentialHashSecret)

	if err := s.adminUserService.RegisterCredential(r.Context(), adminUUID, credHash); err != nil {
		switch {
		case errors.Is(err, service.ErrAdminUserNotFound):
			writeError(w, http.StatusNotFound, "not_found", "admin user not found")
		case errors.Is(err, store.ErrAdminCredentialConflict):
			writeError(w, http.StatusConflict, "duplicate_credential", "credential is already registered to an admin user")
		default:
			s.logger.Printf("admin register admin credential: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to register credential")
		}
		return
	}

	s.logger.Printf("admin: registered credential for admin user %s", adminUUID)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "admin_user_uuid": adminUUID})
}

// ── System health ────────────────────────────────────────────────────────────

func (s *Server) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	audit := s.accessService.AuditHealth()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    audit.ConsecutiveFailures == 0,
		"audit": audit,
	})
}
