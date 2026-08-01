package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr       string
	KubeconfigPath string
	Cluster        ClusterConfig
	Update         UpdateConfig
	AIOps          AIOpsConfig
}

// ClusterConfig holds the connection parameters for the single shared
// Kubernetes client created at process start.
type ClusterConfig struct {
	Timeout time.Duration
	QPS     float32
	Burst   int
}

type UpdateConfig struct {
	Enabled          bool
	AllowPrereleases bool
	Repository       string
	AllowedSubjects  []string
	GitHubToken      string
	TargetPath       string
}

type AIOpsConfig struct {
	DBPath string
}

func Load() (Config, error) {
	cluster, err := loadClusterConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPAddr:       getEnv("HTTP_ADDR", ":8080"),
		KubeconfigPath: kubeconfigPath(),
		Cluster:        cluster,
		Update: UpdateConfig{
			Enabled:          getEnv("KUBEJOJO_UPDATE_ENABLED", "") == "true",
			AllowPrereleases: getEnv("KUBEJOJO_UPDATE_ALLOW_PRERELEASES", "") == "true",
			Repository:       getEnv("KUBEJOJO_UPDATE_REPOSITORY", "heihuzicity-tech/kubejojo"),
			AllowedSubjects:  splitCSVEnv("KUBEJOJO_UPDATE_ALLOWED_SUBJECTS"),
			GitHubToken:      getEnv("KUBEJOJO_UPDATE_GITHUB_TOKEN", ""),
			TargetPath:       getEnv("KUBEJOJO_UPDATE_TARGET_PATH", ""),
		},
		AIOps: AIOpsConfig{
			DBPath: getEnv("KUBEJOJO_AIOPS_DB", "./data/kubejojo.db"),
		},
	}, nil
}

// loadClusterConfig parses the shared Kubernetes client connection parameters.
// Invalid values must fail startup rather than silently falling back to defaults.
func loadClusterConfig() (ClusterConfig, error) {
	timeoutVal, err := time.ParseDuration(getEnv("KUBEJOJO_KUBE_TIMEOUT", "10s"))
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("KUBEJOJO_KUBE_TIMEOUT: %w", err)
	}
	if timeoutVal <= 0 {
		return ClusterConfig{}, fmt.Errorf("KUBEJOJO_KUBE_TIMEOUT must be positive: %s", timeoutVal)
	}

	qpsVal, err := strconv.ParseFloat(getEnv("KUBEJOJO_KUBE_QPS", "20"), 32)
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("KUBEJOJO_KUBE_QPS: %w", err)
	}
	if qpsVal <= 0 {
		return ClusterConfig{}, fmt.Errorf("KUBEJOJO_KUBE_QPS must be positive: %v", qpsVal)
	}

	burstVal, err := strconv.Atoi(getEnv("KUBEJOJO_KUBE_BURST", "40"))
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("KUBEJOJO_KUBE_BURST: %w", err)
	}
	if burstVal <= 0 {
		return ClusterConfig{}, fmt.Errorf("KUBEJOJO_KUBE_BURST must be positive: %d", burstVal)
	}

	return ClusterConfig{
		Timeout: timeoutVal,
		QPS:     float32(qpsVal),
		Burst:   burstVal,
	}, nil
}

func kubeconfigPath() string {
	if value := strings.TrimSpace(os.Getenv("KUBEJOJO_KUBECONFIG")); value != "" {
		return value
	}

	if value := strings.TrimSpace(os.Getenv("KUBECONFIG")); value != "" {
		parts := strings.Split(value, string(os.PathListSeparator))
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(homeDir, ".kube", "config")
}

func getEnv(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}

func splitCSVEnv(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, item := range parts {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}
