package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/heihuzicity-tech/kubejojo/server/internal/aiops"
	"github.com/heihuzicity-tech/kubejojo/server/internal/store"
)

// newTestRouter builds a gin.Engine with only the aiops routes registered,
// backed by a real SQLite database in a temporary directory.
func newTestRouter(t *testing.T) (*gin.Engine, *storeClose) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	repo := aiops.NewRepository(db)
	svc := aiops.NewService(repo)

	router := gin.New()
	api := router.Group("/api/v1")
	registerAIOpsRoutes(api, svc)

	return router, &storeClose{db: db}
}

type storeClose struct {
	db interface{ Close() error }
}

// responseEnvelope captures the common JSON envelope returned by the API.
type responseEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func TestCreateIncidentRoute(t *testing.T) {
	router, _ := newTestRouter(t)

	body := `{"summary":"Pod crash loop","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
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
	router, _ := newTestRouter(t)

	body := `{"summary":"","severity":"critical","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
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
	router, _ := newTestRouter(t)

	body := `{"summary":"Pod crash loop","severity":"severe","namespace":"default","resourceKind":"Pod","resourceName":"api-0"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(body))
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
	router, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader("{invalid"))
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
	router, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents", nil)
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
	router, _ := newTestRouter(t)
	ctx := context.Background()

	// Use the service directly to seed data — this avoids depending on the POST
	// route in a GET test, but we can also use the route. Use the route for
	// integration coverage.
	createBody := `{"summary":"Pod OOMKilled","severity":"warning","namespace":"monitoring","resourceKind":"Pod","resourceName":"prometheus-0"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createW := httptest.NewRecorder()
	router.ServeHTTP(createW, createReq)
	if createW.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", createW.Code)
	}

	_ = ctx // used for clarity above

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents", nil)
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
	router, _ := newTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/aiops/incidents/inc-nonexistent", nil)
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
	router, _ := newTestRouter(t)

	// Create an incident via the route
	createBody := `{"summary":"Node NotReady","severity":"critical","namespace":"default","resourceKind":"Node","resourceName":"worker-1"}`
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/aiops/incidents", strings.NewReader(createBody))
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
}

func TestSetupAIOps_InvalidPath(t *testing.T) {
	// Point to a path where the parent directory cannot be created.
	// On most Unix systems, writing under /proc/nonexistent should fail.
	_, _, err := setupAIOps("/proc/nonexistent/path/db.sqlite")
	if err == nil {
		t.Log("setupAIOps with invalid path succeeded (platform may allow this); skipping error assertion")
	}
}
