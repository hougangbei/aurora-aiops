package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/audit"
	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/buildinfo"
	"github.com/heihuzicity-tech/kubejojo/server/internal/config"
	"github.com/heihuzicity-tech/kubejojo/server/internal/evidence"
	"github.com/heihuzicity-tech/kubejojo/server/internal/experiment"
	"github.com/heihuzicity-tech/kubejojo/server/internal/kube"
	"github.com/heihuzicity-tech/kubejojo/server/internal/remediation"
	"github.com/heihuzicity-tech/kubejojo/server/internal/service"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

// newPlatformRBACRouter builds the full production router with platform auth,
// the EnforcePlatformRBAC middleware, and every /api/v1 route registered, so the
// role matrix and route-inventory tests exercise real route registrations.
func newPlatformRBACRouter(t *testing.T) (*gin.Engine, *auth.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, err := store.Open(filepath.Join(t.TempDir(), "rbac.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, sessionTTL, time.Now)
	seedUser(t, authRepo, "u-admin", "admin", "correct-password", auth.RoleAdmin)
	seedUser(t, authRepo, "u-operator", "operator", "correct-password", auth.RoleOperator)
	seedUser(t, authRepo, "u-viewer", "viewer", "correct-password", auth.RoleViewer)

	aiopsRepo := aiops.NewRepository(db)
	aiopsService := aiops.NewService(aiopsRepo)
	events := aiops.NewEventStore(db)
	wf := aiops.NewWorkflow(aiops.WorkflowOptions{
		DB:              db,
		Incidents:       aiopsRepo,
		Runs:            aiops.NewRunRepository(db),
		Evidence:        evidence.NewRepository(db),
		Collector:       routeFakeCollector{},
		LLM:             &routeFakeLLM{},
		ModelConfigured: true,
		Model:           "fake",
		Events:          events,
	})

	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{})
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
	remediationService := remediation.NewService(aiopsService, aiops.NewRunRepository(db), kubeClient, executor, auditRepo, snapshotStore)

	updateService := service.NewUpdateService(buildinfo.Info{}, config.UpdateConfig{}, false)
	systemLockService := service.NewSystemOperationLockService()

	router := newRouter(
		nil, // sharedClient is unused inside newRouter
		clusterService,
		nil, // probe is only invoked on connection requests, which these tests never send
		authService,
		updateService,
		systemLockService,
		aiopsService,
		wf,
		events,
		remediationService,
		experiment.NewRunRepository(db),
		buildinfo.Info{},
	)
	return router, authService
}

func rbacCookie(t *testing.T, router *gin.Engine, username string) *http.Cookie {
	t.Helper()
	_, cookie := doLogin(t, router, username, "correct-password")
	if cookie == nil {
		t.Fatalf("no session cookie for %s", username)
	}
	return cookie
}

func doRequest(t *testing.T, router *gin.Engine, method, path, body, cookieUser string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookieUser != "" {
		req.AddCookie(rbacCookie(t, router, cookieUser))
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

const validExperimentRunBody = `{"id":"r1","group":"rules","seed":1,"scenario":"dns","expectedRootCause":"dns","top1Correct":true,"top3Contains":true,"mttdSeconds":1.0,"evidenceCompleteness":1.0,"highRiskIntercepted":true,"tokensUsed":100}`

func TestPlatformRBACRoleMatrix(t *testing.T) {
	router, _ := newPlatformRBACRouter(t)

	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		user     string
		wantCode int
	}{
		{"viewer reads incidents", http.MethodGet, "/api/v1/aiops/incidents", "", "viewer", http.StatusOK},
		{"viewer cannot create incident", http.MethodPost, "/api/v1/aiops/incidents", `{"summary":"s","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`, "viewer", http.StatusForbidden},
		{"operator creates incident", http.MethodPost, "/api/v1/aiops/incidents", `{"summary":"s","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`, "operator", http.StatusCreated},
		{"operator cannot record experiment run", http.MethodPost, "/api/v1/experiments/runs", validExperimentRunBody, "operator", http.StatusForbidden},
		{"admin records experiment run", http.MethodPost, "/api/v1/experiments/runs", validExperimentRunBody, "admin", http.StatusCreated},
		{"viewer reads experiment metrics", http.MethodGet, "/api/v1/experiments/metrics", "", "viewer", http.StatusOK},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, router, tt.method, tt.path, tt.body, tt.user)
			if rec.Code != tt.wantCode {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tt.wantCode, rec.Body.String())
			}
		})
	}
}

// TestEnforcePlatformRBACDefaultAdmin registers a throwaway PUT route on a
// middleware harness and asserts the default-admin branch: a viewer is denied
// before the handler runs, while an admin reaches it. No Kubernetes write occurs.
func TestEnforcePlatformRBACDefaultAdmin(t *testing.T) {
	// Build a minimal harness mirroring the production middleware order:
	// RequireSession first, then EnforcePlatformRBAC, with one PUT route that
	// also carries a route-local RequireAdmin for defense in depth.
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "harness.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	authRepo := auth.NewRepository(db)
	authService2 := auth.NewService(authRepo, sessionTTL, time.Now)
	seedUser(t, authRepo, "h-admin", "admin", "correct-password", auth.RoleAdmin)
	seedUser(t, authRepo, "h-viewer", "viewer", "correct-password", auth.RoleViewer)
	kubeClient := kubefake.NewSimpleClientset(&corev1.Node{})
	clusterService := service.NewClusterService(&kube.Client{
		Kubernetes: kubeClient,
		Metrics:    metricsfake.NewSimpleClientset(),
		RESTConfig: &rest.Config{},
	})
	harness := gin.New()
	api := harness.Group("/api/v1")
	registerAuthRoutes(api, authService2, sessionTTL)
	authorized := api.Group("/")
	authorized.Use(RequireSession(authService2, clusterService))
	authorized.Use(EnforcePlatformRBAC())
	authorized.PUT("/test-resource", RequireAdmin(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"reached": true})
	})

	login := func(username string) *http.Cookie {
		body := `{"username":"` + username + `","password":"correct-password"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		harness.ServeHTTP(rec, req)
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookieName {
				return c
			}
		}
		t.Fatalf("no cookie for %s", username)
		return nil
	}

	viewerReq := httptest.NewRequest(http.MethodPut, "/api/v1/test-resource", nil)
	viewerReq.AddCookie(login("viewer"))
	viewerRec := httptest.NewRecorder()
	harness.ServeHTTP(viewerRec, viewerReq)
	if viewerRec.Code != http.StatusForbidden {
		t.Fatalf("viewer status=%d want 403 body=%s", viewerRec.Code, viewerRec.Body.String())
	}
	if strings.Contains(viewerRec.Body.String(), "reached") {
		t.Fatalf("handler must not run for viewer: %s", viewerRec.Body.String())
	}

	adminReq := httptest.NewRequest(http.MethodPut, "/api/v1/test-resource", nil)
	adminReq.AddCookie(login("admin"))
	adminRec := httptest.NewRecorder()
	harness.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin status=%d want 200 body=%s", adminRec.Code, adminRec.Body.String())
	}
	if !strings.Contains(adminRec.Body.String(), "reached") {
		t.Fatalf("handler must run for admin: %s", adminRec.Body.String())
	}
}

func TestPlatformRBACClassifiesEveryRegisteredRoute(t *testing.T) {
	router, _ := newPlatformRBACRouter(t)

	const (
		execWs = "/api/v1/pods/:namespace/:name/exec/ws"
		login  = "/api/v1/auth/login"
		logout = "/api/v1/auth/logout"
		apiV1  = "/api/v1"
	)
	readOnly := func(method string) bool {
		return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
	}

	var classified int
	for _, ri := range router.Routes() {
		if !strings.HasPrefix(ri.Path, apiV1) {
			continue
		}
		if ri.Path == login { // anonymous entry, lives outside the authorized group
			continue
		}
		classified++
		got := requiredPlatformRoles(ri.Method, ri.Path)
		_, isOperatorPath := operatorWritePaths[ri.Method+" "+ri.Path]

		switch {
		case readOnly(ri.Method):
			if ri.Path == execWs {
				if !slices.Equal(got, []auth.Role{auth.RoleAdmin}) {
					t.Errorf("%s %s: roles=%v want [admin]", ri.Method, ri.Path, got)
				}
			} else if len(got) != 0 {
				t.Errorf("%s %s: read route must be ungated, roles=%v", ri.Method, ri.Path, got)
			}
		case ri.Method == http.MethodPost && ri.Path == logout:
			if len(got) != 0 {
				t.Errorf("%s %s: logout must be authenticated-only, roles=%v", ri.Method, ri.Path, got)
			}
		case isOperatorPath:
			if !slices.Equal(got, []auth.Role{auth.RoleOperator, auth.RoleAdmin}) {
				t.Errorf("%s %s: roles=%v want [operator admin]", ri.Method, ri.Path, got)
			}
		default: // every other POST/PUT/PATCH/DELETE resolves to admin
			if !slices.Equal(got, []auth.Role{auth.RoleAdmin}) {
				t.Errorf("%s %s: roles=%v want [admin]", ri.Method, ri.Path, got)
			}
		}
	}
	if classified == 0 {
		t.Fatal("no /api/v1 routes classified")
	}
}
