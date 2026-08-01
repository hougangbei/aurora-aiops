package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	k8sapierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Probe checks the shared cluster connection against the API server without
// connecting to any node directly. All checks share one bounded deadline.
type Probe struct {
	discovery      discovery.DiscoveryInterface
	kubeClient     kubernetes.Interface
	metricsClient  metricsclient.Interface
	currentContext string
	serverHost     string
	timeout        time.Duration
}

// NewProbe returns a Probe bound to the given cluster clients. serverHost is
// the raw API server URL and is normalized before being returned to callers.
func NewProbe(
	discoveryClient discovery.DiscoveryInterface,
	kubeClient kubernetes.Interface,
	metricsClient metricsclient.Interface,
	currentContext string,
	serverHost string,
	timeout time.Duration,
) *Probe {
	return &Probe{
		discovery:      discoveryClient,
		kubeClient:     kubeClient,
		metricsClient:  metricsClient,
		currentContext: currentContext,
		serverHost:     serverHost,
		timeout:        timeout,
	}
}

// Run executes the probe within a single bounded context.
//
// Failure rules:
//   - discovery ServerVersion failure -> unreachable
//   - Nodes list failure -> unreachable
//   - Metrics list failure -> degraded only
//
// The Metrics API being down never makes the whole cluster look offline.
func (p *Probe) Run(ctx context.Context) ProbeResult {
	probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	start := time.Now()
	result := ProbeResult{
		CurrentContext: p.currentContext,
		ServerHost:     normalizeServerHost(p.serverHost),
		CheckedAt:      time.Now().UTC(),
		Checks:         make(map[string]CheckResult),
	}

	version, err := p.discovery.ServerVersion()
	if err != nil {
		code, message := mapConnectError(err)
		result.State = StateUnreachable
		result.Checks["api"] = CheckResult{OK: false, Code: code, Message: message}
		result.LatencyMS = time.Since(start).Milliseconds()
		return result
	}
	result.Version = fmt.Sprintf("%s.%s", version.Major, version.Minor)
	result.Checks["api"] = CheckResult{OK: true, Code: "API_REACHABLE", Message: "API server reachable"}

	_, err = p.kubeClient.CoreV1().Nodes().List(probeCtx, metav1.ListOptions{Limit: 1})
	if err != nil {
		code, message := mapNodesError(err)
		result.State = StateUnreachable
		result.Checks["nodes"] = CheckResult{OK: false, Code: code, Message: message}
		result.LatencyMS = time.Since(start).Milliseconds()
		return result
	}
	result.Checks["nodes"] = CheckResult{OK: true, Code: "NODES_OK", Message: "Nodes API reachable"}
	result.Capabilities.Nodes = true
	result.Capabilities.Events = true
	result.Capabilities.PodLogs = true

	_, err = p.metricsClient.MetricsV1beta1().NodeMetricses().List(probeCtx, metav1.ListOptions{})
	if err != nil {
		result.State = StateDegraded
		result.Capabilities.Metrics = false
		result.Checks["metrics"] = CheckResult{OK: false, Code: "METRICS_UNAVAILABLE", Message: "Metrics API unavailable"}
	} else {
		result.State = StateConnected
		result.Capabilities.Metrics = true
		result.Checks["metrics"] = CheckResult{OK: true, Code: "METRICS_OK", Message: "Metrics API reachable"}
	}

	result.LatencyMS = time.Since(start).Milliseconds()
	return result
}

// mapConnectError classifies discovery/connection failures into stable codes.
func mapConnectError(err error) (code, message string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "PROBE_TIMEOUT", "probe timed out"
	}
	if isTLSFailure(err) {
		return "TLS_FAILED", "TLS handshake failed"
	}
	if k8sapierrors.IsUnauthorized(err) || k8sapierrors.IsForbidden(err) {
		return "AUTH_FAILED", "authentication failed"
	}
	return "API_UNREACHABLE", "API server unreachable"
}

// mapNodesError classifies node API failures into stable codes.
func mapNodesError(err error) (code, message string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "PROBE_TIMEOUT", "probe timed out"
	}
	if k8sapierrors.IsForbidden(err) {
		return "NODES_FORBIDDEN", "nodes access forbidden"
	}
	return "API_UNREACHABLE", "API server unreachable"
}

func isTLSFailure(err error) bool {
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "tls") || strings.Contains(lower, "x509") || strings.Contains(lower, "certificate") {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return isTLSFailure(urlErr.Err)
	}
	return false
}

// normalizeServerHost strips userinfo, query, and fragment from a raw API
// server URL so callers never receive credentials or kubeconfig paths.
func normalizeServerHost(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.Scheme + "://" + u.Host
}
