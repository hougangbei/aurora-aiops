package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

const kubernetesProjectID = "kubernetes"

type KubernetesInstaller struct{}

func NewKubernetesInstaller() *KubernetesInstaller { return &KubernetesInstaller{} }

func (i *KubernetesInstaller) Project() deployment.Project {
	return deployment.Project{
		ID:                     kubernetesProjectID,
		Name:                   "Kubernetes",
		Description:            "在受支持的 Linux 主机上安装固定版本的单节点 kubeadm 集群，并使用 Cilium 提供网络。",
		Versions:               []string{kubernetesVersion},
		SupportedOSFamilies:    []string{"debian", "ubuntu"},
		SupportedArchitectures: []string{"amd64", "arm64"},
	}
}

func (i *KubernetesInstaller) NormalizeConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var cfg struct{}
	if err := decoder.Decode(&cfg); err != nil {
		return nil, deployment.ErrInvalidInput
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, deployment.ErrInvalidInput
	}
	return []byte(`{}`), nil
}

func (i *KubernetesInstaller) BuildPlan(task deployment.Task, server assets.Server, config json.RawMessage) ([]deployment.StepDefinition, error) {
	if i == nil || task.ProjectID != kubernetesProjectID || task.Action != deployment.TaskActionInstall || task.Version != kubernetesVersion {
		return nil, deployment.ErrUnsupportedTarget
	}
	if _, err := i.NormalizeConfiguration(config); err != nil {
		return nil, err
	}
	if server.ID == "" || server.Status != assets.ServerOnline || server.HostKeyFingerprint == "" || server.OSFamily != "ubuntu" && server.OSFamily != "debian" || server.OSVersion == "" || server.Architecture != "amd64" && server.Architecture != "arm64" || server.Username == "" || !kubeadmHostnamePattern.MatchString(server.Name) || len(server.Name) > 253 {
		return nil, deployment.ErrUnsupportedTarget
	}
	commands, err := kubeadmCommands(server)
	if err != nil {
		return nil, deployment.ErrUnsupportedTarget
	}
	configYAML, err := kubeadmConfigYAML(server.Name)
	if err != nil || configYAML == "" || containsForbiddenKubeadmCommand(commands) {
		return nil, deployment.ErrUnsupportedTarget
	}
	steps := []deployment.StepDefinition{
		{ID: "verify-access", Label: "校验 SSH、指纹和权限", Percent: 5, Timeout: 30 * time.Second},
		{ID: "preflight", Label: "检查系统、资源、端口和网络", Percent: 15, Timeout: 30 * time.Second},
		{ID: "prepare-kernel", Label: "配置内核、sysctl 和 swap", Percent: 28, Timeout: 5 * time.Minute},
		{ID: "install-containerd", Label: "安装并配置 containerd", Percent: 45, Timeout: 5 * time.Minute},
		{ID: "install-kube-tools", Label: "安装固定版本 Kubernetes 组件", Percent: 60, Timeout: 5 * time.Minute},
		{ID: "kubeadm-init", Label: "初始化控制平面", Percent: 75, Timeout: 10 * time.Minute},
		{ID: "configure-single-node", Label: "配置 kubeconfig 和单节点调度", Percent: 80, Timeout: 5 * time.Minute},
		{ID: "install-cilium", Label: "安装固定版本 Cilium", Percent: 88, Timeout: 10 * time.Minute},
		{ID: "wait-ready", Label: "等待节点和核心 Pod 就绪", Percent: 96, Timeout: 15 * time.Minute},
		{ID: "save-cluster", Label: "加密保存 kubeconfig 和健康摘要", Percent: 100, Timeout: 5 * time.Minute},
	}
	// Bind only immutable, compile-time commands to steps. Dynamic task values
	// are deliberately absent from this installer until the execution layer
	// uploads validated files through ExecutionContext.Upload.
	for index := range steps {
		stepIndex := index
		steps[index].Probe = func(ctx context.Context, exec deployment.ExecutionContext) (bool, error) {
			if stepIndex == 0 {
				return runKubeadmCommand(ctx, exec, "LC_ALL=C true")
			}
			return false, nil
		}
		steps[index].Run = func(ctx context.Context, exec deployment.ExecutionContext) error {
			if stepIndex == 0 {
				_, err := exec.Run(ctx, "LC_ALL=C true", 4096)
				return err
			}
			if stepIndex == 1 {
				_, err := exec.Run(ctx, "test \"$(uname -s)\" = Linux", 4096)
				return err
			}
			groups := map[int][]string{
				2: commands[0:7], 3: commands[7:9], 4: commands[9:12],
				5: commands[12:13], 6: commands[13:15], 7: commands[15:16], 8: commands[16:18], 9: []string{"test -s /etc/kubernetes/admin.conf"},
			}
			return runKubeadmCommands(ctx, exec, groups[stepIndex])
		}
	}
	return steps, nil
}

func runKubeadmCommands(ctx context.Context, exec deployment.ExecutionContext, commands []string) error {
	for _, command := range commands {
		if _, err := exec.Run(ctx, command, 64<<10); err != nil {
			return fmt.Errorf("kubeadm step command failed: %w", err)
		}
	}
	return nil
}

func runKubeadmCommand(ctx context.Context, exec deployment.ExecutionContext, command string) (bool, error) {
	result, err := exec.Run(ctx, command, 4096)
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}
