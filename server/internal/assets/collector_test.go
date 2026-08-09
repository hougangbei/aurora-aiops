package assets

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type collectorFakeResponse struct {
	result CommandResult
	err    error
}

type collectorFakeRemote struct {
	mu        sync.Mutex
	responses map[string]collectorFakeResponse
	calls     []collectorFakeCall
	onRun     func(int)
}

type collectorFakeCall struct {
	target  RemoteTarget
	secret  CredentialSecret
	command string
	limit   int64
}

func (f *collectorFakeRemote) ProbeHostKey(context.Context, RemoteTarget) (string, error) {
	return "", errors.New("unexpected ProbeHostKey call")
}

func (f *collectorFakeRemote) Run(ctx context.Context, target RemoteTarget, secret CredentialSecret, command string, limit int64) (CommandResult, error) {
	f.mu.Lock()
	callNumber := len(f.calls) + 1
	f.calls = append(f.calls, collectorFakeCall{target: target, secret: secret, command: command, limit: limit})
	response, ok := f.responses[command]
	onRun := f.onRun
	f.mu.Unlock()
	if onRun != nil {
		onRun(callNumber)
	}
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	if !ok {
		return CommandResult{}, errors.New("unexpected command")
	}
	return response.result, response.err
}

func (f *collectorFakeRemote) Upload(context.Context, RemoteTarget, CredentialSecret, io.Reader, int64, string, fs.FileMode) error {
	return errors.New("unexpected Upload call")
}

func collectorBase(osRelease, arch string) string {
	return "AURORA_BASE_V1\n" +
		"OS_RELEASE_BEGIN\n" + osRelease + "OS_RELEASE_END\n" +
		"KERNEL_NAME=Linux\n" +
		"KERNEL_VERSION=6.8.0-40-generic\n" +
		"ARCH=" + arch + "\n" +
		"HOSTNAME=worker-01\n" +
		"CPU_CORES=8\n" +
		"MEM_TOTAL_KB=16384\n" +
		"DISK_TOTAL_BYTES=107374182400\n" +
		"LOAD1=1.25\n" +
		"UPTIME_SECONDS=86400\n" +
		"AURORA_BASE_END\n"
}

func collectorResponses(base string) map[string]collectorFakeResponse {
	commands := collectorCommandsForTest()
	return map[string]collectorFakeResponse{
		commands[collectorCommandBase].Command:     {result: CommandResult{Stdout: base}},
		commands[collectorCommandDebian].Command:   {result: CommandResult{}},
		commands[collectorCommandRPM].Command:      {result: CommandResult{}},
		commands[collectorCommandAPK].Command:      {result: CommandResult{}},
		commands[collectorCommandServices].Command: {result: CommandResult{}},
		commands[collectorCommandVersions].Command: {result: CommandResult{}},
	}
}

func TestCollectorCollectsUbuntuSystemAndDebianPackages(t *testing.T) {
	commands := collectorCommandsForTest()
	responses := collectorResponses(collectorBase("NAME=Ubuntu\nID=ubuntu\nVERSION_ID=\"24.04\"\n", "x86_64"))
	responses[commands[collectorCommandDebian].Command] = collectorFakeResponse{result: CommandResult{Stdout: "curl\t8.5.0-2ubuntu10.4\tamd64\tinstalled\nlegacy\t1.0\tamd64\tconfig-files\nabsent\t1.0\tamd64\tnot-installed\nlibssl3:amd64\t3.0.13-0ubuntu3.4\tamd64\tinstalled\nlibssl3:arm64\t3.0.13-0ubuntu3.4\tarm64\tinstalled\n"}}
	remote := &collectorFakeRemote{responses: responses}
	now := time.Date(2026, time.August, 9, 4, 5, 6, 0, time.FixedZone("fixture", 8*60*60))
	server := Server{ID: "server-1", Address: "192.0.2.10", SSHPort: 2222, Username: "ops", HostKeyFingerprint: "SHA256:fixed"}
	secret := CredentialSecret{Password: "secret"}

	snapshot, software, err := NewCollector(remote, func() time.Time { return now }).Collect(context.Background(), server, secret)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if snapshot.ID == "" || snapshot.ServerID != server.ID {
		t.Fatalf("snapshot identity = %#v", snapshot)
	}
	wantSnapshot := Snapshot{
		ID: snapshot.ID, ServerID: "server-1", OSFamily: "debian", OSVersion: "24.04",
		KernelVersion: "6.8.0-40-generic", Architecture: "amd64", Hostname: "worker-01",
		CPUCores: 8, MemoryBytes: 16384 * 1024, DiskBytes: 107374182400,
		Load1: 1.25, UptimeSeconds: 86400, CollectedAt: now.UTC(),
	}
	if !reflect.DeepEqual(snapshot, wantSnapshot) {
		t.Fatalf("snapshot = %#v, want %#v", snapshot, wantSnapshot)
	}
	wantSoftware := []SoftwareItem{
		{Category: "package", Name: "curl", Version: "8.5.0-2ubuntu10.4", Architecture: "amd64", Source: "dpkg"},
		{Category: "package", Name: "libssl3", Version: "3.0.13-0ubuntu3.4", Architecture: "amd64", Source: "dpkg"},
		{Category: "package", Name: "libssl3", Version: "3.0.13-0ubuntu3.4", Architecture: "arm64", Source: "dpkg"},
	}
	if !reflect.DeepEqual(software, wantSoftware) {
		t.Fatalf("software = %#v, want %#v", software, wantSoftware)
	}
	if got := remote.calls[0].target; got != (RemoteTarget{Address: server.Address, Port: server.SSHPort, Username: server.Username, ExpectedFingerprint: server.HostKeyFingerprint}) {
		t.Fatalf("target = %#v", got)
	}
	if remote.calls[0].secret != secret {
		t.Fatal("credential was not passed separately")
	}
	if len(remote.calls) != 4 {
		t.Fatalf("calls = %d, want base + Debian + services + versions", len(remote.calls))
	}

	other, _, err := NewCollector(remote, func() time.Time { return now }).Collect(context.Background(), server, secret)
	if err != nil || other.ID == "" || other.ID == snapshot.ID {
		t.Fatalf("second snapshot ID = %q, first = %q, err = %v", other.ID, snapshot.ID, err)
	}
}

func TestCollectorParsesRHELAndNormalizesARM64(t *testing.T) {
	commands := collectorCommandsForTest()
	for _, fixture := range []struct{ id, want string }{{"rhel", "rhel"}, {"rocky", "rhel"}, {"centos", "rhel"}} {
		t.Run(fixture.id, func(t *testing.T) {
			responses := collectorResponses(collectorBase("ID="+fixture.id+"\nVERSION_ID='9.4'\n", "aarch64"))
			responses[commands[collectorCommandRPM].Command] = collectorFakeResponse{result: CommandResult{Stdout: "bash\t0:5.1.8-9.el9\taarch64\nbash\t0:5.1.8-9.el9\taarch64\n"}}
			remote := &collectorFakeRemote{responses: responses}
			snapshot, software, err := NewCollector(remote, time.Now).Collect(context.Background(), Server{ID: "s"}, CredentialSecret{})
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.OSFamily != fixture.want || snapshot.OSVersion != "9.4" || snapshot.Architecture != "arm64" {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			want := []SoftwareItem{{Category: "package", Name: "bash", Version: "0:5.1.8-9.el9", Architecture: "arm64", Source: "rpm"}}
			if !reflect.DeepEqual(software, want) {
				t.Fatalf("software = %#v, want %#v", software, want)
			}
		})
	}
}

func TestCollectorNormalizesDebianFamilyReleases(t *testing.T) {
	for _, fixture := range []struct {
		id, version string
	}{
		{id: "ubuntu", version: "22.04"},
		{id: "ubuntu", version: "24.04"},
		{id: "debian", version: "12"},
	} {
		t.Run(fixture.id+fixture.version, func(t *testing.T) {
			snapshot, err := parseCollectorBase(collectorBase("ID="+fixture.id+"\nVERSION_ID=\""+fixture.version+"\"\n", "x86_64"))
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.OSFamily != "debian" || snapshot.OSVersion != fixture.version {
				t.Fatalf("snapshot = %#v", snapshot)
			}
		})
	}
}

func TestCollectorDebianPackagesRequireInstalledState(t *testing.T) {
	output := "installed\t2.0\tamd64\tinstalled\n" +
		"rc-package\t1.0\tamd64\tconfig-files\n" +
		"rc-empty\t\t\tconfig-files\n" +
		"absent\t1.0\tamd64\tnot-installed\n" +
		"absent-empty\t\t\tnot-installed\n" +
		"transition1\t1.0\tamd64\thalf-installed\n" +
		"transition2\t1.0\tamd64\tunpacked\n" +
		"transition3\t1.0\tamd64\thalf-configured\n" +
		"transition4\t1.0\tamd64\ttriggers-awaited\n" +
		"transition5\t1.0\tamd64\ttriggers-pending\n" +
		"unknown\t1.0\tamd64\tmystery-state\n" +
		"missing\t1.0\tamd64\n"
	items, malformed := parseCollectorPackages(collectorCommandDebian, CommandResult{Stdout: output}, "amd64")
	want := []SoftwareItem{{Category: "package", Name: "installed", Version: "2.0", Architecture: "amd64", Source: "dpkg"}}
	if !reflect.DeepEqual(items, want) || malformed != 2 {
		t.Fatalf("items=%#v malformed=%d, want %#v malformed=2", items, malformed, want)
	}
	command := collectorCommandsForTest()[collectorCommandDebian].Command
	if !strings.Contains(command, `${db:Status-Status}`) {
		t.Fatalf("dpkg command lacks machine-readable installed state: %q", command)
	}
}

func TestCollectorRPMUsesAndValidatesFullEVR(t *testing.T) {
	command := collectorCommandsForTest()[collectorCommandRPM].Command
	if !strings.Contains(command, `%{EPOCHNUM}:%{VERSION}-%{RELEASE}`) {
		t.Fatalf("rpm command does not emit normalized EVR: %q", command)
	}
	output := "base\t0:6.10-1.el9\tx86_64\n" +
		"epoch\t2:1.0-4.el9\tx86_64\n" +
		"missing-epoch\t6.10-1.el9\tx86_64\n" +
		"bad-epoch\tnone:1.0-1.el9\tx86_64\n"
	items, malformed := parseCollectorPackages(collectorCommandRPM, CommandResult{Stdout: output}, "amd64")
	want := []SoftwareItem{
		{Category: "package", Name: "base", Version: "0:6.10-1.el9", Architecture: "amd64", Source: "rpm"},
		{Category: "package", Name: "epoch", Version: "2:1.0-4.el9", Architecture: "amd64", Source: "rpm"},
	}
	if !reflect.DeepEqual(items, want) || malformed != 2 {
		t.Fatalf("items=%#v malformed=%d, want %#v malformed=2", items, malformed, want)
	}
}

func TestCollectorOptionalTransportErrorWithoutExitStatusIsGeneric(t *testing.T) {
	command := collectorCommandsForTest()[collectorCommandServices]
	remote := &collectorFakeRemote{responses: map[string]collectorFakeResponse{
		command.Command: {result: CommandResult{ExitCode: 0, Stderr: "secret output"}, err: errors.New("network secret")},
	}}
	collector := NewCollector(remote, time.Now)
	_, warning, err := collector.runOptional(context.Background(), RemoteTarget{Address: "secret target"}, CredentialSecret{Password: "secret password"}, collectorCommandServices)
	if err != nil {
		t.Fatal(err)
	}
	if warning == nil || warning.Status != "command failed" {
		t.Fatalf("warning = %#v, want generic command failed", warning)
	}
}

func TestCollectorSplitsAPKRecordsFromRight(t *testing.T) {
	tests := []struct {
		line, wantName, wantVersion string
	}{
		{line: "foo-2-utils-1.0-r0", wantName: "foo-2-utils", wantVersion: "1.0-r0"},
		{line: "pkg-1.0-r10", wantName: "pkg", wantVersion: "1.0-r10"},
		{line: "pkg-2:1.0-r0", wantName: "pkg", wantVersion: "2:1.0-r0"},
		{line: "pkg-1.0-rx"},
		{line: "pkg-1.0 bad-r0"},
		{line: "pkg-r0"},
		{line: "-1.0-r0"},
		{line: "pkg-1.0-r0-extra"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			name, version := splitCollectorAPK(tt.line)
			if name != tt.wantName || version != tt.wantVersion {
				t.Fatalf("splitCollectorAPK(%q) = (%q, %q), want (%q, %q)", tt.line, name, version, tt.wantName, tt.wantVersion)
			}
		})
	}
}

func TestCollectorBaseCommandUsesPortableDFAndTruncatesUptime(t *testing.T) {
	command := collectorCommandsForTest()[collectorCommandBase].Command
	if !strings.Contains(command, "df -PB1 /") || strings.Contains(command, "df -B1 /") {
		t.Fatalf("base command does not force POSIX one-line df output: %q", command)
	}
	if !strings.Contains(command, `\$2 ~ /^[0-9]+\$/`) {
		t.Fatalf("base command does not validate the numeric total-byte field: %q", command)
	}
	// Without df -P, long device names historically wrapped the numeric fields
	// onto a following line; the fixed protocol must not depend on that layout.
	wrappedDevice := "Filesystem 1-blocks Used Available Capacity Mounted on\n/dev/mapper/very-long-device-name\n 107374182400 1 2 1% /\n"
	if fields := strings.Fields(strings.Split(wrappedDevice, "\n")[1]); len(fields) != 1 {
		t.Fatalf("historical fixture did not wrap as intended: %q", wrappedDevice)
	}
	base := strings.Replace(collectorBase("ID=ubuntu\nVERSION_ID=24.04\n", "x86_64"), "UPTIME_SECONDS=86400", "UPTIME_SECONDS=123.9", 1)
	snapshot, err := parseCollectorBase(base)
	if err != nil || snapshot.UptimeSeconds != 123 {
		t.Fatalf("snapshot=%#v err=%v, want uptime 123", snapshot, err)
	}
}

func TestCollectorParsesAlpinePackagesServicesAndVersions(t *testing.T) {
	commands := collectorCommandsForTest()
	responses := collectorResponses(collectorBase("ID=alpine\nVERSION_ID=3.20.2\n", "x86_64"))
	responses[commands[collectorCommandAPK].Command] = collectorFakeResponse{result: CommandResult{Stdout: "musl-utils-1.2.5-r0\nfoo-bar-2.3.4-r1\n"}}
	responses[commands[collectorCommandServices].Command] = collectorFakeResponse{result: CommandResult{Stdout: "UNIT FILE STATE PRESET\nsshd.service enabled enabled\naurora-aiops.service disabled disabled\n\nLegend: generated output noise\n2 unit files listed.\n"}}
	responses[commands[collectorCommandVersions].Command] = collectorFakeResponse{result: CommandResult{Stdout: "runtime\tcontainerd\t1.7.20\tamd64\trunning\nruntime\tdocker\t27.1.1\tamd64\trunning\nruntime\tcrio\t1.30.3\tamd64\tstopped\nkubernetes\tkubeadm\tv1.30.3\tamd64\tinstalled\nkubernetes\tkubelet\tv1.30.3\tamd64\trunning\nkubernetes\tkubectl\tv1.30.3\tamd64\tinstalled\naurora\taurora-aiops\t2.4.0\tamd64\tactive\nruntime\tdocker\t28.0\t\trunning\nruntime\tcrio\t2.0\tamd64\t\nruntime\tdocker\t27.1.1\tamd64\trunning\n"}}
	remote := &collectorFakeRemote{responses: responses}

	snapshot, got, err := NewCollector(remote, time.Now).Collect(context.Background(), Server{ID: "s"}, CredentialSecret{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.OSFamily != "alpine" || snapshot.OSVersion != "3.20.2" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	want := []SoftwareItem{
		{Category: "aurora", Name: "aurora-aiops", Version: "2.4.0", Architecture: "amd64", Status: "active"},
		{Category: "collector_warning", Name: collectorCommandVersions + collectorMalformedWarningSuffix, Status: "2 malformed inventory records ignored"},
		{Category: "kubernetes", Name: "kubeadm", Version: "v1.30.3", Architecture: "amd64", Status: "installed"},
		{Category: "kubernetes", Name: "kubectl", Version: "v1.30.3", Architecture: "amd64", Status: "installed"},
		{Category: "kubernetes", Name: "kubelet", Version: "v1.30.3", Architecture: "amd64", Status: "running"},
		{Category: "package", Name: "foo-bar", Version: "2.3.4-r1", Architecture: "amd64", Source: "apk"},
		{Category: "package", Name: "musl-utils", Version: "1.2.5-r0", Architecture: "amd64", Source: "apk"},
		{Category: "runtime", Name: "containerd", Version: "1.7.20", Architecture: "amd64", Status: "running"},
		{Category: "runtime", Name: "crio", Version: "1.30.3", Architecture: "amd64", Status: "stopped"},
		{Category: "runtime", Name: "docker", Version: "27.1.1", Architecture: "amd64", Status: "running"},
		{Category: "service", Name: "aurora-aiops.service", Status: "disabled", Source: "systemd"},
		{Category: "service", Name: "sshd.service", Status: "enabled", Source: "systemd"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("software = %#v, want %#v", got, want)
	}
}

func TestCollectorParsersReportMalformedRecordsAndKeepValidRecords(t *testing.T) {
	longName := strings.Repeat("x", 257)
	tests := []struct {
		name      string
		parse     func(CommandResult) ([]SoftwareItem, int)
		output    string
		want      SoftwareItem
		malformed int
	}{
		{
			name: "dpkg",
			parse: func(result CommandResult) ([]SoftwareItem, int) {
				return parseCollectorPackages(collectorCommandDebian, result, "amd64")
			},
			output:    "curl\t8.5.0\tamd64\tinstalled\ntoo\tfew\n\t1.0\tamd64\tinstalled\nbad\x00name\t1.0\tamd64\tinstalled\n" + longName + "\t1.0\tamd64\tinstalled\n:amd64\t1.0\tamd64\tinstalled\nbad:extra:amd64\t1.0\tamd64\tinstalled\nincomplete\t1.0",
			want:      SoftwareItem{Category: "package", Name: "curl", Version: "8.5.0", Architecture: "amd64", Source: "dpkg"},
			malformed: 7,
		},
		{
			name: "rpm",
			parse: func(result CommandResult) ([]SoftwareItem, int) {
				return parseCollectorPackages(collectorCommandRPM, result, "amd64")
			},
			output:    "bash\t0:5.1.8-9.el9\tx86_64\ntoo\tfew\n\t0:1.0-1\tx86_64\nbad\x00name\t0:1.0-1\tx86_64\n" + longName + "\t0:1.0-1\tx86_64\nincomplete\t0:1.0-1",
			want:      SoftwareItem{Category: "package", Name: "bash", Version: "0:5.1.8-9.el9", Architecture: "amd64", Source: "rpm"},
			malformed: 5,
		},
		{
			name: "apk",
			parse: func(result CommandResult) ([]SoftwareItem, int) {
				return parseCollectorPackages(collectorCommandAPK, result, "amd64")
			},
			output:    "foo-bar-2.3.4-r1\nbad\t1.0-r0\n-1.0-r0\nbad\x00name-1.0-r0\n" + longName + "-1.0-r0\nincomplete",
			want:      SoftwareItem{Category: "package", Name: "foo-bar", Version: "2.3.4-r1", Architecture: "amd64", Source: "apk"},
			malformed: 5,
		},
		{
			name:      "systemd",
			parse:     parseCollectorServices,
			output:    "UNIT FILE STATE PRESET\nsshd.service enabled enabled\ntoo-many.service enabled enabled extra\n enabled\nbad\x00.service enabled\n" + longName + ".service enabled\nUNIT FILE garbage\nnot-a-count unit files listed.\nincomplete.service",
			want:      SoftwareItem{Category: "service", Name: "sshd.service", Source: "systemd", Status: "enabled"},
			malformed: 7,
		},
		{
			name:      "versions",
			parse:     parseCollectorVersions,
			output:    "runtime\tdocker\t27.1.1\tamd64\trunning\nruntime\tdocker\ttoo\tfew\nconsecutive\t\t1.0\tamd64\trunning\nruntime\tdock\x00er\t1.0\tamd64\trunning\nruntime\tdocker\t" + strings.Repeat("v", 513) + "\tamd64\trunning\nruntime\tdocker\t28.0\tamd64",
			want:      SoftwareItem{Category: "runtime", Name: "docker", Version: "27.1.1", Architecture: "amd64", Status: "running"},
			malformed: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, malformed := tt.parse(CommandResult{Stdout: tt.output})
			if malformed != tt.malformed {
				t.Fatalf("malformed = %d, want %d", malformed, tt.malformed)
			}
			if !reflect.DeepEqual(items, []SoftwareItem{tt.want}) {
				t.Fatalf("items = %#v, want %#v", items, []SoftwareItem{tt.want})
			}
		})
	}
}

func TestCollectorAddsOneMalformedWarningPerOptionalCommand(t *testing.T) {
	commands := collectorCommandsForTest()
	tests := []struct {
		name, osRelease, arch, commandID, output string
	}{
		{name: "dpkg", osRelease: "ID=ubuntu\nVERSION_ID=24.04\n", arch: "x86_64", commandID: collectorCommandDebian, output: "curl\t8.5.0\tamd64\tinstalled\nbroken\n"},
		{name: "rpm", osRelease: "ID=rocky\nVERSION_ID=9.4\n", arch: "aarch64", commandID: collectorCommandRPM, output: "bash\t0:5.1-1\taarch64\nbroken\n"},
		{name: "apk", osRelease: "ID=alpine\nVERSION_ID=3.20\n", arch: "x86_64", commandID: collectorCommandAPK, output: "musl-1.2-r0\nbroken\n"},
		{name: "services", osRelease: "ID=ubuntu\nVERSION_ID=24.04\n", arch: "x86_64", commandID: collectorCommandServices, output: "sshd.service enabled\nbroken.service\n"},
		{name: "versions", osRelease: "ID=ubuntu\nVERSION_ID=24.04\n", arch: "x86_64", commandID: collectorCommandVersions, output: "runtime\tdocker\t27.1\tamd64\trunning\nbroken\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			responses := collectorResponses(collectorBase(tt.osRelease, tt.arch))
			responses[commands[tt.commandID].Command] = collectorFakeResponse{result: CommandResult{Stdout: tt.output}}
			remote := &collectorFakeRemote{responses: responses}
			_, software, err := NewCollector(remote, time.Now).Collect(context.Background(), Server{ID: "s", Address: "do-not-leak"}, CredentialSecret{Password: "do-not-leak"})
			if err != nil {
				t.Fatal(err)
			}
			warningName := tt.commandID + collectorMalformedWarningSuffix
			warningCount, totalWarnings, validCount := 0, 0, 0
			for _, item := range software {
				switch {
				case item.Category == "collector_warning":
					totalWarnings++
					if item.Name == warningName {
						warningCount++
						if item.Status != "1 malformed inventory record ignored" || len(item.Status) > 512 || strings.Contains(item.Status, "do-not-leak") {
							t.Fatalf("warning = %#v", item)
						}
					}
				case item.Category != "collector_warning":
					validCount++
				}
			}
			if warningCount != 1 || totalWarnings != 1 || validCount != 1 {
				t.Fatalf("software = %#v, warningCount=%d totalWarnings=%d validCount=%d", software, warningCount, totalWarnings, validCount)
			}
		})
	}
}

func TestCollectorRejectsRequiredProbeFailuresAndInvalidBase(t *testing.T) {
	commands := collectorCommandsForTest()
	tests := []struct {
		name     string
		response collectorFakeResponse
	}{
		{name: "remote error", response: collectorFakeResponse{err: errors.New("network secret details")}},
		{name: "nonzero exit", response: collectorFakeResponse{result: CommandResult{ExitCode: 2, Stderr: "bad"}}},
		{name: "truncated", response: collectorFakeResponse{result: CommandResult{Stdout: collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64"), Truncated: true}}},
	}
	for _, field := range []string{"CPU_CORES=8", "MEM_TOTAL_KB=16384", "DISK_TOTAL_BYTES=107374182400", "LOAD1=1.25", "UPTIME_SECONDS=86400"} {
		tests = append(tests, struct {
			name     string
			response collectorFakeResponse
		}{name: "malformed " + field, response: collectorFakeResponse{result: CommandResult{Stdout: strings.Replace(collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64"), field, strings.Split(field, "=")[0]+"=invalid", 1)}}})
	}
	for _, field := range []string{"ID=ubuntu\n", "ARCH=x86_64\n", "HOSTNAME=worker-01\n", "AURORA_BASE_END\n"} {
		tests = append(tests, struct {
			name     string
			response collectorFakeResponse
		}{name: "missing " + field, response: collectorFakeResponse{result: CommandResult{Stdout: strings.Replace(collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64"), field, "", 1)}}})
	}
	validBase := collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64")
	for name, output := range map[string]string{
		"duplicate OS ID":  strings.Replace(validBase, "ID=ubuntu\n", "ID=ubuntu\nID=debian\n", 1),
		"unknown base key": strings.Replace(validBase, "CPU_CORES=8\n", "EXTRA=value\nCPU_CORES=8\n", 1),
		"trailing output":  validBase + "unexpected\n",
	} {
		tests = append(tests, struct {
			name     string
			response collectorFakeResponse
		}{name: name, response: collectorFakeResponse{result: CommandResult{Stdout: output}}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remote := &collectorFakeRemote{responses: map[string]collectorFakeResponse{commands[collectorCommandBase].Command: tt.response}}
			snapshot, software, err := NewCollector(remote, time.Now).Collect(context.Background(), Server{ID: "s"}, CredentialSecret{})
			if err == nil {
				t.Fatal("Collect() error = nil")
			}
			if snapshot != (Snapshot{}) || software != nil {
				t.Fatalf("partial result snapshot=%#v software=%#v", snapshot, software)
			}
			if !errors.Is(err, ErrInvalidInput) && !strings.Contains(err.Error(), "base probe") {
				t.Fatalf("error = %v, want ErrInvalidInput or safe base probe error", err)
			}
			if strings.Contains(err.Error(), "secret details") {
				t.Fatalf("error leaks remote reason: %v", err)
			}
		})
	}
}

func TestCollectorOptionalFailuresAndTruncationBecomeSafeWarnings(t *testing.T) {
	commands := collectorCommandsForTest()
	responses := collectorResponses(collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64"))
	responses[commands[collectorCommandDebian].Command] = collectorFakeResponse{result: CommandResult{ExitCode: 127, Stderr: "password=hunter2"}, err: errors.New("exit status 127: password=hunter2")}
	responses[commands[collectorCommandServices].Command] = collectorFakeResponse{result: CommandResult{ExitCode: 127, Stderr: strings.Repeat("x", 900)}}
	responses[commands[collectorCommandVersions].Command] = collectorFakeResponse{result: CommandResult{Stdout: "runtime\tdocker\t27.1\tamd64\trunning\nruntime\ttruncated", Truncated: true}}
	remote := &collectorFakeRemote{responses: responses}

	_, software, err := NewCollector(remote, time.Now).Collect(context.Background(), Server{ID: "server-secret", Address: "target-secret"}, CredentialSecret{Password: "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	warnings := map[string]SoftwareItem{}
	for _, item := range software {
		if item.Category == "collector_warning" {
			warnings[item.Name] = item
		}
		if item.Name == "truncated" {
			t.Fatal("incomplete trailing record was retained")
		}
	}
	for _, id := range []string{collectorCommandDebian, collectorCommandServices} {
		warning, ok := warnings[id]
		if !ok {
			t.Fatalf("missing warning for %s in %#v", id, software)
		}
		if len(warning.Status) > 512 || strings.ContainsAny(warning.Status, "\n\r\t") || strings.Contains(warning.Status, "hunter2") || strings.Contains(warning.Status, "target-secret") || strings.Contains(warning.Status, "server-secret") {
			t.Fatalf("unsafe warning = %#v", warning)
		}
	}
	if got := warnings[collectorCommandDebian].Status; got != "command exited with status 127" {
		t.Fatalf("exit-error warning status = %q", got)
	}
	truncatedWarning, ok := warnings[collectorCommandVersions+collectorMalformedWarningSuffix]
	if !ok {
		t.Fatalf("missing combined truncation/malformed warning in %#v", software)
	}
	if truncatedWarning.Status != "output truncated; 1 malformed inventory record ignored" {
		t.Fatalf("combined warning = %#v", truncatedWarning)
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %#v, want exactly three", warnings)
	}
	foundDocker := false
	for _, item := range software {
		if item.Category == "runtime" && item.Name == "docker" && item.Version == "27.1" {
			foundDocker = true
		}
	}
	if !foundDocker {
		t.Fatalf("valid complete record was lost: %#v", software)
	}
}

func TestCollectorUsesOnlyFixedCommandsAndLimits(t *testing.T) {
	commands := collectorCommandsForTest()
	responses := collectorResponses(collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64"))
	remote := &collectorFakeRemote{responses: responses}
	server := Server{ID: "$(id)", Address: "host; touch /tmp/pwn", SSHPort: 22, Username: "u`id`", HostKeyFingerprint: "fp$(whoami)"}
	secret := CredentialSecret{Password: "pw; echo leak", PrivateKey: "$(cat key)", Passphrase: "`pwd`"}

	_, _, err := NewCollector(remote, time.Now).Collect(context.Background(), server, secret)
	if err != nil {
		t.Fatal(err)
	}
	allowed := make(map[string]int64, len(commands))
	wantLimits := map[string]int64{
		collectorCommandBase:     1 << 20,
		collectorCommandDebian:   8 << 20,
		collectorCommandRPM:      8 << 20,
		collectorCommandAPK:      8 << 20,
		collectorCommandServices: 4 << 20,
		collectorCommandVersions: 1 << 20,
	}
	for id, command := range commands {
		if !strings.HasPrefix(command.Command, "LC_ALL=C") {
			t.Errorf("command does not set LC_ALL=C: %q", command.Command)
		}
		if command.Limit != wantLimits[id] {
			t.Errorf("command %s limit = %d, want %d", id, command.Limit, wantLimits[id])
		}
		allowed[command.Command] = command.Limit
	}
	for _, call := range remote.calls {
		wantLimit, ok := allowed[call.command]
		if !ok || call.limit != wantLimit {
			t.Fatalf("unexpected command/limit: %q %d", call.command, call.limit)
		}
		for _, unsafe := range []string{server.ID, server.Address, server.Username, server.HostKeyFingerprint, secret.Password, secret.PrivateKey, secret.Passphrase} {
			if strings.Contains(call.command, unsafe) {
				t.Fatalf("command contains runtime value %q: %q", unsafe, call.command)
			}
		}
	}
}

func TestCollectorCancellationStopsOptionalSequence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	responses := collectorResponses(collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64"))
	remote := &collectorFakeRemote{responses: responses, onRun: func(call int) {
		if call == 2 {
			cancel()
		}
	}}

	snapshot, software, err := NewCollector(remote, time.Now).Collect(ctx, Server{ID: "s"}, CredentialSecret{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if snapshot != (Snapshot{}) || software != nil {
		t.Fatalf("partial result snapshot=%#v software=%#v", snapshot, software)
	}
	if len(remote.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(remote.calls))
	}
}

func TestCollectorHandlesNilDependenciesAndUnknownOS(t *testing.T) {
	if _, _, err := NewCollector(nil, nil).Collect(context.Background(), Server{}, CredentialSecret{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("nil remote error = %v, want ErrInvalidInput", err)
	}
	responses := collectorResponses(collectorBase("ID=FreeBSD\nVERSION_ID=14.1\n", "RISCV64"))
	remote := &collectorFakeRemote{responses: responses}
	snapshot, software, err := NewCollector(remote, nil).Collect(context.Background(), Server{ID: "s"}, CredentialSecret{})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.OSFamily != "freebsd" || snapshot.Architecture != "riscv64" || snapshot.CollectedAt.IsZero() || software == nil {
		t.Fatalf("snapshot=%#v software=%#v", snapshot, software)
	}
	if len(remote.calls) != 3 {
		t.Fatalf("unknown OS calls = %d, want base + services + versions", len(remote.calls))
	}

	remote = &collectorFakeRemote{responses: collectorResponses(collectorBase("ID=ubuntu\nVERSION_ID=22.04\n", "x86_64"))}
	if snapshot, _, err = (&Collector{remote: remote}).Collect(context.Background(), Server{ID: "s"}, CredentialSecret{}); err != nil || snapshot.CollectedAt.IsZero() {
		t.Fatalf("literal collector snapshot=%#v error=%v", snapshot, err)
	}
}

func TestCollectorSanitizesAndBoundsFields(t *testing.T) {
	got := sanitizeCollectorField("safe\u202ename"+strings.Repeat("界", 200), 256)
	if strings.ContainsRune(got, '\u202e') {
		t.Fatalf("format control retained in %q", got)
	}
	if len(got) > 256 {
		t.Fatalf("sanitized field is %d bytes, want at most 256", len(got))
	}
}

func TestCollectorDeduplicatesByPersistenceIdentityDeterministically(t *testing.T) {
	input := []SoftwareItem{
		{Category: "runtime", Name: "docker", Version: "27.1.1", Architecture: "amd64", Source: "binary", Status: "active"},
		{Category: "package", Name: "kernel", Version: "6.8.1", Architecture: "amd64", Source: "rpm", Status: "installed"},
		{Category: "runtime", Name: "docker", Version: "27.1.1", Architecture: "amd64", Source: "binary", Status: "running"},
		{Category: "package", Name: "kernel", Version: "6.8.2", Architecture: "amd64", Source: "rpm", Status: "installed"},
		{Category: "collector_warning", Name: collectorCommandServices, Status: "command failed"},
		{Category: "collector_warning", Name: collectorCommandVersions, Status: "output truncated"},
	}
	want := []SoftwareItem{
		{Category: "collector_warning", Name: collectorCommandServices, Status: "command failed"},
		{Category: "collector_warning", Name: collectorCommandVersions, Status: "output truncated"},
		{Category: "package", Name: "kernel", Version: "6.8.2", Architecture: "amd64", Source: "rpm", Status: "installed"},
		{Category: "runtime", Name: "docker", Version: "27.1.1", Architecture: "amd64", Source: "binary", Status: "running"},
	}
	for i := 0; i < 5; i++ {
		got := deduplicateCollectorSoftware(append([]SoftwareItem(nil), input...))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("deduplicated software = %#v, want %#v", got, want)
		}
		input[0], input[len(input)-1] = input[len(input)-1], input[0]
	}
}

func TestCollectorPackageVersionComparisonAndWinnerSelection(t *testing.T) {
	tests := []struct {
		name, lower, higher string
	}{
		{name: "numeric run", lower: "6.9", higher: "6.10"},
		{name: "epoch", lower: "1:9.99", higher: "2:1.0"},
		{name: "apk release", lower: "1.0-r9", higher: "1.0-r10"},
		{name: "rpm release", lower: "1.0-9.el9", higher: "1.0-10.el9"},
		{name: "tilde prerelease", lower: "1.0~rc1", higher: "1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareCollectorPackageVersions(tt.lower, tt.higher); got >= 0 {
				t.Fatalf("compare(%q, %q) = %d, want < 0", tt.lower, tt.higher, got)
			}
			if got := compareCollectorPackageVersions(tt.higher, tt.lower); got <= 0 {
				t.Fatalf("compare(%q, %q) = %d, want > 0", tt.higher, tt.lower, got)
			}
			items := []SoftwareItem{
				{Category: "package", Name: "same", Version: tt.higher, Architecture: "amd64", Source: "fixture"},
				{Category: "package", Name: "same", Version: tt.lower, Architecture: "amd64", Source: "fixture"},
			}
			got := deduplicateCollectorSoftware(items)
			if len(got) != 1 || got[0].Version != tt.higher {
				t.Fatalf("deduplicated items = %#v, want version %q", got, tt.higher)
			}
		})
	}
	if got := compareCollectorPackageVersions("01.002", "1.2"); got != 0 {
		t.Fatalf("numeric semantic equality = %d, want 0", got)
	}
}

func TestCollectorRPMEVRComparisonAndSourceDispatch(t *testing.T) {
	tests := []struct {
		name, lower, higher string
	}{
		{name: "numeric version", lower: "0:6.9-9", higher: "0:6.10-1"},
		{name: "epoch", lower: "0:99-9", higher: "1:1.0-1"},
		{name: "separator then numeric", lower: "0:1.0+git2-1", higher: "0:1.0.git10-1"},
		{name: "tilde prerelease", lower: "0:1.0~rc1-1", higher: "0:1.0-1"},
		{name: "release digits", lower: "0:1.0-r9", higher: "0:1.0-r10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareCollectorRPMEVR(tt.lower, tt.higher); got >= 0 {
				t.Fatalf("compareCollectorRPMEVR(%q, %q) = %d, want < 0", tt.lower, tt.higher, got)
			}
			if got := compareCollectorRPMEVR(tt.higher, tt.lower); got <= 0 {
				t.Fatalf("compareCollectorRPMEVR(%q, %q) = %d, want > 0", tt.higher, tt.lower, got)
			}
			items := []SoftwareItem{
				{Category: "package", Name: "same", Version: tt.lower, Architecture: "amd64", Source: "rpm"},
				{Category: "package", Name: "same", Version: tt.higher, Architecture: "amd64", Source: "rpm"},
			}
			got := deduplicateCollectorSoftware(items)
			if len(got) != 1 || got[0].Version != tt.higher {
				t.Fatalf("deduplicated items = %#v, want version %q", got, tt.higher)
			}
		})
	}
	for _, versions := range [][2]string{
		{"0:1.01-01", "0:1.1-1"},
		{"0:1.0+git2-1", "0:1.0_git2-1"},
	} {
		if got := compareCollectorRPMEVR(versions[0], versions[1]); got != 0 {
			t.Fatalf("RPM semantic equality compare(%q, %q) = %d", versions[0], versions[1], got)
		}
	}

	left := SoftwareItem{Version: "0:1.0+git2-1", Source: "rpm"}
	right := SoftwareItem{Version: "0:1.0_git2-1", Source: "dpkg"}
	if got, want := compareCollectorSoftwareVersions(left, right), compareCollectorPackageVersions(left.Version, right.Version); got != want || got == 0 {
		t.Fatalf("mixed-source comparison = %d, want natural comparison %d", got, want)
	}

	equalItems := []SoftwareItem{
		{Category: "package", Name: "same", Version: "0:1.01-01", Architecture: "amd64", Source: "rpm"},
		{Category: "package", Name: "same", Version: "0:1.1-1", Architecture: "amd64", Source: "rpm"},
	}
	for i := 0; i < 2; i++ {
		got := deduplicateCollectorSoftware(equalItems)
		if len(got) != 1 || got[0].Version != "0:1.1-1" {
			t.Fatalf("semantic-equality tie-break = %#v", got)
		}
		equalItems[0], equalItems[1] = equalItems[1], equalItems[0]
	}
}

func TestCollectorRejectsNumericBoundaries(t *testing.T) {
	base := collectorBase("ID=ubuntu\nVERSION_ID=24.04\n", "x86_64")
	tests := map[string]string{
		"zero CPU":                   "CPU_CORES=0",
		"negative CPU":               "CPU_CORES=-1",
		"CPU overflow":               "CPU_CORES=9223372036854775808",
		"zero memory":                "MEM_TOTAL_KB=0",
		"negative memory":            "MEM_TOTAL_KB=-1",
		"memory integer overflow":    "MEM_TOTAL_KB=9223372036854775808",
		"memory multiply overflow":   "MEM_TOTAL_KB=9007199254740992",
		"zero disk":                  "DISK_TOTAL_BYTES=0",
		"negative disk":              "DISK_TOTAL_BYTES=-1",
		"disk overflow":              "DISK_TOTAL_BYTES=9223372036854775808",
		"NaN load":                   "LOAD1=NaN",
		"positive infinite load":     "LOAD1=+Inf",
		"negative infinite load":     "LOAD1=-Inf",
		"negative uptime":            "UPTIME_SECONDS=-0.1",
		"uptime conversion overflow": "UPTIME_SECONDS=9223372036854775808",
	}
	for name, replacement := range tests {
		t.Run(name, func(t *testing.T) {
			key := strings.SplitN(replacement, "=", 2)[0]
			var original string
			switch key {
			case "CPU_CORES":
				original = "CPU_CORES=8"
			case "MEM_TOTAL_KB":
				original = "MEM_TOTAL_KB=16384"
			case "DISK_TOTAL_BYTES":
				original = "DISK_TOTAL_BYTES=107374182400"
			case "LOAD1":
				original = "LOAD1=1.25"
			case "UPTIME_SECONDS":
				original = "UPTIME_SECONDS=86400"
			}
			_, err := parseCollectorBase(strings.Replace(base, original, replacement, 1))
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
