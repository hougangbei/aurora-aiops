package assets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	collectorCommandBase            = "base"
	collectorCommandDebian          = "packages_debian"
	collectorCommandRPM             = "packages_rpm"
	collectorCommandAPK             = "packages_apk"
	collectorCommandServices        = "services_systemd"
	collectorCommandVersions        = "versions"
	collectorMalformedWarningSuffix = "_malformed"

	collectorBaseLimit     int64 = 1 << 20
	collectorPackageLimit  int64 = 8 << 20
	collectorServicesLimit int64 = 4 << 20
	collectorVersionsLimit int64 = 1 << 20

	collectorBaseCommand = `LC_ALL=C sh -c '
printf "%s\n" AURORA_BASE_V1 OS_RELEASE_BEGIN
if [ -r /etc/os-release ]; then sed -n "s/^[[:space:]]*\(ID\|VERSION_ID\)[[:space:]]*=.*/&/p" /etc/os-release; fi
printf "%s\n" OS_RELEASE_END
printf "KERNEL_NAME=%s\n" "$(uname -s)"
printf "KERNEL_VERSION=%s\n" "$(uname -r)"
printf "ARCH=%s\n" "$(uname -m)"
printf "HOSTNAME=%s\n" "$(hostname)"
printf "CPU_CORES=%s\n" "$(getconf _NPROCESSORS_ONLN)"
awk "/^MemTotal:/ { print \"MEM_TOTAL_KB=\" \$2; found=1; exit } END { if (!found) print \"MEM_TOTAL_KB=\" }" /proc/meminfo
df -B1 / | awk "NR==2 { print \"DISK_TOTAL_BYTES=\" \$2; found=1 } END { if (!found) print \"DISK_TOTAL_BYTES=\" }"
awk "{ print \"LOAD1=\" \$1 }" /proc/loadavg
awk "{ printf \"UPTIME_SECONDS=%.0f\\n\", \$1 }" /proc/uptime
printf "%s\n" AURORA_BASE_END
'`
	collectorDebianCommand   = `LC_ALL=C dpkg-query -W -f='${binary:Package}\t${Version}\t${Architecture}\n'`
	collectorRPMCommand      = `LC_ALL=C rpm -qa --qf '%{NAME}\t%{VERSION}-%{RELEASE}\t%{ARCH}\n'`
	collectorAPKCommand      = `LC_ALL=C apk info -v`
	collectorServicesCommand = `LC_ALL=C systemctl list-unit-files --type=service --no-legend --no-pager`
	collectorVersionsCommand = `LC_ALL=C sh -c '
arch=$(uname -m)
emit() {
  category=$1 name=$2 version=$3 unit=$4 status=installed
  if [ -n "$unit" ] && command -v systemctl >/dev/null 2>&1; then
    service_status=$(systemctl is-active "$unit" 2>/dev/null || true)
    if [ -n "$service_status" ]; then status=$service_status; fi
  fi
  printf "%s\t%s\t%s\t%s\t%s\n" "$category" "$name" "$version" "$arch" "$status"
}
if command -v containerd >/dev/null 2>&1; then emit runtime containerd "$(containerd --version 2>/dev/null | awk "NR==1 { print \$3; exit }")" containerd.service; fi
if command -v docker >/dev/null 2>&1; then emit runtime docker "$(docker --version 2>/dev/null | awk "NR==1 { gsub(/,\$/, \"\", \$3); print \$3; exit }")" docker.service; fi
if command -v crio >/dev/null 2>&1; then emit runtime crio "$(crio --version 2>/dev/null | awk "NR==1 { print \$NF; exit }")" crio.service; fi
if command -v kubeadm >/dev/null 2>&1; then emit kubernetes kubeadm "$(kubeadm version -o short 2>/dev/null | awk "NR==1 { print \$1; exit }")" ""; fi
if command -v kubelet >/dev/null 2>&1; then emit kubernetes kubelet "$(kubelet --version 2>/dev/null | awk "NR==1 { print \$2; exit }")" kubelet.service; fi
if command -v kubectl >/dev/null 2>&1; then emit kubernetes kubectl "$(kubectl version --client=true --output=yaml 2>/dev/null | awk "\$1 == \"gitVersion:\" { print \$2; exit }")" ""; fi
if command -v aurora-aiops >/dev/null 2>&1; then emit aurora aurora-aiops "$(aurora-aiops -version 2>/dev/null | awk "NR==1 { print \$2; exit }")" aurora-aiops.service; fi
'`
)

type collectorCommand struct {
	Command string
	Limit   int64
}

var collectorCommands = map[string]collectorCommand{
	collectorCommandBase:     {Command: collectorBaseCommand, Limit: collectorBaseLimit},
	collectorCommandDebian:   {Command: collectorDebianCommand, Limit: collectorPackageLimit},
	collectorCommandRPM:      {Command: collectorRPMCommand, Limit: collectorPackageLimit},
	collectorCommandAPK:      {Command: collectorAPKCommand, Limit: collectorPackageLimit},
	collectorCommandServices: {Command: collectorServicesCommand, Limit: collectorServicesLimit},
	collectorCommandVersions: {Command: collectorVersionsCommand, Limit: collectorVersionsLimit},
}

// collectorCommandsForTest returns a copy so tests can assert the command
// allowlist without being able to mutate the collector's command definitions.
func collectorCommandsForTest() map[string]collectorCommand {
	commands := make(map[string]collectorCommand, len(collectorCommands))
	for id, command := range collectorCommands {
		commands[id] = command
	}
	return commands
}

type Collector struct {
	remote RemoteTransport
	now    func() time.Time
}

func NewCollector(remote RemoteTransport, now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	return &Collector{remote: remote, now: now}
}

func (c *Collector) Collect(ctx context.Context, server Server, secret CredentialSecret) (Snapshot, []SoftwareItem, error) {
	if c == nil || c.remote == nil {
		return Snapshot{}, nil, fmt.Errorf("collector remote transport is required: %w", ErrInvalidInput)
	}
	if ctx == nil {
		return Snapshot{}, nil, fmt.Errorf("collector context is required: %w", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, nil, err
	}

	target := RemoteTarget{
		Address:             server.Address,
		Port:                server.SSHPort,
		Username:            server.Username,
		ExpectedFingerprint: server.HostKeyFingerprint,
	}
	baseCommand := collectorCommands[collectorCommandBase]
	result, err := c.remote.Run(ctx, target, secret, baseCommand.Command, baseCommand.Limit)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Snapshot{}, nil, ctxErr
	}
	if err != nil || result.ExitCode != 0 {
		return Snapshot{}, nil, fmt.Errorf("base probe failed")
	}
	if result.Truncated {
		return Snapshot{}, nil, fmt.Errorf("base probe output was truncated: %w", ErrInvalidInput)
	}
	snapshot, err := parseCollectorBase(result.Stdout)
	if err != nil {
		return Snapshot{}, nil, err
	}
	snapshot.ID, err = newCollectorSnapshotID()
	if err != nil {
		return Snapshot{}, nil, fmt.Errorf("create snapshot ID: %w", err)
	}
	snapshot.ServerID = server.ID
	clock := c.now
	if clock == nil {
		clock = time.Now
	}
	snapshot.CollectedAt = clock().UTC()

	software := make([]SoftwareItem, 0)
	if packageID := collectorPackageCommand(snapshot.OSFamily); packageID != "" {
		result, warning, runErr := c.runOptional(ctx, target, secret, packageID)
		if runErr != nil {
			return Snapshot{}, nil, runErr
		}
		items, malformed := parseCollectorPackages(packageID, result, snapshot.Architecture)
		software = appendCollectorOptionalResult(software, packageID, items, malformed, warning)
	}
	result, warning, runErr := c.runOptional(ctx, target, secret, collectorCommandServices)
	if runErr != nil {
		return Snapshot{}, nil, runErr
	}
	items, malformed := parseCollectorServices(result)
	software = appendCollectorOptionalResult(software, collectorCommandServices, items, malformed, warning)
	result, warning, runErr = c.runOptional(ctx, target, secret, collectorCommandVersions)
	if runErr != nil {
		return Snapshot{}, nil, runErr
	}
	items, malformed = parseCollectorVersions(result)
	software = appendCollectorOptionalResult(software, collectorCommandVersions, items, malformed, warning)

	return snapshot, deduplicateCollectorSoftware(software), nil
}

func appendCollectorOptionalResult(software []SoftwareItem, commandID string, items []SoftwareItem, malformed int, warning *SoftwareItem) []SoftwareItem {
	software = append(software, items...)
	if malformed > 0 {
		reason := strconv.Itoa(malformed) + " malformed inventory records ignored"
		if malformed == 1 {
			reason = "1 malformed inventory record ignored"
		}
		if warning != nil && warning.Status != "" {
			reason = warning.Status + "; " + reason
		}
		warning = &SoftwareItem{
			Category: "collector_warning",
			Name:     commandID + collectorMalformedWarningSuffix,
			Status:   sanitizeCollectorField(reason, 512),
		}
	}
	if warning != nil {
		software = append(software, *warning)
	}
	return software
}

func (c *Collector) runOptional(ctx context.Context, target RemoteTarget, secret CredentialSecret, id string) (CommandResult, *SoftwareItem, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, nil, err
	}
	command := collectorCommands[id]
	result, err := c.remote.Run(ctx, target, secret, command.Command, command.Limit)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return CommandResult{}, nil, ctxErr
	}
	var reason string
	switch {
	case err != nil:
		reason = "command failed"
	case result.ExitCode != 0:
		reason = "command exited with status " + strconv.Itoa(result.ExitCode)
	case result.Truncated:
		reason = "output truncated"
	}
	if reason == "" {
		return result, nil, nil
	}
	warning := SoftwareItem{Category: "collector_warning", Name: id, Status: sanitizeCollectorField(reason, 512)}
	if err != nil || result.ExitCode != 0 {
		return CommandResult{}, &warning, nil
	}
	return result, &warning, nil
}

func parseCollectorBase(output string) (Snapshot, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	if len(lines) < 3 || lines[0] != "AURORA_BASE_V1" {
		return Snapshot{}, invalidCollectorBase()
	}
	values := make(map[string]string)
	osValues := make(map[string]string)
	inOSRelease := false
	seenOSRelease := false
	closedOSRelease := false
	ended := false
	for _, line := range lines[1:] {
		if ended {
			if line != "" {
				return Snapshot{}, invalidCollectorBase()
			}
			continue
		}
		switch line {
		case "OS_RELEASE_BEGIN":
			if inOSRelease || seenOSRelease {
				return Snapshot{}, invalidCollectorBase()
			}
			inOSRelease = true
			seenOSRelease = true
			continue
		case "OS_RELEASE_END":
			if !inOSRelease || closedOSRelease {
				return Snapshot{}, invalidCollectorBase()
			}
			inOSRelease = false
			closedOSRelease = true
			continue
		case "AURORA_BASE_END":
			if inOSRelease || !closedOSRelease {
				return Snapshot{}, invalidCollectorBase()
			}
			ended = true
			continue
		}
		if line == "" {
			return Snapshot{}, invalidCollectorBase()
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" {
			return Snapshot{}, invalidCollectorBase()
		}
		key = strings.TrimSpace(key)
		if inOSRelease {
			if !validCollectorOSReleaseKey(key) {
				return Snapshot{}, invalidCollectorBase()
			}
			if key == "ID" || key == "VERSION_ID" {
				if _, exists := osValues[key]; exists {
					return Snapshot{}, invalidCollectorBase()
				}
				osValues[key] = trimOSReleaseValue(value)
			}
			continue
		}
		if !closedOSRelease || !collectorBaseKeyAllowed(key) {
			return Snapshot{}, invalidCollectorBase()
		}
		if _, exists := values[key]; exists {
			return Snapshot{}, invalidCollectorBase()
		}
		values[key] = value
	}
	if inOSRelease || !seenOSRelease || !closedOSRelease || !ended {
		return Snapshot{}, invalidCollectorBase()
	}

	osID := sanitizeCollectorField(osValues["ID"], 256)
	osVersion := sanitizeCollectorField(osValues["VERSION_ID"], 512)
	arch := sanitizeCollectorField(values["ARCH"], 512)
	hostname := sanitizeCollectorField(values["HOSTNAME"], 256)
	kernelName := sanitizeCollectorField(values["KERNEL_NAME"], 256)
	kernelVersion := sanitizeCollectorField(values["KERNEL_VERSION"], 512)
	if osID == "" || osVersion == "" || arch == "" || hostname == "" || kernelName == "" || kernelVersion == "" {
		return Snapshot{}, invalidCollectorBase()
	}
	cpu, err := parsePositiveCollectorInt(values["CPU_CORES"], strconv.IntSize)
	if err != nil || cpu > int64(math.MaxInt) {
		return Snapshot{}, invalidCollectorBase()
	}
	memoryKB, err := parsePositiveCollectorInt(values["MEM_TOTAL_KB"], 64)
	if err != nil || memoryKB > math.MaxInt64/1024 {
		return Snapshot{}, invalidCollectorBase()
	}
	diskBytes, err := parsePositiveCollectorInt(values["DISK_TOTAL_BYTES"], 64)
	if err != nil {
		return Snapshot{}, invalidCollectorBase()
	}
	load1, err := strconv.ParseFloat(strings.TrimSpace(values["LOAD1"]), 64)
	if err != nil || math.IsNaN(load1) || math.IsInf(load1, 0) || load1 < 0 {
		return Snapshot{}, invalidCollectorBase()
	}
	uptime, err := strconv.ParseInt(strings.TrimSpace(values["UPTIME_SECONDS"]), 10, 64)
	if err != nil || uptime < 0 {
		return Snapshot{}, invalidCollectorBase()
	}

	return Snapshot{
		OSFamily:      normalizeCollectorOS(osID),
		OSVersion:     osVersion,
		KernelVersion: kernelVersion,
		Architecture:  normalizeCollectorArchitecture(arch),
		Hostname:      hostname,
		CPUCores:      int(cpu),
		MemoryBytes:   memoryKB * 1024,
		DiskBytes:     diskBytes,
		Load1:         load1,
		UptimeSeconds: uptime,
	}, nil
}

func validCollectorOSReleaseKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if (r < 'A' || r > 'Z') && r != '_' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func collectorBaseKeyAllowed(key string) bool {
	switch key {
	case "KERNEL_NAME", "KERNEL_VERSION", "ARCH", "HOSTNAME", "CPU_CORES", "MEM_TOTAL_KB", "DISK_TOTAL_BYTES", "LOAD1", "UPTIME_SECONDS":
		return true
	default:
		return false
	}
}

func invalidCollectorBase() error {
	return fmt.Errorf("invalid base probe output: %w", ErrInvalidInput)
}

func parsePositiveCollectorInt(value string, bits int) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(value), 10, bits)
	if err != nil || n <= 0 {
		return 0, ErrInvalidInput
	}
	return n, nil
}

func trimOSReleaseValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		value = value[1 : len(value)-1]
	}
	return value
}

func normalizeCollectorOS(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "ubuntu", "debian":
		return "debian"
	case "rhel", "centos", "rocky", "almalinux", "fedora":
		return "rhel"
	case "alpine":
		return "alpine"
	default:
		return id
	}
}

func normalizeCollectorArchitecture(arch string) string {
	arch = strings.ToLower(strings.TrimSpace(arch))
	switch arch {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return arch
	}
}

func collectorPackageCommand(osFamily string) string {
	switch osFamily {
	case "debian":
		return collectorCommandDebian
	case "rhel":
		return collectorCommandRPM
	case "alpine":
		return collectorCommandAPK
	default:
		return ""
	}
}

func parseCollectorPackages(commandID string, result CommandResult, defaultArch string) ([]SoftwareItem, int) {
	lines, malformed := collectorRecordLines(result.Stdout, result.Truncated)
	items := make([]SoftwareItem, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var name, version, arch, source string
		switch commandID {
		case collectorCommandDebian:
			fields := strings.Split(line, "\t")
			if len(fields) != 3 || !validCollectorRawField(fields[0], 256) || !validCollectorRawField(fields[1], 512) || !validCollectorRawField(fields[2], 512) {
				malformed++
				continue
			}
			name, version, arch, source = fields[0], fields[1], fields[2], "dpkg"
			if base, suffix, ok := strings.Cut(name, ":"); ok {
				if !validCollectorRawField(base, 256) || !validCollectorRawField(suffix, 512) || strings.Contains(suffix, ":") {
					malformed++
					continue
				}
				name = base
			}
			if !validCollectorRawField(name, 256) {
				malformed++
				continue
			}
		case collectorCommandRPM:
			fields := strings.Split(line, "\t")
			if len(fields) != 3 || !validCollectorRawField(fields[0], 256) || !validCollectorRawField(fields[1], 512) || !validCollectorRawField(fields[2], 512) {
				malformed++
				continue
			}
			name, version, arch, source = fields[0], fields[1], fields[2], "rpm"
		case collectorCommandAPK:
			name, version = splitCollectorAPK(line)
			arch, source = defaultArch, "apk"
			if !validCollectorRawField(name, 256) || !validCollectorRawField(version, 512) {
				malformed++
				continue
			}
		default:
			malformed++
			continue
		}
		item := SoftwareItem{
			Category: "package", Name: sanitizeCollectorField(name, 256),
			Version: sanitizeCollectorField(version, 512), Architecture: normalizeCollectorArchitecture(sanitizeCollectorField(arch, 512)),
			Source: source,
		}
		items = append(items, item)
	}
	return items, malformed
}

func splitCollectorAPK(line string) (string, string) {
	for i := 0; i+1 < len(line); i++ {
		if line[i] == '-' && line[i+1] >= '0' && line[i+1] <= '9' {
			return line[:i], line[i+1:]
		}
	}
	return "", ""
}

func parseCollectorServices(result CommandResult) ([]SoftwareItem, int) {
	lines, malformed := collectorRecordLines(result.Stdout, result.Truncated)
	items := make([]SoftwareItem, 0, len(lines))
	for _, line := range lines {
		if collectorServiceNoise(line) {
			continue
		}
		fields := strings.Fields(line)
		if (len(fields) != 2 && len(fields) != 3) || !strings.HasSuffix(fields[0], ".service") || !validCollectorRawField(fields[0], 256) || !validCollectorRawField(fields[1], 512) || (len(fields) == 3 && !validCollectorRawField(fields[2], 512)) {
			malformed++
			continue
		}
		name := sanitizeCollectorField(fields[0], 256)
		status := sanitizeCollectorField(fields[1], 512)
		items = append(items, SoftwareItem{Category: "service", Name: name, Source: "systemd", Status: status})
	}
	return items, malformed
}

func parseCollectorVersions(result CommandResult) ([]SoftwareItem, int) {
	lines, malformed := collectorRecordLines(result.Stdout, result.Truncated)
	items := make([]SoftwareItem, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 5 || !validCollectorRawField(fields[0], 256) || !validCollectorRawField(fields[1], 256) || !validCollectorRawField(fields[2], 512) || !validCollectorRawField(fields[3], 512) || !validCollectorRawField(fields[4], 512) || !collectorVersionIdentityAllowed(fields[0], fields[1]) {
			malformed++
			continue
		}
		item := SoftwareItem{
			Category: sanitizeCollectorField(fields[0], 256), Name: sanitizeCollectorField(fields[1], 256),
			Version: sanitizeCollectorField(fields[2], 512), Architecture: normalizeCollectorArchitecture(sanitizeCollectorField(fields[3], 512)),
			Status: sanitizeCollectorField(fields[4], 512),
		}
		items = append(items, item)
	}
	return items, malformed
}

func collectorVersionIdentityAllowed(category, name string) bool {
	switch category {
	case "runtime":
		return name == "containerd" || name == "docker" || name == "crio"
	case "kubernetes":
		return name == "kubeadm" || name == "kubelet" || name == "kubectl"
	case "aurora":
		return name == "aurora" || name == "aurora-agent" || name == "aurora-aiops"
	default:
		return false
	}
}

func collectorRecordLines(output string, truncated bool) ([]string, int) {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(output, "\n")
	malformed := 0
	if truncated && !strings.HasSuffix(output, "\n") && len(lines) > 0 {
		if lines[len(lines)-1] != "" {
			malformed = 1
		}
		lines = lines[:len(lines)-1]
	}
	return lines, malformed
}

func collectorServiceNoise(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || line == "UNIT FILE STATE PRESET" || line == "UNIT FILE STATE VENDOR PRESET" || strings.HasPrefix(line, "Legend:") {
		return true
	}
	fields := strings.Fields(line)
	if len(fields) != 4 || fields[1] != "unit" || fields[3] != "listed." || !collectorUnsignedDecimal(fields[0]) {
		return false
	}
	return (fields[0] == "1" && fields[2] == "file") || (fields[0] != "1" && fields[2] == "files")
}

func collectorUnsignedDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validCollectorRawField(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return strings.TrimSpace(value) != ""
}

func sanitizeCollectorField(value string, maxBytes int) string {
	var builder strings.Builder
	builder.Grow(min(len(value), maxBytes))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		if r == utf8.RuneError && size == 1 {
			r = ' '
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			r = ' '
		}
		if builder.Len()+utf8.RuneLen(r) > maxBytes {
			break
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}

func deduplicateCollectorSoftware(items []SoftwareItem) []SoftwareItem {
	type identity struct {
		category     string
		name         string
		architecture string
	}
	winners := make(map[identity]SoftwareItem, len(items))
	for _, item := range items {
		key := identity{category: item.Category, name: item.Name, architecture: item.Architecture}
		current, exists := winners[key]
		if !exists || collectorSoftwareGreater(item, current) {
			winners[key] = item
		}
	}
	result := make([]SoftwareItem, 0, len(winners))
	for _, item := range winners {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Architecture != b.Architecture {
			return a.Architecture < b.Architecture
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		return collectorSoftwareLexical(a) < collectorSoftwareLexical(b)
	})
	return result
}

func collectorSoftwareGreater(candidate, current SoftwareItem) bool {
	if candidate.Version != current.Version {
		return candidate.Version > current.Version
	}
	return collectorSoftwareLexical(candidate) > collectorSoftwareLexical(current)
}

func collectorSoftwareLexical(item SoftwareItem) string {
	return item.Category + "\x00" + item.Name + "\x00" + item.Architecture + "\x00" + item.Version + "\x00" + item.Source + "\x00" + item.Status
}

func newCollectorSnapshotID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}
