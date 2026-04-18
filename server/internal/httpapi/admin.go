package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

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

// ── Cards ───────────────────────────────────────────────────────────────────

func (s *Server) handleAdminListCards(w http.ResponseWriter, r *http.Request) {
	cards, err := s.adminService.ListCards(r.Context())
	if err != nil {
		s.logger.Printf("admin list cards: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list cards")
		return
	}
	if cards == nil {
		cards = []types.CardInfo{}
	}
	writeJSON(w, http.StatusOK, types.ListCardsResponse{OK: true, Cards: cards})
}

func (s *Server) handleAdminRegisterCard(w http.ResponseWriter, r *http.Request) {
	var req types.RegisterCardRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}

	info, err := s.adminService.RegisterCard(r.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrCardIDRequired) {
			writeError(w, http.StatusBadRequest, "missing_card_id", err.Error())
			return
		}
		if errors.Is(err, store.ErrCardAlreadyExists) {
			writeError(w, http.StatusConflict, "card_exists", "card is already registered")
			return
		}
		s.logger.Printf("admin register card: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to register card")
		return
	}

	s.logger.Printf("admin: registered card tag=%q hash=%.16s…", req.Tag, info.CardIDHash)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "card": info})
}

func (s *Server) handleAdminUpdateCardStatus(w http.ResponseWriter, r *http.Request) {
	hashHex := r.PathValue("card_hash")
	if hashHex == "" {
		writeError(w, http.StatusBadRequest, "missing_card_hash", "card_hash path parameter is required")
		return
	}

	var body struct {
		Status string `json:"status"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}

	if err := s.adminService.SetCardStatus(r.Context(), hashHex, body.Status); err != nil {
		if errors.Is(err, service.ErrInvalidStatus) {
			writeError(w, http.StatusBadRequest, "invalid_status", err.Error())
			return
		}
		if errors.Is(err, service.ErrCardNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "card not found")
			return
		}
		s.logger.Printf("admin update card status: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to update card")
		return
	}

	s.logger.Printf("admin: card %.16s… → %s", hashHex, body.Status)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "card_id_hash": hashHex, "status": body.Status})
}

func (s *Server) handleAdminDeleteCard(w http.ResponseWriter, r *http.Request) {
	hashHex := r.PathValue("card_hash")
	if hashHex == "" {
		writeError(w, http.StatusBadRequest, "missing_card_hash", "card_hash path parameter is required")
		return
	}

	if err := s.adminService.DeleteCard(r.Context(), hashHex); err != nil {
		if errors.Is(err, service.ErrCardNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "card not found")
			return
		}
		s.logger.Printf("admin delete card: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete card")
		return
	}

	s.logger.Printf("admin: deleted card %.16s…", hashHex)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "card_id_hash": hashHex, "deleted": true})
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

// ── Admin auth middleware ────────────────────────────────────────────────────

// adminAuthMiddleware protects /admin/v1/* routes with a Bearer token check.
// The token must match the server's PORTUNUS_ADMIN_API_KEY env var.
func adminAuthMiddleware(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only protect paths under /admin/
		if !strings.HasPrefix(r.URL.Path, "/admin/") {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeError(w, http.StatusUnauthorized, "missing_auth",
				"Authorization header is required for admin endpoints")
			return
		}

		// Accept "Bearer <token>" format.
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			writeError(w, http.StatusUnauthorized, "invalid_auth",
				"Authorization header must use Bearer scheme")
			return
		}

		token := strings.TrimSpace(auth[len(prefix):])
		if subtle.ConstantTimeCompare([]byte(token), []byte(apiKey)) != 1 {
			writeError(w, http.StatusForbidden, "forbidden", "invalid admin API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}
