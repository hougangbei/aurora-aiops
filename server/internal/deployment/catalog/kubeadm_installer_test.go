package catalog

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

func TestValidateAdminKubeconfigRequiresSingleHTTPSCurrentContext(t *testing.T) {
	valid := []byte(`apiVersion: v1
kind: Config
clusters:
- name: cluster
  cluster:
    server: https://192.0.2.10:6443
contexts:
- name: admin@cluster
  context:
    cluster: cluster
    user: admin
current-context: admin@cluster
users:
- name: admin
  user:
    token: redacted
`)
	if err := validateAdminKubeconfig(valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{
		[]byte(strings.ReplaceAll(string(valid), "https://", "http://")),
		[]byte(strings.ReplaceAll(string(valid), "current-context: admin@cluster", "current-context: missing")),
		[]byte(strings.ReplaceAll(string(valid), "server: https://192.0.2.10:6443", "server: https://192.0.2.10:6443\n- name: second")),
	} {
		if err := validateAdminKubeconfig(raw); err == nil {
			t.Fatalf("invalid kubeconfig accepted: %s", raw)
		}
	}
}

func TestKubeadmPlanUploadsValidatedConfigBeforeInit(t *testing.T) {
	installer := NewKubernetesInstaller()
	plan, err := installer.BuildPlan(deployment.Task{ID: "task-1", ServerID: "server-1", ProjectID: "kubernetes", Version: "v1.35.6", Action: deployment.TaskActionInstall}, kubeadmTestServer(), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	exec := &recordingKubeadmExecution{server: kubeadmTestServer()}
	if err := plan[5].Run(context.Background(), exec); err != nil {
		t.Fatal(err)
	}
	if exec.uploadPath != kubeadmConfigStagingPath || !strings.Contains(exec.uploaded, "kubeadm.k8s.io/v1beta4") {
		t.Fatalf("upload=(%q,%q)", exec.uploadPath, exec.uploaded)
	}
	joined := strings.Join(exec.commands, "\n")
	if !strings.Contains(joined, "kubeadm init") || strings.Contains(joined, "kubeadm reset") {
		t.Fatalf("commands=%s", joined)
	}
}

type recordingKubeadmExecution struct {
	server     assets.Server
	commands   []string
	uploadPath string
	uploaded   string
}

func (r *recordingKubeadmExecution) Server() assets.Server { return r.server }
func (r *recordingKubeadmExecution) Run(_ context.Context, command string, _ int64) (assets.CommandResult, error) {
	r.commands = append(r.commands, command)
	return assets.CommandResult{ExitCode: 0}, nil
}
func (r *recordingKubeadmExecution) Upload(_ context.Context, source io.Reader, size int64, destination string, _ fs.FileMode) error {
	data, err := io.ReadAll(source)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return io.ErrShortBuffer
	}
	r.uploadPath, r.uploaded = destination, string(data)
	return nil
}
func (r *recordingKubeadmExecution) Log(string)                    {}
func (r *recordingKubeadmExecution) SetValue(string, string) error { return nil }
func (r *recordingKubeadmExecution) Value(string) (string, bool)   { return "", false }
