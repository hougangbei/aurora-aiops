package config

import (
	"log"
	"os"
	"strings"
)

const (
	auroraEnvPrefix = "AURORA_AIOPS_"
	legacyEnvPrefix = "KUBEJOJO_"
)

func getEnvCompat(suffix string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(auroraEnvPrefix + suffix)); value != "" {
		return value
	}

	legacyKey := legacyEnvPrefix + suffix
	if value := strings.TrimSpace(os.Getenv(legacyKey)); value != "" {
		log.Printf("deprecated environment variable %s is in use; migrate to %s%s", legacyKey, auroraEnvPrefix, suffix)
		return value
	}

	return fallback
}

func splitCSVEnvCompat(suffix string) []string {
	value := getEnvCompat(suffix, "")
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, item := range parts {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func compatibleAIOpsDBPath() string {
	if configured := getEnvCompat("AIOPS_DB", ""); configured != "" {
		return configured
	}

	const legacyPath = "./data/kubejojo.db"
	if _, err := os.Stat(legacyPath); err == nil {
		log.Printf("deprecated database path %s is in use; migrate to ./data/aurora-aiops.db", legacyPath)
		return legacyPath
	}

	return "./data/aurora-aiops.db"
}

func BootstrapAdminCredentials() (username string, password string) {
	return getEnvCompat("BOOTSTRAP_ADMIN_USER", ""), getEnvCompat("BOOTSTRAP_ADMIN_PASSWORD", "")
}

func RuntimeDir() string {
	return getEnvCompat("RUNTIME_DIR", "")
}
