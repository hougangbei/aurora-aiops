package cluster

import (
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// NodeAddress is a single resolved address for a Kubernetes node. Source
// identifies where the address came from (e.g. NodeInternalIP); Family is
// ipv4 or ipv6.
type NodeAddress struct {
	Address string `json:"address"`
	Source  string `json:"source"`
	Family  string `json:"family"`
}

// Node is the reduced, connection-safe projection of a Kubernetes node used by
// the platform APIs. InternalIP comes from the Node API; the backend never
// connects to it.
type Node struct {
	Name            string      `json:"name"`
	Role            string      `json:"role"`
	Status          string      `json:"status"`
	InternalAddress NodeAddress `json:"internalAddress"`
	Hostname        string      `json:"hostname"`
	OSImage         string      `json:"osImage"`
	KernelVersion   string      `json:"kernelVersion"`
	KubeletVersion  string      `json:"kubeletVersion"`
}

// SelectNodeAddress chooses the node's internal address deterministically.
//
// Rules:
//   - only corev1.NodeInternalIP entries are considered;
//   - the address must parse with net.ParseIP;
//   - IPv4 is preferred over IPv6;
//   - ties keep the first entry in node address order.
//
// ExternalIP is never used as an InternalIP fallback, and no ping, port scan,
// or SSH is performed. When no valid InternalIP exists, an empty NodeAddress is
// returned and the hostname is kept in the separate Hostname field.
func SelectNodeAddress(addresses []corev1.NodeAddress) NodeAddress {
	var fallbackIPv6 NodeAddress
	for _, address := range addresses {
		if address.Type != corev1.NodeInternalIP {
			continue
		}
		ip := net.ParseIP(address.Address)
		if ip == nil {
			continue
		}
		if ip.To4() != nil {
			return NodeAddress{Address: address.Address, Source: "NodeInternalIP", Family: "ipv4"}
		}
		if fallbackIPv6.Address == "" {
			fallbackIPv6 = NodeAddress{Address: address.Address, Source: "NodeInternalIP", Family: "ipv6"}
		}
	}
	return fallbackIPv6
}

// MapNode converts a corev1.Node into the platform Node projection.
func MapNode(node corev1.Node) Node {
	return Node{
		Name:            node.Name,
		Role:            nodeRole(node),
		Status:          readyStatus(node),
		InternalAddress: SelectNodeAddress(node.Status.Addresses),
		Hostname:        SelectNodeHostname(node.Status.Addresses),
		OSImage:         node.Status.NodeInfo.OSImage,
		KernelVersion:   node.Status.NodeInfo.KernelVersion,
		KubeletVersion:  node.Status.NodeInfo.KubeletVersion,
	}
}

// SelectNodeHostname returns the NodeHostName address, or "" when absent.
func SelectNodeHostname(addresses []corev1.NodeAddress) string {
	for _, address := range addresses {
		if address.Type == corev1.NodeHostName && strings.TrimSpace(address.Address) != "" {
			return address.Address
		}
	}
	return ""
}

func nodeRole(node corev1.Node) string {
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return "control-plane"
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		return "control-plane"
	}
	return "worker"
}

func readyStatus(node corev1.Node) string {
	status := "NotReady"
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			status = "Ready"
			break
		}
	}
	if node.Spec.Unschedulable {
		return status + ",SchedulingDisabled"
	}
	return status
}
