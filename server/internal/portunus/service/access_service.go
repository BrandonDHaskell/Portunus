package service

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// AuditHealthSnapshot is a point-in-time view of audit write health.
// ConsecutiveFailures resets to zero on every successful write.
type AuditHealthSnapshot struct {
	ConsecutiveFailures int64      `json:"consecutive_failures"`
	TotalFailures       int64      `json:"total_failures"`
	LastFailure         *time.Time `json:"last_failure,omitempty"`
	LastSuccess         *time.Time `json:"last_success,omitempty"`
}

type auditState struct {
	mu                  sync.Mutex
	consecutiveFailures int64
	totalFailures       int64
	lastFailure         time.Time
	lastSuccess         time.Time
}

var (
	ErrInvalidCredentialID = errors.New("credential_id is required")
)

type AccessPolicy struct {
	// AllowAll grants access to any credential (dev/testing only).
	AllowAll bool
}

type AccessService struct {
	registry             *DeviceRegistry
	policy               AccessPolicy
	eventStore           store.AccessEventStore
	memberAccessStore    store.MemberAccessStore
	moduleAuthStore      store.ModuleAuthorizationStore
	credentialHashSecret []byte
	logger               *log.Logger
	audit                auditState
}

func NewAccessService(reg *DeviceRegistry, policy AccessPolicy, es store.AccessEventStore) *AccessService {
	return &AccessService{registry: reg, policy: policy, eventStore: es}
}

// SetLogger attaches a logger for audit failure warnings. When nil (the default),
// audit failures are tracked in memory but not logged.
func (s *AccessService) SetLogger(l *log.Logger) { s.logger = l }

// AuditHealth returns a point-in-time snapshot of audit write health.
// ConsecutiveFailures resets to zero on any successful write.
func (s *AccessService) AuditHealth() AuditHealthSnapshot {
	s.audit.mu.Lock()
	defer s.audit.mu.Unlock()
	snap := AuditHealthSnapshot{
		ConsecutiveFailures: s.audit.consecutiveFailures,
		TotalFailures:       s.audit.totalFailures,
	}
	if !s.audit.lastFailure.IsZero() {
		t := s.audit.lastFailure
		snap.LastFailure = &t
	}
	if !s.audit.lastSuccess.IsZero() {
		t := s.audit.lastSuccess
		snap.LastSuccess = &t
	}
	return snap
}

// SetMemberAccessStore wires the member_access store required for access decisions.
// Must be called (along with SetModuleAuthStore) before calling Validate.
func (s *AccessService) SetMemberAccessStore(ms store.MemberAccessStore) {
	s.memberAccessStore = ms
}

// SetModuleAuthStore enables the module authorization check in access decisions.
func (s *AccessService) SetModuleAuthStore(mas store.ModuleAuthorizationStore) {
	s.moduleAuthStore = mas
}

// SetCredentialHashSecret sets the HMAC key used to hash credential IDs. Must
// match the key used at registration time. Call after NewAccessService, before serving traffic.
func (s *AccessService) SetCredentialHashSecret(secret []byte) {
	s.credentialHashSecret = secret
}

// Validate returns an error if the service is not fully wired for production use.
// Call this after all Set* calls and before serving traffic; treat a non-nil return
// as a fatal configuration error.  AllowAll bypasses the check (dev/test only).
func (s *AccessService) Validate() error {
	if s.policy.AllowAll {
		return nil
	}
	if s.memberAccessStore == nil {
		return errors.New("access service: SetMemberAccessStore was not called")
	}
	if s.moduleAuthStore == nil {
		return errors.New("access service: SetModuleAuthStore was not called")
	}
	return nil
}

func (s *AccessService) Decide(ctx context.Context, req types.AccessRequest) (types.AccessResponse, error) {
	now := time.Now().UTC()

	moduleID := strings.TrimSpace(req.ModuleID)
	credentialID := strings.TrimSpace(req.CredentialID)

	if moduleID == "" {
		return types.AccessResponse{}, ErrInvalidModuleID
	}
	if credentialID == "" {
		return types.AccessResponse{}, ErrInvalidCredentialID
	}

	known, err := s.registry.IsKnown(ctx, moduleID)
	if err != nil {
		return types.AccessResponse{}, err
	}
	_ = s.registry.NoteSeen(ctx, moduleID, known)

	if !known {
		resp := types.AccessResponse{
			OK:         false,
			Known:      false,
			Granted:    false,
			Reason:     "unknown_module",
			ModuleID:   moduleID,
			ServerTime: now.Format(time.RFC3339Nano),
		}

		s.recordEvent(ctx, req, false, "unknown_module", now)

		return resp, nil
	}

	// Decision logic
	granted := false
	reason := "denied"
	var grantedMemberUUID string

	if s.policy.AllowAll {
		granted = true
		reason = "allow_all"
	} else if s.memberAccessStore != nil && s.moduleAuthStore != nil {
		// Member access + module authorization path (production path).
		rawUID, parseErr := ParseCredentialUID(credentialID)
		if parseErr != nil {
			s.recordEvent(ctx, req, false, "invalid_credential_format", now)
			return types.AccessResponse{
				OK:         true,
				Known:      true,
				Granted:    false,
				Reason:     "invalid_credential_format",
				ModuleID:   moduleID,
				ServerTime: now.Format(time.RFC3339Nano),
			}, nil
		}
		credHash := HashCredentialID(rawUID, s.credentialHashSecret)
		g, r, memberUUID, err := s.decideMemberAccess(ctx, credHash, moduleID, now)
		if err != nil {
			s.recordEvent(ctx, req, false, "member_lookup_error", now)
			return types.AccessResponse{}, err
		}
		granted, reason, grantedMemberUUID = g, r, memberUUID
	} else {
		// Both member-access and module-auth stores are required; reaching here means
		// Validate() was not called (or was ignored) before serving traffic.
		// Deny and surface as an internal error — never silently fall through to a
		// credential-only path that skips per-module authorization.
		s.recordEvent(ctx, req, false, "service_misconfigured", now)
		return types.AccessResponse{}, errors.New("access service: not fully wired — call Validate() before serving")
	}

	if granted && grantedMemberUUID != "" {
		_ = s.memberAccessStore.UpdateLastAccess(ctx, grantedMemberUUID, now)
	}

	s.recordEvent(ctx, req, granted, reason, now)

	return types.AccessResponse{
		OK:         true,
		Known:      true,
		Granted:    granted,
		Reason:     reason,
		ModuleID:   moduleID,
		ServerTime: now.Format(time.RFC3339Nano),
	}, nil
}

// recordEvent persists the access decision to the audit log.
// Errors are not returned to the caller — a failed write must not block the
// access decision reaching the device (availability-first policy).
// Failures are counted and logged so operators can detect a degraded audit state
// via GET /admin/v1/health without the system silently losing events.
func (s *AccessService) recordEvent(
	ctx context.Context,
	req types.AccessRequest,
	granted bool,
	reason string,
	decidedAt time.Time,
) {
	rec := store.AccessEventRecord{
		ModuleID:   strings.TrimSpace(req.ModuleID),
		ReceivedAt: decidedAt,
		DoorClosed: req.DoorClosed,
		Granted:    granted,
		Reason:     reason,
		DecidedAt:  decidedAt,
	}

	if t := parseOptionalTimestamp(req.RequestedAt); t != nil {
		rec.RequestedAt = t
	}

	credentialID := strings.TrimSpace(req.CredentialID)
	if credentialID != "" {
		if rawUID, err := ParseCredentialUID(credentialID); err == nil {
			rec.CredentialHash = HashCredentialID(rawUID, s.credentialHashSecret)
		}
	}

	if err := s.eventStore.RecordEvent(ctx, rec); err != nil {
		s.audit.mu.Lock()
		s.audit.consecutiveFailures++
		s.audit.totalFailures++
		s.audit.lastFailure = time.Now().UTC()
		consecutive := s.audit.consecutiveFailures
		total := s.audit.totalFailures
		s.audit.mu.Unlock()
		if s.logger != nil {
			s.logger.Printf("WARN audit write failed (consecutive=%d total=%d): %v", consecutive, total, err)
		}
		return
	}

	s.audit.mu.Lock()
	s.audit.consecutiveFailures = 0
	s.audit.lastSuccess = time.Now().UTC()
	s.audit.mu.Unlock()
}

// ListEventsByCredential returns recent access events for the given credential
// hash, newest-first, capped at limit rows. Used by the member detail UI.
func (s *AccessService) ListEventsByCredential(ctx context.Context, credentialHash []byte, limit int) ([]store.AccessEventRecord, error) {
	return s.eventStore.ListEventsByCredential(ctx, credentialHash, limit)
}

// decideMemberAccess checks member_access + module_authorizations and returns
// (granted, reason, memberUUID, error). memberUUID is non-empty only on grant.
func (s *AccessService) decideMemberAccess(
	ctx context.Context,
	credHash []byte,
	moduleID string,
	now time.Time,
) (granted bool, reason string, memberUUID string, err error) {
	member, err := s.memberAccessStore.GetMemberByCredential(ctx, credHash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, "credential_not_found", "", nil
		}
		return false, "", "", err
	}

	if member.Status != store.MemberStatusActive {
		return false, "member_" + string(member.Status), "", nil
	}
	if !member.Enabled {
		return false, "member_disabled", "", nil
	}

	auth, err := s.moduleAuthStore.GetAuthorization(ctx, member.UUID, moduleID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, "credential_not_authorized", "", nil
		}
		return false, "", "", err
	}

	if auth.RevokedAt != nil {
		return false, "authorization_revoked", "", nil
	}
	if auth.ExpiresAt != nil && auth.ExpiresAt.Before(now) {
		return false, "authorization_expired", "", nil
	}

	return true, "credential_allowed", member.UUID, nil
}

// parseOptionalTimestamp attempts to parse a device-reported timestamp.
// Returns nil if the string is empty or unparseable.
func parseOptionalTimestamp(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		return &u
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		u := t.UTC()
		return &u
	}
	return nil
}
