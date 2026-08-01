package config

import "testing"

func TestLoadAIOpsDefaults(t *testing.T) {
	t.Setenv("KUBEJOJO_AIOPS_DB", "")
	cfg := Load()
	if cfg.AIOps.DBPath != "./data/kubejojo.db" {
		t.Fatalf("DBPath = %q", cfg.AIOps.DBPath)
	}
}
