package catalog

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

var errUnsupportedTarget = deployment.ErrUnsupportedTarget

const (
	projectID            = "aurora-aiops"
	defaultAuroraVersion = "0.1.1"
	valueArchive         = "release-archive"
	valueSHA256          = "release-sha256"
	valueStaging         = "release-staging-path"
	valueSize            = "release-size"
	valueEnvStaging      = "env-staging-path"
)

type AuroraInstaller struct {
	resolver deployment.ReleaseResolver
	versions []string
}

func NewAuroraInstaller(resolver deployment.ReleaseResolver, versions ...string) *AuroraInstaller {
	if len(versions) == 0 {
		versions = []string{defaultAuroraVersion}
	}
	copyVersions := append([]string(nil), versions...)
	return &AuroraInstaller{resolver: resolver, versions: copyVersions}
}

func (i *AuroraInstaller) Project() deployment.Project {
	versions := append([]string(nil), i.versions...)
	return deployment.Project{
		ID: projectID, Name: "Aurora AIOps", Description: "在受支持的 Linux 主机上安装 Aurora AIOps。",
		Versions: versions, SupportedOSFamilies: []string{"linux"}, SupportedArchitectures: []string{"amd64", "arm64"},
	}
}

type auroraConfiguration struct {
	BootstrapAdminUser     string `json:"bootstrapAdminUser"`
	BootstrapAdminPassword string `json:"bootstrapAdminPassword"`
}

func (i *AuroraInstaller) NormalizeConfiguration(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("configuration is required")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var cfg auroraConfiguration
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid configuration")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, fmt.Errorf("invalid configuration")
	}
	if cfg.BootstrapAdminUser == "" || !validUsername(cfg.BootstrapAdminUser) {
		return nil, fmt.Errorf("invalid bootstrap admin user")
	}
	if len(cfg.BootstrapAdminPassword) < 12 || len(cfg.BootstrapAdminPassword) > 128 || strings.IndexFunc(cfg.BootstrapAdminPassword, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("invalid bootstrap admin password")
	}
	// Canonical JSON is persisted encrypted by the deployment service; this
	// method never logs or returns the values except to that encryption layer.
	normalized, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("normalize configuration")
	}
	return normalized, nil
}

func (i *AuroraInstaller) BuildPlan(task deployment.Task, server assets.Server, config json.RawMessage) ([]deployment.StepDefinition, error) {
	if i == nil || i.resolver == nil || task.ProjectID != projectID || !containsString(i.versions, task.Version) {
		return nil, errUnsupportedTarget
	}
	statusMessage := strings.ToLower(server.StatusMessage)
	if server.OSFamily != "linux" || (server.Architecture != "amd64" && server.Architecture != "arm64") || server.Status != assets.ServerOnline || server.HostKeyFingerprint == "" || server.Username == "" || (strings.Contains(statusMessage, "systemd") && (strings.Contains(statusMessage, "missing") || strings.Contains(statusMessage, "unavailable"))) {
		return nil, errUnsupportedTarget
	}
	var cfg auroraConfiguration
	if _, err := i.NormalizeConfiguration(config); err != nil {
		return nil, deployment.ErrInvalidInput
	}
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, deployment.ErrInvalidInput
	}
	staging, err := stagingPath(task.ID, "aurora-aiops_"+task.Version+"_linux_"+server.Architecture+".tar.gz")
	if err != nil {
		return nil, deployment.ErrInvalidInput
	}
	steps := []deployment.StepDefinition{
		{ID: "verify-ssh", Label: "校验连接与主机指纹", Percent: 10, Timeout: 30 * time.Second,
			Probe: probeCommand(func() (string, error) { return commandVerifySSH(server) }),
			Run:   runCommand(func() (string, error) { return commandVerifySSH(server) })},
		{ID: "preflight", Label: "检查系统、Kubernetes、架构、磁盘和权限", Percent: 20, Timeout: 2 * time.Minute,
			Probe: func(ctx context.Context, exec deployment.ExecutionContext) (bool, error) {
				return runPreflight(ctx, exec, server)
			},
			Run: func(ctx context.Context, exec deployment.ExecutionContext) error {
				commands, err := preflightCommands(server)
				if err != nil {
					return err
				}
				for _, cmd := range commands {
					if err := runChecked(ctx, exec, cmd, 4096); err != nil {
						return err
					}
				}
				return nil
			}},
		{ID: "resolve-release", Label: "解析发布资产", Percent: 30, Timeout: 2 * time.Minute, ValueKeys: []string{valueArchive, valueSHA256, valueSize},
			Probe: func(_ context.Context, exec deployment.ExecutionContext) (bool, error) {
				_, archiveOK := exec.Value(valueArchive)
				_, digestOK := exec.Value(valueSHA256)
				return archiveOK && digestOK, nil
			},
			Run: func(ctx context.Context, exec deployment.ExecutionContext) error {
				artifact, err := i.resolver.Resolve(ctx, task.Version, server.Architecture)
				if err != nil {
					return safeInstallerError(err)
				}
				if artifact.Version != task.Version || artifact.Architecture != server.Architecture || artifact.ArchiveName == "" || artifact.SHA256 == "" || artifact.Size < 0 {
					return fmt.Errorf("release artifact does not match target")
				}
				if _, err := stagingPath(task.ID, artifact.ArchiveName); err != nil {
					return err
				}
				if err := exec.SetValue(valueArchive, artifact.ArchiveName); err != nil {
					return err
				}
				if err := exec.SetValue(valueSHA256, strings.ToLower(artifact.SHA256)); err != nil {
					return err
				}
				return exec.SetValue(valueSize, fmt.Sprintf("%d", artifact.Size))
			}},
		{ID: "transfer-release", Label: "传输发布包", Percent: 40, Timeout: 10 * time.Minute, ValueKeys: []string{valueStaging},
			Probe: func(ctx context.Context, exec deployment.ExecutionContext) (bool, error) {
				archive, ok := exec.Value(valueStaging)
				if !ok {
					archive = staging
				}
				cmd, err := commandProbeStaging(server, archive)
				if err != nil {
					return false, err
				}
				return runProbe(ctx, exec, func() (string, error) { return cmd, nil })
			},
			Run: func(ctx context.Context, exec deployment.ExecutionContext) error {
				artifact, err := i.resolver.Resolve(ctx, task.Version, server.Architecture)
				if err != nil {
					return safeInstallerError(err)
				}
				staging, err := stagingPath(task.ID, artifact.ArchiveName)
				if err != nil {
					return err
				}
				tmp, err := os.CreateTemp("", "aurora-aiops-release-*")
				if err != nil {
					return fmt.Errorf("prepare release transfer")
				}
				name := tmp.Name()
				defer os.Remove(name)
				_ = tmp.Chmod(0o600)
				if err := i.resolver.Download(ctx, artifact, tmp); err != nil {
					_ = tmp.Close()
					return safeInstallerError(err)
				}
				if err := tmp.Sync(); err != nil {
					_ = tmp.Close()
					return fmt.Errorf("prepare release transfer")
				}
				stat, err := tmp.Stat()
				if err != nil {
					_ = tmp.Close()
					return fmt.Errorf("prepare release transfer")
				}
				if artifact.Size != stat.Size() {
					_ = tmp.Close()
					return fmt.Errorf("release size mismatch")
				}
				if _, err := tmp.Seek(0, io.SeekStart); err != nil {
					_ = tmp.Close()
					return fmt.Errorf("prepare release transfer")
				}
				if err := validateArchive(tmp); err != nil {
					_ = tmp.Close()
					return err
				}
				if _, err := tmp.Seek(0, io.SeekStart); err != nil {
					_ = tmp.Close()
					return fmt.Errorf("prepare release transfer")
				}
				if err := exec.Upload(ctx, tmp, stat.Size(), staging, 0o600); err != nil {
					_ = tmp.Close()
					return safeInstallerError(err)
				}
				if err := tmp.Close(); err != nil {
					return fmt.Errorf("release transfer close failed")
				}
				return exec.SetValue(valueStaging, staging)
			}},
		{ID: "verify-checksum", Label: "校验 SHA256", Percent: 50, Timeout: 2 * time.Minute,
			Probe: func(ctx context.Context, exec deployment.ExecutionContext) (bool, error) {
				archive, ok := exec.Value(valueStaging)
				if !ok {
					archive = staging
				}
				digest, ok := exec.Value(valueSHA256)
				if !ok {
					return false, nil
				}
				return runProbe(ctx, exec, func() (string, error) { return commandChecksum(server, archive, digest) })
			},
			Run: func(ctx context.Context, exec deployment.ExecutionContext) error {
				archive, ok := exec.Value(valueStaging)
				if !ok {
					archive = staging
				}
				digest, ok := exec.Value(valueSHA256)
				if !ok {
					return fmt.Errorf("release checksum is unavailable")
				}
				cmd, err := commandChecksum(server, archive, digest)
				if err != nil {
					return err
				}
				_, err = exec.Run(ctx, cmd, 4096)
				return safeInstallerError(err)
			}},
		{ID: "activate-version", Label: "解包并原子切换版本", Percent: 65, Timeout: 5 * time.Minute,
			Probe: func(ctx context.Context, exec deployment.ExecutionContext) (bool, error) {
				release, err := releasePath(task.Version)
				if err != nil {
					return false, err
				}
				cmd, err := fixedCommand(server, "test \"$(readlink -f "+commandPath(pathCurrent)+")\" = "+commandPath(release))
				if err != nil {
					return false, err
				}
				return runProbe(ctx, exec, func() (string, error) { return cmd, nil })
			},
			Run: activationRun(server, task, staging)},
		{ID: "configure-service", Label: "配置环境与 systemd", Percent: 78, Timeout: 3 * time.Minute,
			Probe: func(ctx context.Context, exec deployment.ExecutionContext) (bool, error) {
				cmd, err := fixedCommand(server, "test -s "+commandPath(pathConfig))
				if err != nil {
					return false, err
				}
				return runProbe(ctx, exec, func() (string, error) { return cmd, nil })
			},
			Run: configureRun(server, task, cfg, staging)},
		{ID: "restart-service", Label: "启用并重启服务", Percent: 90, Timeout: 3 * time.Minute,
			Probe: probeCommand(func() (string, error) { return fixedCommand(server, "systemctl is-active aurora-aiops.service") }),
			Run:   restartRun(server, task, cfg)},
		{ID: "verify-health", Label: "验证健康并记录安装", Percent: 100, Timeout: 2 * time.Minute,
			Probe: probeCommand(func() (string, error) { return commandHealth(server) }),
			Run:   runCommand(func() (string, error) { return commandHealth(server) })},
	}
	return steps, nil
}

func activationRun(server assets.Server, task deployment.Task, staging string) func(context.Context, deployment.ExecutionContext) error {
	return func(ctx context.Context, exec deployment.ExecutionContext) error {
		release, err := releasePath(task.Version)
		if err != nil {
			return err
		}
		cmd, err := commandInstallLayout(server)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 1024); err != nil {
			return err
		}
		cmd, err = commandListArchive(server, staging)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 16<<10); err != nil {
			return err
		}
		cmd, err = commandExtractArchive(server, staging, release)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 16<<10); err != nil {
			return err
		}
		cmd, err = commandAtomicLink(server, release)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 1024); err != nil {
			return err
		}
		cmd, err = commandAtomicActivate(server)
		if err != nil {
			return err
		}
		return runChecked(ctx, exec, cmd, 1024)
	}
}

func configureRun(server assets.Server, task deployment.Task, cfg auroraConfiguration, staging string) func(context.Context, deployment.ExecutionContext) error {
	return func(ctx context.Context, exec deployment.ExecutionContext) error {
		cmd, err := commandEnsureUser(server)
		if err != nil {
			return err
		}
		if result, runErr := exec.Run(ctx, cmd, 1024); runErr != nil || result.ExitCode != 0 { // user may already exist; probe is authoritative on retries.
			if result, probeErr := exec.Run(ctx, "id -u aurora-aiops", 1024); probeErr != nil || result.ExitCode != 0 {
				if runErr != nil {
					return safeInstallerError(runErr)
				}
				return fmt.Errorf("remote installer operation failed")
			}
		}
		cmd, err = commandKubeconfig(server)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 2048); err != nil {
			return err
		}
		envPath, err := envStagingPath(task.ID)
		if err != nil {
			return err
		}
		env, err := bootstrapEnv(cfg)
		if err != nil {
			return err
		}
		if err := exec.Upload(ctx, strings.NewReader(env), int64(len(env)), envPath, 0o600); err != nil {
			return safeInstallerError(err)
		}
		cmd, err = commandInstallEnv(server, envPath)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 2048); err != nil {
			return err
		}
		release, err := releasePath(task.Version)
		if err != nil {
			return err
		}
		cmd, err = commandInstallService(server, path.Join(release, "aurora-aiops.service"))
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 2048); err != nil {
			return err
		}
		return exec.SetValue(valueEnvStaging, envPath)
	}
}

func bootstrapEnv(cfg auroraConfiguration) (string, error) {
	if !validUsername(cfg.BootstrapAdminUser) || len(cfg.BootstrapAdminPassword) < 12 || len(cfg.BootstrapAdminPassword) > 128 || strings.IndexFunc(cfg.BootstrapAdminPassword, unicode.IsControl) >= 0 {
		return "", deployment.ErrInvalidInput
	}
	return "AURORA_AIOPS_BOOTSTRAP_ADMIN_USER=" + cfg.BootstrapAdminUser + "\nAURORA_AIOPS_BOOTSTRAP_ADMIN_PASSWORD=" + cfg.BootstrapAdminPassword + "\n", nil
}

func probeCommand(command func() (string, error)) func(context.Context, deployment.ExecutionContext) (bool, error) {
	return func(ctx context.Context, exec deployment.ExecutionContext) (bool, error) {
		return runProbe(ctx, exec, command)
	}
}

func runPreflight(ctx context.Context, exec deployment.ExecutionContext, server assets.Server) (bool, error) {
	commands, err := preflightCommands(server)
	if err != nil {
		return false, err
	}
	for _, cmd := range commands {
		result, runErr := exec.Run(ctx, cmd, 4096)
		if runErr != nil {
			return false, safeInstallerError(runErr)
		}
		if result.ExitCode != 0 {
			return false, nil
		}
	}
	return true, nil
}

func restartRun(server assets.Server, task deployment.Task, cfg auroraConfiguration) func(context.Context, deployment.ExecutionContext) error {
	return func(ctx context.Context, exec deployment.ExecutionContext) error {
		cmd, err := commandDaemonReload(server)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 1024); err != nil {
			return err
		}
		cmd, err = commandRestart(server)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 1024); err != nil {
			return err
		}
		// The first start consumes bootstrap credentials. Replace the file before
		// restarting again so credentials do not remain on the target.
		envPath, err := envStagingPath(task.ID)
		if err != nil {
			return err
		}
		env := ""
		if cfg.BootstrapAdminUser != "" || cfg.BootstrapAdminPassword != "" {
			env = "# bootstrap credentials removed after first start\n"
		}
		if err := exec.Upload(ctx, strings.NewReader(env), int64(len(env)), envPath, 0o600); err != nil {
			return safeInstallerError(err)
		}
		cmd, err = commandInstallEnv(server, envPath)
		if err != nil {
			return err
		}
		if err := runChecked(ctx, exec, cmd, 2048); err != nil {
			return err
		}
		cmd, err = commandRestart(server)
		if err != nil {
			return err
		}
		return runChecked(ctx, exec, cmd, 1024)
	}
}

func runProbe(ctx context.Context, exec deployment.ExecutionContext, command func() (string, error)) (bool, error) {
	cmd, err := command()
	if err != nil {
		return false, err
	}
	result, err := exec.Run(ctx, cmd, 4096)
	if err != nil {
		return false, safeInstallerError(err)
	}
	return result.ExitCode == 0, nil
}
func runCommand(command func() (string, error)) func(context.Context, deployment.ExecutionContext) error {
	return func(ctx context.Context, exec deployment.ExecutionContext) error {
		cmd, err := command()
		if err != nil {
			return err
		}
		return runChecked(ctx, exec, cmd, 4096)
	}
}
func safeInstallerError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("remote installer operation failed")
}

func runChecked(ctx context.Context, exec deployment.ExecutionContext, command string, limit int64) error {
	result, err := exec.Run(ctx, command, limit)
	if err != nil {
		return safeInstallerError(err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("remote installer operation failed")
	}
	return nil
}
func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func validateArchive(file *os.File) error {
	if file == nil {
		return errors.New("release archive unavailable")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return errors.New("release archive unavailable")
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		return errors.New("release archive is invalid")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	members := 0
	topLevel := ""
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errors.New("release archive is invalid")
		}
		members++
		clean := path.Clean(h.Name)
		if strings.HasPrefix(h.Name, "/") || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(h.Name, 0) {
			return errors.New("release archive contains unsafe path")
		}
		parts := strings.Split(clean, "/")
		if topLevel == "" {
			topLevel = parts[0]
		}
		if parts[0] != topLevel || !strings.HasPrefix(topLevel, "aurora-aiops_") || len(parts) < 2 {
			return errors.New("release archive layout is invalid")
		}
	}
	if members == 0 {
		return errors.New("release archive is empty")
	}
	return nil
}

func archiveDigest(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
