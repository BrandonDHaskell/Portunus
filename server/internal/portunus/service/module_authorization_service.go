package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/permissions"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

var (
	ErrAuthorizationNotFound      = errors.New("authorization not found")
	ErrAuthorizationAlreadyExists = errors.New("authorization already exists for this member and module")
	// ErrGrantOutOfScope is returned when the actor holds only grant_held/revoke_held
	// but their linked member does not currently hold access to the target module.
	// The specific reason is recorded in the audit log; the error itself is
	// intentionally uninformative so UI messages stay uninformative to the caller.
	ErrGrantOutOfScope = errors.New("grant out of scope")
)

// GrantActor carries the acting admin's identity and permissions for scope
// evaluation inside GrantAuthorization / RevokeAuthorization.
type GrantActor struct {
	AdminUUID  string
	MemberUUID string              // admin_users.member_uuid; empty if unlinked
	Perms      map[string]struct{} // resolved session permissions
}

type ModuleAuthorizationService struct {
	authStore   store.ModuleAuthorizationStore
	memberStore store.MemberAccessStore
	auditStore  store.AuditStore // may be nil; writes are best-effort
}

func NewModuleAuthorizationService(
	as store.ModuleAuthorizationStore,
	ms store.MemberAccessStore,
	audit store.AuditStore,
) *ModuleAuthorizationService {
	return &ModuleAuthorizationService{authStore: as, memberStore: ms, auditStore: audit}
}

// GrantAuthorization creates a new active module authorization.
// actor.AdminUUID is recorded as granted_by_uuid; it may be empty for
// automated/system grants (pass a GrantActor with _any semantics explicitly).
// timeRestriction is an optional JSON policy string; pass "" for none.
func (s *ModuleAuthorizationService) GrantAuthorization(
	ctx context.Context,
	actor GrantActor,
	memberUUID, moduleID string,
	expiresAt *time.Time,
	timeRestriction string,
) error {
	memberUUID = strings.TrimSpace(memberUUID)
	moduleID = strings.TrimSpace(moduleID)
	if memberUUID == "" {
		return ErrMemberUUIDRequired
	}
	if moduleID == "" {
		return ErrModuleIDRequired
	}

	_, hasAny := actor.Perms[permissions.ModuleAuthGrantAny]
	_, hasHeld := actor.Perms[permissions.ModuleAuthGrantHeld]

	var qualifyingID int64

	switch {
	case hasAny:
		// unscoped — proceed
	case hasHeld:
		// scoped — actor's linked member must hold the module
		id, err := s.checkHeldScope(ctx, actor, moduleID, "granted")
		if err != nil {
			return err
		}
		qualifyingID = id
	default:
		s.recordAudit(ctx, actor.AdminUUID, "module_auth.granted", "module_authorization",
			moduleID, "failure",
			fmt.Sprintf(`{"reason":"no_grant_permission","member_uuid":%q}`, memberUUID))
		return ErrGrantOutOfScope
	}

	if err := s.authStore.GrantAuthorization(ctx, memberUUID, moduleID, actor.AdminUUID, expiresAt, timeRestriction); err != nil {
		if errors.Is(err, store.ErrAuthorizationAlreadyExists) {
			return ErrAuthorizationAlreadyExists
		}
		return fmt.Errorf("grant authorization: %w", err)
	}

	details := fmt.Sprintf(`{"member_uuid":%q,"module_id":%q}`, memberUUID, moduleID)
	if qualifyingID != 0 {
		details = fmt.Sprintf(`{"member_uuid":%q,"module_id":%q,"qualifying_authorization_id":%d}`,
			memberUUID, moduleID, qualifyingID)
	}
	s.recordAudit(ctx, actor.AdminUUID, "module_auth.granted", "module_authorization",
		moduleID, "success", details)
	return nil
}

// RevokeAuthorization soft-deletes an active authorization.
func (s *ModuleAuthorizationService) RevokeAuthorization(ctx context.Context, actor GrantActor, memberUUID, moduleID string) error {
	memberUUID = strings.TrimSpace(memberUUID)
	moduleID = strings.TrimSpace(moduleID)
	if memberUUID == "" {
		return ErrMemberUUIDRequired
	}
	if moduleID == "" {
		return ErrModuleIDRequired
	}

	_, hasAny := actor.Perms[permissions.ModuleAuthRevokeAny]
	_, hasHeld := actor.Perms[permissions.ModuleAuthRevokeHeld]

	if !hasAny {
		if !hasHeld {
			s.recordAudit(ctx, actor.AdminUUID, "module_auth.revoked", "module_authorization",
				moduleID, "failure",
				fmt.Sprintf(`{"reason":"no_revoke_permission","member_uuid":%q}`, memberUUID))
			return ErrGrantOutOfScope
		}
		if _, err := s.checkHeldScope(ctx, actor, moduleID, "revoked"); err != nil {
			return err
		}
	}

	if err := s.authStore.RevokeAuthorization(ctx, memberUUID, moduleID, actor.AdminUUID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrAuthorizationNotFound
		}
		return fmt.Errorf("revoke authorization: %w", err)
	}
	s.recordAudit(ctx, actor.AdminUUID, "module_auth.revoked", "module_authorization",
		moduleID, "success",
		fmt.Sprintf(`{"member_uuid":%q,"module_id":%q}`, memberUUID, moduleID))
	return nil
}

// ListByMember returns all authorization records for a member.
func (s *ModuleAuthorizationService) ListByMember(ctx context.Context, memberUUID string) ([]store.ModuleAuthorizationRecord, error) {
	recs, err := s.authStore.ListByMember(ctx, strings.TrimSpace(memberUUID))
	if err != nil {
		return nil, fmt.Errorf("list authorizations by member: %w", err)
	}
	return recs, nil
}

// ListByModule returns all authorization records for a module.
func (s *ModuleAuthorizationService) ListByModule(ctx context.Context, moduleID string) ([]store.ModuleAuthorizationRecord, error) {
	recs, err := s.authStore.ListByModule(ctx, strings.TrimSpace(moduleID))
	if err != nil {
		return nil, fmt.Errorf("list authorizations by module: %w", err)
	}
	return recs, nil
}

// ── scope check ───────────────────────────────────────────────────────────────

// checkHeldScope validates that the actor's linked member is active, enabled,
// and holds a non-revoked non-expired authorization on moduleID. Returns the
// qualifying authorization_id on success. On any failure it writes an audit
// entry and returns ErrGrantOutOfScope.
func (s *ModuleAuthorizationService) checkHeldScope(
	ctx context.Context,
	actor GrantActor,
	moduleID, action string,
) (int64, error) {
	auditAction := "module_auth." + action

	if actor.MemberUUID == "" {
		s.recordAudit(ctx, actor.AdminUUID, auditAction, "module_authorization",
			moduleID, "failure",
			fmt.Sprintf(`{"reason":"actor_not_linked","module_id":%q}`, moduleID))
		return 0, ErrGrantOutOfScope
	}

	linked, err := s.memberStore.GetMember(ctx, actor.MemberUUID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.recordAudit(ctx, actor.AdminUUID, auditAction, "module_authorization",
				moduleID, "failure",
				fmt.Sprintf(`{"reason":"actor_member_not_found","actor_member_uuid":%q,"module_id":%q}`,
					actor.MemberUUID, moduleID))
			return 0, ErrGrantOutOfScope
		}
		return 0, fmt.Errorf("scope check: load actor member: %w", err)
	}
	if linked.Status != store.MemberStatusActive || !linked.Enabled {
		s.recordAudit(ctx, actor.AdminUUID, auditAction, "module_authorization",
			moduleID, "failure",
			fmt.Sprintf(`{"reason":"actor_member_inactive","actor_member_uuid":%q,"module_id":%q}`,
				actor.MemberUUID, moduleID))
		return 0, ErrGrantOutOfScope
	}

	authID, ok, err := s.authStore.HasActiveAuthorization(ctx, actor.MemberUUID, moduleID)
	if err != nil {
		return 0, fmt.Errorf("scope check: %w", err)
	}
	if !ok {
		s.recordAudit(ctx, actor.AdminUUID, auditAction, "module_authorization",
			moduleID, "failure",
			fmt.Sprintf(`{"reason":"actor_lacks_module_access","actor_member_uuid":%q,"module_id":%q}`,
				actor.MemberUUID, moduleID))
		return 0, ErrGrantOutOfScope
	}
	return authID, nil
}

// ── audit helper ──────────────────────────────────────────────────────────────

func (s *ModuleAuthorizationService) recordAudit(
	ctx context.Context,
	actorUUID, action, resourceType, resourceID, result, details string,
) {
	if s.auditStore == nil {
		return
	}
	e := store.AuditEntry{
		ActorUUID:    actorUUID,
		ActorType:    store.ActorTypeAdmin,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Result:       result,
		Details:      details,
	}
	if actorUUID == "" {
		e.ActorType = store.ActorTypeSystem
	}
	if err := s.auditStore.RecordAuditEntry(ctx, e); err != nil {
		// Best-effort; primary operation must not fail due to audit failures.
		_ = err
	}
}
