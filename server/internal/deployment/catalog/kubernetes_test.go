package catalog

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

func TestKubernetesProjectMetadataAndFixedTenStepPlan(t *testing.T) {
	installer := NewKubernetesInstaller()
	project := installer.Project()
	if project.ID != "kubernetes" || len(project.Versions) != 1 || project.Versions[0] != "v1.35.6" {
		t.Fatalf("project=%+v", project)
	}
	if !equalStrings(project.SupportedOSFamilies, []string{"debian", "ubuntu"}) || !equalStrings(project.SupportedArchitectures, []string{"amd64", "arm64"}) {
		t.Fatalf("support matrix=%+v", project)
	}
	task := deployment.Task{ID: "task-1", ServerID: "server-1", ProjectID: "kubernetes", Version: "v1.35.6", Action: deployment.TaskActionInstall}
	steps, err := installer.BuildPlan(task, kubeadmTestServer(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"verify-access", "preflight", "prepare-kernel", "install-containerd", "install-kube-tools", "kubeadm-init", "configure-single-node", "install-cilium", "wait-ready", "save-cluster"}
	wantLabels := []string{"校验 SSH、指纹和权限", "检查系统、资源、端口和网络", "配置内核、sysctl 和 swap", "安装并配置 containerd", "安装固定版本 Kubernetes 组件", "初始化控制平面", "配置 kubeconfig 和单节点调度", "安装固定版本 Cilium", "等待节点和核心 Pod 就绪", "加密保存 kubeconfig 和健康摘要"}
	wantPercents := []int{5, 15, 28, 45, 60, 75, 80, 88, 96, 100}
	if len(steps) != len(wantIDs) {
		t.Fatalf("steps=%d", len(steps))
	}
	for i, step := range steps {
		if step.ID != wantIDs[i] || step.Label != wantLabels[i] || step.Percent != wantPercents[i] || step.Timeout <= 0 {
			t.Errorf("step %d=%+v", i, step)
		}
	}
}

func TestKubernetesRejectsUnsafeTargetAndVersion(t *testing.T) {
	installer := NewKubernetesInstaller()
	base := deployment.Task{ID: "task-1", ServerID: "server-1", ProjectID: "kubernetes", Version: "v1.35.6", Action: deployment.TaskActionInstall}
	cases := []struct {
		name   string
		task   deployment.Task
		server assets.Server
	}{
		{"version", func() deployment.Task { x := base; x.Version = "latest"; return x }(), kubeadmTestServer()},
		{"hostname", base, func() assets.Server { s := kubeadmTestServer(); s.Name = "bad;hostname"; return s }()},
		{"os", base, func() assets.Server { s := kubeadmTestServer(); s.OSFamily = "darwin"; return s }()},
		{"arch", base, func() assets.Server { s := kubeadmTestServer(); s.Architecture = "riscv64"; return s }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := installer.BuildPlan(tc.task, tc.server, json.RawMessage(`{}`)); !errors.Is(err, deployment.ErrUnsupportedTarget) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func kubeadmTestServer() assets.Server {
	return assets.Server{ID: "server-1", Name: "node-1", Address: "192.0.2.10", Username: "root", SSHPort: 22, Status: assets.ServerOnline, HostKeyFingerprint: "SHA256:fingerprint", OSFamily: "ubuntu", OSVersion: "22.04", Architecture: "amd64", CPUCores: 4, MemoryBytes: 8 << 30, DiskBytes: 50 << 30}
}

func TestKubernetesPlanNeverMentionsForbiddenOperations(t *testing.T) {
	installer := NewKubernetesInstaller()
	task := deployment.Task{ID: "task-safe", ServerID: "server-1", ProjectID: "kubernetes", Version: "v1.35.6", Action: deployment.TaskActionInstall}
	steps, err := installer.BuildPlan(task, kubeadmTestServer(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if strings.Contains(strings.ToLower(step.ID+step.Label), "latest") {
			t.Fatal("latest in metadata")
		}
	}
}
