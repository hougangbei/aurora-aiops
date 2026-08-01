package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/heihuzicity-tech/kubejojo/server/internal/cluster"
)

const (
	// collectTailLines is the fixed tail window for pod logs.
	collectTailLines = 200
	// defaultCollectTimeout bounds the whole collection unless configured.
	defaultCollectTimeout = 15 * time.Second
)

// PodAPI is the minimal Kubernetes surface the collector needs. Splitting it
// out lets tests inject a fake that records options and failures; the shared
// client-go clients are adapted through KubernetesPodAPI.
type PodAPI interface {
	GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error)
	ListEvents(ctx context.Context, namespace, fieldSelector string) (*corev1.EventList, error)
	GetPodLogs(ctx context.Context, namespace, name, container string, opts *corev1.PodLogOptions) (string, error)
	GetPodMetrics(ctx context.Context, namespace, name string) (*metricsv1beta1.PodMetrics, error)
}

// KubernetesPodAPI adapts the shared client-go clients to PodAPI.
type KubernetesPodAPI struct {
	Kube    kubernetes.Interface
	Metrics metricsclient.Interface
}

func (k KubernetesPodAPI) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	return k.Kube.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
}

func (k KubernetesPodAPI) ListEvents(ctx context.Context, namespace, fieldSelector string) (*corev1.EventList, error) {
	return k.Kube.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{FieldSelector: fieldSelector})
}

func (k KubernetesPodAPI) GetPodLogs(ctx context.Context, namespace, name, container string, opts *corev1.PodLogOptions) (string, error) {
	raw, err := k.Kube.CoreV1().Pods(namespace).GetLogs(name, opts).Do(ctx).Raw()
	return string(raw), err
}

func (k KubernetesPodAPI) GetPodMetrics(ctx context.Context, namespace, name string) (*metricsv1beta1.PodMetrics, error) {
	return k.Metrics.MetricsV1beta1().PodMetricses(namespace).Get(ctx, name, metav1.GetOptions{})
}

// KubernetesCollector gathers pod diagnostics from the shared Kubernetes
// client. The snapshot, events and logs are core: a failure in any of them
// terminates collection with a hard error. The Metrics API is optional and
// gated by the shared capability matrix; its failure yields partial evidence
// plus a CollectionError, never a fatal one.
type KubernetesCollector struct {
	pods         PodAPI
	capabilities cluster.Capabilities
	timeout      time.Duration
}

// NewKubernetesCollector returns a collector bound to the given PodAPI and
// capability matrix. A non-positive timeout selects the default.
func NewKubernetesCollector(pods PodAPI, capabilities cluster.Capabilities, timeout time.Duration) *KubernetesCollector {
	if timeout <= 0 {
		timeout = defaultCollectTimeout
	}
	return &KubernetesCollector{pods: pods, capabilities: capabilities, timeout: timeout}
}

// Collect gathers snapshot, event, log and (optionally) metric evidence for
// the target pod. Returned nodes have an empty IncidentID; the workflow stamps
// it before persistence. All requests share the bounded context derived from
// ctx.
func (c *KubernetesCollector) Collect(ctx context.Context, target Target, window Window) ([]Node, []Edge, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	now := time.Now().UTC()
	var (
		nodes []Node
		edges []Edge
		fail  []string
	)

	pod, err := c.pods.GetPod(ctx, target.Namespace, target.Name)
	if err != nil {
		return nil, nil, fmt.Errorf("get pod %s/%s: %w", target.Namespace, target.Name, err)
	}

	// Snapshot root: observed pod state with env values, secret refs and token
	// paths deliberately omitted so the payload never carries credentials.
	raw, err := json.Marshal(buildPodSnapshot(pod))
	if err != nil {
		return nil, nil, fmt.Errorf("marshal pod snapshot: %w", err)
	}
	snapshotID := "snapshot"
	nodes = append(nodes, Node{ID: snapshotID, Kind: NodeKindSnapshot, Payload: string(raw), ObservedAt: now})

	// Associated pod events, one node per event.
	events, err := c.pods.ListEvents(ctx, target.Namespace,
		fmt.Sprintf("involvedObject.name=%s,involvedObject.namespace=%s", target.Name, target.Namespace))
	if err != nil {
		return nodes, edges, fmt.Errorf("list events for %s/%s: %w", target.Namespace, target.Name, err)
	}
	for _, ev := range events.Items {
		obs := ev.LastTimestamp.Time
		if obs.IsZero() {
			obs = ev.CreationTimestamp.Time
		}
		if obs.IsZero() {
			obs = now
		}
		payload, perr := marshalEvent(&ev)
		if perr != nil {
			continue
		}
		evID := "event-" + string(ev.UID)
		nodes = append(nodes, Node{ID: evID, Kind: NodeKindEvent, Payload: payload, ObservedAt: obs})
		edges = append(edges, Edge{FromID: evID, ToID: snapshotID, Relation: RelationSupports})
	}

	// Tail logs of the primary container, fixed TailLines and bounded payload.
	if len(pod.Spec.Containers) > 0 {
		container := pod.Spec.Containers[0].Name
		tail := int64(collectTailLines)
		opts := &corev1.PodLogOptions{TailLines: &tail}
		if !window.Start.IsZero() {
			start := metav1.NewTime(window.Start)
			opts.SinceTime = &start
		}
		logText, lerr := c.pods.GetPodLogs(ctx, target.Namespace, target.Name, container, opts)
		if lerr != nil {
			return nodes, edges, fmt.Errorf("get logs for %s/%s container %s: %w", target.Namespace, target.Name, container, lerr)
		}
		logText = RedactSecrets(logText)
		if len(logText) > MaxLogPayloadBytes {
			logText = tailOf(logText, MaxLogPayloadBytes)
		}
		if strings.TrimSpace(logText) != "" {
			logID := "log-" + container
			nodes = append(nodes, Node{ID: logID, Kind: NodeKindLog, Payload: logText, ObservedAt: now})
			edges = append(edges, Edge{FromID: logID, ToID: snapshotID, Relation: RelationSupports})
		}
	}

	// Metrics: optional, gated by the shared capability matrix.
	if c.capabilities.Metrics {
		m, merr := c.pods.GetPodMetrics(ctx, target.Namespace, target.Name)
		if merr != nil {
			fail = append(fail, fmt.Sprintf("metrics for %s/%s unavailable: %v", target.Namespace, target.Name, merr))
		} else if m == nil {
			fail = append(fail, fmt.Sprintf("metrics for %s/%s returned no data", target.Namespace, target.Name))
		} else if payload, perr := json.Marshal(buildMetricSummary(m)); perr == nil && len(payload) > 0 {
			nodes = append(nodes, Node{ID: "metrics", Kind: NodeKindMetric, Payload: string(payload), ObservedAt: now})
			edges = append(edges, Edge{FromID: "metrics", ToID: snapshotID, Relation: RelationSupports})
		}
	}

	if len(fail) > 0 {
		return nodes, edges, &CollectionError{Failed: fail}
	}
	return nodes, edges, nil
}

// podSnapshot is the sanitized projection of a Pod. Env var values, secret
// key refs, volume definitions and service-account token paths are all
// excluded so the payload cannot leak credentials.
type podSnapshot struct {
	Namespace  string              `json:"namespace"`
	Name       string              `json:"name"`
	UID        string              `json:"uid"`
	Phase      corev1.PodPhase     `json:"phase"`
	NodeName   string              `json:"nodeName,omitempty"`
	Reason     string              `json:"reason,omitempty"`
	Message    string              `json:"message,omitempty"`
	PodIP      string              `json:"podIp,omitempty"`
	HostIP     string              `json:"hostIp,omitempty"`
	StartTime  string              `json:"startTime,omitempty"`
	Containers []containerSnapshot `json:"containers"`
	Conditions []conditionSnapshot `json:"conditions,omitempty"`
}

type containerSnapshot struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	RestartCount int32  `json:"restartCount"`
	Ready        bool   `json:"ready"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	ExitCode     *int32 `json:"exitCode,omitempty"`
	LastReason   string `json:"lastReason,omitempty"`
}

type conditionSnapshot struct {
	Type    corev1.PodConditionType `json:"type"`
	Status  corev1.ConditionStatus  `json:"status"`
	Reason  string                  `json:"reason,omitempty"`
	Message string                  `json:"message,omitempty"`
}

func buildPodSnapshot(pod *corev1.Pod) podSnapshot {
	snap := podSnapshot{
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       string(pod.UID),
		Phase:     pod.Status.Phase,
		NodeName:  pod.Spec.NodeName,
		Reason:    RedactSecrets(pod.Status.Reason),
		Message:   RedactSecrets(pod.Status.Message),
		PodIP:     pod.Status.PodIP,
		HostIP:    pod.Status.HostIP,
	}
	if pod.Status.StartTime != nil {
		snap.StartTime = pod.Status.StartTime.UTC().Format(time.RFC3339)
	}
	for _, cs := range pod.Spec.Containers {
		csnap := containerSnapshot{Name: cs.Name, Image: cs.Image}
		if status := findContainerStatus(pod, cs.Name); status != nil {
			csnap.RestartCount = status.RestartCount
			csnap.Ready = status.Ready
			csnap.State, csnap.Reason, csnap.ExitCode = containerState(status.State)
			if status.LastTerminationState.Terminated != nil {
				csnap.LastReason = status.LastTerminationState.Terminated.Reason
			}
		}
		snap.Containers = append(snap.Containers, csnap)
	}
	for _, cond := range pod.Status.Conditions {
		snap.Conditions = append(snap.Conditions, conditionSnapshot{
			Type:    cond.Type,
			Status:  cond.Status,
			Reason:  RedactSecrets(cond.Reason),
			Message: RedactSecrets(cond.Message),
		})
	}
	return snap
}

func findContainerStatus(pod *corev1.Pod, name string) *corev1.ContainerStatus {
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == name {
			return &pod.Status.ContainerStatuses[i]
		}
	}
	return nil
}

func containerState(s corev1.ContainerState) (state, reason string, exit *int32) {
	switch {
	case s.Running != nil:
		return "running", "", nil
	case s.Terminated != nil:
		return "terminated", s.Terminated.Reason, &s.Terminated.ExitCode
	case s.Waiting != nil:
		return "waiting", s.Waiting.Reason, nil
	default:
		return "unknown", "", nil
	}
}

func marshalEvent(ev *corev1.Event) (string, error) {
	payload, err := json.Marshal(struct {
		Reason  string `json:"reason"`
		Type    string `json:"type"`
		Message string `json:"message"`
		Count   int32  `json:"count"`
		Source  string `json:"source,omitempty"`
	}{
		Reason:  ev.Reason,
		Type:    ev.Type,
		Message: RedactSecrets(ev.Message),
		Count:   ev.Count,
		Source:  ev.Source.Component,
	})
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

type containerMetric struct {
	Name   string `json:"name"`
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

func buildMetricSummary(m *metricsv1beta1.PodMetrics) []containerMetric {
	summary := make([]containerMetric, 0, len(m.Containers))
	for _, cm := range m.Containers {
		summary = append(summary, containerMetric{
			Name:   cm.Name,
			CPU:    cm.Usage.Cpu().String(),
			Memory: cm.Usage.Memory().String(),
		})
	}
	return summary
}
