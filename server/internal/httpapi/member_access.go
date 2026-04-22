package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ── Members ──────────────────────────────────────────────────────────────────

func (s *Server) handleAdminListMembers(w http.ResponseWriter, r *http.Request) {
	members, err := s.memberAccessService.ListMembers(r.Context())
	if err != nil {
		s.logger.Printf("admin list members: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list members")
		return
	}
	infos := make([]types.MemberInfo, len(members))
	for i := range members {
		infos[i] = memberRecordToInfo(&members[i])
	}
	writeJSON(w, http.StatusOK, types.ListMembersResponse{OK: true, Members: infos})
}

func (s *Server) handleAdminListPendingAuthorizations(w http.ResponseWriter, r *http.Request) {
	members, err := s.memberAccessService.ListPendingAuthorizations(r.Context())
	if err != nil {
		s.logger.Printf("admin list pending authorizations: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list pending authorizations")
		return
	}
	infos := make([]types.MemberInfo, len(members))
	for i := range members {
		infos[i] = memberRecordToInfo(&members[i])
	}
	writeJSON(w, http.StatusOK, types.ListPendingAuthorizationsResponse{OK: true, Members: infos})
}

func (s *Server) handleAdminGetMember(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if memberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid path parameter is required")
		return
	}
	rec, err := s.memberAccessService.GetMember(r.Context(), memberUUID)
	if err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "member not found")
			return
		}
		s.logger.Printf("admin get member: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get member")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member": memberRecordToInfo(rec)})
}

func (s *Server) handleAdminProvisionMember(w http.ResponseWriter, r *http.Request) {
	var req types.ProvisionMemberRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
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

	rec, err := s.memberAccessService.ProvisionMember(r.Context(), req.RoleID, req.CreatedByUUID, expiresAt, req.InactivityLimitDays)
	if err != nil {
		if errors.Is(err, service.ErrRoleIDRequired) {
			writeError(w, http.StatusBadRequest, "missing_role_id", err.Error())
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "role_not_found", "the specified role does not exist")
			return
		}
		s.logger.Printf("admin provision member: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to provision member")
		return
	}

	s.logger.Printf("admin: provisioned member uuid=%s role=%s", rec.UUID, rec.RoleID)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "member": memberRecordToInfo(rec)})
}

func (s *Server) handleAdminAttachCredential(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if memberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid path parameter is required")
		return
	}

	var req types.AttachCredentialRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}

	credHash, err := hex.DecodeString(req.CredentialHashHex)
	if err != nil || len(credHash) != 32 {
		writeError(w, http.StatusBadRequest, "invalid_credential_hash", "credential_hash must be a 64-character hex string (32 bytes)")
		return
	}

	if err := s.memberAccessService.AttachCredential(r.Context(), memberUUID, credHash); err != nil {
		switch {
		case errors.Is(err, service.ErrMemberNotFound):
			writeError(w, http.StatusNotFound, "not_found", "member not found")
		case errors.Is(err, service.ErrDuplicateCredentialActive):
			writeError(w, http.StatusConflict, "duplicate_credential_active", "credential is already assigned to an active member")
		case errors.Is(err, service.ErrDuplicateCredentialPending):
			writeError(w, http.StatusConflict, "duplicate_credential_pending", "credential is already attached to a pending member — resolve the pending record first")
		case errors.Is(err, service.ErrDuplicateCredentialInactive):
			writeError(w, http.StatusConflict, "duplicate_credential_inactive", "credential belongs to an expired or archived member — archive and re-provision if intentional")
		default:
			s.logger.Printf("admin attach credential: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to attach credential")
		}
		return
	}

	s.logger.Printf("admin: attached credential to member %s", memberUUID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member_uuid": memberUUID})
}

func (s *Server) handleAdminAssignRole(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if memberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid path parameter is required")
		return
	}

	var req types.AssignRoleRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", "invalid JSON body")
		return
	}

	if err := s.memberAccessService.AssignRole(r.Context(), memberUUID, req.RoleID); err != nil {
		switch {
		case errors.Is(err, service.ErrMemberNotFound):
			writeError(w, http.StatusNotFound, "not_found", "member not found")
		case errors.Is(err, service.ErrRoleIDRequired):
			writeError(w, http.StatusBadRequest, "missing_role_id", err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusBadRequest, "role_not_found", "the specified role does not exist")
		default:
			s.logger.Printf("admin assign role: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to assign role")
		}
		return
	}

	s.logger.Printf("admin: assigned role %s to member %s", req.RoleID, memberUUID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member_uuid": memberUUID, "role_id": req.RoleID})
}

func (s *Server) handleAdminDisableMember(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if memberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid path parameter is required")
		return
	}
	if err := s.memberAccessService.Disable(r.Context(), memberUUID); err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "member not found")
			return
		}
		s.logger.Printf("admin disable member: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to disable member")
		return
	}
	s.logger.Printf("admin: disabled member %s", memberUUID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member_uuid": memberUUID, "enabled": false})
}

func (s *Server) handleAdminArchiveMember(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if memberUUID == "" {
		writeError(w, http.StatusBadRequest, "missing_member_uuid", "member_uuid path parameter is required")
		return
	}

	sess := sessionFromContext(r.Context())
	archivedByUUID := ""
	if sess != nil {
		archivedByUUID = sess.AdminUUID
	}

	if err := s.memberAccessService.Archive(r.Context(), memberUUID, archivedByUUID); err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "member not found")
			return
		}
		s.logger.Printf("admin archive member: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to archive member")
		return
	}
	s.logger.Printf("admin: archived member %s by %s", memberUUID, archivedByUUID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "member_uuid": memberUUID, "status": "archived"})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func memberRecordToInfo(rec *store.MemberAccessRecord) types.MemberInfo {
	info := types.MemberInfo{
		UUID:                rec.UUID,
		RoleID:              rec.RoleID,
		Status:              string(rec.Status),
		Enabled:             rec.Enabled,
		ProvisioningStatus:  string(rec.ProvisioningStatus),
		CreatedAt:           rec.CreatedAt.Format(time.RFC3339),
		CreatedByUUID:       rec.CreatedByUUID,
		InactivityLimitDays: rec.InactivityLimitDays,
	}
	// Expose only an 8-byte (16 hex char) prefix of the hash — enough to
	// cross-reference without exposing the full value.
	if len(rec.CredentialHash) > 0 {
		info.CredentialHash = hex.EncodeToString(rec.CredentialHash[:8]) + "…"
	}
	if rec.ExpiresAt != nil {
		info.ExpiresAt = rec.ExpiresAt.Format(time.RFC3339)
	}
	if rec.LastAccessAt != nil {
		info.LastAccessAt = rec.LastAccessAt.Format(time.RFC3339)
	}
	if rec.ArchivedAt != nil {
		info.ArchivedAt = rec.ArchivedAt.Format(time.RFC3339)
		info.ArchivedByUUID = rec.ArchivedByUUID
	}
	return info
}
