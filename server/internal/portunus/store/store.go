package store

import (
	"context"
	"errors"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/types"
)

// ErrNotFound is returned by any store method that looks up a single record and finds none.
var ErrNotFound = errors.New("record not found")

type HeartbeatRecord struct {
	ReceivedAt time.Time
	Request    types.HeartbeatRequest
}

type HeartbeatStore interface {
	UpsertHeartbeat(ctx context.Context, moduleID string, rec HeartbeatRecord) error
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
