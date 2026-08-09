package assets

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/audit"
	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

type serviceFakeRemote struct {
	mu          sync.Mutex
	probeCalls  []RemoteTarget
	runCalls    []serviceRemoteRun
	probeResult string
	probeErr    error
	run         func(RemoteTarget, CredentialSecret, string, int64) (CommandResult, error)
}

type serviceRemoteRun struct {
	target RemoteTarget
	secret CredentialSecret
	cmd    string
	limit  int64
}

func (f *serviceFakeRemote) ProbeHostKey(_ context.Context, target RemoteTarget) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeCalls = append(f.probeCalls, target)
	return f.probeResult, f.probeErr
}

func (f *serviceFakeRemote) Run(_ context.Context, target RemoteTarget, secret CredentialSecret, command string, limit int64) (CommandResult, error) {
	f.mu.Lock()
	f.runCalls = append(f.runCalls, serviceRemoteRun{target: target, secret: secret, cmd: command, limit: limit})
	run := f.run
	f.mu.Unlock()
	if run == nil {
		return CommandResult{}, errors.New("unexpected remote run")
	}
	return run(target, secret, command, limit)
}

func (*serviceFakeRemote) Upload(context.Context, RemoteTarget, CredentialSecret, io.Reader, int64, string, fs.FileMode) error {
	return errors.New("unexpected upload")
}

func (f *serviceFakeRemote) callCounts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.probeCalls), len(f.runCalls)
}

type serviceFakeAudit struct {
	mu      sync.Mutex
	records []audit.Record
	err     error
}

func (f *serviceFakeAudit) Append(_ context.Context, record audit.Record) (audit.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return audit.Record{}, f.err
	}
	f.records = append(f.records, record)
	return record, nil
}

func (f *serviceFakeAudit) List(context.Context) ([]audit.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Record(nil), f.records...), nil
}

func (f *serviceFakeAudit) all() []audit.Record {
	records, _ := f.List(context.Background())
	return records
}

type serviceHarness struct {
	db     *sql.DB
	repo   *Repository
	remote *serviceFakeRemote
	audit  *serviceFakeAudit
	cipher CredentialCipher
	clock  time.Time
	svc    *Service
}

func newServiceHarness(t *testing.T) *serviceHarness {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := NewAESGCMCredentialCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	h := &serviceHarness{
		db:     db,
		repo:   NewRepository(db),
		remote: &serviceFakeRemote{},
		audit:  &serviceFakeAudit{},
		cipher: cipher,
		clock:  time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
	}
	h.svc = NewService(h.repo, h.cipher, h.remote, NewCollector(h.remote, func() time.Time { return h.clock }), h.audit, func() time.Time { return h.clock })
	return h
}

type serviceFailCipher struct {
	delegate   CredentialCipher
	encryptErr error
	decryptErr error
}

func (c serviceFailCipher) Encrypt(secret CredentialSecret) (CredentialEnvelope, error) {
	if c.encryptErr != nil {
		return CredentialEnvelope{}, c.encryptErr
	}
	return c.delegate.Encrypt(secret)
}

func (c serviceFailCipher) Decrypt(envelope CredentialEnvelope) (CredentialSecret, error) {
	if c.decryptErr != nil {
		return CredentialSecret{}, c.decryptErr
	}
	return c.delegate.Decrypt(envelope)
}

func serviceCreateInput() CreateServerInput {
	return CreateServerInput{
		Name: " edge-1 ", Address: " 192.0.2.10 ", Username: " root ", SSHPort: 0,
		AuthType: AuthPassword, Secret: CredentialSecret{Password: "correct horse battery staple"},
	}
}

func (h *serviceHarness) create(t *testing.T) Server {
	t.Helper()
	server, err := h.svc.Create(context.Background(), "admin", serviceCreateInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return server
}

func TestServiceCreatesPendingServerWithoutSSH(t *testing.T) {
	h := newServiceHarness(t)
	created, err := h.svc.Create(context.Background(), "admin", serviceCreateInput())
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CredentialID == "" || created.Status != ServerPending {
		t.Fatalf("created=%+v", created)
	}
	if created.Name != "edge-1" || created.Address != "192.0.2.10" || created.Username != "root" || created.SSHPort != 22 {
		t.Fatalf("normalization failed: %+v", created)
	}
	if created.CredentialAuthType != AuthPassword || !created.CredentialConfigured {
		t.Fatalf("credential metadata=%q configured=%v", created.CredentialAuthType, created.CredentialConfigured)
	}
	if probes, runs := h.remote.callCounts(); probes != 0 || runs != 0 {
		t.Fatalf("offline create touched remote: probes=%d runs=%d", probes, runs)
	}
	stored, err := h.repo.GetCredential(context.Background(), created.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Envelope.Ciphertext), serviceCreateInput().Secret.Password) || len(stored.Envelope.Nonce) == 0 {
		t.Fatal("credential was not encrypted before persistence")
	}
}

func TestServiceCreateWithTestReturnsHostKeyConfirmation(t *testing.T) {
	h := newServiceHarness(t)
	hostKeyErr := &HostKeyError{Actual: "SHA256:new-host"}
	h.remote.probeResult = hostKeyErr.Actual
	h.remote.probeErr = hostKeyErr
	input := serviceCreateInput()
	input.TestConnection = true

	created, err := h.svc.Create(context.Background(), "admin", input)
	if created.ID == "" {
		t.Fatal("created server missing from host-key confirmation result")
	}
	var gotHostKey *HostKeyError
	if !errors.As(err, &gotHostKey) || gotHostKey != hostKeyErr {
		t.Fatalf("error=%#v want original typed error %#v", err, hostKeyErr)
	}
	if _, getErr := h.repo.GetServer(context.Background(), created.ID); getErr != nil {
		t.Fatalf("server must stay persisted: %v", getErr)
	}
	if probes, runs := h.remote.callCounts(); probes != 1 || runs != 0 {
		t.Fatalf("first-contact create calls: probes=%d runs=%d; authentication must wait for confirmation", probes, runs)
	}
}

func TestServiceConfirmHostKeyAuditsFingerprintOnly(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	h.audit.records = nil
	h.remote.probeResult = "SHA256:confirmed"
	h.remote.probeErr = &HostKeyError{Actual: "SHA256:confirmed"}

	if err := h.svc.ConfirmHostKey(context.Background(), "admin", server.ID, "SHA256:confirmed"); err != nil {
		t.Fatal(err)
	}
	got, err := h.repo.GetServer(context.Background(), server.ID)
	if err != nil || got.HostKeyFingerprint != "SHA256:confirmed" {
		t.Fatalf("confirmed server=%+v err=%v", got, err)
	}
	records := h.audit.all()
	if len(records) != 1 || records[0].Actor != "admin" || records[0].Target != server.ID || !strings.Contains(records[0].Payload, "SHA256:confirmed") {
		t.Fatalf("audit=%+v", records)
	}
	for _, forbidden := range []string{"correct horse battery staple", "password", "privateKey", "passphrase", "ciphertext", "nonce"} {
		if strings.Contains(records[0].Payload, forbidden) {
			t.Fatalf("audit payload leaked %q: %s", forbidden, records[0].Payload)
		}
	}
}

func TestServiceConfirmHostKeyRejectsUnobservedFingerprint(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	h.remote.probeResult = "SHA256:observed"
	h.remote.probeErr = &HostKeyError{Actual: "SHA256:observed"}
	if err := h.svc.ConfirmHostKey(context.Background(), "admin", server.ID, "SHA256:substituted"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error=%v want ErrInvalidInput", err)
	}
	got, _ := h.repo.GetServer(context.Background(), server.ID)
	if got.HostKeyFingerprint != "" {
		t.Fatalf("unobserved fingerprint persisted: %q", got.HostKeyFingerprint)
	}
}

func TestServiceCollectDecryptsCredentialAndPersistsSnapshot(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	h.repo.ConfirmHostKey(context.Background(), server.ID, "SHA256:trusted", h.clock)
	h.remote.run = successfulServiceCollection(t, CredentialSecret{Password: "correct horse battery staple"})
	h.clock = h.clock.Add(time.Minute)

	snapshot, software, err := h.svc.Collect(context.Background(), "operator", server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ServerID != server.ID || snapshot.ID == "" || len(software) == 0 {
		t.Fatalf("snapshot=%+v software=%+v", snapshot, software)
	}
	latest, err := h.repo.LatestSnapshot(context.Background(), server.ID)
	if err != nil || latest.ID != snapshot.ID {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	storedSoftware, err := h.repo.ListLatestSoftware(context.Background(), server.ID)
	if err != nil || len(storedSoftware) != len(software) {
		t.Fatalf("software=%+v err=%v", storedSoftware, err)
	}
}

func TestServiceCollectFailureKeepsPreviousSnapshot(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	previous := Snapshot{ID: "snapshot-previous", ServerID: server.ID, OSFamily: "debian", Hostname: "old", CollectedAt: h.clock}
	previousSoftware := []SoftwareItem{{Category: "package", Name: "keep-me", Version: "1"}}
	if err := h.repo.SaveCollection(context.Background(), server, previous, previousSoftware); err != nil {
		t.Fatal(err)
	}
	h.remote.run = func(RemoteTarget, CredentialSecret, string, int64) (CommandResult, error) {
		return CommandResult{Stdout: "sensitive complete command output"}, errors.New("password=top-secret")
	}
	h.clock = h.clock.Add(time.Minute)

	if _, _, err := h.svc.Collect(context.Background(), "operator", server.ID); err == nil {
		t.Fatal("expected collection failure")
	}
	latest, err := h.repo.LatestSnapshot(context.Background(), server.ID)
	if err != nil || latest.ID != previous.ID {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
	software, err := h.repo.ListLatestSoftware(context.Background(), server.ID)
	if err != nil || len(software) != 1 || software[0].Name != "keep-me" {
		t.Fatalf("software=%+v err=%v", software, err)
	}
	failed, _ := h.repo.GetServer(context.Background(), server.ID)
	if failed.Status != ServerError || failed.StatusMessage == "" || strings.Contains(failed.StatusMessage, "sensitive") || strings.Contains(failed.StatusMessage, "top-secret") {
		t.Fatalf("unsafe failure state: %+v", failed)
	}
}

func TestServiceCollectPreservesTypedHostKeyError(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	if err := h.repo.ConfirmHostKey(context.Background(), server.ID, "SHA256:old", h.clock); err != nil {
		t.Fatal(err)
	}
	hostKeyErr := &HostKeyError{Expected: "SHA256:old", Actual: "SHA256:new", Changed: true}
	h.remote.run = func(RemoteTarget, CredentialSecret, string, int64) (CommandResult, error) {
		return CommandResult{}, hostKeyErr
	}
	mustServiceExec(t, h.db, `CREATE TRIGGER fail_failure_mark BEFORE UPDATE ON asset_servers BEGIN SELECT RAISE(ABORT, 'mark-secret-canary'); END`)
	_, _, err := h.svc.Collect(context.Background(), "operator", server.ID)
	var got *HostKeyError
	if !errors.As(err, &got) || got != hostKeyErr || !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("error=%#v want original typed host-key error %#v", err, hostKeyErr)
	}
	if strings.Contains(err.Error(), "mark-secret-canary") {
		t.Fatalf("MarkCollectionFailure error replaced or leaked through typed error: %v", err)
	}
}

func TestServiceAuditPayloadNeverContainsCredential(t *testing.T) {
	h := newServiceHarness(t)
	input := serviceCreateInput()
	input.Secret.Password = "audit-password-canary"
	server, err := h.svc.Create(context.Background(), "admin", input)
	if err != nil {
		t.Fatal(err)
	}
	privateKey := CredentialSecret{PrivateKey: "audit-private-key-canary", Passphrase: "audit-passphrase-canary"}
	authType := AuthPrivateKey
	if _, err := h.svc.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: server.Name, Address: server.Address, Username: server.Username, SSHPort: server.SSHPort, AuthType: &authType, Secret: &privateKey}); err != nil {
		t.Fatal(err)
	}
	for _, record := range h.audit.all() {
		whole := record.Actor + record.Action + record.Target + record.Result + record.Payload
		for _, forbidden := range []string{"audit-password-canary", "audit-private-key-canary", "audit-passphrase-canary", "nonce", "ciphertext"} {
			if strings.Contains(whole, forbidden) {
				t.Fatalf("audit leaked %q: %+v", forbidden, record)
			}
		}
	}
}

func TestServiceAuditsEveryRecognizedFailure(t *testing.T) {
	tests := []struct {
		name   string
		action string
		run    func(*testing.T, *serviceHarness) (string, error)
	}{
		{
			name: "create validation", action: "asset.server.create",
			run: func(_ *testing.T, h *serviceHarness) (string, error) {
				input := serviceCreateInput()
				input.Name = "\n"
				_, err := h.svc.Create(context.Background(), "admin", input)
				return "", err
			},
		},
		{
			name: "create cipher", action: "asset.server.create",
			run: func(_ *testing.T, h *serviceHarness) (string, error) {
				h.svc.cipher = serviceFailCipher{delegate: h.cipher, encryptErr: errors.New("cipher-secret-canary")}
				_, err := h.svc.Create(context.Background(), "admin", serviceCreateInput())
				return "", err
			},
		},
		{
			name: "create repository", action: "asset.server.create",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				mustServiceExec(t, h.db, `CREATE TRIGGER fail_create BEFORE INSERT ON asset_servers BEGIN SELECT RAISE(ABORT, 'repo-secret-canary'); END`)
				_, err := h.svc.Create(context.Background(), "admin", serviceCreateInput())
				return "", err
			},
		},
		{
			name: "update validation", action: "asset.server.update",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				h.audit.records = nil
				_, err := h.svc.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: "bad\nname", Address: server.Address, Username: server.Username, SSHPort: 22})
				return server.ID, err
			},
		},
		{
			name: "update not found", action: "asset.server.update",
			run: func(_ *testing.T, h *serviceHarness) (string, error) {
				_, err := h.svc.Update(context.Background(), "admin", "missing-update", UpdateServerInput{Name: "edge", Address: "host", Username: "root", SSHPort: 22})
				return "missing-update", err
			},
		},
		{
			name: "update cipher", action: "asset.server.update",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				h.audit.records = nil
				h.svc.cipher = serviceFailCipher{delegate: h.cipher, encryptErr: errors.New("cipher-secret-canary")}
				secret := CredentialSecret{Password: "rotation-secret-canary"}
				_, err := h.svc.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: server.Name, Address: server.Address, Username: server.Username, SSHPort: 22, Secret: &secret})
				return server.ID, err
			},
		},
		{
			name: "update repository", action: "asset.server.update",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				h.audit.records = nil
				mustServiceExec(t, h.db, `CREATE TRIGGER fail_update BEFORE UPDATE ON asset_servers BEGIN SELECT RAISE(ABORT, 'repo-secret-canary'); END`)
				_, err := h.svc.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: "renamed", Address: server.Address, Username: server.Username, SSHPort: 22})
				return server.ID, err
			},
		},
		{
			name: "delete repository", action: "asset.server.delete",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				h.audit.records = nil
				mustServiceExec(t, h.db, `CREATE TRIGGER fail_delete BEFORE DELETE ON asset_servers BEGIN SELECT RAISE(ABORT, 'repo-secret-canary'); END`)
				err := h.svc.Delete(context.Background(), "admin", server.ID)
				return server.ID, err
			},
		},
		{
			name: "test connection not found", action: "asset.server.test_connection",
			run: func(_ *testing.T, h *serviceHarness) (string, error) {
				_, err := h.svc.TestConnection(context.Background(), "operator", "missing-connection")
				return "missing-connection", err
			},
		},
		{
			name: "test connection probe", action: "asset.server.test_connection",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				h.audit.records = nil
				h.remote.probeErr = errors.New("probe-secret-canary")
				_, err := h.svc.TestConnection(context.Background(), "operator", server.ID)
				return server.ID, err
			},
		},
		{
			name: "test connection auth", action: "asset.server.test_connection",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				mustConfirmServiceHost(t, h, server.ID, "SHA256:trusted")
				h.audit.records = nil
				h.remote.probeResult, h.remote.probeErr = "SHA256:trusted", nil
				h.remote.run = func(RemoteTarget, CredentialSecret, string, int64) (CommandResult, error) {
					return CommandResult{Stdout: "complete-output-canary"}, errors.New("auth-secret-canary")
				}
				_, err := h.svc.TestConnection(context.Background(), "operator", server.ID)
				return server.ID, err
			},
		},
		{
			name: "confirm validation", action: "asset.server.confirm_host_key",
			run: func(_ *testing.T, h *serviceHarness) (string, error) {
				err := h.svc.ConfirmHostKey(context.Background(), "admin", "confirm-id", "\n")
				return "confirm-id", err
			},
		},
		{
			name: "confirm not found", action: "asset.server.confirm_host_key",
			run: func(_ *testing.T, h *serviceHarness) (string, error) {
				err := h.svc.ConfirmHostKey(context.Background(), "admin", "missing-confirm", "SHA256:valid")
				return "missing-confirm", err
			},
		},
		{
			name: "confirm probe", action: "asset.server.confirm_host_key",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				h.audit.records = nil
				h.remote.probeErr = errors.New("probe-secret-canary")
				err := h.svc.ConfirmHostKey(context.Background(), "admin", server.ID, "SHA256:valid")
				return server.ID, err
			},
		},
		{
			name: "confirm repository", action: "asset.server.confirm_host_key",
			run: func(t *testing.T, h *serviceHarness) (string, error) {
				server := h.create(t)
				h.audit.records = nil
				h.remote.probeResult = "SHA256:valid"
				h.remote.probeErr = &HostKeyError{Actual: "SHA256:valid"}
				mustServiceExec(t, h.db, `CREATE TRIGGER fail_confirm BEFORE UPDATE OF host_key_fingerprint ON asset_servers BEGIN SELECT RAISE(ABORT, 'repo-secret-canary'); END`)
				err := h.svc.ConfirmHostKey(context.Background(), "admin", server.ID, "SHA256:valid")
				return server.ID, err
			},
		},
		{
			name: "collect not found", action: "asset.server.collect",
			run: func(_ *testing.T, h *serviceHarness) (string, error) {
				_, _, err := h.svc.Collect(context.Background(), "operator", "missing-collect")
				return "missing-collect", err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newServiceHarness(t)
			target, err := tt.run(t, h)
			if err == nil {
				t.Fatal("expected operation failure")
			}
			for _, forbidden := range []string{"secret-canary", "complete-output-canary"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("operation error leaked %q: %v", forbidden, err)
				}
			}
			assertServiceFailureAudit(t, h.audit.all(), tt.action, target)
		})
	}
}

func TestServiceCollectFailuresMarkSafeStateAndPreserveInventory(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T, *serviceHarness, Server) error
	}{
		{
			name: "nil cipher",
			run: func(_ *testing.T, h *serviceHarness, server Server) error {
				svc := NewService(h.repo, nil, h.remote, NewCollector(h.remote, func() time.Time { return h.clock }), h.audit, func() time.Time { return h.clock })
				_, _, err := svc.Collect(context.Background(), "operator", server.ID)
				return err
			},
		},
		{
			name: "get credential",
			run: func(t *testing.T, h *serviceHarness, server Server) error {
				mustServiceExec(t, h.db, `UPDATE asset_credentials SET created_at = 'corrupt-time' WHERE id = ?`, server.CredentialID)
				_, _, err := h.svc.Collect(context.Background(), "operator", server.ID)
				return err
			},
		},
		{
			name: "decrypt",
			run: func(_ *testing.T, h *serviceHarness, server Server) error {
				h.svc.cipher = serviceFailCipher{delegate: h.cipher, decryptErr: errors.New("decrypt-secret-canary")}
				_, _, err := h.svc.Collect(context.Background(), "operator", server.ID)
				return err
			},
		},
		{
			name: "collector",
			run: func(_ *testing.T, h *serviceHarness, server Server) error {
				h.remote.run = func(RemoteTarget, CredentialSecret, string, int64) (CommandResult, error) {
					return CommandResult{Stdout: "complete-output-canary"}, errors.New("collector-secret-canary")
				}
				_, _, err := h.svc.Collect(context.Background(), "operator", server.ID)
				return err
			},
		},
		{
			name: "save collection",
			run: func(t *testing.T, h *serviceHarness, server Server) error {
				h.remote.run = successfulServiceCollection(t, CredentialSecret{Password: "correct horse battery staple"})
				mustServiceExec(t, h.db, `CREATE TRIGGER fail_snapshot BEFORE INSERT ON asset_snapshots BEGIN SELECT RAISE(ABORT, 'save-secret-canary'); END`)
				_, _, err := h.svc.Collect(context.Background(), "operator", server.ID)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newServiceHarness(t)
			server := h.create(t)
			mustConfirmServiceHost(t, h, server.ID, "SHA256:trusted")
			baseline := Snapshot{ID: "baseline-snapshot", ServerID: server.ID, OSFamily: "baseline-os", OSVersion: "1", CPUCores: 2, MemoryBytes: 2048, DiskBytes: 4096, CollectedAt: h.clock}
			baselineSoftware := []SoftwareItem{{Category: "package", Name: "keep-package", Version: "1"}}
			if err := h.repo.SaveCollection(context.Background(), server, baseline, baselineSoftware); err != nil {
				t.Fatal(err)
			}
			h.clock = h.clock.Add(time.Minute)
			h.audit.records = nil

			err := tt.run(t, h, server)
			if err == nil {
				t.Fatal("expected collect failure")
			}
			for _, forbidden := range []string{"secret-canary", "complete-output-canary", "correct horse battery staple"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("collect error leaked %q: %v", forbidden, err)
				}
			}
			assertServiceFailureAudit(t, h.audit.all(), "asset.server.collect", server.ID)
			gotServer, getErr := h.repo.GetServer(context.Background(), server.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if gotServer.Status != ServerError || gotServer.StatusMessage != collectionFailureMessage || gotServer.OSFamily != baseline.OSFamily || gotServer.OSVersion != baseline.OSVersion || gotServer.CPUCores != baseline.CPUCores || gotServer.MemoryBytes != baseline.MemoryBytes || gotServer.DiskBytes != baseline.DiskBytes {
				t.Fatalf("unsafe or destructive failure state: %+v", gotServer)
			}
			latest, latestErr := h.repo.LatestSnapshot(context.Background(), server.ID)
			if latestErr != nil || latest.ID != baseline.ID {
				t.Fatalf("latest=%+v err=%v", latest, latestErr)
			}
			software, softwareErr := h.repo.ListLatestSoftware(context.Background(), server.ID)
			if softwareErr != nil || len(software) != 1 || software[0].Name != "keep-package" {
				t.Fatalf("software=%+v err=%v", software, softwareErr)
			}
		})
	}
}

func TestServiceAuditFailureIsGenericAndNeverLeaksCredential(t *testing.T) {
	h := newServiceHarness(t)
	h.audit.err = errors.New("audit-password-secret-canary")
	created, err := h.svc.Create(context.Background(), "admin", serviceCreateInput())
	if created.ID == "" || err == nil {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if strings.Contains(err.Error(), "audit-password-secret-canary") || strings.Contains(err.Error(), serviceCreateInput().Secret.Password) {
		t.Fatalf("audit failure leaked credential: %v", err)
	}
}

func assertServiceFailureAudit(t *testing.T, records []audit.Record, action, target string) {
	t.Helper()
	if len(records) != 1 {
		t.Fatalf("audit records=%+v want one failure", records)
	}
	record := records[0]
	if record.Action != action || (target != "" && record.Target != target) || record.Result != "failure" || record.Actor == "" {
		t.Fatalf("audit record=%+v", record)
	}
	for _, forbidden := range []string{"privateKey", "passphrase", "nonce", "ciphertext", "secret-canary", "complete-output-canary"} {
		if strings.Contains(record.Payload, forbidden) {
			t.Fatalf("audit payload leaked %q: %s", forbidden, record.Payload)
		}
	}
}

func mustServiceExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}

func mustConfirmServiceHost(t *testing.T, h *serviceHarness, id, fingerprint string) {
	t.Helper()
	if err := h.repo.ConfirmHostKey(context.Background(), id, fingerprint, h.clock); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRejectsMissingEncryptionKeyForCredentialMutation(t *testing.T) {
	h := newServiceHarness(t)
	h.audit.records = nil
	withoutCipher := NewService(h.repo, nil, h.remote, NewCollector(h.remote, func() time.Time { return h.clock }), h.audit, func() time.Time { return h.clock })
	if _, err := withoutCipher.Create(context.Background(), "admin", serviceCreateInput()); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("Create error=%v", err)
	}
	if records := h.audit.all(); len(records) != 1 || records[0].Result != "failure" || !strings.Contains(records[0].Payload, `"authType":"password"`) {
		t.Fatalf("rejected create audit=%+v", records)
	}
	server := h.create(t)
	h.audit.records = nil
	secret := CredentialSecret{Password: "rotated"}
	if _, err := withoutCipher.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: server.Name, Address: server.Address, Username: server.Username, SSHPort: server.SSHPort, Secret: &secret}); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("Update credential error=%v", err)
	}
	if records := h.audit.all(); len(records) != 1 || records[0].Result != "failure" || records[0].Target != server.ID {
		t.Fatalf("rejected update audit=%+v", records)
	}
	updated, err := withoutCipher.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: "renamed", Address: server.Address, Username: server.Username, SSHPort: server.SSHPort})
	if err != nil || updated.Name != "renamed" {
		t.Fatalf("metadata update=%+v err=%v", updated, err)
	}
	if _, err := withoutCipher.Get(context.Background(), server.ID); err != nil {
		t.Fatalf("read-only Get: %v", err)
	}
}

func TestServiceUpdateDeleteListGetAndValidation(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	updated, err := h.svc.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: " renamed ", Address: server.Address, Username: server.Username, SSHPort: 2222})
	if err != nil || updated.Name != "renamed" || updated.SSHPort != 2222 || updated.Address != server.Address {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	updated, err = h.svc.Update(context.Background(), "admin", server.ID, UpdateServerInput{Name: updated.Name, Address: updated.Address, Username: updated.Username, SSHPort: 0})
	if err != nil || updated.SSHPort != 22 {
		t.Fatalf("zero port normalization updated=%+v err=%v", updated, err)
	}
	got, err := h.svc.Get(context.Background(), server.ID)
	if err != nil || got.Name != "renamed" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	listed, err := h.svc.List(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID != server.ID {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	if _, err := h.svc.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get missing error=%v", err)
	}
	if _, err := h.svc.Update(context.Background(), "admin", "missing", UpdateServerInput{Name: "x", Address: "host", Username: "root"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update missing error=%v", err)
	}
	for _, input := range []CreateServerInput{
		{Address: "host", Username: "root", AuthType: AuthPassword, Secret: CredentialSecret{Password: "x"}},
		{Name: "bad\u2028name", Address: "host", Username: "root", AuthType: AuthPassword, Secret: CredentialSecret{Password: "x"}},
		{Name: "ok", Address: "host\x00", Username: "root", AuthType: AuthPassword, Secret: CredentialSecret{Password: "x"}},
		{Name: "ok", Address: "host", Username: "root", SSHPort: 65536, AuthType: AuthPassword, Secret: CredentialSecret{Password: "x"}},
		{Name: "ok", Address: "host", Username: "root", AuthType: AuthPassword, Secret: CredentialSecret{Password: "x", PrivateKey: "also-key"}},
	} {
		if _, err := h.svc.Create(context.Background(), "admin", input); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid input %+v error=%v", input, err)
		}
	}
	if err := h.svc.Delete(context.Background(), "admin", server.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Delete(context.Background(), "admin", server.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing error=%v", err)
	}
}

func TestServiceValidatesIPv4IPv6OrASCIIDNSAddress(t *testing.T) {
	label63 := strings.Repeat("a", 63)
	maxHostname := label63 + "." + label63 + "." + label63 + "." + strings.Repeat("b", 61)
	valid := []struct {
		address string
		want    string
	}{
		{address: " 192.0.2.1 ", want: "192.0.2.1"},
		{address: "2001:0db8:0:0:0:0:0:1", want: "2001:db8::1"},
		{address: "Example.COM.", want: "example.com"},
		{address: "edge-01", want: "edge-01"},
		{address: maxHostname, want: maxHostname},
	}
	for index, tc := range valid {
		t.Run("valid "+tc.address, func(t *testing.T) {
			h := newServiceHarness(t)
			input := serviceCreateInput()
			input.Name = fmt.Sprintf("valid-%d", index)
			input.Address = tc.address
			created, err := h.svc.Create(context.Background(), "admin", input)
			if err != nil || created.Address != tc.want {
				t.Fatalf("created=%+v err=%v want address %q", created, err, tc.want)
			}
		})
	}

	invalid := []string{
		"http://example.com", "https://example.com", "example.com/path", "user@example.com", "example.com:22",
		"[2001:db8::1]", "fe80::1%eth0", "bad..example", ".example.com", "example.com..",
		"-bad.example", "bad-.example", strings.Repeat("a", 64) + ".example", maxHostname + "c",
		"exa_mple.com", "例子.example", "host\n", "host\x00name", ".", "",
	}
	for index, address := range invalid {
		t.Run(fmt.Sprintf("invalid-%d", index), func(t *testing.T) {
			h := newServiceHarness(t)
			input := serviceCreateInput()
			input.Name = fmt.Sprintf("invalid-%d", index)
			input.Address = address
			if _, err := h.svc.Create(context.Background(), "admin", input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("address=%q error=%v want ErrInvalidInput", address, err)
			}
		})
	}
}

func TestServiceTestConnectionSuccessAndHostKeyError(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	h.remote.probeResult = "SHA256:first"
	h.remote.probeErr = &HostKeyError{Actual: "SHA256:first"}
	result, err := h.svc.TestConnection(context.Background(), "operator", server.ID)
	var hostKeyErr *HostKeyError
	if !errors.As(err, &hostKeyErr) || result.Fingerprint != "SHA256:first" || result.Trusted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := h.svc.TestConnection(context.Background(), "operator", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error=%v", err)
	}
	if err := h.repo.ConfirmHostKey(context.Background(), server.ID, "SHA256:first", h.clock); err != nil {
		t.Fatal(err)
	}
	h.remote.probeErr = nil
	h.remote.run = func(target RemoteTarget, secret CredentialSecret, command string, limit int64) (CommandResult, error) {
		if secret != (CredentialSecret{Password: "correct horse battery staple"}) {
			t.Fatalf("authentication secret=%+v", secret)
		}
		if target.ExpectedFingerprint != "SHA256:first" || command != "LC_ALL=C true" || limit != 1024 {
			t.Fatalf("authentication target=%+v command=%q limit=%d", target, command, limit)
		}
		return CommandResult{ExitCode: 0}, nil
	}
	result, err = h.svc.TestConnection(context.Background(), "operator", server.ID)
	if err != nil || !result.Trusted || result.Fingerprint != "SHA256:first" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, runs := h.remote.callCounts(); runs != 1 {
		t.Fatalf("trusted host authentication runs=%d want 1", runs)
	}

	h.remote.run = func(RemoteTarget, CredentialSecret, string, int64) (CommandResult, error) {
		return CommandResult{ExitCode: 255, Stdout: "complete command output canary"}, errors.New("wrong-password-canary")
	}
	result, err = h.svc.TestConnection(context.Background(), "operator", server.ID)
	if err == nil || result.Trusted {
		t.Fatalf("authentication failure result=%+v err=%v", result, err)
	}
	for _, forbidden := range []string{"wrong-password-canary", "complete command output canary", "LC_ALL=C true"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("authentication error leaked %q: %v", forbidden, err)
		}
	}
}

func TestServiceLatestSnapshotAndSoftwareSuccessAndErrors(t *testing.T) {
	h := newServiceHarness(t)
	server := h.create(t)
	if _, err := h.svc.LatestSnapshot(context.Background(), server.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty snapshot error=%v", err)
	}
	software, err := h.svc.Software(context.Background(), server.ID)
	if err != nil || software == nil || len(software) != 0 {
		t.Fatalf("empty software=%v err=%v", software, err)
	}
	snapshot := Snapshot{ID: "snapshot", ServerID: server.ID, CollectedAt: h.clock}
	wantSoftware := []SoftwareItem{{Category: "package", Name: "curl", Version: "8"}}
	if err := h.repo.SaveCollection(context.Background(), server, snapshot, wantSoftware); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.LatestSnapshot(context.Background(), server.ID)
	if err != nil || got.ID != snapshot.ID {
		t.Fatalf("snapshot=%+v err=%v", got, err)
	}
	software, err = h.svc.Software(context.Background(), server.ID)
	if err != nil || len(software) != 1 || software[0].Name != "curl" {
		t.Fatalf("software=%v err=%v", software, err)
	}
	if _, err := h.svc.Software(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing software error=%v", err)
	}
}

func successfulServiceCollection(t *testing.T, wantSecret CredentialSecret) func(RemoteTarget, CredentialSecret, string, int64) (CommandResult, error) {
	t.Helper()
	return func(_ RemoteTarget, secret CredentialSecret, command string, _ int64) (CommandResult, error) {
		if secret != wantSecret {
			t.Fatalf("decrypted secret=%+v want configured secret", secret)
		}
		switch command {
		case collectorBaseCommand:
			return CommandResult{ExitCode: 0, Stdout: "AURORA_BASE_V1\nOS_RELEASE_BEGIN\nID=debian\nVERSION_ID=12\nOS_RELEASE_END\nKERNEL_NAME=Linux\nKERNEL_VERSION=6.1\nARCH=x86_64\nHOSTNAME=edge\nCPU_CORES=4\nMEM_TOTAL_KB=1024\nDISK_TOTAL_BYTES=4096\nLOAD1=0.25\nUPTIME_SECONDS=60\nAURORA_BASE_END\n"}, nil
		case collectorDebianCommand:
			return CommandResult{ExitCode: 0, Stdout: "curl\t8.0\tamd64\tinstalled\n"}, nil
		case collectorServicesCommand, collectorVersionsCommand:
			return CommandResult{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected collector command: %q", command)
			return CommandResult{}, nil
		}
	}
}
