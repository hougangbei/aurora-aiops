package catalog

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hougangbei/aurora-aiops/server/internal/assets"
)

const (
	pathRoot       = "/opt/aurora-aiops"
	pathRelease    = "/opt/aurora-aiops/releases"
	pathCurrent    = "/opt/aurora-aiops/current"
	pathConfig     = "/etc/aurora-aiops/aurora-aiops.env"
	pathService    = "/etc/systemd/system/aurora-aiops.service"
	pathKubeconfig = "/opt/aurora-aiops/config/kubeconfig"
	pathStaging    = "/tmp/aurora-aiops"
)

var (
	taskIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	versionPattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	archivePattern  = regexp.MustCompile(`^aurora-aiops_[0-9A-Za-z.+-]+_linux_(amd64|arm64)\.tar\.gz$`)
	usernamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,62}[a-z0-9])?$`)
)

func stagingPath(taskID, archive string) (string, error) {
	if !taskIDPattern.MatchString(taskID) || !archivePattern.MatchString(archive) || strings.Contains(archive, "..") {
		return "", fmt.Errorf("invalid staging path")
	}
	return pathStaging + "/" + taskID + "/" + archive, nil
}

func envStagingPath(taskID string) (string, error) {
	if !taskIDPattern.MatchString(taskID) {
		return "", fmt.Errorf("invalid staging path")
	}
	return pathStaging + "/" + taskID + "/bootstrap.env", nil
}

func releasePath(version string) (string, error) {
	if !versionPattern.MatchString(version) || strings.Contains(version, "/") {
		return "", fmt.Errorf("invalid release version")
	}
	return pathRelease + "/" + version, nil
}

func commandPath(path string) string { return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'" }

func privilege(server assets.Server, command string) (string, error) {
	if strings.TrimSpace(server.Username) == "" {
		return "", fmt.Errorf("target username is missing: %w", errUnsupportedTarget)
	}
	if server.Username == "root" {
		return command, nil
	}
	return "sudo -n " + command, nil
}

func fixedCommand(server assets.Server, command string) (string, error) {
	if strings.ContainsAny(command, "\r\n") {
		return "", fmt.Errorf("invalid command")
	}
	return privilege(server, command)
}

func commandVerifySSH(server assets.Server) (string, error) {
	return fixedCommand(server, "LC_ALL=C true")
}

func commandPreflight(server assets.Server) (string, error) {
	return fixedCommand(server, "test \"$(uname -s)\" = Linux")
}

func preflightCommands(server assets.Server) ([]string, error) {
	arch := "x86_64"
	if server.Architecture == "arm64" {
		arch = "aarch64"
	}
	items := []string{"test \"$(uname -s)\" = Linux", "test -d /run/systemd/system", "test -s /etc/kubernetes/admin.conf", "test \"$(uname -m)\" = " + arch, "df -Pk /opt"}
	if server.Username != "root" {
		items = append(items, "sudo -n true")
	}
	commands := make([]string, 0, len(items))
	for _, item := range items {
		cmd, err := fixedCommand(server, item)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}
	return commands, nil
}

func commandProbeStaging(server assets.Server, path string) (string, error) {
	return fixedCommand(server, "test -s "+commandPath(path))
}

func commandChecksum(server assets.Server, path, digest string) (string, error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(digest) {
		return "", fmt.Errorf("invalid release checksum")
	}
	if err := validateRemotePath(path); err != nil {
		return "", err
	}
	return fixedCommand(server, "test \"$(sha256sum "+commandPath(path)+" | awk '{print $1}')\" = "+digest)
}

func commandListArchive(server assets.Server, archivePath string) (string, error) {
	if err := validateRemotePath(archivePath); err != nil {
		return "", err
	}
	return fixedCommand(server, "tar -tzf "+commandPath(archivePath))
}

func commandExtractArchive(server assets.Server, archivePath, releaseDir string) (string, error) {
	if err := validateRemotePath(archivePath); err != nil {
		return "", err
	}
	if err := validateRemotePath(releaseDir); err != nil {
		return "", err
	}
	return fixedCommand(server, "tar --strip-components=1 -xzf "+commandPath(archivePath)+" -C "+commandPath(releaseDir))
}

func commandAtomicLink(server assets.Server, releaseDir string) (string, error) {
	tmp := pathRoot + "/current.tmp"
	if err := validateRemotePath(releaseDir); err != nil {
		return "", err
	}
	return fixedCommand(server, "ln -sfn "+commandPath(releaseDir)+" "+commandPath(tmp))
}

func commandAtomicActivate(server assets.Server) (string, error) {
	return fixedCommand(server, "mv -Tf "+commandPath(pathRoot+"/current.tmp")+" "+commandPath(pathCurrent))
}

func commandInstallLayout(server assets.Server) (string, error) {
	return fixedCommand(server, "install -d -m 0755 "+commandPath(pathRoot)+" "+commandPath(pathRelease)+" "+commandPath(pathRoot+"/config"))
}

func commandEnsureUser(server assets.Server) (string, error) {
	return fixedCommand(server, "useradd --system --home-dir "+commandPath(pathRoot)+" --shell /usr/sbin/nologin aurora-aiops")
}

func commandKubeconfig(server assets.Server) (string, error) {
	return fixedCommand(server, "install -o aurora-aiops -g aurora-aiops -m 0600 /etc/kubernetes/admin.conf "+commandPath(pathKubeconfig))
}

func commandInstallEnv(server assets.Server, stagingEnv string) (string, error) {
	if err := validateRemotePath(stagingEnv); err != nil {
		return "", err
	}
	return fixedCommand(server, "install -o aurora-aiops -g aurora-aiops -m 0600 "+commandPath(stagingEnv)+" "+commandPath(pathConfig))
}

func commandInstallService(server assets.Server, servicePath string) (string, error) {
	if err := validateRemotePath(servicePath); err != nil {
		return "", err
	}
	return fixedCommand(server, "install -m 0644 "+commandPath(servicePath)+" "+commandPath(pathService))
}

func commandDaemonReload(server assets.Server) (string, error) {
	return fixedCommand(server, "systemctl daemon-reload")
}
func commandRestart(server assets.Server) (string, error) {
	return fixedCommand(server, "systemctl enable --now aurora-aiops.service")
}
func commandHealth(server assets.Server) (string, error) {
	return fixedCommand(server, "curl --fail --silent --show-error --max-time 5 http://127.0.0.1:8080/api/v1/system/version")
}

func validUsername(value string) bool {
	return usernamePattern.MatchString(value) && len(value) >= 3 && len(value) <= 64
}

func validateRemotePath(value string) error {
	if value == "" || strings.ContainsAny(value, "\r\n\x00") || strings.Contains(value, "..") || !strings.HasPrefix(value, "/") {
		return fmt.Errorf("invalid remote path")
	}
	allowed := strings.HasPrefix(value, pathStaging+"/") || strings.HasPrefix(value, pathRelease+"/") || value == pathCurrent || strings.HasPrefix(value, pathRoot+"/")
	if !allowed {
		return fmt.Errorf("remote path is outside installer roots")
	}
	return nil
}
