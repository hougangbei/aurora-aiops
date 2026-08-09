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
	responses[commands[collectorCommandDebian].Command] = collectorFakeResponse{result: CommandResult{Stdout: "curl\t8.5.0-2ubuntu10.4\tamd64\nlibssl3:amd64\t3.0.13-0ubuntu3.4\tamd64\nlibssl3:arm64\t3.0.13-0ubuntu3.4\tarm64\n"}}
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
			responses[commands[collectorCommandRPM].Command] = collectorFakeResponse{result: CommandResult{Stdout: "bash\t5.1.8-9.el9\taarch64\nbash\t5.1.8-9.el9\taarch64\n"}}
			remote := &collectorFakeRemote{responses: responses}
			snapshot, software, err := NewCollector(remote, time.Now).Collect(context.Background(), Server{ID: "s"}, CredentialSecret{})
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.OSFamily != fixture.want || snapshot.OSVersion != "9.4" || snapshot.Architecture != "arm64" {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			want := []SoftwareItem{{Category: "package", Name: "bash", Version: "5.1.8-9.el9", Architecture: "arm64", Source: "rpm"}}
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

func TestCollectorParsesAlpinePackagesServicesAndVersions(t *testing.T) {
	commands := collectorCommandsForTest()
	responses := collectorResponses(collectorBase("ID=alpine\nVERSION_ID=3.20.2\n", "x86_64"))
	responses[commands[collectorCommandAPK].Command] = collectorFakeResponse{result: CommandResult{Stdout: "musl-utils-1.2.5-r0\nfoo-bar-2.3.4-r1\n"}}
	responses[commands[collectorCommandServices].Command] = collectorFakeResponse{result: CommandResult{Stdout: "UNIT FILE STATE PRESET\nsshd.service enabled enabled\naurora-aiops.service disabled disabled\n\n2 unit files listed.\n"}}
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
	responses[commands[collectorCommandDebian].Command] = collectorFakeResponse{err: errors.New("password=hunter2\nremote\tcontrol")}
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
	for _, id := range []string{collectorCommandDebian, collectorCommandServices, collectorCommandVersions} {
		warning, ok := warnings[id]
		if !ok {
			t.Fatalf("missing warning for %s in %#v", id, software)
		}
		if len(warning.Status) > 512 || strings.ContainsAny(warning.Status, "\n\r\t") || strings.Contains(warning.Status, "hunter2") || strings.Contains(warning.Status, "target-secret") || strings.Contains(warning.Status, "server-secret") {
			t.Fatalf("unsafe warning = %#v", warning)
		}
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
