package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr string
	GRPCAddr string // e.g. ":50051" — empty means gRPC listener disabled

	// DB
	Env    string // "dev" | "prod"
	DBPath string // e.g. "./data/portunus.db"

	KnownModules         []string
	AllowAll             bool
	AllowedCredentialIDs []string

	// Heartbeat retention
	HeartbeatRetentionDays int // 0 = keep forever
	PruneIntervalHours     int // how often the pruner runs (default 6)

	// Expiry worker
	ExpiryWorkerIntervalMinutes int // how often member expiry sweeps run (default 60)

	// TLS
	// When TLSCertFile and TLSKeyFile are both set, the server starts in
	// HTTPS mode using ListenAndServeTLS.  Leave both empty to use plain
	// HTTP (development only).
	TLSCertFile string
	TLSKeyFile  string

	// HMAC request authentication
	// When non-empty, every inbound POST must include an X-Portunus-Sig
	// header containing HMAC-SHA256(HMACSecret, request_body).  Requests
	// with missing or invalid signatures are rejected with 401.
	// Set to the same value as CONFIG_PORTUNUS_HMAC_SECRET in the firmware.
	HMACSecret string

	// Credential hash secret for keyed HMAC-SHA256 credential ID hashing.
	// When set, credential IDs are hashed with HMAC-SHA256(secret, credentialID)
	// instead of bare SHA-256, preventing rainbow-table attacks on a stolen database.
	// Generate with: openssl rand -hex 32
	// Required in prod mode.
	CredentialHashSecret string
}

// Validate returns an error if the config is unsafe for prod mode.
// In dev mode it always returns nil.
func (c Config) Validate() error {
	if c.Env != "prod" {
		return nil
	}

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
}

// FromEnv reads configuration from environment variables.
// It returns an error if PORTUNUS_ENV is set to an unrecognised value.
// Valid values are "dev", "test", and "prod".
func FromEnv() (Config, error) {
	addr := getenvDefault("PORTUNUS_HTTP_ADDR", ":8080")

	env := strings.ToLower(getenvDefault("PORTUNUS_ENV", "dev"))
	if env != "dev" && env != "test" && env != "prod" {
		return Config{}, fmt.Errorf("PORTUNUS_ENV %q is not valid: must be one of: dev, test, prod", env)
	}

	dbPath := getenvDefault("PORTUNUS_DB_PATH", "./data/portunus.db")

	knownModules := splitCSV(os.Getenv("PORTUNUS_KNOWN_MODULES"))
	allowedCredentials := splitCSV(os.Getenv("PORTUNUS_ALLOWED_CREDENTIAL_IDS"))

	allowAll := strings.EqualFold(os.Getenv("PORTUNUS_ALLOW_ALL"), "true") ||
		os.Getenv("PORTUNUS_ALLOW_ALL") == "1"

	retentionDays := getenvInt("PORTUNUS_HEARTBEAT_RETENTION_DAYS", 30)
	pruneInterval := getenvInt("PORTUNUS_PRUNE_INTERVAL_HOURS", 6)
	expiryInterval := getenvInt("PORTUNUS_EXPIRY_WORKER_INTERVAL_MINUTES", 60)

	tlsCert := strings.TrimSpace(os.Getenv("PORTUNUS_TLS_CERT_FILE"))
	tlsKey := strings.TrimSpace(os.Getenv("PORTUNUS_TLS_KEY_FILE"))
	hmacSecret := os.Getenv("PORTUNUS_HMAC_SECRET")
	grpcAddr := strings.TrimSpace(os.Getenv("PORTUNUS_GRPC_ADDR"))
	credentialHashSecret := os.Getenv("PORTUNUS_CREDENTIAL_HASH_SECRET")

	return Config{
		HTTPAddr: addr,
		GRPCAddr: grpcAddr,
		Env:      env,
		DBPath:   dbPath,

		KnownModules:         knownModules,
		AllowAll:             allowAll,
		AllowedCredentialIDs: allowedCredentials,

		HeartbeatRetentionDays:      retentionDays,
		PruneIntervalHours:          pruneInterval,
		ExpiryWorkerIntervalMinutes: expiryInterval,

		TLSCertFile:          tlsCert,
		TLSKeyFile:           tlsKey,
		HMACSecret:           hmacSecret,
		CredentialHashSecret: credentialHashSecret,
	}, nil
}

func getenvDefault(key, def string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func getenvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
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
