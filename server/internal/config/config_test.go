package config

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadAIOpsDefaults(t *testing.T) {
	t.Setenv("AURORA_AIOPS_AIOPS_DB", "")
	t.Setenv("KUBEJOJO_AIOPS_DB", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AIOps.DBPath != "./data/aurora-aiops.db" {
		t.Fatalf("DBPath = %q", cfg.AIOps.DBPath)
	}
}

func TestLoadPrefersAuroraEnvironmentOverLegacy(t *testing.T) {
	t.Setenv("AURORA_AIOPS_LLM_MODEL", "aurora-model")
	t.Setenv("KUBEJOJO_LLM_MODEL", "legacy-model")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "aurora-model" {
		t.Fatalf("model=%q want aurora-model", cfg.LLM.Model)
	}
}

func TestLoadFallsBackToLegacyWithoutLoggingSecretValues(t *testing.T) {
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	t.Setenv("AURORA_AIOPS_LLM_API_KEY", "")
	t.Setenv("KUBEJOJO_LLM_API_KEY", "legacy-secret-value")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "legacy-secret-value" {
		t.Fatalf("APIKey was not loaded from the compatibility variable")
	}
	if !strings.Contains(logs.String(), "KUBEJOJO_LLM_API_KEY") {
		t.Fatalf("missing legacy variable warning: %q", logs.String())
	}
	if strings.Contains(logs.String(), "legacy-secret-value") {
		t.Fatalf("secret value leaked into logs: %q", logs.String())
	}
}

func TestBootstrapAdminCredentialsPreferAuroraVariables(t *testing.T) {
	t.Setenv("AURORA_AIOPS_BOOTSTRAP_ADMIN_USER", "aurora-admin")
	t.Setenv("AURORA_AIOPS_BOOTSTRAP_ADMIN_PASSWORD", "aurora-password")
	t.Setenv("KUBEJOJO_BOOTSTRAP_ADMIN_USER", "legacy-admin")
	t.Setenv("KUBEJOJO_BOOTSTRAP_ADMIN_PASSWORD", "legacy-password")

	username, password := BootstrapAdminCredentials()
	if username != "aurora-admin" || password != "aurora-password" {
		t.Fatalf("credentials=(%q,%q), want Aurora variables", username, password)
	}
}

func TestRuntimeDirFallsBackToLegacyVariable(t *testing.T) {
	t.Setenv("AURORA_AIOPS_RUNTIME_DIR", "")
	t.Setenv("KUBEJOJO_RUNTIME_DIR", "/var/run/legacy")
	if got := RuntimeDir(); got != "/var/run/legacy" {
		t.Fatalf("RuntimeDir=%q want legacy fallback", got)
	}
}

func TestLoadUsesLegacyDefaultDatabaseWhenPresent(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("data/kubejojo.db", []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURORA_AIOPS_AIOPS_DB", "")
	t.Setenv("KUBEJOJO_AIOPS_DB", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AIOps.DBPath != "./data/kubejojo.db" {
		t.Fatalf("DBPath=%q want legacy database path", cfg.AIOps.DBPath)
	}
}

func TestLoadClusterClientDefaults(t *testing.T) {
	t.Setenv("KUBEJOJO_KUBE_TIMEOUT", "")
	t.Setenv("KUBEJOJO_KUBE_QPS", "")
	t.Setenv("KUBEJOJO_KUBE_BURST", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cluster.Timeout != 10*time.Second {
		t.Fatalf("timeout=%s", cfg.Cluster.Timeout)
	}
	if cfg.Cluster.QPS != 20 {
		t.Fatalf("qps=%v", cfg.Cluster.QPS)
	}
	if cfg.Cluster.Burst != 40 {
		t.Fatalf("burst=%d", cfg.Cluster.Burst)
	}
}

func TestLoadLLMDefaults(t *testing.T) {
	t.Setenv("KUBEJOJO_LLM_BASE_URL", "")
	t.Setenv("KUBEJOJO_LLM_MODEL", "")
	t.Setenv("KUBEJOJO_LLM_TIMEOUT", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.BaseURL != "" || cfg.LLM.Model != "" || cfg.LLM.APIKey != "" {
		t.Fatalf("LLM defaults not empty: %+v", cfg.LLM)
	}
	if cfg.LLM.Timeout != 60*time.Second {
		t.Fatalf("LLM timeout=%s want 60s", cfg.LLM.Timeout)
	}
}

func TestLoadLLMInvalidTimeout(t *testing.T) {
	t.Setenv("KUBEJOJO_LLM_TIMEOUT", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid KUBEJOJO_LLM_TIMEOUT")
	}
	t.Setenv("KUBEJOJO_LLM_TIMEOUT", "-5s")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for negative KUBEJOJO_LLM_TIMEOUT")
	}
}

// TestKubeconfigPathPrecedence asserts that the config layer only resolves the
// explicit and KUBECONFIG-first-entry variables. The empty case defers identity
// selection to kube.NewSharedClient (explicit/in-cluster/default).
func TestKubeconfigPathPrecedence(t *testing.T) {
	t.Setenv("AURORA_AIOPS_KUBECONFIG", "/aurora")
	t.Setenv("KUBEJOJO_KUBECONFIG", "/explicit")
	t.Setenv("KUBECONFIG", "/secondary")
	if got := kubeconfigPath(); got != "/aurora" {
		t.Fatalf("kubeconfigPath=%q want /aurora", got)
	}

	t.Setenv("AURORA_AIOPS_KUBECONFIG", "")
	t.Setenv("KUBEJOJO_KUBECONFIG", "")
	t.Setenv("KUBECONFIG", "/first"+string(os.PathListSeparator)+"/second")
	if got := kubeconfigPath(); got != "/first" {
		t.Fatalf("kubeconfigPath=%q want /first", got)
	}

	t.Setenv("KUBEJOJO_KUBECONFIG", "")
	t.Setenv("KUBECONFIG", "")
	if got := kubeconfigPath(); got != "" {
		t.Fatalf("kubeconfigPath=%q want empty (selection deferred to kube client)", got)
	}
}

func TestLoadClusterClientInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		env  string
		val  string
	}{
		{"timeout", "KUBEJOJO_KUBE_TIMEOUT", "not-a-duration"},
		{"qps", "KUBEJOJO_KUBE_QPS", "abc"},
		{"burst", "KUBEJOJO_KUBE_BURST", "-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.env, tc.val)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for %s=%q", tc.env, tc.val)
			}
		})
	}
}
