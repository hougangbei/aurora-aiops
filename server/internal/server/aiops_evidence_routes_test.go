package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/heihuzicity-tech/aurora-aiops/server/internal/aiops"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/audit"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/auth"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/evidence"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/kube"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/llm"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/remediation"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/service"
	"github.com/heihuzicity-tech/aurora-aiops/server/internal/store"
)

type routeFakeCollector struct{}

func (routeFakeCollector) Collect(_ context.Context, _ evidence.Target, _ evidence.Window) ([]evidence.Node, []evidence.Edge, error) {
	t0 := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	return []evidence.Node{
		{ID: "snapshot", Kind: evidence.NodeKindSnapshot, Payload: `{"phase":"Running"}`, ObservedAt: t0},
		{ID: "log-app", Kind: evidence.NodeKindLog, Payload: "level=error boom", ObservedAt: t0.Add(time.Second)},
	}, []evidence.Edge{}, nil
}

type routeFakeLLM struct {
	invalidTriage bool
}

func (f *routeFakeLLM) GenerateJSON(_ context.Context, req llm.Request) (llm.Response, error) {
	content := ""
	if len(req.Messages) > 0 {
		content = req.Messages[0].Content
	}
	switch {
	case strings.Contains(content, "TRIAGE role"):
		if f.invalidTriage {
			return llm.Response{Text: "not json at all"}, nil
		}
		return llm.Response{Text: `{"summary":"pod crash","severity":"critical","rationale":"r"}`, Model: "fake"}, nil
	case strings.Contains(content, "COLLECTOR role"):
		return llm.Response{Text: `{"targetKind":"Pod","targetName":"api-0","commands":["snapshot"]}`, Model: "fake"}, nil
	case strings.Contains(content, "ROOT CAUSE role"):
		return llm.Response{Text: `{"candidates":[{"summary":"oomkilled","confidence":0.9,"evidenceIds":["snapshot"],"verificationSteps":["check events"]}]}`, Model: "fake"}, nil
	case strings.Contains(content, "REMEDIATION role"):
		return llm.Response{Text: `{"actions":[{"command":"restart deployment api-0","reason":"restart after crash","risk":"medium","kind":"restart_deployment","namespace":"default","resourceKind":"Deployment","resourceName":"api-0"}]}`, Model: "fake"}, nil
	case strings.Contains(content, "RISK REVIEW role"):
		return llm.Response{Text: `{"riskLevel":"low","approved":true}`, Model: "fake"}, nil
	}
	return llm.Response{}, errors.New("unrecognized role prompt")
}

// newAIOpsRouteRouter builds a router with platform auth + the full aiops route
// set (evidence, runs, reanalyze, events) backed by a real SQLite database.
func newAIOpsRouteRouter(t *testing.T) (*gin.Engine, *aiops.Service, *aiops.Workflow, *aiops.EventStore, *auth.Service, *routeFakeLLM) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, sessionTTL, time.Now)
	seedUser(t, authRepo, "u-admin", "admin", "correct-password", auth.RoleAdmin)
	seedUser(t, authRepo, "u-op", "operator", "correct-password", auth.RoleOperator)
	seedUser(t, authRepo, "u-viewer", "viewer", "correct-password", auth.RoleViewer)

	aiopsRepo := aiops.NewRepository(db)
	svc := aiops.NewService(aiopsRepo)
	events := aiops.NewEventStore(db)
	llm := &routeFakeLLM{}
	wf := aiops.NewWorkflow(aiops.WorkflowOptions{
		DB:              db,
		Incidents:       aiopsRepo,
		Runs:            aiops.NewRunRepository(db),
		Evidence:        evidence.NewRepository(db),
		Collector:       routeFakeCollector{},
		LLM:             llm,
		ModelConfigured: true,
		Model:           "fake",
		Events:          events,
	})

	replicas := int32(1)
	kubeClient := kubefake.NewSimpleClientset(
		&corev1.Node{},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api-0"}},
			},
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: "True"}},
			},
		},
	)
	clusterService := service.NewClusterService(&kube.Client{
		Kubernetes: kubeClient,
		Metrics:    metricsfake.NewSimpleClientset(),
		RESTConfig: &rest.Config{},
	})

	auditRepo := audit.NewRepository(db)
	snapshotStore := remediation.NewSnapshotStore(db)
	executor := remediation.NewExecutor(
		&remediation.KubeExecutorClient{Client: kubeClient, RolloutTimeout: time.Second},
		remediation.SnapshotterFunc(func(ctx context.Context, incidentID, ns, kind, name string) (remediation.Snapshot, error) {
			return remediation.SnapshotResource(ctx, kubeClient, incidentID, ns, kind, name)
		}),
		snapshotStore,
		auditRepo,
	)
	remediationService := remediation.NewService(svc, aiops.NewRunRepository(db), kubeClient, executor, auditRepo, snapshotStore, remediation.NewDecisionStore(db))

	router := gin.New()
	api := router.Group("/api/v1")
	authorized := api.Group("/")
	authorized.Use(RequireSession(authService, clusterService))
	registerAIOpsRoutes(authorized, aiopsRoutesDeps{svc: svc, workflow: wf, events: events, remediation: remediationService})
	return router, svc, wf, events, authService, llm
}

func routeSessionCookie(t *testing.T, authService *auth.Service, username, password string) string {
	t.Helper()
	raw, _, err := authService.Login(context.Background(), username, password)
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	return sessionCookieName + "=" + raw
}

func mustRouteIncident(t *testing.T, svc *aiops.Service) aiops.Incident {
	t.Helper()
	inc, err := svc.Create(context.Background(), aiops.CreateIncidentInput{
		Summary:      "pod crash loop",
		Severity:     "critical",
		Namespace:    "default",
		ResourceKind: "Pod",
		ResourceName: "api-0",
	})
	if err != nil {
		t.Fatalf("create incident: %v", err)
	}
	return inc
}

func TestEvidenceRouteReturnsNodesAndEdges(t *testing.T) {
	router, svc, wf, _, authService, _ := newAIOpsRouteRouter(t)
	ctx := context.Background()
	inc := mustRouteIncident(t, svc)
	if err := wf.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents/"+inc.ID+"/evidence", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "viewer", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Nodes []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"nodes"`
		Edges []json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Nodes) != 2 {
		t.Fatalf("nodes=%d want 2", len(data.Nodes))
	}
	if data.Nodes[0].ID != "snapshot" || data.Nodes[0].Kind != "snapshot" {
		t.Fatalf("node[0]=%+v", data.Nodes[0])
	}
}

func TestCreateIncidentAutoRunsWorkflow(t *testing.T) {
	router, _, _, _, authService, _ := newAIOpsRouteRouter(t)

	body := `{"summary":"Pod crash loop","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "admin", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var incident struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &incident); err != nil {
		t.Fatal(err)
	}
	// The diagnostic workflow ran as part of creation: the incident left the
	// received state and collected evidence.
	if incident.Status != string(aiops.StatusAwaitingApproval) {
		t.Fatalf("create returned status=%s want awaiting_approval", incident.Status)
	}
}

func TestEvidenceRouteNotFound(t *testing.T) {
	router, _, _, _, authService, _ := newAIOpsRouteRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents/inc-missing/evidence", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "viewer", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRunsRouteReturnsRuns(t *testing.T) {
	router, svc, wf, _, authService, _ := newAIOpsRouteRouter(t)
	ctx := context.Background()
	inc := mustRouteIncident(t, svc)
	if err := wf.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents/"+inc.ID+"/runs", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "viewer", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Runs []struct {
			Role   string `json:"role"`
			Status string `json:"status"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Runs) != 5 {
		t.Fatalf("runs=%d want 5", len(data.Runs))
	}
	if data.Runs[0].Role != "triage" || data.Runs[0].Status != "succeeded" {
		t.Fatalf("run[0]=%+v", data.Runs[0])
	}
}

func TestReanalyzeRouteRoles(t *testing.T) {
	router, svc, wf, _, authService, llm := newAIOpsRouteRouter(t)
	ctx := context.Background()
	inc := mustRouteIncident(t, svc)

	// Drive the incident to a terminal failed state first: the triage role
	// returns invalid JSON, so the workflow records a failure and stops.
	llm.invalidTriage = true
	if err := wf.Run(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, inc.ID)
	if got.Status != aiops.StatusFailed {
		t.Fatalf("setup status=%s want failed", got.Status)
	}

	// Unauthenticated: 401.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+inc.ID+"/reanalyze", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anon status=%d want 401", w.Code)
	}

	// Viewer: 403.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+inc.ID+"/reanalyze", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "viewer", "correct-password"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer status=%d want 403", w.Code)
	}

	// Operator: allowed; workflow reruns to awaiting_approval.
	llm.invalidTriage = false
	req = httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+inc.ID+"/reanalyze", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "operator", "correct-password"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("operator status=%d body=%s", w.Code, w.Body.String())
	}
	got2, _ := svc.Get(ctx, inc.ID)
	if got2.Status != aiops.StatusAwaitingApproval {
		t.Fatalf("after reanalyze status=%s want awaiting_approval", got2.Status)
	}
}

func TestReanalyzeRouteRejectsActiveStatus(t *testing.T) {
	router, svc, _, _, authService, _ := newAIOpsRouteRouter(t)
	inc := mustRouteIncident(t, svc) // status received, never run

	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+inc.ID+"/reanalyze", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "admin", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func TestReanalyzeRouteNotFound(t *testing.T) {
	router, _, _, _, authService, _ := newAIOpsRouteRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/inc-missing/reanalyze", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "admin", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", w.Code)
	}
}

func TestIncidentEventsSSEResume(t *testing.T) {
	router, svc, _, events, authService, _ := newAIOpsRouteRouter(t)
	ctx := context.Background()
	inc := mustRouteIncident(t, svc)

	if _, err := events.Append(ctx, inc.ID, aiops.EventRunStarted, `{"n":1}`); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Append(ctx, inc.ID, aiops.EventRunCompleted, `{"n":2}`); err != nil {
		t.Fatal(err)
	}

	getSSE := func(lastEventID string) string {
		t.Helper()
		reqCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/aiops/incidents/"+inc.ID+"/events?lastEventId="+lastEventID, nil).WithContext(reqCtx)
		req.Header.Set("Cookie", routeSessionCookie(t, authService, "viewer", "correct-password"))
		w := httptest.NewRecorder()
		go func() {
			time.Sleep(250 * time.Millisecond)
			cancel()
		}()
		router.ServeHTTP(w, req)
		return w.Body.String()
	}

	full := getSSE("0")
	if !strings.Contains(full, "id: 1") || !strings.Contains(full, "id: 2") {
		t.Fatalf("full replay missing events: %q", full)
	}
	if !strings.Contains(full, "event: run_started") || !strings.Contains(full, "event: run_completed") {
		t.Fatalf("full replay missing event types: %q", full)
	}

	resumed := getSSE("1")
	if strings.Contains(resumed, "id: 1") {
		t.Fatalf("resume should skip event 1: %q", resumed)
	}
	if !strings.Contains(resumed, "id: 2") {
		t.Fatalf("resume missing event 2: %q", resumed)
	}
}

func createIncidentViaRoute(t *testing.T, router *gin.Engine, authService *auth.Service, cookieUser string) (string, string) {
	t.Helper()
	body := `{"summary":"pod crash","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", routeSessionCookie(t, authService, cookieUser, "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var incident struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &incident); err != nil {
		t.Fatal(err)
	}
	return incident.ID, incident.Status
}

func TestRemediationRoutesRBAC(t *testing.T) {
	router, _, _, _, authService, _ := newAIOpsRouteRouter(t)

	// approve as viewer -> 403
	id, _ := createIncidentViaRoute(t, router, authService, "admin")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/approve-remediation", strings.NewReader(`{"reason":"looks safe"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "viewer", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("viewer approve status=%d want 403", w.Code)
	}

	// execute as operator -> 403 (admin only)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/execute-remediation", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "operator", "correct-password"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("operator execute status=%d want 403", w.Code)
	}

	// rollback as operator -> 403
	req = httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/rollback", strings.NewReader(`{"snapshotId":"snap-x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "operator", "correct-password"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("operator rollback status=%d want 403", w.Code)
	}

	// rollback as admin with an unknown snapshot -> 404
	req = httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/rollback", strings.NewReader(`{"snapshotId":"snap-missing"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "admin", "correct-password"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("admin rollback missing snapshot status=%d want 404", w.Code)
	}
}

func TestApproveExecuteRemediationFlow(t *testing.T) {
	router, svc, _, _, authService, _ := newAIOpsRouteRouter(t)
	ctx := context.Background()

	id, status := createIncidentViaRoute(t, router, authService, "admin")
	if status != string(aiops.StatusAwaitingApproval) {
		t.Fatalf("create status=%s want awaiting_approval", status)
	}

	// approve as operator -> 200
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/approve-remediation", strings.NewReader(`{"reason":"验证过脚本与变更范围，同意执行"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "operator", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ := svc.Get(ctx, id)
	if got.Status != aiops.StatusApproved {
		t.Fatalf("after approve status=%s want approved", got.Status)
	}

	// execute as admin -> 200, incident resolved
	req = httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/execute-remediation", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "admin", "correct-password"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("execute status=%d body=%s", w.Code, w.Body.String())
	}
	got, _ = svc.Get(ctx, id)
	if got.Status != aiops.StatusResolved {
		t.Fatalf("after execute status=%s want resolved", got.Status)
	}

	// second execute is blocked: incident no longer approved -> 400
	req = httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/execute-remediation", nil)
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "admin", "correct-password"))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("second execute status=%d want 400", w.Code)
	}
}

func TestConcurrentApproveRejectRouteConflict(t *testing.T) {
	router, _, _, _, authService, _ := newAIOpsRouteRouter(t)
	id, _ := createIncidentViaRoute(t, router, authService, "admin") // status now awaiting_approval
	cookie := routeSessionCookie(t, authService, "operator", "correct-password")
	body := `{"reason":"concurrent decision"}`

	do := func(suffix string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents/"+id+"/"+suffix, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", cookie)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	var appRec, rejRec *httptest.ResponseRecorder
	wg.Add(2)
	go func() { defer wg.Done(); <-start; appRec = do("approve-remediation") }()
	go func() { defer wg.Done(); <-start; rejRec = do("reject-remediation") }()
	close(start)
	wg.Wait()

	okCount, conflictCount := 0, 0
	for _, c := range []int{appRec.Code, rejRec.Code} {
		if c == http.StatusOK {
			okCount++
		}
		if c == http.StatusConflict {
			conflictCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("okCount=%d want 1 (approve=%d reject=%d)", okCount, appRec.Code, rejRec.Code)
	}
	if conflictCount != 1 {
		t.Fatalf("conflictCount=%d want 1 (approve=%d reject=%d)", conflictCount, appRec.Code, rejRec.Code)
	}

	conflictBody := rejRec.Body.String()
	if appRec.Code == http.StatusConflict {
		conflictBody = appRec.Body.String()
	}
	if !strings.Contains(conflictBody, `"code":"STATE_TRANSITION_CONFLICT"`) {
		t.Fatalf("conflict body missing STATE_TRANSITION_CONFLICT: %s", conflictBody)
	}
}
