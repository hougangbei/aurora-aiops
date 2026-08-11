package deployment

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/audit"
	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

type serviceInstaller struct{}

func (serviceInstaller) Project() Project {
	return Project{ID: "aurora", Name: "Aurora", Versions: []string{"1.0.0"}, SupportedOSFamilies: []string{"linux"}, SupportedArchitectures: []string{"amd64"}}
}
func (serviceInstaller) NormalizeConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	var cfg struct {
		Channel string `json:"channel"`
	}
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	return json.Marshal(cfg)
}
func (serviceInstaller) BuildPlan(Task, assets.Server, json.RawMessage) ([]StepDefinition, error) {
	return []StepDefinition{{ID: "install", Label: "Install", Percent: 100}}, nil
}

func openServiceTest(t *testing.T) (*sql.DB, *Service, *Repository) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key := []byte("0123456789abcdef0123456789abcdef")
	cipher, err := NewSecretCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	catalog := &Catalog{}
	if err := catalog.Register(serviceInstaller{}); err != nil {
		t.Fatal(err)
	}
	assetsRepo := assets.NewRepository(db)
	svc := NewService(NewRepository(db, time.Now), catalog, cipher, func(ctx context.Context, id string) (assets.Server, error) { return assetsRepo.GetServer(ctx, id) }, audit.NewRepository(db), NewEventStore(db), time.Now)
	cred := "cred-1"
	if _, err := db.Exec(`INSERT INTO asset_credentials (id,auth_type,nonce,ciphertext,created_at,updated_at) VALUES (?, 'password', x'01', x'02', '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z')`, cred); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO asset_servers (id,name,address,ssh_port,username,credential_id,status,os_family,architecture,created_at,updated_at) VALUES ('srv-1','one','192.0.2.1',22,'root',?,'online','linux','amd64','2026-08-11T00:00:00Z','2026-08-11T00:00:00Z')`, cred); err != nil {
		t.Fatal(err)
	}
	return db, svc, NewRepository(db, time.Now)
}

func TestServiceInstallAndReads(t *testing.T) {
	_, svc, repo := openServiceTest(t)
	task, err := svc.Install(context.Background(), "alice", "aurora", InstallRequest{ServerID: "srv-1", Version: "1.0.0", Configuration: json.RawMessage(`{"channel":"stable"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if task.Actor != "alice" || task.Status != TaskQueued {
		t.Fatalf("task=%+v", task)
	}
	got, steps, err := svc.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != task.ID || len(steps) != 1 || steps[0].ID != "install" {
		t.Fatalf("got=%+v steps=%+v", got, steps)
	}
	if _, err := repo.OpenTaskConfiguration(context.Background(), task.ID, svc.cipher); err != nil {
		t.Fatal(err)
	}
}

func TestServiceInstallValidationAndConflict(t *testing.T) {
	_, svc, _ := openServiceTest(t)
	if _, err := svc.Install(context.Background(), "alice", "missing", InstallRequest{ServerID: "srv-1", Version: "1.0.0"}); !errors.Is(err, ErrUnknownProject) {
		t.Fatalf("err=%v", err)
	}
	if _, err := svc.Install(context.Background(), "alice", "aurora", InstallRequest{ServerID: "srv-1", Version: "1.0.0", Configuration: json.RawMessage(`{"unexpected":true}`)}); err == nil {
		t.Fatal("unknown config accepted")
	}
	if _, err := svc.Install(context.Background(), "alice", "aurora", InstallRequest{ServerID: "srv-1", Version: "1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(context.Background(), "alice", "aurora", InstallRequest{ServerID: "srv-1", Version: "1.0.0"}); !errors.Is(err, ErrActiveTask) {
		t.Fatalf("err=%v", err)
	}
}

func TestServiceCancelRetry(t *testing.T) {
	_, svc, _ := openServiceTest(t)
	task, err := svc.Install(context.Background(), "alice", "aurora", InstallRequest{ServerID: "srv-1", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(context.Background(), "admin", task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Retry(context.Background(), "admin", task.ID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retry queued err=%v", err)
	}
}
