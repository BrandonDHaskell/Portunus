package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr string

	// DB
	Env    string // "dev" | "prod"
	DBPath string // e.g. "./data/portunus.db"

	KnownModules   []string
	AllowAll       bool
	AllowedCardIDs []string

	// Heartbeat retention
	HeartbeatRetentionDays int // 0 = keep forever
	PruneIntervalHours     int // how often the pruner runs (default 6)
}

func FromEnv() Config {
	addr := getenvDefault("PORTUNUS_HTTP_ADDR", ":8080")

	env := strings.ToLower(getenvDefault("PORTUNUS_ENV", "dev"))
	if env != "dev" && env != "prod" {
		// fail-soft: treat unknown as dev
		env = "dev"
	}

	dbPath := getenvDefault("PORTUNUS_DB_PATH", "./data/portunus.db")

	knownModules := splitCSV(os.Getenv("PORTUNUS_KNOWN_MODULES"))
	allowedCards := splitCSV(os.Getenv("PORTUNUS_ALLOWED_CARD_IDS"))

	allowAll := strings.EqualFold(os.Getenv("PORTUNUS_ALLOW_ALL"), "true") ||
		os.Getenv("PORTUNUS_ALLOW_ALL") == "1"

	retentionDays := getenvInt("PORTUNUS_HEARTBEAT_RETENTION_DAYS", 30)
	pruneInterval := getenvInt("PORTUNUS_PRUNE_INTERVAL_HOURS", 6)

	return Config{
		HTTPAddr: addr,
		Env:      env,
		DBPath:   dbPath,

		KnownModules:   knownModules,
		AllowAll:       allowAll,
		AllowedCardIDs: allowedCards,

		HeartbeatRetentionDays: retentionDays,
		PruneIntervalHours:     pruneInterval,
	}
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
