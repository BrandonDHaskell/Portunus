package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Env is the server runtime profile. It selects validation strictness and a
// few runtime behaviours (dev seeding, gRPC reflection). It is NOT the firmware
// profile, which is a separate client-side axis.
type Env string

const (
	// EnvLocal is the localhost iteration profile: relaxed, plain HTTP by
	// default, dev data seeded. Formerly "dev".
	EnvLocal Env = "local"
	// EnvCI exercises the full TLS + gRPC transport against ephemeral,
	// no-secret material so CI has parity with the firmware ci profile.
	// Formerly "test".
	EnvCI Env = "ci"
	// EnvProd is the hardened deployment profile. Validate() rejects an
	// unsafe prod boot.
	EnvProd Env = "prod"
)

func parseEnv(s string) (Env, error) {
	switch Env(strings.ToLower(strings.TrimSpace(s))) {
	case EnvLocal:
		return EnvLocal, nil
	case EnvCI:
		return EnvCI, nil
	case EnvProd:
		return EnvProd, nil
	default:
		return "", fmt.Errorf("PORTUNUS_ENV %q is not valid: must be one of: local, ci, prod", s)
	}
}

type Config struct {
	HTTPAddr string
	GRPCAddr string // e.g. ":50051"; empty means the gRPC listener is disabled

	// DB
	Env    Env
	DBPath string // e.g. "./data/portunus.db"; ":memory:" for an ephemeral DB

	KnownModules []string
	AllowAll     bool

	// Heartbeat retention
	HeartbeatRetentionDays int // 0 = keep forever
	PruneIntervalHours     int // how often the pruner runs (default 6)

	// Expiry worker
	ExpiryWorkerIntervalMinutes int // how often member expiry sweeps run (default 60)
	PendingTTLDays              int // how many days before stale pending rows are archived; 0 disables (default 7)

	// TLS. When both files are set, the server serves HTTPS using them.
	// When unset under the ci profile, the server generates an ephemeral
	// self-signed cert in-process (see EphemeralCert). Under local, unset
	// means plain HTTP.
	TLSCertFile string
	TLSKeyFile  string

	// HMAC request authentication. When non-empty, inbound device POSTs must
	// carry a valid X-Portunus-Sig. Same value as CONFIG_PORTUNUS_HMAC_SECRET
	// in the firmware.
	HMACSecret string

	// CredentialHashSecret keys the HMAC-SHA256 credential-ID hashing.
	// Generate with: openssl rand -hex 32. Required in prod.
	CredentialHashSecret string

	// AllowUnkeyedCredentialHash permits starting without a CredentialHashSecret
	// (resulting in reversible SHA-256 hashes).  Dev/test escape hatch only —
	// never set in production.  Set PORTUNUS_ALLOW_UNKEYED_CREDENTIAL_HASH=true.
	AllowUnkeyedCredentialHash bool
}

// Validate enforces per-profile invariants.
//   - prod: rejects any unsafe configuration (no TLS, no HMAC, AllowAll, no
//     credential hash secret).
//   - ci: requires the gRPC listener so CI actually exercises the transport.
//     ci provides its own TLS cert in-process, so no secret material is required.
//   - local: always valid.
func (c Config) Validate() error {
	switch c.Env {
	case EnvProd:
		var errs []error
		if c.TLSCertFile == "" || c.TLSKeyFile == "" {
			errs = append(errs, errors.New("prod requires TLS: set PORTUNUS_TLS_CERT_FILE and PORTUNUS_TLS_KEY_FILE"))
		}
		if c.HMACSecret == "" {
			errs = append(errs, errors.New("prod requires HMAC auth: set PORTUNUS_HMAC_SECRET"))
		}
		if c.AllowAll {
			errs = append(errs, errors.New("prod forbids PORTUNUS_ALLOW_ALL=true"))
		}
		if c.CredentialHashSecret == "" {
			errs = append(errs, errors.New("prod requires a credential hash secret: set PORTUNUS_CREDENTIAL_HASH_SECRET"))
		}
		return errors.Join(errs...)
	case EnvCI:
		if c.GRPCAddr == "" {
			return errors.New("ci requires the gRPC listener: set PORTUNUS_GRPC_ADDR (e.g. :50051) so CI exercises the gRPC transport")
		}
		return nil
	default:
		return nil
	}
}

// FromEnv reads configuration from environment variables.
// PORTUNUS_ENV defaults to "local"; an unrecognised value is an error.
//
// An unset or empty variable takes its default. A variable that is set but
// cannot be parsed as its type is an error: silently falling back to the
// default would hide a misconfiguration. Every bad variable is reported.
func FromEnv() (Config, error) {
	env, err := parseEnv(getenvDefault("PORTUNUS_ENV", string(EnvLocal)))
	if err != nil {
		return Config{}, err
	}

	var p envParser
	cfg := Config{
		HTTPAddr: getenvDefault("PORTUNUS_HTTP_ADDR", ":8080"),
		GRPCAddr: strings.TrimSpace(os.Getenv("PORTUNUS_GRPC_ADDR")),
		Env:      env,
		DBPath:   getenvDefault("PORTUNUS_DB_PATH", "./data/portunus.db"),

		KnownModules: splitCSV(os.Getenv("PORTUNUS_KNOWN_MODULES")),
		AllowAll:     p.bool("PORTUNUS_ALLOW_ALL"),

		HeartbeatRetentionDays:      p.int("PORTUNUS_HEARTBEAT_RETENTION_DAYS", 30),
		PruneIntervalHours:          p.int("PORTUNUS_PRUNE_INTERVAL_HOURS", 6),
		ExpiryWorkerIntervalMinutes: p.int("PORTUNUS_EXPIRY_WORKER_INTERVAL_MINUTES", 60),
		PendingTTLDays:              p.int("PORTUNUS_PENDING_TTL_DAYS", 7),

		TLSCertFile:                strings.TrimSpace(os.Getenv("PORTUNUS_TLS_CERT_FILE")),
		TLSKeyFile:                 strings.TrimSpace(os.Getenv("PORTUNUS_TLS_KEY_FILE")),
		HMACSecret:                 os.Getenv("PORTUNUS_HMAC_SECRET"),
		CredentialHashSecret:       os.Getenv("PORTUNUS_CREDENTIAL_HASH_SECRET"),
		AllowUnkeyedCredentialHash: p.bool("PORTUNUS_ALLOW_UNKEYED_CREDENTIAL_HASH"),
	}
	if len(p.errs) > 0 {
		return Config{}, errors.Join(p.errs...)
	}
	return cfg, nil
}

func getenvDefault(key, def string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

// envParser reads typed variables and collects every parse error, so one
// startup reports all misconfigured variables at once. Only non-secret
// variables go through it, so echoing the bad value is safe.
type envParser struct{ errs []error }

// int reads a non-negative base-10 integer, or def when unset.
func (p *envParser) int(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		p.errs = append(p.errs, fmt.Errorf("%s=%q: must be a non-negative whole number", key, v))
		return def
	}
	return n
}

// bool reads true/false/1/0 (case-insensitive), or false when unset.
func (p *envParser) bool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "":
		return false
	case "true", "1":
		return true
	case "false", "0":
		return false
	}
	p.errs = append(p.errs, fmt.Errorf("%s=%q: must be true, false, 1 or 0", key, v))
	return false
}

func splitCSV(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
