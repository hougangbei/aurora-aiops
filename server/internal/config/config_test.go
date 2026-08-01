package config

import (
	"testing"
	"time"
)

func TestLoadAIOpsDefaults(t *testing.T) {
	t.Setenv("KUBEJOJO_AIOPS_DB", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AIOps.DBPath != "./data/kubejojo.db" {
		t.Fatalf("DBPath = %q", cfg.AIOps.DBPath)
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
