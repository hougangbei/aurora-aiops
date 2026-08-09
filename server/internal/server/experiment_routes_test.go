package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/hougangbei/aurora-aiops/server/internal/auth"
	"github.com/hougangbei/aurora-aiops/server/internal/experiment"
	"github.com/hougangbei/aurora-aiops/server/internal/kube"
	"github.com/hougangbei/aurora-aiops/server/internal/service"
	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

func newExperimentTestRouter(t *testing.T) (*gin.Engine, *auth.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, sessionTTL, time.Now)
	seedUser(t, authRepo, "u-viewer", "viewer", "correct-password", auth.RoleViewer)
	seedUser(t, authRepo, "u-admin", "admin", "correct-password", auth.RoleAdmin)

	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{})
	clusterService := service.NewClusterService(&kube.Client{
		Kubernetes: kubeClient,
		Metrics:    metricsfake.NewSimpleClientset(),
		RESTConfig: &rest.Config{},
	})

	router := gin.New()
	api := router.Group("/api/v1")
	authorized := api.Group("/")
	authorized.Use(RequireSession(authService, clusterService))
	registerExperimentRoutes(authorized, experiment.NewRunRepository(db))
	return router, authService
}

func TestExperimentMetricsJSONAndCSV(t *testing.T) {
	router, authService := newExperimentTestRouter(t)
	cookie := routeSessionCookie(t, authService, "admin", "correct-password")

	// 记录 2 条 multi_agent 运行。
	for i, top1 := range []bool{true, false} {
		body := `{"id":"run-` + string(rune('a'+i)) + `","group":"multi_agent","seed":42,"scenario":"image-pull","expectedRootCause":"image_pull_error","top1Correct":` + strconvBool(top1) + `,"top3Contains":true,"mttdSeconds":12.5,"evidenceCompleteness":0.9,"highRiskIntercepted":true,"tokensUsed":150}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/experiments/runs", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cookie", cookie)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("add run status=%d body=%s", w.Code, w.Body.String())
		}
	}

	// JSON metrics
	req := httptest.NewRequest(http.MethodGet, "/api/v1/experiments/metrics", nil)
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics status=%d", w.Code)
	}
	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Metrics []experiment.Metrics `json:"metrics"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Metrics) != 3 {
		t.Fatalf("metrics groups=%d want 3", len(data.Metrics))
	}
	multi := data.Metrics[2]
	if multi.SampleCount != 2 || multi.Top1Rate != 0.5 {
		t.Fatalf("multi metrics=%+v", multi)
	}

	// CSV export with UTF-8 BOM
	req = httptest.NewRequest(http.MethodGet, "/api/v1/experiments/metrics?format=csv", nil)
	req.Header.Set("Cookie", cookie)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
		t.Fatal("CSV missing UTF-8 BOM")
	}
	if !strings.Contains(body, "multi_agent") || !strings.Contains(body, "sampleCount") {
		t.Fatalf("CSV missing columns/rows: %q", body[:200])
	}
}

func TestExperimentRunRejectsUnknownGroup(t *testing.T) {
	router, authService := newExperimentTestRouter(t)
	body := `{"id":"run-x","group":"unknown","seed":1,"scenario":"x","expectedRootCause":"x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/experiments/runs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", routeSessionCookie(t, authService, "admin", "correct-password"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", w.Code)
	}
}

func strconvBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
