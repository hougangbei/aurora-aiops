package service

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/heihuzicity-tech/kubejojo/server/internal/cluster"
	"github.com/heihuzicity-tech/kubejojo/server/internal/kube"
)

// TestListNodesIPMatchesClusterAddressRule proves the legacy NodeItem.IP
// projection and the new cluster.Node address selection share one rule: even
// when an IPv6 InternalIP appears first in the node address list, both return
// the same preferred IPv4 address.
func TestListNodesIPMatchesClusterAddressRule(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "fd00::12"},
				{Type: corev1.NodeHostName, Address: "node-1"},
				{Type: corev1.NodeInternalIP, Address: "10.0.0.12"},
			},
			NodeInfo: corev1.NodeSystemInfo{
				OSImage:                 "Ubuntu 24.04.3 LTS",
				KernelVersion:           "6.8.0-45-generic",
				KubeletVersion:          "v1.30.14",
				ContainerRuntimeVersion: "containerd://2.0.0",
			},
		},
	}

	kubeClient := kubefake.NewSimpleClientset(&node)
	metricsClient := metricsfake.NewSimpleClientset()
	svc := &ClusterService{
		client: &kube.Client{
			Kubernetes: kubeClient,
			Metrics:    metricsClient,
			RESTConfig: &rest.Config{},
		},
	}

	items, err := svc.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("nodes=%d", len(items))
	}

	want := cluster.SelectNodeAddress(node.Status.Addresses).Address
	if want != "10.0.0.12" {
		t.Fatalf("test fixture must select IPv4, got %q", want)
	}
	if items[0].IP != want {
		t.Fatalf("ListNodes IP=%q, cluster.SelectNodeAddress=%q", items[0].IP, want)
	}
}

func TestSanitizeManifestYAML(t *testing.T) {
	input := `
apiVersion: apps/v1
kind: Deployment
metadata:
  name: demo
  namespace: demo-workloads
  uid: abc
  resourceVersion: "123"
  generation: 7
  creationTimestamp: "2026-04-13T00:00:00Z"
  managedFields:
    - manager: kubectl
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '{"kind":"Deployment"}'
    custom.example/key: keep-me
spec:
  replicas: 1
status:
  readyReplicas: 1
`

	output, err := sanitizeManifestYAML([]byte(input))
	if err != nil {
		t.Fatalf("sanitizeManifestYAML returned error: %v", err)
	}

	result := string(output)

	for _, unexpected := range []string{
		"kubectl.kubernetes.io/last-applied-configuration",
		"resourceVersion:",
		"uid:",
		"managedFields:",
		"generation:",
		"creationTimestamp:",
		"status:",
	} {
		if strings.Contains(result, unexpected) {
			t.Fatalf("expected sanitized yaml to remove %q, got:\n%s", unexpected, result)
		}
	}

	for _, expected := range []string{
		"kind: Deployment",
		"name: demo",
		"namespace: demo-workloads",
		"custom.example/key: keep-me",
		"replicas: 1",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("expected sanitized yaml to keep %q, got:\n%s", expected, result)
		}
	}
}

func TestSanitizeManifestYAMLRemovesEmptyAnnotations(t *testing.T) {
	input := `
apiVersion: v1
kind: Pod
metadata:
  name: demo
  namespace: demo-workloads
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '{"kind":"Pod"}'
spec:
  containers:
    - name: demo
      image: nginx
`

	output, err := sanitizeManifestYAML([]byte(input))
	if err != nil {
		t.Fatalf("sanitizeManifestYAML returned error: %v", err)
	}

	result := string(output)
	if strings.Contains(result, "annotations:") {
		t.Fatalf("expected empty annotations block to be removed, got:\n%s", result)
	}
}
