package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
)

type requestIPKey struct{}

// WithRequestIP attaches the client address to ctx so audit entries written
// deeper in the service layer record where an action came from. The HTTP
// transport sets it once per request.
func WithRequestIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, requestIPKey{}, ip)
}

func requestIP(ctx context.Context) string {
	ip, _ := ctx.Value(requestIPKey{}).(string)
	return ip
}

// writeAudit records one audit_log entry. It is best-effort: a failed write
// must never fail the operation being audited. It fills in what callers
// leave blank: the actor type (system when there is no actor), the client
// address from ctx, and a success result.
func writeAudit(ctx context.Context, as store.AuditStore, e store.AuditEntry) {
	if as == nil {
		return
	}
	if e.ActorType == "" {
		e.ActorType = store.ActorTypeAdmin
		if e.ActorUUID == "" {
			e.ActorType = store.ActorTypeSystem
		}
	}
	if e.IPAddress == "" {
		e.IPAddress = requestIP(ctx)
	}
	if e.Result == "" {
		e.Result = "success"
	}
	_ = as.RecordAuditEntry(ctx, e)
}

// auditDetails encodes audit details as JSON. Values are escaped properly,
// unlike hand-built format strings.
func auditDetails(kv map[string]any) string {
	if len(kv) == 0 {
		return ""
	}
	b, err := json.Marshal(kv)
	if err != nil {
		return ""
	}
	return string(b)
}

// auditReason pairs a sentinel error with the reason recorded for it.
type auditReason struct {
	err    error
	reason string
}

// resultOf maps an operation's error to an audit result and, on failure, a
// short machine-readable reason. Reasons are checked in order, so the first
// matching sentinel wins.
func resultOf(err error, reasons ...auditReason) (result, reason string) {
	if err == nil {
		return "success", ""
	}
	for _, r := range reasons {
		if errors.Is(err, r.err) {
			return "failure", r.reason
		}
	}
	return "failure", "error"
}
