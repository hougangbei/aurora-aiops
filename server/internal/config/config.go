package config

import (
	"encoding/base64"
	"fmt"
	"os"
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
	Asset          AssetConfig
	LLM            LLMConfig
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

type AssetConfig struct {
	EncryptionKey   []byte
	CollectInterval time.Duration
}

// LLMConfig holds the OpenAI-compatible model endpoint. An empty BaseURL
// disables model-backed roles; the deterministic triage/collector still run.
type LLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Style   string
	Timeout time.Duration
}

func Load() (Config, error) {
	cluster, err := loadClusterConfig()
	if err != nil {
		return Config{}, err
	}

	llm, err := loadLLMConfig()
	if err != nil {
		return Config{}, err
	}

	asset, err := loadAssetConfig()
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPAddr:       getEnv("HTTP_ADDR", ":8080"),
		KubeconfigPath: kubeconfigPath(),
		Cluster:        cluster,
		Update: UpdateConfig{
			Enabled:          getEnvCompat("UPDATE_ENABLED", "") == "true",
			AllowPrereleases: getEnvCompat("UPDATE_ALLOW_PRERELEASES", "") == "true",
			Repository:       getEnvCompat("UPDATE_REPOSITORY", "hougangbei/aurora-aiops"),
			AllowedSubjects:  splitCSVEnvCompat("UPDATE_ALLOWED_SUBJECTS"),
			GitHubToken:      getEnvCompat("UPDATE_GITHUB_TOKEN", ""),
			TargetPath:       getEnvCompat("UPDATE_TARGET_PATH", ""),
		},
		AIOps: AIOpsConfig{
			DBPath: compatibleAIOpsDBPath(),
		},
		Asset: asset,
		LLM:   llm,
	}, nil
}

func loadAssetConfig() (AssetConfig, error) {
	var encryptionKey []byte
	if encodedKey := getEnvCompat("ASSET_ENCRYPTION_KEY", ""); encodedKey != "" {
		decodedKey, err := base64.StdEncoding.DecodeString(encodedKey)
		if err != nil {
			return AssetConfig{}, fmt.Errorf("AURORA_AIOPS_ASSET_ENCRYPTION_KEY: %w", err)
		}
		if len(decodedKey) != 32 {
			return AssetConfig{}, fmt.Errorf("AURORA_AIOPS_ASSET_ENCRYPTION_KEY must decode to exactly 32 bytes: got %d", len(decodedKey))
		}
		encryptionKey = decodedKey
	}

	collectInterval, err := time.ParseDuration(getEnvCompat("ASSET_COLLECT_INTERVAL", "15m"))
	if err != nil {
		return AssetConfig{}, fmt.Errorf("AURORA_AIOPS_ASSET_COLLECT_INTERVAL: %w", err)
	}
	if collectInterval <= 0 {
		return AssetConfig{}, fmt.Errorf("AURORA_AIOPS_ASSET_COLLECT_INTERVAL must be positive: %s", collectInterval)
	}

	return AssetConfig{
		EncryptionKey:   encryptionKey,
		CollectInterval: collectInterval,
	}, nil
}

// loadLLMConfig parses the model endpoint settings. Only the timeout is
// validated: an empty base URL/model simply disables model-backed roles.
func loadLLMConfig() (LLMConfig, error) {
	timeout, err := time.ParseDuration(getEnvCompat("LLM_TIMEOUT", "60s"))
	if err != nil {
		return LLMConfig{}, fmt.Errorf("AURORA_AIOPS_LLM_TIMEOUT: %w", err)
	}
	if timeout <= 0 {
		return LLMConfig{}, fmt.Errorf("AURORA_AIOPS_LLM_TIMEOUT must be positive: %s", timeout)
	}

	return LLMConfig{
		BaseURL: getEnvCompat("LLM_BASE_URL", ""),
		APIKey:  getEnvCompat("LLM_API_KEY", ""),
		Model:   getEnvCompat("LLM_MODEL", ""),
		Style:   getEnvCompat("LLM_API_STYLE", ""),
		Timeout: timeout,
	}, nil
}

// loadClusterConfig parses the shared Kubernetes client connection parameters.
// Invalid values must fail startup rather than silently falling back to defaults.
func loadClusterConfig() (ClusterConfig, error) {
	timeoutVal, err := time.ParseDuration(getEnvCompat("KUBE_TIMEOUT", "10s"))
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("AURORA_AIOPS_KUBE_TIMEOUT: %w", err)
	}
	if timeoutVal <= 0 {
		return ClusterConfig{}, fmt.Errorf("AURORA_AIOPS_KUBE_TIMEOUT must be positive: %s", timeoutVal)
	}

	qpsVal, err := strconv.ParseFloat(getEnvCompat("KUBE_QPS", "20"), 32)
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("AURORA_AIOPS_KUBE_QPS: %w", err)
	}
	if qpsVal <= 0 {
		return ClusterConfig{}, fmt.Errorf("AURORA_AIOPS_KUBE_QPS must be positive: %v", qpsVal)
	}

	burstVal, err := strconv.Atoi(getEnvCompat("KUBE_BURST", "40"))
	if err != nil {
		return ClusterConfig{}, fmt.Errorf("AURORA_AIOPS_KUBE_BURST: %w", err)
	}
	if burstVal <= 0 {
		return ClusterConfig{}, fmt.Errorf("AURORA_AIOPS_KUBE_BURST must be positive: %d", burstVal)
	}

	return ClusterConfig{
		Timeout: timeoutVal,
		QPS:     float32(qpsVal),
		Burst:   burstVal,
	}, nil
}

// kubeconfigPath resolves only the explicit and KUBECONFIG-first-entry paths.
// An empty result defers identity selection to kube.NewSharedClient so the
// in-cluster service account identity can be preferred over a default
// kubeconfig when running inside the cluster.
func kubeconfigPath() string {
	if value := getEnvCompat("KUBECONFIG", ""); value != "" {
		return value
	}

	if value := strings.TrimSpace(os.Getenv("KUBECONFIG")); value != "" {
		parts := strings.Split(value, string(os.PathListSeparator))
		if len(parts) > 0 {
			return strings.TrimSpace(parts[0])
		}
	}

	return ""
}

func getEnv(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
