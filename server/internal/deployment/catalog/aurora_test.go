package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

type fakeResolver struct{}

func (fakeResolver) Resolve(context.Context, string, string) (deployment.ReleaseArtifact, error) {
	b := validArchiveBytes()
	h := sha256.Sum256(b)
	return deployment.ReleaseArtifact{Version: "0.1.1", Architecture: "amd64", ArchiveName: "aurora-aiops_0.1.1_linux_amd64.tar.gz", DownloadURL: "https://github.com/hougangbei/aurora-aiops/releases/download/v0.1.1/a.tar.gz", SHA256: hex.EncodeToString(h[:]), Size: int64(len(b))}, nil
}
func (fakeResolver) Download(_ context.Context, _ deployment.ReleaseArtifact, dst io.Writer) error {
	_, err := dst.Write(validArchiveBytes())
	return err
}

func validArchiveBytes() []byte {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	contents := map[string]string{"aurora-aiops_0.1.1/aurora-aiops": "binary", "aurora-aiops_0.1.1/aurora-aiops.service": "[Service]\nExecStart=/opt/aurora-aiops/current/aurora-aiops\n", "aurora-aiops_0.1.1/migrate.sql": "-- migration\n"}
	for name, value := range contents {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(value))})
		_, _ = tw.Write([]byte(value))
	}
	_ = tw.Close()
	_ = gz.Close()
	return raw.Bytes()
}

func onlineServer() assets.Server {
	return assets.Server{ID: "server-1", Status: assets.ServerOnline, OSFamily: "linux", OSVersion: "ubuntu-22.04", Architecture: "amd64", HostKeyFingerprint: "SHA256:fingerprint", Address: "192.0.2.10", Username: "root"}
}

func TestAuroraProjectMetadataAndPlanContract(t *testing.T) {
	installer := NewAuroraInstaller(fakeResolver{})
	project := installer.Project()
	if project.ID != "aurora-aiops" || project.Name == "" || len(project.Versions) == 0 {
		t.Fatalf("project=%+v", project)
	}
	if want := []string{"amd64", "arm64"}; !equalStrings(project.SupportedArchitectures, want) {
		t.Fatalf("architectures=%v want %v", project.SupportedArchitectures, want)
	}
	task := deployment.Task{ID: "task-1", ServerID: "server-1", ProjectID: project.ID, Version: "0.1.1", Action: deployment.TaskActionInstall}
	plan, err := installer.BuildPlan(task, onlineServer(), json.RawMessage(`{"bootstrapAdminUser":"admin","bootstrapAdminPassword":"correct horse battery"}`))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"verify-ssh", "preflight", "resolve-release", "transfer-release", "verify-checksum", "activate-version", "configure-service", "restart-service", "verify-health"}
	wantLabels := []string{"校验连接与主机指纹", "检查系统、Kubernetes、架构、磁盘和权限", "解析发布资产", "传输发布包", "校验 SHA256", "解包并原子切换版本", "配置环境与 systemd", "启用并重启服务", "验证健康并记录安装"}
	wantPercents := []int{10, 20, 30, 40, 50, 65, 78, 90, 100}
	if len(plan) != len(wantIDs) {
		t.Fatalf("plan length=%d", len(plan))
	}
	for i, step := range plan {
		if step.ID != wantIDs[i] || step.Label != wantLabels[i] || step.Percent != wantPercents[i] || step.Timeout <= 0 {
			t.Errorf("step %d=%+v", i, step)
		}
	}
}

func TestAuroraRejectsUnsupportedTargetsAndVersions(t *testing.T) {
	installer := NewAuroraInstaller(fakeResolver{})
	base := deployment.Task{ID: "task-1", ServerID: "server-1", ProjectID: "aurora-aiops", Version: "0.1.1", Action: deployment.TaskActionInstall}
	cases := []struct {
		name   string
		server assets.Server
		task   deployment.Task
	}{
		{name: "os", server: func() assets.Server { s := onlineServer(); s.OSFamily = "darwin"; return s }()},
		{name: "arch", server: func() assets.Server { s := onlineServer(); s.Architecture = "riscv64"; return s }()},
		{name: "offline", server: func() assets.Server { s := onlineServer(); s.Status = assets.ServerOffline; return s }()},
		{name: "version", server: onlineServer(), task: func() deployment.Task { x := base; x.Version = "9.9.9"; return x }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := base
			if tc.task.Version != "" {
				task = tc.task
			}
			_, err := installer.BuildPlan(task, tc.server, json.RawMessage(`{"bootstrapAdminUser":"admin","bootstrapAdminPassword":"correct horse battery"}`))
			if !errors.Is(err, deployment.ErrUnsupportedTarget) {
				t.Fatalf("err=%v want ErrUnsupportedTarget", err)
			}
		})
	}
}

func TestAuroraConfigurationIsExactAndDoesNotLeakSecrets(t *testing.T) {
	installer := NewAuroraInstaller(fakeResolver{})
	secret := "super-secret-password"
	good, err := installer.NormalizeConfiguration(json.RawMessage(`{"bootstrapAdminUser":"admin","bootstrapAdminPassword":"` + secret + `"}`))
	if err != nil || !json.Valid(good) || strings.Contains(string(good), secret) == false {
		t.Fatalf("normalized=%s err=%v", good, err)
	}
	for _, raw := range []string{`{}`, `{"bootstrapAdminUser":"ab","bootstrapAdminPassword":"123456789012"}`, `{"bootstrapAdminUser":"admin","bootstrapAdminPassword":"short","extra":"x"}`, `{"bootstrapAdminUser":"bad user","bootstrapAdminPassword":"correct horse battery"}`} {
		if _, err := installer.NormalizeConfiguration(json.RawMessage(raw)); err == nil {
			t.Fatalf("NormalizeConfiguration(%s) unexpectedly succeeded", raw)
		}
	}
	plan, err := installer.BuildPlan(deployment.Task{ID: "task-secret", ServerID: "server-1", ProjectID: "aurora-aiops", Version: "0.1.1", Action: deployment.TaskActionInstall}, onlineServer(), good)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plan {
		if strings.Contains(step.ID+step.Label, secret) {
			t.Fatal("secret in step metadata")
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Keep compile-time coverage on the narrow execution surface used by tests.
var _ deployment.ExecutionContext = (*fakeExecutionContext)(nil)

type fakeExecutionContext struct{ server assets.Server }

func (f *fakeExecutionContext) Server() assets.Server { return f.server }
func (f *fakeExecutionContext) Run(context.Context, string, int64) (assets.CommandResult, error) {
	return assets.CommandResult{}, nil
}
func (*fakeExecutionContext) Upload(context.Context, io.Reader, int64, string, fs.FileMode) error {
	return nil
}
func (*fakeExecutionContext) Log(string)                    {}
func (*fakeExecutionContext) SetValue(string, string) error { return nil }
func (*fakeExecutionContext) Value(string) (string, bool)   { return "", false }

var _ = time.Second
