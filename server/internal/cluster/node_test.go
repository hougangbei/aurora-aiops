package cluster

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSelectNodeAddressPrefersInternalIPv4(t *testing.T) {
	addresses := []corev1.NodeAddress{
		{Type: corev1.NodeHostName, Address: "worker-1"},
		{Type: corev1.NodeInternalIP, Address: "fd00::12"},
		{Type: corev1.NodeExternalIP, Address: "203.0.113.12"},
		{Type: corev1.NodeInternalIP, Address: "10.0.0.12"},
	}
	got := SelectNodeAddress(addresses)
	if got.Address != "10.0.0.12" || got.Source != "NodeInternalIP" {
		t.Fatalf("got=%+v", got)
	}
	if got.Family != "ipv4" {
		t.Fatalf("family=%q", got.Family)
	}
}

func TestSelectNodeAddressFallsBackToInternalIPv6(t *testing.T) {
	addresses := []corev1.NodeAddress{
		{Type: corev1.NodeHostName, Address: "worker-1"},
		{Type: corev1.NodeInternalIP, Address: "fd00::12"},
		{Type: corev1.NodeExternalIP, Address: "203.0.113.12"},
	}
	got := SelectNodeAddress(addresses)
	if got.Address != "fd00::12" || got.Family != "ipv6" {
		t.Fatalf("got=%+v", got)
	}
}

func TestSelectNodeAddressIgnoresInvalidInternalIP(t *testing.T) {
	addresses := []corev1.NodeAddress{
		{Type: corev1.NodeInternalIP, Address: "not-an-ip"},
		{Type: corev1.NodeInternalIP, Address: "10.0.0.12"},
	}
	got := SelectNodeAddress(addresses)
	if got.Address != "10.0.0.12" {
		t.Fatalf("got=%+v", got)
	}
}

func TestSelectNodeAddressReturnsEmptyWhenOnlyHostname(t *testing.T) {
	addresses := []corev1.NodeAddress{
		{Type: corev1.NodeHostName, Address: "worker-1"},
		{Type: corev1.NodeExternalIP, Address: "203.0.113.12"},
	}
	got := SelectNodeAddress(addresses)
	if got.Address != "" {
		t.Fatalf("got=%+v", got)
	}
}

func TestSelectNodeAddressMultipleIPv4KeepsNodeOrder(t *testing.T) {
	addresses := []corev1.NodeAddress{
		{Type: corev1.NodeInternalIP, Address: "10.0.0.2"},
		{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
	}
	got := SelectNodeAddress(addresses)
	if got.Address != "10.0.0.2" {
		t.Fatalf("got=%+v, want first in node order", got)
	}
}

func TestMapNodePopulatesIdentityAndHostname(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker-1",
			Labels: map[string]string{"node-role.kubernetes.io/worker": ""},
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: "worker-1"},
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
	got := MapNode(node)
	if got.Name != "worker-1" {
		t.Fatalf("name=%q", got.Name)
	}
	if got.InternalAddress.Address != "10.0.0.12" {
		t.Fatalf("internalAddress=%+v", got.InternalAddress)
	}
	if got.Hostname != "worker-1" {
		t.Fatalf("hostname=%q", got.Hostname)
	}
	if got.KubeletVersion != "v1.30.14" {
		t.Fatalf("kubelet=%q", got.KubeletVersion)
	}
}

func TestMapNodeKeepsHostnameWhenInternalIPMissing(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: "worker-1"},
			},
		},
	}
	got := MapNode(node)
	if got.InternalAddress.Address != "" {
		t.Fatalf("internalAddress=%+v", got.InternalAddress)
	}
	if got.Hostname != "worker-1" {
		t.Fatalf("hostname=%q", got.Hostname)
	}
}
