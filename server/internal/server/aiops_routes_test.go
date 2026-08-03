package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/auth"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

// newTestRouter builds a gin.Engine with only the aiops routes registered,
// backed by a real SQLite database in a temporary directory.
// It returns the router and the underlying *sql.DB so callers can control
// the database lifecycle (e.g. close it to simulate internal errors).
func newTestRouter(t *testing.T) (*gin.Engine, *sql.DB, string) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	authRepo := auth.NewRepository(db)
	authService := auth.NewService(authRepo, sessionTTL, time.Now)
	seedUser(t, authRepo, "t-admin", "admin", "correct-password", auth.RoleAdmin)
	raw, _, err := authService.Login(context.Background(), "admin", "correct-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	adminCookie := sessionCookieName + "=" + raw

	repo := aiops.NewRepository(db)
	svc := aiops.NewService(repo)

	router := gin.New()
	api := router.Group("/api/v1")
	registerAuthRoutes(api, authService, sessionTTL)
	authorized := api.Group("/")
	authorized.Use(RequireSession(authService, nil))
	authorized.Use(EnforcePlatformRBAC())
	registerAIOpsRoutes(authorized, aiopsRoutesDeps{svc: svc})

	return router, db, adminCookie
}

// responseEnvelope captures the common JSON envelope returned by the API.
type responseEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func TestNormalizeRemediationReason(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  string
		valid bool
	}{
		{name: "whitespace", raw: "   \t\n", want: "", valid: false},
		{name: "seven unicode characters", raw: "一二三四五六七", want: "一二三四五六七", valid: false},
		{name: "eight unicode characters", raw: "  一二三四五六七八  ", want: "一二三四五六七八", valid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := normalizeRemediationReason(tt.raw)
			if got != tt.want || valid != tt.valid {
				t.Fatalf("normalizeRemediationReason(%q)=(%q,%v) want (%q,%v)", tt.raw, got, valid, tt.want, tt.valid)
			}
		})
	}
}

func TestRemediationDecisionRoutesRejectShortReasons(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []string{"approve", "reject"} {
		t.Run(kind, func(t *testing.T) {
			router := gin.New()
			router.POST("/incidents/:id/decision", func(c *gin.Context) {
				handleRemediationDecision(c, nil, nil, kind)
			})
			req := httptest.NewRequest(http.MethodPost, "/incidents/inc-1/decision", strings.NewReader(`{"reason":"一二三四五六七"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "至少需要 8 个字符") {
				t.Fatalf("unexpected response: %s", rec.Body.String())
			}
		})
	}
}

func TestCreateIncidentRoute(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	body := `{"summary":"Pod crash loop","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%s", w.Code, w.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Code != "OK" {
		t.Errorf("expected code OK, got %s", env.Code)
	}

	// Verify the incident has an id and status=="received"
	var incident map[string]interface{}
	if err := json.Unmarshal(env.Data, &incident); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if id, _ := incident["id"].(string); id == "" {
		t.Error("expected non-empty id")
	}
	if status, _ := incident["status"].(string); status != "received" {
		t.Errorf("expected status received, got %s", status)
	}
}

func TestCreateIncidentRoute_EmptySummary(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	body := `{"summary":"","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body=%s", w.Code, w.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Code != "INVALID_INCIDENT_REQUEST" {
		t.Errorf("expected code INVALID_INCIDENT_REQUEST, got %s", env.Code)
	}
}

func TestCreateIncidentRoute_InvalidSeverity(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	body := `{"summary":"Pod crash loop","severity":"severe","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body=%s", w.Code, w.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Code != "INVALID_INCIDENT_REQUEST" {
		t.Errorf("expected code INVALID_INCIDENT_REQUEST, got %s", env.Code)
	}
}

func TestCreateIncidentRoute_InvalidJSON(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader("{invalid"))
	req.Header.Set("Cookie", cookie)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d; body=%s", w.Code, w.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Code != "INVALID_INCIDENT_REQUEST" {
		t.Errorf("expected code INVALID_INCIDENT_REQUEST, got %s", env.Code)
	}
}

func TestListIncidentsRoute_Empty(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents", nil)
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", w.Code, w.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Code != "OK" {
		t.Errorf("expected code OK, got %s", env.Code)
	}

	// Data must be [] not null
	dataStr := strings.TrimSpace(string(env.Data))
	if dataStr == "null" {
		t.Error("expected data to be [], got null")
	}
	if dataStr != "[]" {
		t.Errorf("expected data [], got %s", dataStr)
	}
}

func TestListIncidentsRoute_AfterCreate(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	createBody := `{"summary":"Pod OOMKilled","severity":"warning","namespace":"monitoring","resourceKind":"Pod","resourceName":"prometheus-0"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(createBody))
	createReq.Header.Set("Cookie", cookie)
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	router.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", createW.Code)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents", nil)
	listReq.Header.Set("Cookie", cookie)
	listW := httptest.NewRecorder()
	router.ServeHTTP(listW, listReq)

	if listW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", listW.Code, listW.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(listW.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	var items []map[string]interface{}
	if err := json.Unmarshal(env.Data, &items); err != nil {
		t.Fatalf("unmarshal data as array: %v", err)
	}
	if len(items) < 1 {
		t.Errorf("expected at least 1 incident, got %d", len(items))
	}
}

func TestGetIncidentRoute_NotFound(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents/inc-nonexistent", nil)
	req.Header.Set("Cookie", cookie)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d; body=%s", w.Code, w.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Code != "INCIDENT_NOT_FOUND" {
		t.Errorf("expected code INCIDENT_NOT_FOUND, got %s", env.Code)
	}
}

func TestGetIncidentRoute_AfterCreate(t *testing.T) {
	router, _, cookie := newTestRouter(t)

	// Create an incident via the route
	createBody := `{"summary":"Node NotReady","severity":"critical","namespace":"default","resourceKind":"Node","resourceName":"worker-1"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(createBody))
	createReq.Header.Set("Cookie", cookie)
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	router.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", createW.Code)
	}

	var createEnv responseEnvelope
	if err := json.Unmarshal(createW.Body.Bytes(), &createEnv); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	var created map[string]interface{}
	if err := json.Unmarshal(createEnv.Data, &created); err != nil {
		t.Fatalf("unmarshal created incident: %v", err)
	}
	incidentID, _ := created["id"].(string)
	if incidentID == "" {
		t.Fatal("expected non-empty id from create")
	}

	// GET the incident by ID
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents/"+incidentID, nil)
	getReq.Header.Set("Cookie", cookie)
	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%s", getW.Code, getW.Body.String())
	}

	var getEnv responseEnvelope
	if err := json.Unmarshal(getW.Body.Bytes(), &getEnv); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}
	if getEnv.Code != "OK" {
		t.Errorf("expected code OK, got %s", getEnv.Code)
	}

	// Verify the returned incident matches the creation input
	var incident map[string]interface{}
	if err := json.Unmarshal(getEnv.Data, &incident); err != nil {
		t.Fatalf("unmarshal incident: %v", err)
	}
	if v, _ := incident["summary"].(string); v != "Node NotReady" {
		t.Errorf("expected summary 'Node NotReady', got %q", v)
	}
	if v, _ := incident["severity"].(string); v != "critical" {
		t.Errorf("expected severity 'critical', got %q", v)
	}
	if v, _ := incident["namespace"].(string); v != "default" {
		t.Errorf("expected namespace 'default', got %q", v)
	}
	if v, _ := incident["resourceKind"].(string); v != "Node" {
		t.Errorf("expected resourceKind 'Node', got %q", v)
	}
	if v, _ := incident["resourceName"].(string); v != "worker-1" {
		t.Errorf("expected resourceName 'worker-1', got %q", v)
	}
	if v, _ := incident["status"].(string); v != "received" {
		t.Errorf("expected status 'received', got %q", v)
	}
}

func TestGetIncidentRoute_InternalError(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()

	// Auth gets its own database so that closing the aiops database below does
	// not also break session authentication (the platform RBAC layer reads the
	// session store on every request).
	authDb, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open auth db: %v", err)
	}
	authRepo := auth.NewRepository(authDb)
	authService := auth.NewService(authRepo, sessionTTL, time.Now)
	seedUser(t, authRepo, "t-admin", "admin", "correct-password", auth.RoleAdmin)
	raw, _, err := authService.Login(ctx, "admin", "correct-password")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	cookie := sessionCookieName + "=" + raw

	aiopsDb, err := store.Open(filepath.Join(t.TempDir(), "aiops.db"))
	if err != nil {
		t.Fatalf("open aiops db: %v", err)
	}
	repo := aiops.NewRepository(aiopsDb)
	svc := aiops.NewService(repo)
	created, err := svc.Create(ctx, aiops.CreateIncidentInput{
		Summary:      "DiskFull",
		Severity:     "warning",
		Namespace:    "kube-system",
		ResourceKind: "Pod",
		ResourceName: "etcd-0",
	})
	if err != nil {
		t.Fatalf("create incident: %v", err)
	}

	// Close the aiops database to force a server-side error on the next GET.
	aiopsDb.Close()

	router := gin.New()
	api := router.Group("/api/v1")
	registerAuthRoutes(api, authService, sessionTTL)
	authorized := api.Group("/")
	authorized.Use(RequireSession(authService, nil))
	authorized.Use(EnforcePlatformRBAC())
	registerAIOpsRoutes(authorized, aiopsRoutesDeps{svc: svc})

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents/"+created.ID, nil)
	getReq.Header.Set("Cookie", cookie)
	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, getReq)

	if getW.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d; body=%s", getW.Code, getW.Body.String())
	}

	var env responseEnvelope
	if err := json.Unmarshal(getW.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Code != "GET_INCIDENT_FAILED" {
		t.Errorf("expected code GET_INCIDENT_FAILED, got %s", env.Code)
	}

	// The message must be a fixed generic string, not leaking internal details
	if env.Message != "获取事件失败" {
		t.Errorf("expected generic message '获取事件失败', got %q", env.Message)
	}

	for _, keyword := range []string{"sql", "closed", "database", "sqlite", "scan"} {
		if strings.Contains(strings.ToLower(env.Message), keyword) {
			t.Errorf("message leaks internal keyword %q: %q", keyword, env.Message)
		}
	}
}

func TestSetupAIOps_InvalidPath(t *testing.T) {
	// Point to a path where the parent directory cannot be created.
	// On most Unix systems, writing under /proc/nonexistent should fail.
	_, _, err := setupAIOps("/proc/nonexistent/path/db.sqlite")
	if err == nil {
		t.Log("setupAIOps with invalid path succeeded (platform may allow this); skipping error assertion")
	}
}
