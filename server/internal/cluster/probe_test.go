package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func newProbeFixture() (*kubefake.Clientset, *metricsfake.Clientset) {
	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}})
	metricsClient := metricsfake.NewSimpleClientset()
	return kubeClient, metricsClient
}

func TestProbeReportsConnected(t *testing.T) {
	kubeClient, metricsClient := newProbeFixture()
	probe := NewProbe(kubeClient.Discovery(), kubeClient, metricsClient, "lab", "https://10.0.0.101:6443", 10*time.Second)

	got := probe.Run(context.Background())
	if got.State != StateConnected {
		t.Fatalf("state=%s", got.State)
	}
	if got.ServerHost != "https://10.0.0.101:6443" {
		t.Fatalf("serverHost=%q", got.ServerHost)
	}
	if got.Version == "" {
		t.Fatal("version is empty")
	}
	if !got.Capabilities.Nodes || !got.Capabilities.Metrics || !got.Capabilities.Events || !got.Capabilities.PodLogs {
		t.Fatalf("capabilities=%+v", got.Capabilities)
	}
}

func TestProbeReportsDegradedWithoutMetrics(t *testing.T) {
	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}})
	metricsClient := metricsfake.NewSimpleClientset()
	metricsClient.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewServiceUnavailable("metrics unavailable")
	})

	probe := NewProbe(kubeClient.Discovery(), kubeClient, metricsClient, "lab", "https://10.0.0.101:6443", 10*time.Second)
	got := probe.Run(context.Background())

	if got.State != StateDegraded {
		t.Fatalf("state=%s", got.State)
	}
	if !got.Capabilities.Nodes || got.Capabilities.Metrics {
		t.Fatalf("capabilities=%+v", got.Capabilities)
	}
	if got.Checks["metrics"].Code != "METRICS_UNAVAILABLE" {
		t.Fatalf("checks=%+v", got.Checks)
	}
	if got.Checks["metrics"].OK {
		t.Fatal("metrics check should not be OK")
	}
}

func TestProbeReportsUnreachableWhenNodesForbidden(t *testing.T) {
	kubeClient, metricsClient := newProbeFixture()
	kubeClient.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "nodes", errors.New("forbidden"))
	})

	probe := NewProbe(kubeClient.Discovery(), kubeClient, metricsClient, "lab", "https://10.0.0.101:6443", 10*time.Second)
	got := probe.Run(context.Background())

	if got.State != StateUnreachable {
		t.Fatalf("state=%s", got.State)
	}
	if got.Checks["nodes"].Code != "NODES_FORBIDDEN" {
		t.Fatalf("checks=%+v", got.Checks)
	}
	if got.Capabilities.Nodes {
		t.Fatal("nodes capability should be false")
	}
}

func TestProbeReportsUnreachableWhenDiscoveryFails(t *testing.T) {
	kubeClient, metricsClient := newProbeFixture()
	kubeClient.PrependReactor("get", "version", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New("upstream down"))
	})

	probe := NewProbe(kubeClient.Discovery(), kubeClient, metricsClient, "lab", "https://10.0.0.101:6443", 10*time.Second)
	got := probe.Run(context.Background())

	if got.State != StateUnreachable {
		t.Fatalf("state=%s", got.State)
	}
	if got.Checks["api"].Code != "API_UNREACHABLE" {
		t.Fatalf("checks=%+v", got.Checks)
	}
}

func TestProbeReportsTimeout(t *testing.T) {
	kubeClient, metricsClient := newProbeFixture()
	kubeClient.PrependReactor("list", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})

	probe := NewProbe(kubeClient.Discovery(), kubeClient, metricsClient, "lab", "https://10.0.0.101:6443", 50*time.Millisecond)
	got := probe.Run(context.Background())

	if got.State != StateUnreachable {
		t.Fatalf("state=%s", got.State)
	}
	if got.Checks["nodes"].Code != "PROBE_TIMEOUT" {
		t.Fatalf("checks=%+v", got.Checks)
	}
}

func TestProbeResultDoesNotLeakCredentials(t *testing.T) {
	kubeClient, metricsClient := newProbeFixture()
	probe := NewProbe(
		kubeClient.Discovery(),
		kubeClient,
		metricsClient,
		"lab",
		"https://admin:secretpw@10.0.0.101:6443/kubeconfig?token=SECRET123&raw=true",
		10*time.Second,
	)

	got := probe.Run(context.Background())
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)

	if got.ServerHost != "https://10.0.0.101:6443" {
		t.Fatalf("serverHost=%q", got.ServerHost)
	}
	for _, secret := range []string{"secretpw", "SECRET123", "/kubeconfig", "raw=true"} {
		if strings.Contains(body, secret) {
			t.Fatalf("probe result leaked %q: %s", secret, body)
		}
	}
}

func TestNormalizeServerHostHandlesGarbage(t *testing.T) {
	if got := normalizeServerHost(""); got != "" {
		t.Fatalf("empty -> %q", got)
	}
	if got := normalizeServerHost("%%%not a url"); got != "" {
		t.Fatalf("garbage -> %q", got)
	}
	if got := normalizeServerHost("HTTPS://10.0.0.101:6443/path"); got != "https://10.0.0.101:6443" {
		t.Fatalf("host -> %q", got)
	}
}
