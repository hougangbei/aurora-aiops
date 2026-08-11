package catalog

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

type recordingExecutionContext struct {
	server   assets.Server
	commands []string
	uploads  []string
	logs     []string
	values   map[string]string
}

func (r *recordingExecutionContext) Server() assets.Server { return r.server }
func (r *recordingExecutionContext) Run(_ context.Context, command string, _ int64) (assets.CommandResult, error) {
	r.commands = append(r.commands, command)
	return assets.CommandResult{ExitCode: 0, Stdout: "ok\n"}, nil
}
func (r *recordingExecutionContext) Upload(_ context.Context, src io.Reader, size int64, path string, _ fs.FileMode) error {
	b, err := io.ReadAll(src)
	if err != nil {
		return err
	}
	if int64(len(b)) != size {
		return io.ErrShortBuffer
	}
	r.uploads = append(r.uploads, path)
	return nil
}
func (r *recordingExecutionContext) Log(message string) { r.logs = append(r.logs, message) }
func (r *recordingExecutionContext) SetValue(key, value string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	r.values[key] = value
	return nil
}
func (r *recordingExecutionContext) Value(key string) (string, bool) {
	v, ok := r.values[key]
	return v, ok
}

func TestAuroraCommandsUseOnlyFixedPathsAndNoSecrets(t *testing.T) {
	ctx := &recordingExecutionContext{server: onlineServer()}
	installer := NewAuroraInstaller(fakeResolver{})
	plan, err := installer.BuildPlan(taskForTest("task-safe"), ctx.server, normalizedConfigForTest("admin", "super-secret-password"))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plan {
		if step.Run == nil {
			continue
		}
		if err := step.Run(context.Background(), ctx); err != nil {
			t.Fatalf("step %s: %v", step.ID, err)
		}
	}
	for _, command := range ctx.commands {
		if strings.Contains(command, "super-secret-password") || strings.Contains(command, "bootstrapAdminPassword") {
			t.Fatalf("credential leaked into command: %q", command)
		}
		if strings.Contains(command, ";") || strings.Contains(command, "&&") || strings.Contains(command, "||") || strings.Contains(command, "\n") {
			t.Fatalf("unsafe command syntax: %q", command)
		}
	}
	for _, path := range ctx.uploads {
		if !(strings.HasPrefix(path, "/tmp/aurora-aiops/task-safe/") || strings.HasPrefix(path, "/opt/aurora-aiops/releases/")) {
			t.Fatalf("unexpected upload path %q", path)
		}
	}
	for _, value := range ctx.values {
		if strings.Contains(value, "super-secret-password") {
			t.Fatal("secret persisted as task value")
		}
	}
	for _, log := range ctx.logs {
		if strings.Contains(log, "super-secret-password") {
			t.Fatal("secret leaked to log")
		}
	}
}

func taskForTest(id string) deployment.Task {
	return deployment.Task{ID: id, ServerID: "server-1", ProjectID: "aurora-aiops", Version: "0.1.1", Action: deployment.TaskActionInstall}
}

func normalizedConfigForTest(user, password string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"bootstrapAdminUser": user, "bootstrapAdminPassword": password})
	return b
}
