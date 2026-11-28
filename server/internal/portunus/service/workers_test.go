package service_test

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/service"
	"github.com/BrandonDHaskell/Portunus/server/internal/portunus/store"
	sqlitestore "github.com/BrandonDHaskell/Portunus/server/internal/portunus/store/sqlite"
)

// syncBuffer is a log sink safe to read while a worker goroutine writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// eventually polls cond until it holds or the deadline passes. Workers run
// their first pass asynchronously on Start.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSessionSweeper_RemovesOnlyExpiredSessions(t *testing.T) {
	conn, w := openSvcTestDB(t)
	users := sqlitestore.NewAdminUserStore(conn, w)
	sessions := sqlitestore.NewSessionStore(conn, w)
	ctx := context.Background()
	if err := users.CreateAdminUser(ctx, "u1", "user1", "x", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.CreateSession(ctx, "old", "u1", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := sessions.CreateSession(ctx, "live", "u1", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	logs := &syncBuffer{}
	sw := service.NewSessionSweeper(sessions, time.Hour, log.New(logs, "", 0))
	sw.Start(ctx)
	eventually(t, "expired session removed", func() bool {
		_, err := sessions.GetSession(ctx, "old")
		return errors.Is(err, store.ErrNotFound)
	})
	sw.Stop()

	if _, err := sessions.GetSession(ctx, "live"); err != nil {
		t.Fatalf("live session removed: %v", err)
	}
	if !strings.Contains(logs.String(), "session sweeper started (interval=1h0m0s)") {
		t.Errorf("start not logged: %q", logs.String())
	}
}

type failingSessionStore struct{ store.SessionStore }

func (failingSessionStore) DeleteExpiredSessions(context.Context) error { return errBoom }

func TestSessionSweeper_LogsStoreErrors(t *testing.T) {
	logs := &syncBuffer{}
	sw := service.NewSessionSweeper(failingSessionStore{}, 0, log.New(logs, "", 0))
	sw.Start(context.Background())
	eventually(t, "sweep error logged", func() bool { return strings.Contains(logs.String(), "session sweep error: boom") })
	sw.Stop()
	if !strings.Contains(logs.String(), "interval=1h0m0s") {
		t.Errorf("interval <= 0 did not default to 1h: %q", logs.String())
	}
}

type failingHeartbeatStore struct{ store.HeartbeatStore }

func (failingHeartbeatStore) PruneOlderThan(context.Context, time.Time) (int64, error) {
	return 0, errBoom
}

func TestHeartbeatPruner_LogsStoreErrors(t *testing.T) {
	logs := &syncBuffer{}
	p := service.NewHeartbeatPruner(failingHeartbeatStore{}, service.PrunerConfig{RetentionDays: 7, IntervalHours: 1}, log.New(logs, "", 0))
	p.Start(context.Background())
	eventually(t, "prune error logged", func() bool { return strings.Contains(logs.String(), "heartbeat prune error: boom") })
	p.Stop()
}

// expiryStub fails the hard-deadline step and counts how often later steps
// still run.
type expiryStub struct {
	store.MemberAccessStore
	inactivityCalls, staleCalls atomic.Int32
	archived                    int64
}

func (*expiryStub) ExpireByHardDeadline(context.Context, time.Time) (int, error) { return 0, errBoom }

func (s *expiryStub) ExpireByInactivity(context.Context, time.Time) (int, error) {
	s.inactivityCalls.Add(1)
	return 0, nil
}

func (s *expiryStub) ArchiveStalePending(context.Context, time.Time, int) (int64, error) {
	s.staleCalls.Add(1)
	return s.archived, nil
}

type failingAuditStore struct{ store.AuditStore }

func (failingAuditStore) RecordAuditEntry(context.Context, store.AuditEntry) error { return errBoom }

// One failing step must not stop the others: an inactivity sweep that never
// runs leaves members active past their policy.
func TestExpiryWorker_FailedStepDoesNotSkipTheRest(t *testing.T) {
	stub := &expiryStub{archived: 2}
	logs := &syncBuffer{}
	ew := service.NewExpiryWorker(stub, failingAuditStore{}, service.ExpiryWorkerConfig{IntervalMinutes: 60, PendingTTLDays: 7}, log.New(logs, "", 0))
	ew.Start(context.Background())
	eventually(t, "first sweep finished", func() bool { return strings.Contains(logs.String(), "audit write error") })
	ew.Stop()

	if stub.inactivityCalls.Load() == 0 || stub.staleCalls.Load() == 0 {
		t.Fatalf("later steps skipped after the hard-deadline error: inactivity=%d stale=%d", stub.inactivityCalls.Load(), stub.staleCalls.Load())
	}
	out := logs.String()
	for _, want := range []string{"hard-deadline sweep error: boom", "archived 2 stale pending member(s)", "audit write error: boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
}

// Each background worker must tolerate Stop without Start, Stop twice, and a
// second Start, so shutdown code cannot hang or panic on an unexpected order.
func TestWorkers_StartStopOrderingIsSafe(t *testing.T) {
	quiet := log.New(&syncBuffer{}, "", 0)
	type worker interface {
		Start(context.Context)
		Stop()
	}
	makers := map[string]func() worker{
		"session sweeper": func() worker { return service.NewSessionSweeper(failingSessionStore{}, time.Hour, quiet) },
		"heartbeat pruner": func() worker {
			return service.NewHeartbeatPruner(failingHeartbeatStore{}, service.PrunerConfig{RetentionDays: 7}, quiet)
		},
		"heartbeat pruner (disabled)": func() worker {
			return service.NewHeartbeatPruner(failingHeartbeatStore{}, service.PrunerConfig{RetentionDays: 0}, quiet)
		},
		"expiry worker": func() worker {
			return service.NewExpiryWorker(&expiryStub{}, nil, service.ExpiryWorkerConfig{}, quiet)
		},
	}
	within := func(t *testing.T, what string, f func()) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked: %v", what, r)
				}
			}()
			f()
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s hung", what)
		}
	}
	for name, mk := range makers {
		t.Run(name, func(t *testing.T) {
			within(t, "Stop without Start", func() { mk().Stop() })
			w := mk()
			within(t, "Start twice, Stop twice", func() {
				w.Start(context.Background())
				w.Start(context.Background())
				w.Stop()
				w.Stop()
			})
		})
	}
}
