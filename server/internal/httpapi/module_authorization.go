package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ── Module Authorizations ─────────────────────────────────────────────────────

func (s *Server) handleAdminGrantModuleAuthorization(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	if moduleID == "" {
		writeError(w, http.StatusBadRequest, "missing_module_id", "module_id path parameter is required")
		return
	}

	var req types.GrantAuthorizationRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}
	if req.MemberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid is required")
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_expires_at", "expires_at must be RFC 3339")
			return
		}
		u := t.UTC()
		expiresAt = &u
	}

	grantedBy := req.GrantedByUUID
	if grantedBy == "" {
		if sess := sessionFromContext(r.Context()); sess != nil {
			grantedBy = sess.AdminUUID
		}
	}

	if err := s.moduleAuthService.GrantAuthorization(r.Context(), req.MemberUUID, moduleID, grantedBy, expiresAt, req.TimeRestriction); err != nil {
		switch {
		case errors.Is(err, service.ErrAuthorizationAlreadyExists):
			writeError(w, http.StatusConflict, "authorization_exists", "an active authorization already exists for this member and module")
		case errors.Is(err, service.ErrMemberUUIDRequired):
			writeError(w, http.StatusBadRequest, "missing_member_uuid", err.Error())
		case errors.Is(err, service.ErrModuleIDRequired):
			writeError(w, http.StatusBadRequest, "missing_module_id", err.Error())
		default:
			s.logger.Printf("admin grant module authorization: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to grant authorization")
		}
		return
	}

	s.logger.Printf("admin: granted authorization member=%s module=%s by=%s", req.MemberUUID, moduleID, grantedBy)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "member_uuid": req.MemberUUID, "module_id": moduleID})
}

func (s *Server) handleAdminRevokeModuleAuthorization(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	memberUUID := r.PathValue("member_uuid")
	if moduleID == "" {
		writeError(w, http.StatusBadRequest, "missing_module_id", "module_id path parameter is required")
		return
	}
	if memberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid path parameter is required")
		return
	}

	revokedBy := ""
	if sess := sessionFromContext(r.Context()); sess != nil {
		revokedBy = sess.AdminUUID
	}

	if err := s.moduleAuthService.RevokeAuthorization(r.Context(), memberUUID, moduleID, revokedBy); err != nil {
		if errors.Is(err, service.ErrAuthorizationNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no active authorization found for this member and module")
			return
		}
		s.logger.Printf("admin revoke module authorization: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to revoke authorization")
		return
	}

	s.logger.Printf("admin: revoked authorization member=%s module=%s by=%s", memberUUID, moduleID, revokedBy)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member_uuid": memberUUID, "module_id": moduleID, "status": "revoked"})
}

func (s *Server) handleAdminListAuthorizationsByModule(w http.ResponseWriter, r *http.Request) {
	moduleID := r.PathValue("module_id")
	if moduleID == "" {
		writeError(w, http.StatusBadRequest, "missing_module_id", "module_id path parameter is required")
		return
	}

	recs, err := s.moduleAuthService.ListByModule(r.Context(), moduleID)
	if err != nil {
		s.logger.Printf("admin list authorizations by module: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list authorizations")
		return
	}
	infos := make([]types.ModuleAuthorizationInfo, len(recs))
	for i := range recs {
		infos[i] = moduleAuthRecordToInfo(&recs[i])
	}
	writeJSON(w, http.StatusOK, types.ListModuleAuthorizationsResponse{OK: true, Authorizations: infos})
}

func (s *Server) handleAdminListAuthorizationsByMember(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if memberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid path parameter is required")
		return
	}

	recs, err := s.moduleAuthService.ListByMember(r.Context(), memberUUID)
	if err != nil {
		s.logger.Printf("admin list authorizations by member: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list authorizations")
		return
	}
	infos := make([]types.ModuleAuthorizationInfo, len(recs))
	for i := range recs {
		infos[i] = moduleAuthRecordToInfo(&recs[i])
	}
	writeJSON(w, http.StatusOK, types.ListModuleAuthorizationsResponse{OK: true, Authorizations: infos})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func moduleAuthRecordToInfo(rec *store.ModuleAuthorizationRecord) types.ModuleAuthorizationInfo {
	info := types.ModuleAuthorizationInfo{
		AuthorizationID: rec.AuthorizationID,
		MemberUUID:      rec.MemberUUID,
		ModuleID:        rec.ModuleID,
		GrantedAt:       rec.GrantedAt.Format(time.RFC3339),
		GrantedByUUID:   rec.GrantedByUUID,
		TimeRestriction: rec.TimeRestriction,
		RevokedByUUID:   rec.RevokedByUUID,
	}
	if rec.ExpiresAt != nil {
		info.ExpiresAt = rec.ExpiresAt.Format(time.RFC3339)
	}
	if rec.RevokedAt != nil {
		info.RevokedAt = rec.RevokedAt.Format(time.RFC3339)
	}
	return info
}
