package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/hougangbei/aurora-aiops/server/internal/cluster"
)

type fakePodAPI struct {
	pod           *corev1.Pod
	podErr        error
	events        *corev1.EventList
	eventsErr     error
	fieldSel      string
	logs          string
	logsErr       error
	logsOpts      *corev1.PodLogOptions
	logsContainer string
	metrics       *metricsv1beta1.PodMetrics
	metricsErr    error
	metricsCalled bool
}

func (f *fakePodAPI) GetPod(_ context.Context, _, _ string) (*corev1.Pod, error) {
	return f.pod, f.podErr
}
func (f *fakePodAPI) ListEvents(_ context.Context, _, fieldSelector string) (*corev1.EventList, error) {
	f.fieldSel = fieldSelector
	return f.events, f.eventsErr
}
func (f *fakePodAPI) GetPodLogs(_ context.Context, _, _, container string, opts *corev1.PodLogOptions) (string, error) {
	f.logsContainer = container
	f.logsOpts = opts
	return f.logs, f.logsErr
}
func (f *fakePodAPI) GetPodMetrics(_ context.Context, _, _ string) (*metricsv1beta1.PodMetrics, error) {
	f.metricsCalled = true
	if f.metricsErr != nil {
		return nil, f.metricsErr
	}
	if f.metrics == nil {
		return &metricsv1beta1.PodMetrics{ObjectMeta: metav1.ObjectMeta{Name: "x"}}, nil
	}
	return f.metrics, nil
}

func fullCapabilities() cluster.Capabilities {
	return cluster.Capabilities{Nodes: true, Events: true, PodLogs: true, Metrics: true}
}

func makePod(ns, name string) *corev1.Pod {
	started := metav1.NewTime(time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC))
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: types.UID("uid-" + name)},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{{
				Name:  "app",
				Image: "nginx:1.27",
				Env: []corev1.EnvVar{{Name: "API_KEY", Value: "supersecretenvvalue"},
					{Name: "REDIS_URL", Value: "redis://:hunter2@10.0.0.6:6379"}},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.5",
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", RestartCount: 3, Ready: true,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: started}},
			}},
		},
	}
}

func makeEvent(uid, name, reason, message string, ts time.Time) corev1.Event {
	return corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: "evt-" + uid, Namespace: "default", UID: types.UID(uid)},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: name, Namespace: "default"},
		Reason:         reason,
		Message:        message,
		Type:           "Warning",
		Count:          1,
		LastTimestamp:  metav1.NewTime(ts),
	}
}

func makePodMetrics(ns, name string) *metricsv1beta1.PodMetrics {
	cpu := resource.MustParse("10m")
	mem := resource.MustParse("50Mi")
	return &metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Containers: []metricsv1beta1.ContainerMetrics{{
			Name:  "app",
			Usage: corev1.ResourceList{corev1.ResourceCPU: cpu, corev1.ResourceMemory: mem},
		}},
	}
}

func collectTarget() (Target, Window) {
	return Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{}
}

func TestKubernetesCollectorCollectsEvidence(t *testing.T) {
	api := &fakePodAPI{
		pod: makePod("default", "api-0"),
		events: &corev1.EventList{Items: []corev1.Event{
			makeEvent("e1", "api-0", "BackOff", "Back-off restarting failed container", time.Date(2026, 8, 1, 8, 1, 0, 0, time.UTC)),
			makeEvent("e2", "api-0", "Pulled", "Container image already present", time.Date(2026, 8, 1, 8, 0, 30, 0, time.UTC)),
		}},
		logs:    "line1\nline2\nerror=boom\n",
		metrics: makePodMetrics("default", "api-0"),
	}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	target, window := collectTarget()
	nodes, edges, err := c.Collect(context.Background(), target, window)
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[NodeKind]int{}
	for _, n := range nodes {
		kinds[n.Kind]++
	}
	for _, want := range []NodeKind{NodeKindSnapshot, NodeKindEvent, NodeKindLog, NodeKindMetric} {
		if kinds[want] == 0 {
			t.Fatalf("missing %s node: kinds=%v", want, kinds)
		}
	}
	if kinds[NodeKindEvent] != 2 {
		t.Fatalf("expected 2 event nodes, got %d", kinds[NodeKindEvent])
	}

	// Every edge must terminate at the snapshot root (acyclic by construction).
	for _, e := range edges {
		if e.ToID != "snapshot" {
			t.Fatalf("edge %s->%s does not target snapshot", e.FromID, e.ToID)
		}
		if e.Relation != RelationSupports {
			t.Fatalf("edge relation=%s want supports", e.Relation)
		}
	}

	if api.logsOpts == nil || api.logsOpts.TailLines == nil || *api.logsOpts.TailLines != collectTailLines {
		t.Fatalf("log options TailLines=%v, want %d", api.logsOpts, collectTailLines)
	}
	if api.logsContainer != "app" {
		t.Fatalf("log container=%q want app", api.logsContainer)
	}
	if !api.metricsCalled {
		t.Fatal("metrics client not called when capability enabled")
	}
	if !strings.Contains(api.fieldSel, "involvedObject.name=api-0") {
		t.Fatalf("event field selector=%q", api.fieldSel)
	}
}

func TestKubernetesCollectorLogsSinceWindow(t *testing.T) {
	api := &fakePodAPI{pod: makePod("default", "api-0"), events: &corev1.EventList{}, logs: "x"}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	start := time.Date(2026, 8, 1, 7, 30, 0, 0, time.UTC)
	_, _, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{Start: start})
	if err != nil {
		t.Fatal(err)
	}
	if api.logsOpts.SinceTime == nil {
		t.Fatal("SinceTime not set from window.Start")
	}
	if !api.logsOpts.SinceTime.Time.Equal(start) {
		t.Fatalf("SinceTime=%v want %v", api.logsOpts.SinceTime.Time, start)
	}
}

func TestKubernetesCollectorRedactsSecrets(t *testing.T) {
	api := &fakePodAPI{
		pod:    makePod("default", "api-0"),
		events: &corev1.EventList{},
		logs:   "level=error token=eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnop status=500\n",
	}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	nodes, _, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{})
	if err != nil {
		t.Fatal(err)
	}

	var logNode *Node
	for i := range nodes {
		if nodes[i].Kind == NodeKindLog {
			logNode = &nodes[i]
		}
	}
	if logNode == nil {
		t.Fatal("no log node collected")
	}
	if strings.Contains(logNode.Payload, "eyJhbGciOiJIUzI1NiJ9") {
		t.Fatalf("token leaked in log evidence: %s", logNode.Payload)
	}
	if !strings.Contains(logNode.Payload, "[REDACTED]") {
		t.Fatalf("no redaction marker in log evidence: %s", logNode.Payload)
	}

	// Snapshot must not contain container env values or redis password.
	var snapNode *Node
	for i := range nodes {
		if nodes[i].Kind == NodeKindSnapshot {
			snapNode = &nodes[i]
		}
	}
	if snapNode == nil {
		t.Fatal("no snapshot node collected")
	}
	for _, secret := range []string{"supersecretenvvalue", "hunter2"} {
		if strings.Contains(snapNode.Payload, secret) {
			t.Fatalf("snapshot leaked %q: %s", secret, snapNode.Payload)
		}
	}
}

func TestKubernetesCollectorMetricsOff(t *testing.T) {
	api := &fakePodAPI{pod: makePod("default", "api-0"), events: &corev1.EventList{}, logs: "x"}
	caps := fullCapabilities()
	caps.Metrics = false
	c := NewKubernetesCollector(api, caps, 0)

	nodes, _, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{})
	if err != nil {
		t.Fatal(err)
	}
	if api.metricsCalled {
		t.Fatal("metrics called when capability is off")
	}
	for _, n := range nodes {
		if n.Kind == NodeKindMetric {
			t.Fatal("metric node present when capability is off")
		}
	}
}

func TestKubernetesCollectorMetricsAPIErrorReturnsPartial(t *testing.T) {
	api := &fakePodAPI{
		pod:        makePod("default", "api-0"),
		events:     &corev1.EventList{},
		logs:       "x",
		metrics:    nil,
		metricsErr: errors.New("metrics API 503"),
	}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	nodes, edges, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{})
	if err == nil {
		t.Fatal("expected non-nil error for failed optional capability")
	}
	var colErr *CollectionError
	if !errors.As(err, &colErr) {
		t.Fatalf("expected *CollectionError, got %T: %v", err, err)
	}
	if len(colErr.Failed) == 0 {
		t.Fatal("CollectionError.Failed empty")
	}
	// Core evidence survives the optional failure.
	var sawSnapshot, sawLog bool
	for _, n := range nodes {
		if n.Kind == NodeKindSnapshot {
			sawSnapshot = true
		}
		if n.Kind == NodeKindLog {
			sawLog = true
		}
	}
	if !sawSnapshot || !sawLog {
		t.Fatalf("core evidence lost on optional failure: snapshot=%v log=%v", sawSnapshot, sawLog)
	}
	if len(edges) == 0 {
		t.Fatal("edges lost on optional failure")
	}
}

func TestKubernetesCollectorCoreFailureTerminates(t *testing.T) {
	api := &fakePodAPI{podErr: errors.New("the server could not find the requested resource")}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	nodes, edges, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "missing"}, Window{})
	if err == nil {
		t.Fatal("expected hard error for core API failure")
	}
	var colErr *CollectionError
	if errors.As(err, &colErr) {
		t.Fatalf("core failure must not be a CollectionError: %v", err)
	}
	if nodes != nil || edges != nil {
		t.Fatal("expected nil evidence on core failure")
	}
}

func TestKubernetesCollectorLogFailureIsCore(t *testing.T) {
	api := &fakePodAPI{
		pod:     makePod("default", "api-0"),
		events:  &corev1.EventList{},
		logsErr: errors.New("forbidden"),
	}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	_, _, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{})
	if err == nil {
		t.Fatal("expected hard error for log failure")
	}
	var colErr *CollectionError
	if errors.As(err, &colErr) {
		t.Fatalf("log failure must not be a CollectionError: %v", err)
	}
}

func TestKubernetesCollectorSnapshotShape(t *testing.T) {
	api := &fakePodAPI{pod: makePod("default", "api-0"), events: &corev1.EventList{}, logs: "x"}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	nodes, _, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{})
	if err != nil {
		t.Fatal(err)
	}
	var snapNode *Node
	for i := range nodes {
		if nodes[i].Kind == NodeKindSnapshot {
			snapNode = &nodes[i]
		}
	}
	if snapNode == nil {
		t.Fatal("no snapshot node")
	}
	var shape struct {
		Name       string `json:"name"`
		Phase      string `json:"phase"`
		NodeName   string `json:"nodeName"`
		Containers []struct {
			Name         string `json:"name"`
			RestartCount int32  `json:"restartCount"`
			State        string `json:"state"`
		} `json:"containers"`
	}
	if err := json.Unmarshal([]byte(snapNode.Payload), &shape); err != nil {
		t.Fatalf("snapshot payload not valid JSON: %v", err)
	}
	if shape.Name != "api-0" || shape.Phase != "Running" || shape.NodeName != "node-1" {
		t.Fatalf("unexpected snapshot shape: %+v", shape)
	}
	if len(shape.Containers) != 1 || shape.Containers[0].Name != "app" ||
		shape.Containers[0].RestartCount != 3 || shape.Containers[0].State != "running" {
		t.Fatalf("unexpected container snapshot: %+v", shape.Containers)
	}
}

func TestKubernetesCollectorEmptyLogsSkipped(t *testing.T) {
	api := &fakePodAPI{pod: makePod("default", "api-0"), events: &corev1.EventList{}, logs: "   \n"}
	c := NewKubernetesCollector(api, fullCapabilities(), 0)

	nodes, _, err := c.Collect(context.Background(), Target{Namespace: "default", Kind: "Pod", Name: "api-0"}, Window{})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Kind == NodeKindLog {
			t.Fatalf("blank log node persisted: %q", n.Payload)
		}
	}
}
