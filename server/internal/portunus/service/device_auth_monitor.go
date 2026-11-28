package service

import (
	"log"
	"sort"
	"sync"
	"time"
)

// Device signature failure reasons.
const (
	ReasonMissingSignature = "missing_signature"
	ReasonBadSignature     = "bad_signature"
)

// MaxTrackedDevices caps how many module ids the monitor remembers. Ids come
// from unauthenticated requests, so without a cap forged ids could grow
// memory without bound; the least recently seen id is evicted.
const MaxTrackedDevices = 256

const maxModuleIDLen = 64

// DeviceAuthFailure summarizes signature failures for one claimed module id.
type DeviceAuthFailure struct {
	ModuleID      string    `json:"module_id"`
	Count         int64     `json:"count"`
	FirstAt       time.Time `json:"first_at"`
	LastAt        time.Time `json:"last_at"`
	LastPeer      string    `json:"last_peer"`
	LastTransport string    `json:"last_transport"`
	LastReason    string    `json:"last_reason"`
}

// DeviceAuthMonitor tracks device requests whose HMAC signature was missing
// or did not verify, per claimed module id. A module whose NVS hmac_secret no
// longer matches the server shows up here (and in the log) instead of failing
// silently. Both transports share one monitor. A nil monitor is a no-op.
type DeviceAuthMonitor struct {
	mu      sync.Mutex
	logger  *log.Logger
	entries map[string]*DeviceAuthFailure
	lastSeq map[string]uint64 // recency order for eviction; clock ticks can tie
	seq     uint64
	now     func() time.Time
}

func NewDeviceAuthMonitor(logger *log.Logger) *DeviceAuthMonitor {
	return &DeviceAuthMonitor{
		logger:  logger,
		entries: make(map[string]*DeviceAuthFailure),
		lastSeq: make(map[string]uint64),
		now:     time.Now,
	}
}

// RecordFailure notes one failed request. moduleID is the id the request
// claimed, unverified; empty becomes "unknown". Neither the secret nor the
// received signature is ever logged.
func (m *DeviceAuthMonitor) RecordFailure(moduleID, peer, transport, reason string) {
	if m == nil {
		return
	}
	if moduleID == "" {
		moduleID = "unknown"
	}
	if len(moduleID) > maxModuleIDLen {
		moduleID = moduleID[:maxModuleIDLen]
	}
	now := m.now().UTC()

	m.mu.Lock()
	f, ok := m.entries[moduleID]
	if !ok {
		if len(m.entries) >= MaxTrackedDevices {
			m.evictOldest()
		}
		f = &DeviceAuthFailure{ModuleID: moduleID, FirstAt: now}
		m.entries[moduleID] = f
	}
	m.seq++
	m.lastSeq[moduleID] = m.seq
	f.Count++
	f.LastAt = now
	f.LastPeer = peer
	f.LastTransport = transport
	f.LastReason = reason
	count := f.Count
	m.mu.Unlock()

	if m.logger != nil {
		m.logger.Printf("WARN device auth failure module=%q peer=%s transport=%s reason=%s count=%d (check the module's NVS hmac_secret)",
			moduleID, peer, transport, reason, count)
	}
}

// evictOldest drops the least recently seen entry. Must hold m.mu.
func (m *DeviceAuthMonitor) evictOldest() {
	var oldest string
	var min uint64
	for id, seq := range m.lastSeq {
		if oldest == "" || seq < min {
			oldest, min = id, seq
		}
	}
	delete(m.entries, oldest)
	delete(m.lastSeq, oldest)
}

// ForModule returns the failure summary for one module id.
func (m *DeviceAuthMonitor) ForModule(moduleID string) (DeviceAuthFailure, bool) {
	if m == nil {
		return DeviceAuthFailure{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.entries[moduleID]
	if !ok {
		return DeviceAuthFailure{}, false
	}
	return *f, true
}

// Snapshot returns every tracked failure, most recent first.
func (m *DeviceAuthMonitor) Snapshot() []DeviceAuthFailure {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	out := make([]DeviceAuthFailure, 0, len(m.entries))
	for _, f := range m.entries {
		out = append(out, *f)
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out
}
