package config

import (
	"reflect"
	"strings"
	"testing"
)

// clearEnv blanks every PORTUNUS_ variable FromEnv reads so the host
// environment cannot leak into a test.
func clearEnv(t *testing.T) {
	for _, k := range []string{
		"PORTUNUS_ENV", "PORTUNUS_HTTP_ADDR", "PORTUNUS_GRPC_ADDR", "PORTUNUS_DB_PATH",
		"PORTUNUS_KNOWN_MODULES", "PORTUNUS_ALLOW_ALL", "PORTUNUS_HEARTBEAT_RETENTION_DAYS",
		"PORTUNUS_PRUNE_INTERVAL_HOURS", "PORTUNUS_EXPIRY_WORKER_INTERVAL_MINUTES",
		"PORTUNUS_PENDING_TTL_DAYS", "PORTUNUS_TLS_CERT_FILE", "PORTUNUS_TLS_KEY_FILE",
		"PORTUNUS_HMAC_SECRET", "PORTUNUS_CREDENTIAL_HASH_SECRET", "PORTUNUS_ALLOW_UNKEYED_CREDENTIAL_HASH",
	} {
		t.Setenv(k, "")
	}
}

func TestFromEnv_Defaults(t *testing.T) {
	clearEnv(t)
	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		HTTPAddr: ":8080", Env: EnvLocal, DBPath: "./data/portunus.db",
		HeartbeatRetentionDays: 30, PruneIntervalHours: 6, ExpiryWorkerIntervalMinutes: 60, PendingTTLDays: 7,
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("defaults:\n got %+v\nwant %+v", cfg, want)
	}
	if cfg.AllowAll || cfg.AllowUnkeyedCredentialHash {
		t.Fatal("a bypass flag defaults to on")
	}
}

func TestFromEnv_ReadsValues(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORTUNUS_ENV", "prod")
	t.Setenv("PORTUNUS_HTTP_ADDR", ":8443")
	t.Setenv("PORTUNUS_GRPC_ADDR", "  :50051 ")
	t.Setenv("PORTUNUS_DB_PATH", "/var/lib/portunus/p.db")
	t.Setenv("PORTUNUS_KNOWN_MODULES", " door-1, ,door-2 ,, ")
	t.Setenv("PORTUNUS_PENDING_TTL_DAYS", "0")
	t.Setenv("PORTUNUS_TLS_CERT_FILE", " /etc/c.pem ")
	t.Setenv("PORTUNUS_HMAC_SECRET", "h")
	t.Setenv("PORTUNUS_CREDENTIAL_HASH_SECRET", "x")

	cfg, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != EnvProd || cfg.HTTPAddr != ":8443" || cfg.GRPCAddr != ":50051" || cfg.DBPath != "/var/lib/portunus/p.db" ||
		cfg.TLSCertFile != "/etc/c.pem" || cfg.HMACSecret != "h" || cfg.CredentialHashSecret != "x" {
		t.Fatalf("values not read: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.KnownModules, []string{"door-1", "door-2"}) {
		t.Errorf("KnownModules = %q", cfg.KnownModules)
	}
	if cfg.PendingTTLDays != 0 {
		t.Errorf("PendingTTLDays = %d, want 0 (explicit zero disables the sweep)", cfg.PendingTTLDays)
	}
}

func TestFromEnv_RejectsUnknownProfile(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORTUNUS_ENV", "dev")
	if _, err := FromEnv(); err == nil {
		t.Fatal(`PORTUNUS_ENV=dev accepted; "dev" is rejected at startup`)
	}
}

// Unparseable or negative integers fall back to the default.
func TestFromEnv_BadIntegersFallBack(t *testing.T) {
	for _, v := range []string{"abc", "-5", "3.5", "1e3"} {
		clearEnv(t)
		t.Setenv("PORTUNUS_HEARTBEAT_RETENTION_DAYS", v)
		cfg, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.HeartbeatRetentionDays != 30 {
			t.Errorf("%q: HeartbeatRetentionDays = %d, want default 30", v, cfg.HeartbeatRetentionDays)
		}
	}
}

// The allow-all switch bypasses credential checks entirely, so only an
// explicit "true" or "1" turns it on.
func TestFromEnv_BypassFlagsNeedExplicitTrue(t *testing.T) {
	cases := map[string]bool{"true": true, "TRUE": true, "1": true, "yes": false, "on": false, "0": false, "false": false, " true": false}
	for v, want := range cases {
		clearEnv(t)
		t.Setenv("PORTUNUS_ALLOW_ALL", v)
		t.Setenv("PORTUNUS_ALLOW_UNKEYED_CREDENTIAL_HASH", v)
		cfg, err := FromEnv()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AllowAll != want || cfg.AllowUnkeyedCredentialHash != want {
			t.Errorf("%q: AllowAll=%v AllowUnkeyed=%v, want %v", v, cfg.AllowAll, cfg.AllowUnkeyedCredentialHash, want)
		}
	}
}

func TestValidateProd_ReportsEveryProblem(t *testing.T) {
	err := (Config{Env: EnvProd, AllowAll: true}).Validate()
	if err == nil {
		t.Fatal("unsafe prod config accepted")
	}
	for _, want := range []string{"TLS", "HMAC", "ALLOW_ALL", "credential hash secret"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
	if err := (Config{Env: EnvProd, TLSCertFile: "c", HMACSecret: "h", CredentialHashSecret: "x"}).Validate(); err == nil {
		t.Error("prod with a cert but no key accepted")
	}
}
