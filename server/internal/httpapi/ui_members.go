package httpapi

import (
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
	render.render(w, "members_provision", d)
}

// handleUIMembersCreate handles POST /admin/ui/members.
func (s *Server) handleUIMembersCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/members/new", "Invalid form submission.", "error")
		return
	}

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

	rec, err := s.memberAccessService.ProvisionMember(r.Context(), createdBy, expiresAt, inactivityDays)
	if err != nil {
		s.logger.Printf("ui provision member: %v", err)
		flashRedirect(w, r, "/admin/ui/members/new", "Failed to provision member.", "error")
		return
	}

	s.logger.Printf("admin ui: provisioned member uuid=%s by=%s", rec.UUID, createdBy)
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

	credentialID := r.FormValue("credential_id")
	rawUID, err := service.ParseCredentialUID(credentialID)
	if err != nil {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Invalid credential UID — use colon-separated hex, e.g. 04:A3:2B:1C.", "error")
		return
	}
	credHash := service.HashCredentialID(rawUID, s.credentialHashSecret)

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

// handleUIMembersApprove handles POST /admin/ui/members/{member_uuid}/approve.
func (s *Server) handleUIMembersApprove(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Invalid form submission.", "error")
		return
	}

	expiresAtStr := r.FormValue("expires_at")
	inactivityStr := r.FormValue("inactivity_limit_days")

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

	// Inactivity is required at approval time.
	if inactivityStr == "" {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Inactivity limit is required when approving a member.", "error")
		return
	}
	var inactivityDays int
	if _, err := parseIntFormValue(inactivityStr, &inactivityDays); err != nil || inactivityDays < 1 {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Inactivity limit must be a positive number.", "error")
		return
	}

	sess := sessionFromContext(r.Context())
	approvedBy := ""
	if sess != nil {
		approvedBy = sess.AdminUUID
	}

	if err := s.memberAccessService.ApprovePending(r.Context(), memberUUID, approvedBy, expiresAt, &inactivityDays); err != nil {
		msg := "Failed to approve member."
		switch {
		case errors.Is(err, service.ErrMemberNotFound):
			http.NotFound(w, r)
			return
		case errors.Is(err, service.ErrMemberNotPending):
			msg = "Member is not pending authorization."
		case errors.Is(err, service.ErrInactivityLimitRequired):
			msg = "Inactivity limit is required."
		default:
			s.logger.Printf("ui approve pending member %s: %v", memberUUID, err)
		}
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, msg, "error")
		return
	}

	s.logger.Printf("admin ui: approved pending member %s by %s", memberUUID, approvedBy)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Member approved and activated.", "success")
}

// handleUIMembersEdit serves GET /admin/ui/members/{member_uuid}/edit.
func (s *Server) handleUIMembersEdit(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	d := newUIPageData(r, "members")

	rec, err := s.memberAccessService.GetMember(r.Context(), memberUUID)
	if err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui get member for edit: %v", err)
		flashRedirect(w, r, "/admin/ui/members", "Failed to load member.", "error")
		return
	}
	info := memberRecordToInfo(rec)
	d.Member = &info

	render.render(w, "members_edit", d)
}

// handleUIMembersUpdate handles POST /admin/ui/members/{member_uuid}/edit.
func (s *Server) handleUIMembersUpdate(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	if err := r.ParseForm(); err != nil {
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID+"/edit", "Invalid form submission.", "error")
		return
	}

	expiresAtStr := r.FormValue("expires_at")
	inactivityStr := r.FormValue("inactivity_limit_days")

	var expiresAt *time.Time
	if expiresAtStr != "" {
		t, err := time.Parse("2006-01-02", expiresAtStr)
		if err != nil {
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID+"/edit", "Invalid expiry date format (use YYYY-MM-DD).", "error")
			return
		}
		u := t.UTC()
		expiresAt = &u
	}

	var inactivityDays *int
	if inactivityStr != "" {
		var n int
		if _, err := parseIntFormValue(inactivityStr, &n); err != nil || n < 1 {
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID+"/edit", "Inactivity limit must be a positive number.", "error")
			return
		}
		inactivityDays = &n
	}

	if err := s.memberAccessService.UpdateMemberPolicy(r.Context(), memberUUID, expiresAt, inactivityDays); err != nil {
		if errors.Is(err, service.ErrMemberNotFound) {
			http.NotFound(w, r)
			return
		}
		s.logger.Printf("ui update member policy %s: %v", memberUUID, err)
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID+"/edit", "Failed to update member.", "error")
		return
	}

	s.logger.Printf("admin ui: updated policy for member %s", memberUUID)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Member policy updated.", "success")
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
	actor := service.GrantActor{}
	if sess != nil {
		actor = service.GrantActor{
			AdminUUID:  sess.AdminUUID,
			MemberUUID: sess.MemberUUID,
			Perms:      sess.Permissions,
		}
	}

	if err := s.moduleAuthService.GrantAuthorization(r.Context(), actor, memberUUID, moduleID, expiresAt, ""); err != nil {
		msg := "Failed to grant authorization."
		switch {
		case errors.Is(err, service.ErrAuthorizationAlreadyExists):
			msg = "An active authorization already exists for that module."
		case errors.Is(err, service.ErrGrantOutOfScope):
			msg = "Your linked member does not currently hold access to that module."
		case errors.Is(err, service.ErrMemberUUIDRequired), errors.Is(err, service.ErrModuleIDRequired):
			msg = "Member UUID and module are required."
		default:
			s.logger.Printf("ui grant authorization member=%s module=%s: %v", memberUUID, moduleID, err)
		}
		flashRedirect(w, r, "/admin/ui/members/"+memberUUID, msg, "error")
		return
	}

	s.logger.Printf("admin ui: granted authorization member=%s module=%s by=%s", memberUUID, moduleID, actor.AdminUUID)
	flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Module authorization granted.", "success")
}

// handleUIRevokeAuthorization handles POST /admin/ui/members/{member_uuid}/authorizations/{module_id}/revoke.
func (s *Server) handleUIRevokeAuthorization(w http.ResponseWriter, r *http.Request) {
	memberUUID := r.PathValue("member_uuid")
	moduleID := r.PathValue("module_id")

	sess := sessionFromContext(r.Context())
	actor := service.GrantActor{}
	if sess != nil {
		actor = service.GrantActor{
			AdminUUID:  sess.AdminUUID,
			MemberUUID: sess.MemberUUID,
			Perms:      sess.Permissions,
		}
	}

	if err := s.moduleAuthService.RevokeAuthorization(r.Context(), actor, memberUUID, moduleID); err != nil {
		switch {
		case errors.Is(err, service.ErrAuthorizationNotFound):
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "No active authorization found for that module.", "error")
		case errors.Is(err, service.ErrGrantOutOfScope):
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Your linked member does not currently hold access to that module.", "error")
		default:
			s.logger.Printf("ui revoke authorization member=%s module=%s: %v", memberUUID, moduleID, err)
			flashRedirect(w, r, "/admin/ui/members/"+memberUUID, "Failed to revoke authorization.", "error")
		}
		return
	}

	s.logger.Printf("admin ui: revoked authorization member=%s module=%s by=%s", memberUUID, moduleID, actor.AdminUUID)
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
		perm(permissions.MemberEnroll, s.handleUIMembersNew))
	mux.HandleFunc("POST /admin/ui/members",
		perm(permissions.MemberEnroll, s.handleUIMembersCreate))

	// Member detail + actions
	mux.HandleFunc("GET /admin/ui/members/{member_uuid}",
		perm(permissions.MemberView, s.handleUIMembersDetail))
	mux.HandleFunc("GET /admin/ui/members/{member_uuid}/edit",
		perm(permissions.MemberEdit, s.handleUIMembersEdit))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/edit",
		perm(permissions.MemberEdit, s.handleUIMembersUpdate))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/approve",
		perm(permissions.MemberEnroll, s.handleUIMembersApprove))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/credential",
		perm(permissions.MemberEnroll, s.handleUIMembersAttachCredential))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/disable",
		perm(permissions.MemberDisable, s.handleUIMembersDisable))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/enable",
		perm(permissions.MemberDisable, s.handleUIMembersEnable))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/archive",
		perm(permissions.MemberArchive, s.handleUIMembersArchive))

	// Module authorization grant/revoke (either _held or _any variant passes the gate;
	// the service performs the scope check for _held).
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/authorizations",
		requireUIEitherPermission(permissions.ModuleAuthGrantHeld, permissions.ModuleAuthGrantAny, s.handleUIGrantAuthorization))
	mux.HandleFunc("POST /admin/ui/members/{member_uuid}/authorizations/{module_id}/revoke",
		requireUIEitherPermission(permissions.ModuleAuthRevokeHeld, permissions.ModuleAuthRevokeAny, s.handleUIRevokeAuthorization))
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
