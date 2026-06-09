package config

import "testing"

func TestParseEnvRejectsLegacyNames(t *testing.T) {
	for _, bad := range []string{"dev", "test", "staging", ""} {
		if _, err := parseEnv(bad); err == nil {
			t.Errorf("parseEnv(%q) = nil error, want error", bad)
		}
	}
	for _, good := range []string{"local", "CI", " prod "} {
		if _, err := parseEnv(good); err != nil {
			t.Errorf("parseEnv(%q) returned error: %v", good, err)
		}
	}
}

func TestValidateProd(t *testing.T) {
	ok := Config{Env: EnvProd, TLSCertFile: "c", TLSKeyFile: "k", HMACSecret: "h", CredentialHashSecret: "x"}
	if err := ok.Validate(); err != nil {
		t.Errorf("complete prod config rejected: %v", err)
	}
	if err := (Config{Env: EnvProd}).Validate(); err == nil {
		t.Error("empty prod config accepted, want error")
	}
	if err := (Config{Env: EnvProd, TLSCertFile: "c", TLSKeyFile: "k", HMACSecret: "h", CredentialHashSecret: "x", AllowAll: true}).Validate(); err == nil {
		t.Error("prod with AllowAll accepted, want error")
	}
}

func TestValidateCIRequiresGRPC(t *testing.T) {
	if err := (Config{Env: EnvCI}).Validate(); err == nil {
		t.Error("ci without GRPCAddr accepted, want error")
	}
	if err := (Config{Env: EnvCI, GRPCAddr: ":50051"}).Validate(); err != nil {
		t.Errorf("ci with GRPCAddr rejected: %v", err)
	}
}

func TestValidateLocalLenient(t *testing.T) {
	if err := (Config{Env: EnvLocal}).Validate(); err != nil {
		t.Errorf("local config rejected: %v", err)
	}
}

func TestDefaultProfileIsLocal(t *testing.T) {
	t.Setenv("PORTUNUS_ENV", "")
	cfg, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if cfg.Env != EnvLocal {
		t.Errorf("default Env = %q, want %q", cfg.Env, EnvLocal)
	}
}
