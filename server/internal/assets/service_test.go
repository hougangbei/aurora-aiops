package assets

import (
	"context"
	"errors"
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
		repo:   NewRepository(db),
		remote: &serviceFakeRemote{},
		audit:  &serviceFakeAudit{},
		cipher: cipher,
		clock:  time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
	}
	h.svc = NewService(h.repo, h.cipher, h.remote, NewCollector(h.remote, func() time.Time { return h.clock }), h.audit, func() time.Time { return h.clock })
	return h
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
	result, err = h.svc.TestConnection(context.Background(), "operator", server.ID)
	if err != nil || !result.Trusted || result.Fingerprint != "SHA256:first" {
		t.Fatalf("result=%+v err=%v", result, err)
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
