package catalog

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/hougangbei/aurora-aiops/server/internal/deployment"
)

const (
	minKubeadmKernel = 5
	minKubeadmMinor  = 10
	minKubeadmCPU    = 2
	minKubeadmMemory = int64(4 << 30)
	minKubeadmDisk   = int64(30 << 30)
)

var kubeadmRequiredPorts = []int{6443, 2379, 2380, 10250, 10257, 10259}

type PreflightInput struct {
	OSRelease             string
	KernelVersion         string
	CPUs                  int
	MemoryBytes           int64
	RootFreeBytes         int64
	DefaultRoute          bool
	DNS                   bool
	OccupiedPorts         map[int]bool
	SudoNonInteractive    bool
	SwapDisablePersistent bool
	ExistingAdminConfig   bool
	ExistingReadyz        bool
	ExistingVersion       string
	ExistingNodes         []string
}

type OSRelease struct {
	ID        string
	VersionID string
}

type PreflightResult struct {
	OSFamily  string
	OSVersion string
}

func ParseOSRelease(raw string) (OSRelease, error) {
	values := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"")
		values[key] = value
	}
	result := OSRelease{ID: strings.ToLower(values["ID"]), VersionID: values["VERSION_ID"]}
	if result.ID == "" || result.VersionID == "" {
		return OSRelease{}, fmt.Errorf("unsupported /etc/os-release")
	}
	return result, nil
}

func ValidateKubeadmPreflight(input PreflightInput) (PreflightResult, error) {
	if input.ExistingAdminConfig || input.ExistingReadyz {
		nodes := append([]string(nil), input.ExistingNodes...)
		return PreflightResult{}, &deployment.AdoptionRequiredError{Summary: deployment.AdoptionSummary{Version: input.ExistingVersion, Nodes: nodes}}
	}
	osRelease, err := ParseOSRelease(input.OSRelease)
	if err != nil {
		return PreflightResult{}, err
	}
	if !supportedOS(osRelease) {
		return PreflightResult{}, fmt.Errorf("unsupported operating system %s %s", osRelease.ID, osRelease.VersionID)
	}
	if !kernelAtLeast(input.KernelVersion, minKubeadmKernel, minKubeadmMinor) {
		return PreflightResult{}, fmt.Errorf("kernel must be at least 5.10")
	}
	if input.CPUs < minKubeadmCPU {
		return PreflightResult{}, fmt.Errorf("at least two CPUs are required")
	}
	if input.MemoryBytes < minKubeadmMemory {
		return PreflightResult{}, fmt.Errorf("at least 4 GiB memory is required")
	}
	if input.RootFreeBytes < minKubeadmDisk {
		return PreflightResult{}, fmt.Errorf("at least 30 GiB free on / is required")
	}
	if !input.DefaultRoute || !input.DNS {
		return PreflightResult{}, fmt.Errorf("default route and DNS are required")
	}
	for _, port := range kubeadmRequiredPorts {
		if input.OccupiedPorts != nil && input.OccupiedPorts[port] {
			return PreflightResult{}, fmt.Errorf("required TCP port %d is occupied", port)
		}
	}
	if !input.SudoNonInteractive {
		return PreflightResult{}, fmt.Errorf("passwordless non-interactive sudo is required")
	}
	if !input.SwapDisablePersistent {
		return PreflightResult{}, fmt.Errorf("swap cannot be disabled persistently")
	}
	return PreflightResult{OSFamily: osRelease.ID, OSVersion: osRelease.VersionID}, nil
}

func supportedOS(os OSRelease) bool {
	return (os.ID == "ubuntu" && (os.VersionID == "22.04" || os.VersionID == "24.04")) || (os.ID == "debian" && os.VersionID == "12")
}

func kernelAtLeast(value string, wantMajor, wantMinor int) bool {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minorText := parts[1]
	for i, r := range minorText {
		if r < '0' || r > '9' {
			minorText = minorText[:i]
			break
		}
	}
	minor, err := strconv.Atoi(minorText)
	if err != nil {
		return false
	}
	return major > wantMajor || (major == wantMajor && minor >= wantMinor)
}
