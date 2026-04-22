package httpapi

import (
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ── List ─────────────────────────────────────────────────────────────────────

// handleUIMembersList serves GET /admin/ui/members.
func (s *Server) handleUIMembersList(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "members")

	recs, err := s.memberAccessService.ListMembers(r.Context())
	if err != nil {
		s.logger.Printf("ui list members: %v", err)
		d.Flash = "Failed to load members."
		d.FlashType = "error"
	} else {
		d.Members = memberRecsToInfos(recs)
	}

	render.render(w, "members_list", d)
}

// handleUIMembersPending serves GET /admin/ui/members/pending.
func (s *Server) handleUIMembersPending(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "members")

	recs, err := s.memberAccessService.ListPendingAuthorizations(r.Context())
	if err != nil {
		s.logger.Printf("ui list pending authorizations: %v", err)
		d.Flash = "Failed to load pending authorizations."
		d.FlashType = "error"
	} else {
		d.Members = memberRecsToInfos(recs)
	}

	render.render(w, "members_pending", d)
}

// ── Detail ────────────────────────────────────────────────────────────────────

// handleUIMembersDetail serves GET /admin/ui/members/{member_uuid}.
func (s *Server) handleUIMembersDetail(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	d := newUIPageData(r, "members")

	rec, err := s.memberAccessService.GetMember(r.Context(), memberUUID)
	if err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui get member: %v", err)
		flashRedirect(w, r, "/admin/ui/members", "Failed to load member.", "error")
		return
	}
	info := memberRecordToInfo(rec)
	d.Member = &info

	// Module authorizations for this member.
	authRecs, err := s.moduleAuthService.ListByMember(r.Context(), memberUUID)
	if err != nil {
		s.logger.Printf("ui list authorizations for member %s: %v", memberUUID, err)
	} else {
		d.Authorizations = make([]types.ModuleAuthorizationInfo, len(authRecs))
		for i := range authRecs {
			d.Authorizations[i] = moduleAuthRecordToInfo(&authRecs[i])
		}
	}

	// All modules — used by the grant-authorization form.
	if s.adminService != nil {
		mods, err := s.adminService.ListModules(r.Context())
		if err != nil {
			s.logger.Printf("ui list modules for grant form: %v", err)
		} else {
			d.Modules = mods
		}
	}

	// All roles — used by the assign-role form.
	roles, err := s.roleService.ListRoles(r.Context())
	if err != nil {
		s.logger.Printf("ui list roles for member detail: %v", err)
	} else {
		d.Roles = roles
	}

	// Recent access events (only if a credential is attached).
	if len(rec.CredentialHash) == 32 && s.accessService != nil {
		events, err := s.accessService.ListEventsByCredential(r.Context(), rec.CredentialHash, 50)
		if err != nil {
			s.logger.Printf("ui list access events for member %s: %v", memberUUID, err)
		} else {
			d.AccessEvents = accessEventsToUI(events)
		}
	}

	render.render(w, "members_detail", d)
}

// ── Provision ─────────────────────────────────────────────────────────────────

// handleUIMembersNew serves GET /admin/ui/members/new.
func (s *Server) handleUIMembersNew(w http.ResponseWriter, r *http.Request) {
	d := newUIPageData(r, "members")

	roles, err := s.roleService.ListRoles(r.Context())
	if err != nil {
		s.logger.Printf("ui new member: list roles: %v", err)
		d.Flash = "Failed to load roles."
		d.FlashType = "error"
	}
	d.Roles = roles

	render.render(w, "members_provision", d)
}

// handleUIMembersCreate handles POST /admin/ui/members.
func (s *Server) handleUIMembersCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/members/new", "Invalid form submission.", "error")
		return
	}

	roleID := r.FormValue("role_id")
	expiresAtStr := r.FormValue("expires_at")
	inactivityStr := r.FormValue("inactivity_limit_days")

	var expiresAt *time.Time
	if expiresAtStr != "" {
		t, err := time.Parse("2006-01-02", expiresAtStr)
		if err != nil {
			flashRedirect(w, r, "/admin/ui/members/new", "Invalid expiry date format (use YYYY-MM-DD).", "error")
			return
		}
		u := t.UTC()
		expiresAt = &u
	}

	var inactivityDays *int
	if inactivityStr != "" {
		var n int
		if _, err := parseIntFormValue(inactivityStr, &n); err != nil || n < 1 {
			flashRedirect(w, r, "/admin/ui/members/new", "Inactivity limit must be a positive number.", "error")
			return
		}
		inactivityDays = &n
	}

	sess := sessionFromContext(r.Context())
	createdBy := ""
	if sess != nil {
		createdBy = sess.AdminUUID
	}

	rec, err := s.memberAccessService.ProvisionMember(r.Context(), roleID, createdBy, expiresAt, inactivityDays)
	if err != nil {
		if errors.Is(err, service.ErrRoleIDRequired) {
			flashRedirect(w, r, "/admin/ui/members/new", "A role is required.", "error")
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			flashRedirect(w, r, "/admin/ui/members/new", "Selected role does not exist.", "error")
			return
		}
		s.logger.Printf("ui provision member: %v", err)
		flashRedirect(w, r, "/admin/ui/members/new", "Failed to provision member.", "error")
		return
	}

	s.logger.Printf("admin ui: provisioned member uuid=%s role=%s by=%s", rec.UUID, rec.RoleID, createdBy)
	http.Redirect(w, r, "/admin/ui/members/"+rec.UUID+"?flash=Member+provisioned.+Copy+UUID+below.&ft=success", http.StatusSeeOther)
}

// ── Actions ───────────────────────────────────────────────────────────────────

// handleUIMembersAttachCredential handles POST /admin/ui/members/{member_uuid}/credential.
func (s *Server) handleUIMembersAttachCredential(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Invalid form submission.", "error")
		return
	}

	hashHex := r.FormValue("credential_hash")
	credHash, err := hex.DecodeString(hashHex)
	if err != nil || len(credHash) != 32 {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Credential hash must be 64 hex characters (32 bytes).", "error")
		return
	}

	if err := s.memberAccessService.AttachCredential(r.Context(), memberUUID, credHash); err != nil {
		msg := "Failed to attach credential."
		switch {
		case errors.Is(err, service.ErrMemberNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, service.ErrDuplicateCredentialActive):
			msg = "That credential is already assigned to an active member."
		case errors.Is(err, service.ErrDuplicateCredentialPending):
			msg = "That credential is already attached to a pending member — resolve the pending record first."
		case errors.Is(err, service.ErrDuplicateCredentialInactive):
			msg = "That credential belongs to an expired or archived member."
		default:
			s.logger.Printf("ui attach credential: %v", err)
		}
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, msg, "error")
		return
	}

	s.logger.Printf("admin ui: attached credential to member %s", memberUUID)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Credential attached.", "success")
}

// handleUIMembersAssignRole handles POST /admin/ui/members/{member_uuid}/role.
func (s *Server) handleUIMembersAssignRole(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Invalid form submission.", "error")
		return
	}

	roleID := r.FormValue("role_id")
	if err := s.memberAccessService.AssignRole(r.Context(), memberUUID, roleID); err != nil {
		switch {
		case errors.Is(err, service.ErrMemberNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, service.ErrRoleIDRequired):
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "A role is required.", "error")
			return
		case errors.Is(err, store.ErrNotFound):
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Selected role does not exist.", "error")
			return
		default:
			s.logger.Printf("ui assign role to member %s: %v", memberUUID, err)
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Failed to assign role.", "error")
			return
		}
	}

	s.logger.Printf("admin ui: assigned role %q to member %s", roleID, memberUUID)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Role updated.", "success")
}

// handleUIMembersDisable handles POST /admin/ui/members/{member_uuid}/disable.
func (s *Server) handleUIMembersDisable(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if err := s.memberAccessService.Disable(r.Context(), memberUUID); err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui disable member %s: %v", memberUUID, err)
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Failed to disable member.", "error")
		return
	}
	s.logger.Printf("admin ui: disabled member %s", memberUUID)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Member disabled.", "success")
}

// handleUIMembersEnable handles POST /admin/ui/members/{member_uuid}/enable.
func (s *Server) handleUIMembersEnable(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if err := s.memberAccessService.Enable(r.Context(), memberUUID); err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui enable member %s: %v", memberUUID, err)
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Failed to enable member.", "error")
		return
	}
	s.logger.Printf("admin ui: enabled member %s", memberUUID)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Member enabled.", "success")
}

// handleUIMembersArchive handles POST /admin/ui/members/{member_uuid}/archive.
func (s *Server) handleUIMembersArchive(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	sess := sessionFromContext(r.Context())
	archivedBy := ""
	if sess != nil {
		archivedBy = sess.AdminUUID
	}

	if err := s.memberAccessService.Archive(r.Context(), memberUUID, archivedBy); err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui archive member %s: %v", memberUUID, err)
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Failed to archive member.", "error")
		return
	}
	s.logger.Printf("admin ui: archived member %s by %s", memberUUID, archivedBy)
	flashRedirect(w, r, "/admin/ui/members", "Member archived.", "success")
}

// ── Module Authorizations ─────────────────────────────────────────────────────

// handleUIGrantAuthorization handles POST /admin/ui/members/{member_uuid}/authorizations.
func (s *Server) handleUIGrantAuthorization(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Invalid form submission.", "error")
		return
	}

	moduleID := r.FormValue("module_id")
	expiresAtStr := r.FormValue("expires_at")

	var expiresAt *time.Time
	if expiresAtStr != "" {
		t, err := time.Parse("2006-01-02", expiresAtStr)
		if err != nil {
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Invalid expiry date format (use YYYY-MM-DD).", "error")
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
			msg = "An active authorization already exists for that module."
		case errors.Is(err, service.ErrMemberUUIDRequired), errors.Is(err, service.ErrModuleIDRequired):
			msg = "Member UUID and module are required."
		default:
			s.logger.Printf("ui grant authorization member=%s module=%s: %v", memberUUID, moduleID, err)
		}
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, msg, "error")
		return
	}

	s.logger.Printf("admin ui: granted authorization member=%s module=%s by=%s", memberUUID, moduleID, grantedBy)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Module authorization granted.", "success")
}

// handleUIRevokeAuthorization handles POST /admin/ui/members/{member_uuid}/authorizations/{module_id}/revoke.
func (s *Server) handleUIRevokeAuthorization(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	moduleID := r.PathValue("module_id")

	sess := sessionFromContext(r.Context())
	revokedBy := ""
	if sess != nil {
		revokedBy = sess.AdminUUID
	}

	if err := s.moduleAuthService.RevokeAuthorization(r.Context(), memberUUID, moduleID, revokedBy); err != nil {
		if errors.Is(err, service.ErrAuthorizationNotFound) {
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "No active authorization found for that module.", "error")
			return
		}
		s.logger.Printf("ui revoke authorization member=%s module=%s: %v", memberUUID, moduleID, err)
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Failed to revoke authorization.", "error")
		return
	}

	s.logger.Printf("admin ui: revoked authorization member=%s module=%s by=%s", memberUUID, moduleID, revokedBy)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Authorization revoked.", "success")
}

// ── Route registration ────────────────────────────────────────────────────────

// uiMemberRoutes registers all /admin/ui/members/* routes on the given mux.
func (s *Server) uiMemberRoutes(mux *http.ServeMux) {
	perm := requireUIPermission

	// List + pending queue
	mux.HandleFunc("GET /admin/ui/members",
		perm(permissions.MemberList, s.handleUIMembersList))
	mux.HandleFunc("GET /admin/ui/members/pending",
		perm(permissions.MemberList, s.handleUIMembersPending))

	// Provision new member (must be registered before /{member_uuid} to avoid ambiguity)
	mux.HandleFunc("GET /admin/ui/members/new",
		perm(permissions.MemberProvision, s.handleUIMembersNew))
	mux.HandleFunc("POST /admin/ui/members",
		perm(permissions.MemberProvision, s.handleUIMembersCreate))

	// Member detail + actions
	mux.HandleFunc("GET /admin/ui/members/{member_uuid}",
		perm(permissions.MemberView, s.handleUIMembersDetail))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/credential",
		perm(permissions.MemberProvision, s.handleUIMembersAttachCredential))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/role",
		perm(permissions.MemberAssignRole, s.handleUIMembersAssignRole))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/disable",
		perm(permissions.MemberDisable, s.handleUIMembersDisable))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/enable",
		perm(permissions.MemberDisable, s.handleUIMembersEnable))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/archive",
		perm(permissions.MemberArchive, s.handleUIMembersArchive))

	// Module authorization grant/revoke
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/authorizations",
		perm(permissions.ModuleAuthGrant, s.handleUIGrantAuthorization))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/authorizations/{module_id}/revoke",
		perm(permissions.ModuleAuthRevoke, s.handleUIRevokeAuthorization))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func memberRecsToInfos(recs []store.MemberAccessRecord) []types.MemberInfo {
	out := make([]types.MemberInfo, len(recs))
	for i := range recs {
		out[i] = memberRecordToInfo(&recs[i])
	}
	return out
}

func accessEventsToUI(recs []store.AccessEventRecord) []UIAccessEventInfo {
	out := make([]UIAccessEventInfo, len(recs))
	for i, r := range recs {
		out[i] = UIAccessEventInfo{
			ModuleID:   r.ModuleID,
			ReceivedAt: r.ReceivedAt.Format("2006-01-02 15:04:05 UTC"),
			Granted:    r.Granted,
			Reason:     r.Reason,
		}
	}
	return out
}

// parseIntFormValue parses a decimal integer from a form string into *dst.
func parseIntFormValue(s string, dst *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	*dst = n
	return n, nil
}
