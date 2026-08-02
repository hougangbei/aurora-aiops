package kube

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func writeTestKubeconfig(t *testing.T, path string) {
	t.Helper()
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
}

func TestNewSharedClientUsesKubeconfigIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	writeTestKubeconfig(t, path)

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

func TestNewSharedClientPrecedenceExplicitWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	writeTestKubeconfig(t, path)

	inClusterCalled := false
	client, err := newSharedClientWithLoaders(path, Options{}, clientConfigLoaders{
		inCluster: func() (*rest.Config, error) {
			inClusterCalled = true
			return &rest.Config{Host: "https://in-cluster:6443"}, nil
		},
		homeDir: func() (string, error) { return "/fake/home", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if inClusterCalled {
		t.Fatal("explicit path must never consult the in-cluster config")
	}
	if client.AuthMode != "shared-kubeconfig" {
		t.Fatalf("auth mode=%q want shared-kubeconfig", client.AuthMode)
	}
	if client.ConfigPath != path {
		t.Fatalf("config path=%q want %q", client.ConfigPath, path)
	}
}

func TestNewSharedClientPrecedenceUsesInCluster(t *testing.T) {
	client, err := newSharedClientWithLoaders("", Options{}, clientConfigLoaders{
		inCluster: func() (*rest.Config, error) {
			return &rest.Config{Host: "https://in-cluster:6443", BearerToken: "sa-token"}, nil
		},
		homeDir: func() (string, error) { return "/fake/home", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.AuthMode != "in-cluster" {
		t.Fatalf("auth mode=%q want in-cluster", client.AuthMode)
	}
	if client.ConfigPath != "" {
		t.Fatalf("config path=%q want empty", client.ConfigPath)
	}
	if client.RawConfig.CurrentContext != "in-cluster" {
		t.Fatalf("current context=%q want in-cluster", client.RawConfig.CurrentContext)
	}
	if client.AccessToken != "sa-token" {
		t.Fatalf("access token=%q want service account token", client.AccessToken)
	}
}

func TestNewSharedClientPrecedenceFallsBackToDefaultKubeconfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".kube", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestKubeconfig(t, path)

	client, err := newSharedClientWithLoaders("", Options{}, clientConfigLoaders{
		inCluster: func() (*rest.Config, error) {
			return nil, rest.ErrNotInCluster
		},
		homeDir: func() (string, error) { return home, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.AuthMode != "shared-kubeconfig" {
		t.Fatalf("auth mode=%q", client.AuthMode)
	}
	if client.ConfigPath != path {
		t.Fatalf("config path=%q want %q", client.ConfigPath, path)
	}
}

func TestNewSharedClientPrecedenceBothSourcesFail(t *testing.T) {
	home := "/fake/home"
	_, err := newSharedClientWithLoaders("", Options{}, clientConfigLoaders{
		inCluster: func() (*rest.Config, error) {
			return nil, rest.ErrNotInCluster
		},
		homeDir: func() (string, error) { return home, nil },
	})
	if err == nil {
		t.Fatal("expected error when both sources fail")
	}
	msg := err.Error()
	for _, want := range []string{"in-cluster config unavailable", "default kubeconfig unavailable"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing source category %q", msg, want)
		}
	}
	if strings.Contains(msg, home) {
		t.Fatalf("error must not print local path: %q", msg)
	}
}

func TestNewSharedClientExplicitErrorRedactsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config") // file does not exist
	_, err := newSharedClientWithLoaders(path, Options{}, clientConfigLoaders{
		inCluster: func() (*rest.Config, error) { return nil, rest.ErrNotInCluster },
		homeDir:   func() (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected error for missing explicit kubeconfig")
	}
	msg := err.Error()
	if !strings.Contains(msg, "explicit kubeconfig unavailable") {
		t.Fatalf("error %q missing explicit category", msg)
	}
	if !strings.Contains(msg, "[redacted]") {
		t.Fatalf("error %q missing redaction marker", msg)
	}
	if strings.Contains(msg, path) {
		t.Fatalf("error must not print local path: %q", msg)
	}
}
