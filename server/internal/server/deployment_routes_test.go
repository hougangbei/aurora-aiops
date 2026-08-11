package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/audit"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

type routeInstaller struct{}

func (routeInstaller) Project() deployment.Project {
	return deployment.Project{ID: "aurora", Name: "Aurora", Versions: []string{"1.0.0"}, SupportedOSFamilies: []string{"linux"}, SupportedArchitectures: []string{"amd64"}}
}
func (routeInstaller) NormalizeConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	return raw, nil
}
func (routeInstaller) BuildPlan(deployment.Task, assets.Server, json.RawMessage) ([]deployment.StepDefinition, error) {
	return []deployment.StepDefinition{{ID: "install", Label: "Install", Percent: 100}}, nil
}

func testDeploymentRouteService(t *testing.T, db *sql.DB) *deployment.Service {
	t.Helper()
	catalog := &deployment.Catalog{}
	if err := catalog.Register(routeInstaller{}); err != nil {
		t.Fatal(err)
	}
	cipher, err := deployment.NewSecretCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	assetsRepo := assets.NewRepository(db)
	return deployment.NewService(deployment.NewRepository(db, time.Now), catalog, cipher, func(ctx context.Context, id string) (assets.Server, error) { return assetsRepo.GetServer(ctx, id) }, audit.NewRepository(db), deployment.NewEventStore(db), time.Now)
}

func TestDeploymentRoutesListProjects(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := gin.New()
	registerDeploymentRoutes(r.Group(""), testDeploymentRouteService(t, db))
	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeploymentErrorMapping(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) { respondDeploymentError(c, deployment.ErrActiveTask) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusConflict || !containsString(rec.Body.String(), "DEPLOYMENT_ACTIVE_TASK") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDeploymentSSEReplaysFramesAndClosesTerminalTask(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "sse.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO asset_credentials (id,auth_type,nonce,ciphertext,created_at,updated_at) VALUES ('cred-sse','password',x'01',x'02','2026-08-11T00:00:00Z','2026-08-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO asset_servers (id,name,address,ssh_port,username,credential_id,status,os_family,architecture,created_at,updated_at) VALUES ('srv-sse','sse','192.0.2.1',22,'root','cred-sse','online','linux','amd64','2026-08-11T00:00:00Z','2026-08-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO deployment_tasks (id,server_id,project_id,version,action,status,actor,percent,config_nonce,config_ciphertext,created_at,updated_at,finished_at) VALUES ('task-sse','srv-sse','aurora','1.0.0','install','succeeded','alice',100,x'01',x'02','2026-08-11T00:00:00Z','2026-08-11T00:00:00Z','2026-08-11T00:00:01Z')`); err != nil {
		t.Fatal(err)
	}
	events := deployment.NewEventStore(db)
	ev, err := events.Append(context.Background(), "task-sse", "finished", json.RawMessage(`{"status":"succeeded"}`))
	if err != nil {
		t.Fatal(err)
	}
	svc := testDeploymentRouteService(t, db)
	r := gin.New()
	registerDeploymentRoutes(r.Group(""), svc)
	req := httptest.NewRequest(http.MethodGet, "/deployment-tasks/task-sse/events", nil)
	req.Header.Set("Last-Event-ID", "0")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !containsString(rec.Body.String(), "id: "+strconv.FormatInt(ev.ID, 10)) || !containsString(rec.Body.String(), "event: finished") || !containsString(rec.Body.String(), "data: {\"status\":\"succeeded\"}") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func containsString(value, needle string) bool {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
