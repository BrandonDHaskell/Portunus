package store

import (
	"context"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

type HeartbeatRecord struct {
	ReceivedAt time.Time
	Request    types.HeartbeatRequest
}

type HeartbeatStore interface {
	UpsertHeartbeat(ctx context.Context, moduleID string, rec HeartbeatRecord) error
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
