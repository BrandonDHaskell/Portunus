package config

import (
	"os"
	"strings"
)

type Config struct {
	HTTPAddr        string
	KnownModules    []string
	AllowAll        bool
	AllowedCardIDs  []string
}

func FromEnv() Config {
	addr := getenvDefault("PORTUNUS_HTTP_ADDR", ":8080")

	knownModules := splitCSV(os.Getenv("PORTUNUS_KNOWN_MODULES"))
	allowedCards := splitCSV(os.Getenv("PORTUNUS_ALLOWED_CARD_IDS"))

	allowAll := strings.EqualFold(os.Getenv("PORTUNUS_ALLOW_ALL"), "true") ||
		os.Getenv("PORTUNUS_ALLOW_ALL") == "1"

	return Config{
		HTTPAddr:       addr,
		KnownModules:   knownModules,
		AllowAll:       allowAll,
		AllowedCardIDs: allowedCards,
	}
}

func getenvDefault(key, def string) string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
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
