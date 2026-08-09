package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/audit"
	"github.com/hougangbei/aurora-aiops/server/internal/auth"
	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

type assetRouteRemote struct {
	probeResult string
	probeErr    error
	run         func(assets.RemoteTarget, assets.CredentialSecret, string, int64) (assets.CommandResult, error)
	runCalls    int
}

func (f *assetRouteRemote) ProbeHostKey(context.Context, assets.RemoteTarget) (string, error) {
	return f.probeResult, f.probeErr
}

func (f *assetRouteRemote) Run(_ context.Context, target assets.RemoteTarget, secret assets.CredentialSecret, command string, limit int64) (assets.CommandResult, error) {
	f.runCalls++
	if f.run == nil {
		return assets.CommandResult{}, errors.New("asset route remote reached")
	}
	return f.run(target, secret, command, limit)
}

func (*assetRouteRemote) Upload(context.Context, assets.RemoteTarget, assets.CredentialSecret, io.Reader, int64, string, fs.FileMode) error {
	return errors.New("unexpected upload")
}

type assetRouteHarness struct {
	router *gin.Engine
	svc    *assets.Service
	remote *assetRouteRemote
}

func newAssetRouteHarness(t *testing.T) *assetRouteHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := store.Open(filepath.Join(t.TempDir(), "asset-routes.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := assets.NewAESGCMCredentialCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	remote := &assetRouteRemote{}
	now := func() time.Time { return time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) }
	svc := assets.NewService(assets.NewRepository(db), cipher, remote, assets.NewCollector(remote, now), audit.NewRepository(db), now)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(actorContextKey, auth.User{ID: "admin", Username: "admin", Role: auth.RoleAdmin, Enabled: true})
		c.Next()
	})
	api := router.Group("/api/v1")
	registerAssetRoutes(api, svc)
	return &assetRouteHarness{router: router, svc: svc, remote: remote}
}

func assetRouteRequest(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func validAssetCreateBody(testConnection bool) string {
	return `{"name":"edge-1","address":"192.0.2.10","username":"root","sshPort":22,"credentialAuthType":"password","password":"route-password-canary","testConnection":` +
		map[bool]string{true: "true", false: "false"}[testConnection] + `}`
}

func decodeAssetEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestAssetRoutesCreatePendingAndNeverExposeCredential(t *testing.T) {
	h := newAssetRouteHarness(t)
	rec := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers", validAssetCreateBody(false))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeAssetEnvelope(t, rec)
	data := body["data"].(map[string]any)
	if data["status"] != string(assets.ServerPending) || data["credentialAuthType"] != string(assets.AuthPassword) || data["credentialConfigured"] != true {
		t.Fatalf("data=%+v", data)
	}
	for _, forbidden := range []string{"route-password-canary", "credentialId", "nonce", "ciphertext", "privateKey", "passphrase"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestAssetRoutesValidationNotFoundAndEmptyList(t *testing.T) {
	h := newAssetRouteHarness(t)
	list := assetRouteRequest(t, h.router, http.MethodGet, "/api/v1/assets/servers", "")
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"data":[]`) {
		t.Fatalf("empty list status=%d body=%s", list.Code, list.Body.String())
	}
	invalid := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers", `{"name":"","address":"host","username":"root","credentialAuthType":"password","password":"x"}`)
	if invalid.Code != http.StatusBadRequest || decodeAssetEnvelope(t, invalid)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("invalid status=%d body=%s", invalid.Code, invalid.Body.String())
	}
	missing := assetRouteRequest(t, h.router, http.MethodGet, "/api/v1/assets/servers/missing", "")
	if missing.Code != http.StatusNotFound || decodeAssetEnvelope(t, missing)["code"] != "ASSET_NOT_FOUND" {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestAssetRoutesCreateHostKeyConfirmationIncludesServerAndFingerprint(t *testing.T) {
	h := newAssetRouteHarness(t)
	h.remote.probeResult = "SHA256:route-host"
	h.remote.probeErr = &assets.HostKeyError{Actual: "SHA256:route-host"}
	rec := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers", validAssetCreateBody(true))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeAssetEnvelope(t, rec)
	if body["code"] != "SSH_HOST_KEY_CONFIRMATION_REQUIRED" {
		t.Fatalf("body=%+v", body)
	}
	data := body["data"].(map[string]any)
	if data["fingerprint"] != "SHA256:route-host" || data["server"] == nil {
		t.Fatalf("data=%+v", data)
	}
	server := data["server"].(map[string]any)
	if server["status"] != string(assets.ServerPending) || server["id"] == "" {
		t.Fatalf("server=%+v", server)
	}
	if strings.Contains(rec.Body.String(), "route-password-canary") {
		t.Fatal("host-key response leaked credential")
	}
}

func TestAssetRoutesPatchDeleteConnectionCollectSnapshotAndSoftware(t *testing.T) {
	h := newAssetRouteHarness(t)
	created := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers", validAssetCreateBody(false))
	id := decodeAssetEnvelope(t, created)["data"].(map[string]any)["id"].(string)

	patched := assetRouteRequest(t, h.router, http.MethodPatch, "/api/v1/assets/servers/"+id, `{"name":"renamed"}`)
	if patched.Code != http.StatusOK || decodeAssetEnvelope(t, patched)["data"].(map[string]any)["name"] != "renamed" {
		t.Fatalf("patch status=%d body=%s", patched.Code, patched.Body.String())
	}
	h.remote.probeResult = "SHA256:host"
	h.remote.probeErr = &assets.HostKeyError{Actual: "SHA256:host"}
	connection := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers/"+id+"/test-connection", "")
	if connection.Code != http.StatusConflict || decodeAssetEnvelope(t, connection)["code"] != "SSH_HOST_KEY_CONFIRMATION_REQUIRED" {
		t.Fatalf("connection status=%d body=%s", connection.Code, connection.Body.String())
	}
	connectionData := decodeAssetEnvelope(t, connection)["data"].(map[string]any)
	connectionServer, ok := connectionData["server"].(map[string]any)
	if !ok || connectionServer["id"] != id || connectionData["fingerprint"] != "SHA256:host" || connectionData["trusted"] != false || connectionData["changed"] != false {
		t.Fatalf("connection data=%+v", connectionData)
	}
	if strings.Contains(connection.Body.String(), "route-password-canary") {
		t.Fatal("test-connection confirmation leaked credential")
	}
	confirmed := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers/"+id+"/confirm-host-key", `{"fingerprint":"SHA256:host"}`)
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}

	collect := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers/"+id+"/collect", "")
	if collect.Code != http.StatusInternalServerError || strings.Contains(collect.Body.String(), "asset route remote reached") {
		t.Fatalf("collect error must be generic: status=%d body=%s", collect.Code, collect.Body.String())
	}
	snapshot := assetRouteRequest(t, h.router, http.MethodGet, "/api/v1/assets/servers/"+id+"/snapshots/latest", "")
	if snapshot.Code != http.StatusNotFound {
		t.Fatalf("snapshot status=%d body=%s", snapshot.Code, snapshot.Body.String())
	}
	software := assetRouteRequest(t, h.router, http.MethodGet, "/api/v1/assets/servers/"+id+"/software", "")
	if software.Code != http.StatusOK || !strings.Contains(software.Body.String(), `"data":[]`) {
		t.Fatalf("software status=%d body=%s", software.Code, software.Body.String())
	}
	deleted := assetRouteRequest(t, h.router, http.MethodDelete, "/api/v1/assets/servers/"+id, "")
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestAssetRoutesCollectMapsTypedHostKeyErrorToConflict(t *testing.T) {
	h := newAssetRouteHarness(t)
	created := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers", validAssetCreateBody(false))
	id := decodeAssetEnvelope(t, created)["data"].(map[string]any)["id"].(string)
	h.remote.probeResult = "SHA256:old"
	h.remote.probeErr = &assets.HostKeyError{Actual: "SHA256:old"}
	confirmed := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers/"+id+"/confirm-host-key", `{"fingerprint":"SHA256:old"}`)
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirm status=%d body=%s", confirmed.Code, confirmed.Body.String())
	}
	h.remote.run = func(assets.RemoteTarget, assets.CredentialSecret, string, int64) (assets.CommandResult, error) {
		return assets.CommandResult{}, &assets.HostKeyError{Expected: "SHA256:old", Actual: "SHA256:new", Changed: true}
	}
	rec := assetRouteRequest(t, h.router, http.MethodPost, "/api/v1/assets/servers/"+id+"/collect", "")
	body := decodeAssetEnvelope(t, rec)
	if rec.Code != http.StatusConflict || body["code"] != "SSH_HOST_KEY_CONFIRMATION_REQUIRED" {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	data := body["data"].(map[string]any)
	server, ok := data["server"].(map[string]any)
	if !ok || server["id"] != id || server["hostKeyFingerprint"] != "SHA256:old" || server["credentialAuthType"] != string(assets.AuthPassword) || server["credentialConfigured"] != true {
		t.Fatalf("collect conflict data=%+v", data)
	}
	if data["fingerprint"] != "SHA256:new" || data["changed"] != true {
		t.Fatalf("collect host-key data=%+v", data)
	}
	for _, forbidden := range []string{"route-password-canary", "credentialId", "CredentialID", "envelope", "nonce", "ciphertext", "privateKey", "passphrase"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("collect conflict leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestAssetServiceStartupAllowsEmptyDatabaseWithoutKeyButRejectsExistingCredentials(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "asset-startup.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	remote := &assetRouteRemote{}
	now := func() time.Time { return time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC) }
	auditRepo := audit.NewRepository(db)

	readOnly, err := buildAssetService(db, nil, remote, auditRepo, now)
	if err != nil || readOnly == nil {
		t.Fatalf("empty database without key service=%v err=%v", readOnly, err)
	}
	if _, err := readOnly.Create(context.Background(), "admin", assets.CreateServerInput{
		Name: "no-key", Address: "192.0.2.30", Username: "root", AuthType: assets.AuthPassword,
		Secret: assets.CredentialSecret{Password: "must-not-leak"},
	}); !errors.Is(err, assets.ErrEncryptionUnavailable) {
		t.Fatalf("mutation error=%v want encryption unavailable", err)
	}

	key := []byte("0123456789abcdef0123456789abcdef")
	configured, err := buildAssetService(db, key, remote, auditRepo, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := configured.Create(context.Background(), "admin", assets.CreateServerInput{
		Name: "configured", Address: "192.0.2.31", Username: "root", AuthType: assets.AuthPassword,
		Secret: assets.CredentialSecret{Password: "startup-password-canary"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := buildAssetService(db, nil, remote, auditRepo, now); err == nil {
		t.Fatal("existing credentials without key must fail startup")
	} else if strings.Contains(err.Error(), "startup-password-canary") || strings.Contains(err.Error(), "ciphertext") || strings.Contains(err.Error(), "nonce") {
		t.Fatalf("startup error leaked credential material: %v", err)
	}
}
