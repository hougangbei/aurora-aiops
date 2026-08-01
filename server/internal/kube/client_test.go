package kube

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSharedClientUsesKubeconfigIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	content := []byte(`apiVersion: v1
kind: Config
clusters:
- name: lab
  cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
contexts:
- name: lab
  context:
    cluster: lab
    user: shared
current-context: lab
users:
- name: shared
  user:
    token: kubeconfig-owned-token
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	client, err := NewSharedClient(path, Options{Timeout: time.Second, QPS: 20, Burst: 40})
	if err != nil {
		t.Fatal(err)
	}
	if client.RESTConfig.Timeout != time.Second {
		t.Fatalf("timeout=%s", client.RESTConfig.Timeout)
	}
	if client.RESTConfig.QPS != 20 || client.RESTConfig.Burst != 40 {
		t.Fatalf("rate=%v/%d", client.RESTConfig.QPS, client.RESTConfig.Burst)
	}
	if client.AccessToken != "kubeconfig-owned-token" {
		t.Fatalf("access token=%q, want kubeconfig identity", client.AccessToken)
	}
	if client.AuthMode != "shared-kubeconfig" {
		t.Fatalf("auth mode=%q", client.AuthMode)
	}
	if client.ConfigPath != path {
		t.Fatalf("config path=%q", client.ConfigPath)
	}
}
