package assets

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	testSSHUser       = "aurora"
	testSSHPassword   = "test-password-do-not-leak"
	testSSHPassphrase = "test-passphrase-do-not-leak"
)

type loopbackSSHServer struct {
	t              *testing.T
	listener       net.Listener
	hostSigner     ssh.Signer
	clientKey      ed25519.PrivateKey
	clientSigner   ssh.Signer
	address        string
	port           int
	accepted       atomic.Int64
	authAttempts   atomic.Int64
	uploadExitCode atomic.Uint32

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	executes []string
	uploads  [][]byte
	guard    *loopbackGuardModel
	wg       sync.WaitGroup
}

type loopbackGuardModel struct {
	root               string
	rootExists         bool
	rootDirectory      bool
	rootSymlink        bool
	rootUID            int
	rootMode           os.FileMode
	resolvedParent     string
	destinationSymlink bool
	target             []byte
	diagnostic         string
}

func trustedLoopbackGuard(root, resolvedParent string) loopbackGuardModel {
	return loopbackGuardModel{
		root:           root,
		rootExists:     true,
		rootDirectory:  true,
		rootUID:        0,
		rootMode:       0o755,
		resolvedParent: resolvedParent,
		target:         []byte("original target"),
		diagnostic:     "guard rejected\x00" + strings.Repeat("z", 5000),
	}
}

func (m *loopbackGuardModel) allowsInstall() bool {
	if !m.rootExists || !m.rootDirectory || m.rootSymlink || m.rootUID != 0 || m.rootMode.Perm()&0o022 != 0 || m.destinationSymlink {
		return false
	}
	return m.resolvedParent == m.root || strings.HasPrefix(m.resolvedParent, m.root+"/")
}

type transientZeroEOFReader struct {
	reader        *bytes.Reader
	zeroReadsLeft int
	onZero        func()
}

func (r *transientZeroEOFReader) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	if r.zeroReadsLeft > 0 {
		r.zeroReadsLeft--
		if r.onZero != nil {
			r.onZero()
		}
		return 0, nil
	}
	return 0, io.EOF
}

func (*transientZeroEOFReader) Close() error { return nil }

type noProgressReader struct{}

func (noProgressReader) Read([]byte) (int, error) {
	return 0, nil
}

func (noProgressReader) Close() error { return nil }

type readCloserAdapter struct{ io.Reader }

func (*readCloserAdapter) Close() error { return nil }

type blockingReadCloser struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	return 0, errors.New("reader closed")
}

func (r *blockingReadCloser) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

type blockingNonCloserReader struct {
	started     chan struct{}
	release     chan struct{}
	startOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingNonCloserReader() *blockingNonCloserReader {
	return &blockingNonCloserReader{started: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingNonCloserReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.release
	return 0, io.EOF
}

func (r *blockingNonCloserReader) unblock() {
	r.releaseOnce.Do(func() { close(r.release) })
}

type partialThenStallReader struct {
	reader      *bytes.Reader
	stalled     chan struct{}
	release     chan struct{}
	stalledOnce sync.Once
	releaseOnce sync.Once
}

func newPartialThenStallReader(payload []byte) *partialThenStallReader {
	return &partialThenStallReader{
		reader:  bytes.NewReader(payload),
		stalled: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *partialThenStallReader) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	r.stalledOnce.Do(func() { close(r.stalled) })
	select {
	case <-r.release:
		return 0, io.EOF
	default:
		runtime.Gosched()
		return 0, nil
	}
}

func (r *partialThenStallReader) stopStalling() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func (r *partialThenStallReader) Close() error {
	r.stopStalling()
	return nil
}

func newLoopbackSSHServer(t *testing.T) *loopbackSSHServer {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpAddress := listener.Addr().(*net.TCPAddr)
	server := &loopbackSSHServer{
		t:            t,
		listener:     listener,
		hostSigner:   hostSigner,
		clientKey:    clientKey,
		clientSigner: clientSigner,
		address:      tcpAddress.IP.String(),
		port:         tcpAddress.Port,
		conns:        make(map[net.Conn]struct{}),
	}
	server.wg.Add(1)
	go server.serve()
	t.Cleanup(server.close)
	return server
}

func (s *loopbackSSHServer) target(fingerprint string) RemoteTarget {
	return RemoteTarget{
		Address:             s.address,
		Port:                s.port,
		Username:            testSSHUser,
		ExpectedFingerprint: fingerprint,
	}
}

func (s *loopbackSSHServer) fingerprint() string {
	return ssh.FingerprintSHA256(s.hostSigner.PublicKey())
}

func (s *loopbackSSHServer) privateKeyPEM(t *testing.T, passphrase string) string {
	t.Helper()
	var (
		block *pem.Block
		err   error
	)
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(s.clientKey, "test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(s.clientKey, "test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(block))
}

func (s *loopbackSSHServer) connectionCount() int64 {
	return s.accepted.Load()
}

func (s *loopbackSSHServer) lastExecute(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.executes) == 0 {
		t.Fatal("server did not receive an exec request")
	}
	return s.executes[len(s.executes)-1]
}

func (s *loopbackSSHServer) lastUpload(t *testing.T) []byte {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.uploads) == 0 {
		t.Fatal("server did not receive an upload")
	}
	return append([]byte(nil), s.uploads[len(s.uploads)-1]...)
}

func (s *loopbackSSHServer) setGuardModel(model loopbackGuardModel) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.guard = &model
}

func (s *loopbackSSHServer) modeledTarget() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.guard == nil {
		return nil
	}
	return append([]byte(nil), s.guard.target...)
}

func (s *loopbackSSHServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.accepted.Add(1)
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serveConn(conn)
	}
}

func (s *loopbackSSHServer) serveConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
	}()
	config := &ssh.ServerConfig{
		AuthLogCallback: func(ssh.ConnMetadata, string, error) {
			s.authAttempts.Add(1)
		},
		PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if metadata.User() == testSSHUser && string(password) == testSSHPassword {
				return nil, nil
			}
			return nil, errors.New("authentication rejected")
		},
		PublicKeyCallback: func(metadata ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if metadata.User() == testSSHUser && bytes.Equal(key.Marshal(), s.clientSigner.PublicKey().Marshal()) {
				return nil, nil
			}
			return nil, errors.New("authentication rejected")
		},
	}
	config.AddHostKey(s.hostSigner)
	_, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	var sessions sync.WaitGroup
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		sessions.Add(1)
		go func() {
			defer sessions.Done()
			s.handleSession(channel, channelRequests)
		}()
	}
	sessions.Wait()
}

func (s *loopbackSSHServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			_ = request.Reply(false, nil)
			return
		}
		s.mu.Lock()
		s.executes = append(s.executes, payload.Command)
		s.mu.Unlock()
		_ = request.Reply(true, nil)
		exitCode := uint32(0)
		switch payload.Command {
		case "emit":
			_, _ = io.WriteString(channel, "standard output")
			_, _ = io.WriteString(channel.Stderr(), "standard error")
		case "key-auth":
			_, _ = io.WriteString(channel, "key accepted")
		case "exit-23":
			_, _ = io.WriteString(channel, "before exit")
			_, _ = io.WriteString(channel.Stderr(), "failed deliberately")
			exitCode = 23
		case "large-output":
			_, _ = io.WriteString(channel, strings.Repeat("o", 96))
			_, _ = io.WriteString(channel.Stderr(), strings.Repeat("e", 96))
		case "block":
			_, _ = io.Copy(io.Discard, channel)
		case "":
			exitCode = 127
		default:
			if strings.Contains(payload.Command, "install -m ") {
				contents, _ := io.ReadAll(channel)
				s.mu.Lock()
				allowed := true
				diagnostic := ""
				if s.guard != nil {
					if loopbackCommandHasUploadGuards(payload.Command) {
						allowed = s.guard.allowsInstall()
					}
					if allowed {
						s.guard.target = append([]byte(nil), contents...)
					} else {
						diagnostic = s.guard.diagnostic
					}
				}
				if allowed {
					s.uploads = append(s.uploads, append([]byte(nil), contents...))
				}
				s.mu.Unlock()
				if allowed {
					exitCode = s.uploadExitCode.Load()
				} else {
					_, _ = io.WriteString(channel.Stderr(), diagnostic)
					exitCode = 73
				}
			} else {
				exitCode = 127
			}
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exitCode}))
		return
	}
}

func loopbackCommandHasUploadGuards(command string) bool {
	markers := []string{
		`[ -d "$root" ]`,
		`[ ! -L "$root" ]`,
		`stat -c %u -- "$root"`,
		`stat -c %a -- "$root"`,
		`case "$root_mode" in *[2367][0-7]|*[0-7][2367])`,
		`readlink -f -- "$root"`,
		`readlink -f -- "$parent"`,
		`[ ! -L "$destination" ]`,
	}
	for _, marker := range markers {
		if !strings.Contains(command, marker) {
			return false
		}
	}
	return true
}

func TestSSHTransportSanitizesAndBoundsUploadDiagnostic(t *testing.T) {
	diagnostic := string(bytes.Repeat([]byte{0xff}, int(maxSSHUploadStderr))) + "\x00\n"
	got := sanitizeSSHDiagnostic(diagnostic)
	if len(got) > int(maxSSHUploadStderr) {
		t.Fatalf("sanitized diagnostic = %d bytes, want <= %d", len(got), maxSSHUploadStderr)
	}
	if strings.ContainsAny(got, "\x00\n") {
		t.Fatalf("sanitized diagnostic contains control characters: %q", got)
	}
}

func (s *loopbackSSHServer) close() {
	_ = s.listener.Close()
	s.mu.Lock()
	for conn := range s.conns {
		_ = conn.Close()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		s.t.Errorf("loopback SSH server did not stop")
	}
}

func TestSSHTransportReportsUntrustedFingerprint(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)

	fingerprint, err := transport.ProbeHostKey(context.Background(), server.target(""))
	if fingerprint != server.fingerprint() {
		t.Fatalf("fingerprint = %q, want %q", fingerprint, server.fingerprint())
	}
	if !errors.Is(err, ErrHostKeyUntrusted) {
		t.Fatalf("error = %v, want ErrHostKeyUntrusted", err)
	}
	var hostKeyErr *HostKeyError
	if !errors.As(err, &hostKeyErr) {
		t.Fatalf("error type = %T, want *HostKeyError", err)
	}
	if hostKeyErr.Expected != "" || hostKeyErr.Actual != fingerprint || hostKeyErr.Changed {
		t.Fatalf("HostKeyError = %#v", hostKeyErr)
	}
}

func TestSSHTransportVerifiesPinnedFingerprint(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)

	fingerprint, err := transport.ProbeHostKey(context.Background(), server.target(server.fingerprint()))
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint != server.fingerprint() {
		t.Fatalf("fingerprint = %q, want %q", fingerprint, server.fingerprint())
	}
	if got := server.authAttempts.Load(); got != 0 {
		t.Fatalf("probe reached authentication %d times, want 0", got)
	}

	wrong := "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	actual, err := transport.ProbeHostKey(context.Background(), server.target(wrong))
	if actual != server.fingerprint() {
		t.Fatalf("actual fingerprint = %q, want %q", actual, server.fingerprint())
	}
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("error = %v, want ErrHostKeyChanged", err)
	}
	var hostKeyErr *HostKeyError
	if !errors.As(err, &hostKeyErr) || hostKeyErr.Expected != wrong || hostKeyErr.Actual != actual || !hostKeyErr.Changed {
		t.Fatalf("HostKeyError = %#v", hostKeyErr)
	}
}

func TestSSHTransportProbeFailsBeforeHostKeyIsObserved(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.Close()
		}
	}()
	tcpAddress := listener.Addr().(*net.TCPAddr)
	fingerprint, err := NewSSHTransport(time.Second).ProbeHostKey(context.Background(), RemoteTarget{
		Address: tcpAddress.IP.String(), Port: tcpAddress.Port, Username: testSSHUser,
		ExpectedFingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	})
	if err == nil {
		t.Fatal("expected pre-host-key disconnect error")
	}
	if fingerprint != "" {
		t.Fatalf("fingerprint = %q, want empty", fingerprint)
	}
	<-done
}

func TestSSHTransportPasswordAuthRunsCommand(t *testing.T) {
	server := newLoopbackSSHServer(t)
	result, err := NewSSHTransport(time.Second).Run(
		context.Background(), server.target(server.fingerprint()),
		CredentialSecret{Password: testSSHPassword}, "emit", 1024,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "standard output" || result.Stderr != "standard error" || result.ExitCode != 0 || result.Truncated {
		t.Fatalf("result = %#v", result)
	}
}

func TestSSHTransportPrivateKeyAuthAndEncryptedKey(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)
	target := server.target(server.fingerprint())

	for name, secret := range map[string]CredentialSecret{
		"plain":     {PrivateKey: server.privateKeyPEM(t, "")},
		"encrypted": {PrivateKey: server.privateKeyPEM(t, testSSHPassphrase), Passphrase: testSSHPassphrase},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := transport.Run(context.Background(), target, secret, "key-auth", 1024)
			if err != nil {
				t.Fatal(err)
			}
			if result.Stdout != "key accepted" || result.ExitCode != 0 {
				t.Fatalf("result = %#v", result)
			}
		})
	}

	for name, secret := range map[string]CredentialSecret{
		"wrong passphrase": {PrivateKey: server.privateKeyPEM(t, testSSHPassphrase), Passphrase: "wrong-passphrase-do-not-leak"},
		"wrong password":   {Password: "wrong-password-do-not-leak"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := transport.Run(context.Background(), target, secret, "key-auth", 1024)
			if err == nil {
				t.Fatal("expected authentication failure")
			}
			for _, value := range []string{secret.Password, secret.PrivateKey, secret.Passphrase} {
				if value != "" && strings.Contains(err.Error(), value) {
					t.Fatalf("error leaked credential: %v", err)
				}
			}
		})
	}
}

func TestSSHTransportReturnsRemoteExitStatus(t *testing.T) {
	server := newLoopbackSSHServer(t)
	result, err := NewSSHTransport(time.Second).Run(
		context.Background(), server.target(server.fingerprint()),
		CredentialSecret{Password: testSSHPassword}, "exit-23", 1024,
	)
	if err == nil {
		t.Fatal("expected nonzero exit error")
	}
	if result.Stdout != "before exit" || result.Stderr != "failed deliberately" || result.ExitCode != 23 {
		t.Fatalf("result = %#v", result)
	}
}

func TestSSHTransportHonorsContextDuringHandshakeAndCommand(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()
	tcpAddress := listener.Addr().(*net.TCPAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = NewSSHTransport(5*time.Second).ProbeHostKey(ctx, RemoteTarget{
		Address: tcpAddress.IP.String(), Port: tcpAddress.Port, Username: testSSHUser,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("handshake cancellation took %v", elapsed)
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	default:
	}

	server := newLoopbackSSHServer(t)
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := NewSSHTransport(5*time.Second).Run(ctx, server.target(server.fingerprint()), CredentialSecret{Password: testSSHPassword}, "block", 1024)
		done <- runErr
	}()
	deadline := time.Now().Add(time.Second)
	for server.lastExecuteIfAny() != "block" && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if server.lastExecuteIfAny() != "block" {
		t.Fatal("blocking command did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("command error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active command did not stop promptly")
	}
}

func (s *loopbackSSHServer) lastExecuteIfAny() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.executes) == 0 {
		return ""
	}
	return s.executes[len(s.executes)-1]
}

func TestSSHTransportBoundsCombinedOutput(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)
	target := server.target(server.fingerprint())
	secret := CredentialSecret{Password: testSSHPassword}

	result, err := transport.Run(context.Background(), target, secret, "large-output", 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(result.Stdout) + len(result.Stderr); got > 100 {
		t.Fatalf("combined output = %d bytes, want <= 100", got)
	}
	if !result.Truncated {
		t.Fatal("Truncated = false, want true")
	}

	before := server.connectionCount()
	for _, limit := range []int64{0, -1, 16<<20 + 1} {
		if _, err := transport.Run(context.Background(), target, secret, "emit", limit); err == nil {
			t.Fatalf("limit %d: expected validation error", limit)
		}
	}
	if got := server.connectionCount(); got != before {
		t.Fatalf("invalid limits dialed server: connections %d -> %d", before, got)
	}
}

func TestSSHTransportUploadStreamsExactBytesAndQuotesPath(t *testing.T) {
	server := newLoopbackSSHServer(t)
	payload := []byte("exact upload contents\n")
	path := "/tmp/aurora-aiops/collector's binary"
	err := NewSSHTransport(time.Second).Upload(
		context.Background(), server.target(server.fingerprint()),
		CredentialSecret{Password: testSSHPassword}, bytes.NewReader(payload), int64(len(payload)), path, 0o750,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := server.lastUpload(t); !bytes.Equal(got, payload) {
		t.Fatalf("uploaded bytes = %q, want %q", got, payload)
	}
	wantSuffix := "install -m 0750 /dev/stdin '/tmp/aurora-aiops/collector'\"'\"'s binary'"
	if got := server.lastExecute(t); !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("exec request = %q, want suffix %q", got, wantSuffix)
	}
}

func TestSSHTransportUploadCommandGuardsResolvedTrustedPath(t *testing.T) {
	server := newLoopbackSSHServer(t)
	destination := "/tmp/aurora-aiops/child/collector's binary"
	payload := []byte("guarded")
	err := NewSSHTransport(time.Second).Upload(
		context.Background(), server.target(server.fingerprint()), CredentialSecret{Password: testSSHPassword},
		bytes.NewReader(payload), int64(len(payload)), destination, 0o750,
	)
	if err != nil {
		t.Fatal(err)
	}
	command := server.lastExecute(t)
	for _, marker := range []string{
		`root='/tmp/aurora-aiops'`,
		`parent='/tmp/aurora-aiops/child'`,
		`destination='/tmp/aurora-aiops/child/collector'"'"'s binary'`,
		`[ -d "$root" ]`,
		`[ ! -L "$root" ]`,
		`stat -c %u -- "$root"`,
		`stat -c %a -- "$root"`,
		`case "$root_mode" in *[2367][0-7]|*[0-7][2367])`,
		`readlink -f -- "$root"`,
		`readlink -f -- "$parent"`,
		`case "$parent_resolved" in "$root_resolved"|"$root_resolved"/*)`,
		`[ ! -L "$destination" ]`,
		`install -m 0750 /dev/stdin '/tmp/aurora-aiops/child/collector'"'"'s binary'`,
	} {
		if !strings.Contains(command, marker) {
			t.Errorf("command missing guard %q: %s", marker, command)
		}
	}
}

func TestSSHTransportUploadRemoteGuardsPreventTargetMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*loopbackGuardModel)
	}{
		{name: "root symlink", mutate: func(model *loopbackGuardModel) { model.rootSymlink = true }},
		{name: "untrusted owner", mutate: func(model *loopbackGuardModel) { model.rootUID = 1000 }},
		{name: "untrusted mode", mutate: func(model *loopbackGuardModel) { model.rootMode = 0o777 }},
		{name: "parent outside root", mutate: func(model *loopbackGuardModel) { model.resolvedParent = "/tmp/attacker" }},
		{name: "destination symlink", mutate: func(model *loopbackGuardModel) { model.destinationSymlink = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newLoopbackSSHServer(t)
			model := trustedLoopbackGuard("/tmp/aurora-aiops", "/tmp/aurora-aiops/child")
			test.mutate(&model)
			server.setGuardModel(model)
			err := NewSSHTransport(time.Second).Upload(
				context.Background(), server.target(server.fingerprint()), CredentialSecret{Password: testSSHPassword},
				strings.NewReader("replacement"), int64(len("replacement")), "/tmp/aurora-aiops/child/target", 0o640,
			)
			if err == nil {
				t.Fatal("expected remote guard failure")
			}
			if got := server.modeledTarget(); !bytes.Equal(got, []byte("original target")) {
				t.Fatalf("modeled target mutated to %q", got)
			}
			if strings.ContainsRune(err.Error(), '\x00') {
				t.Fatalf("remote diagnostic contains control character: %q", err)
			}
			if count := strings.Count(err.Error(), "z"); count > 4096 {
				t.Fatalf("remote diagnostic exceeded 4KiB: %d bytes", count)
			}
		})
	}
}

func TestSSHTransportUploadToleratesTransientZeroReadAtEOF(t *testing.T) {
	server := newLoopbackSSHServer(t)
	payload := []byte("exact after transient zero")
	reader := &transientZeroEOFReader{
		reader:        bytes.NewReader(payload),
		zeroReadsLeft: 2,
	}
	err := NewSSHTransport(time.Second).Upload(
		context.Background(), server.target(server.fingerprint()),
		CredentialSecret{Password: testSSHPassword}, reader, int64(len(payload)), "/opt/aurora-aiops/transient", 0o640,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := server.lastUpload(t); !bytes.Equal(got, payload) {
		t.Fatalf("uploaded bytes = %q, want %q", got, payload)
	}
	if got, want := server.lastExecute(t), "install -m 0640 /dev/stdin '/opt/aurora-aiops/transient'"; !strings.HasSuffix(got, want) {
		t.Fatalf("exec request = %q, want suffix %q", got, want)
	}
}

func TestSSHTransportUploadToleratesTransientZeroReadsWhileCopying(t *testing.T) {
	server := newLoopbackSSHServer(t)
	payload := []byte("partial-rest")
	reader := &readCloserAdapter{Reader: io.MultiReader(
		strings.NewReader("partial"),
		&transientZeroEOFReader{reader: bytes.NewReader(nil), zeroReadsLeft: 2},
		strings.NewReader("-rest"),
	)}
	err := NewSSHTransport(time.Second).Upload(
		context.Background(), server.target(server.fingerprint()),
		CredentialSecret{Password: testSSHPassword}, reader, int64(len(payload)), "/tmp/aurora-aiops/transient-copy", 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := server.lastUpload(t); !bytes.Equal(got, payload) {
		t.Fatalf("uploaded bytes = %q, want %q", got, payload)
	}
}

func TestSSHTransportUploadCancelsBlockingReadCloserBeforeDial(t *testing.T) {
	server := newLoopbackSSHServer(t)
	reader := newBlockingReadCloser()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewSSHTransport(time.Second).Upload(
			ctx, server.target(server.fingerprint()),
			CredentialSecret{Password: testSSHPassword}, reader, 1, "/tmp/aurora-aiops/blocking", 0o600,
		)
	}()
	select {
	case <-reader.started:
	case <-time.After(time.Second):
		reader.Close()
		<-done
		t.Fatal("blocking reader was not read")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		reader.Close()
		<-done
		t.Fatal("blocking ReadCloser did not unblock promptly on cancellation")
	}
	if got := server.connectionCount(); got != 0 {
		t.Fatalf("blocking source dialed server %d times, want 0", got)
	}
}

func TestSSHTransportUploadRejectsBlockingNonCloserBeforeReadOrDial(t *testing.T) {
	server := newLoopbackSSHServer(t)
	reader := newBlockingNonCloserReader()
	defer reader.unblock()
	done := make(chan error, 1)
	go func() {
		done <- NewSSHTransport(time.Second).Upload(
			context.Background(), server.target(server.fingerprint()),
			CredentialSecret{Password: testSSHPassword}, reader, 1, "/opt/aurora-aiops/unsupported", 0o600,
		)
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("error = %v, want ErrInvalidInput", err)
		}
	case <-time.After(500 * time.Millisecond):
		reader.unblock()
		<-done
		t.Fatal("unsupported non-closer reader was read instead of rejected")
	}
	select {
	case <-reader.started:
		t.Fatal("unsupported non-closer reader was read")
	default:
	}
	if got := server.connectionCount(); got != 0 {
		t.Fatalf("unsupported source dialed server %d times, want 0", got)
	}
}

func TestSSHTransportUploadRejectsReaderWithNoProgress(t *testing.T) {
	server := newLoopbackSSHServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := NewSSHTransport(time.Second).Upload(
		ctx, server.target(server.fingerprint()),
		CredentialSecret{Password: testSSHPassword}, noProgressReader{}, 0, "/tmp/aurora-aiops/no-progress", 0o600,
	)
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("error = %v, want io.ErrNoProgress", err)
	}
}

func TestSSHTransportUploadHonorsCancellationWhileProbingEOF(t *testing.T) {
	server := newLoopbackSSHServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	reader := &transientZeroEOFReader{
		reader:        bytes.NewReader(nil),
		zeroReadsLeft: 1,
		onZero:        cancel,
	}
	err := NewSSHTransport(time.Second).Upload(
		ctx, server.target(server.fingerprint()),
		CredentialSecret{Password: testSSHPassword}, reader, 0, "/tmp/aurora-aiops/cancel", 0o600,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestSSHTransportUploadBoundsNoProgressWhileCopyingPositiveSize(t *testing.T) {
	server := newLoopbackSSHServer(t)
	reader := newPartialThenStallReader([]byte("partial"))
	defer reader.stopStalling()
	done := make(chan error, 1)
	go func() {
		done <- NewSSHTransport(time.Second).Upload(
			context.Background(), server.target(server.fingerprint()),
			CredentialSecret{Password: testSSHPassword}, reader, int64(len("partial")+1), "/tmp/aurora-aiops/stalled", 0o600,
		)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, io.ErrNoProgress) {
			t.Fatalf("error = %v, want io.ErrNoProgress", err)
		}
	case <-time.After(500 * time.Millisecond):
		reader.stopStalling()
		<-done
		t.Fatal("positive-size upload did not bound a stalled reader")
	}
}

func TestSSHTransportUploadCancelsNoProgressWhileCopyingPositiveSize(t *testing.T) {
	server := newLoopbackSSHServer(t)
	reader := newPartialThenStallReader([]byte("partial"))
	defer reader.stopStalling()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewSSHTransport(time.Second).Upload(
			ctx, server.target(server.fingerprint()),
			CredentialSecret{Password: testSSHPassword}, reader, int64(len("partial")+1), "/opt/aurora-aiops/canceled-stall", 0o640,
		)
	}()
	select {
	case <-reader.stalled:
	case <-time.After(time.Second):
		reader.stopStalling()
		<-done
		t.Fatal("reader did not enter stalled state")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		reader.stopStalling()
		<-done
		t.Fatal("positive-size stalled upload did not honor cancellation promptly")
	}
}

func TestSSHTransportUploadRejectsInvalidSizesAndModesBeforeDial(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)
	target := server.target(server.fingerprint())
	secret := CredentialSecret{Password: testSSHPassword}
	before := server.connectionCount()

	tests := []struct {
		name string
		size int64
		mode os.FileMode
	}{
		{name: "negative size", size: -1, mode: 0o600},
		{name: "oversize", size: 1<<30 + 1, mode: 0o600},
		{name: "zero mode", size: 0, mode: 0},
		{name: "type bits", size: 0, mode: os.ModeDir | 0o700},
		{name: "special permission bits", size: 0, mode: os.ModeSetuid | 0o700},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := transport.Upload(context.Background(), target, secret, bytes.NewReader(nil), test.size, "/tmp/aurora-aiops/file", test.mode); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if got := server.connectionCount(); got != before {
		t.Fatalf("invalid upload dialed server: connections %d -> %d", before, got)
	}
}

func TestSSHTransportUploadRejectsLengthMismatchAndCommandFailure(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)
	target := server.target(server.fingerprint())
	secret := CredentialSecret{Password: testSSHPassword}
	path := "/opt/aurora-aiops/file"
	connectionsBefore := server.connectionCount()

	for name, reader := range map[string]io.Reader{
		"short":    strings.NewReader("123"),
		"overlong": strings.NewReader("12345"),
	} {
		t.Run(name, func(t *testing.T) {
			err := transport.Upload(context.Background(), target, secret, reader, 4, path, 0o640)
			if err == nil {
				t.Fatal("expected length mismatch")
			}
			if name == "short" && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("error = %v, want io.ErrUnexpectedEOF", err)
			}
		})
	}
	if got := server.connectionCount(); got != connectionsBefore {
		t.Fatalf("length mismatch dialed server: connections %d -> %d", connectionsBefore, got)
	}
	server.mu.Lock()
	executesAfterMismatch := len(server.executes)
	uploadsAfterMismatch := len(server.uploads)
	server.mu.Unlock()
	if executesAfterMismatch != 0 || uploadsAfterMismatch != 0 {
		t.Fatalf("length mismatch mutated remote model: executes=%d uploads=%d", executesAfterMismatch, uploadsAfterMismatch)
	}

	server.uploadExitCode.Store(42)
	err := transport.Upload(context.Background(), target, secret, strings.NewReader("1234"), 4, path, 0o640)
	if err == nil {
		t.Fatal("expected remote command failure")
	}
	var exitErr *ssh.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitStatus() != 42 {
		t.Fatalf("error = %v, want SSH exit status 42", err)
	}
}

func TestSSHTransportUploadRejectsUnsafePathsBeforeDial(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)
	target := server.target(server.fingerprint())
	secret := CredentialSecret{Password: testSSHPassword}
	paths := []string{
		"relative/file",
		"/tmp/aurora-aiops",
		"/tmp/aurora-aiops/",
		"/opt/aurora-aiops",
		"/opt/aurora-aiops/",
		"/tmp/aurora-aiops/../escape",
		"/tmp/aurora-aiops-evil/file",
		"/opt/aurora-aiops-evil/file",
		"/tmp/aurora-aiops/file\ncommand",
		"/tmp/aurora-aiops/file\x00suffix",
		"/tmp/aurora-aiops/file\x1f",
	}
	before := server.connectionCount()
	for _, path := range paths {
		t.Run(fmt.Sprintf("%q", path), func(t *testing.T) {
			if err := transport.Upload(context.Background(), target, secret, strings.NewReader("x"), 1, path, 0o600); err == nil {
				t.Fatal("expected unsafe path error")
			}
		})
	}
	if got := server.connectionCount(); got != before {
		t.Fatalf("unsafe path dialed server: connections %d -> %d", before, got)
	}
}

func TestSSHTransportValidatesTargetAndCommandBeforeDial(t *testing.T) {
	server := newLoopbackSSHServer(t)
	transport := NewSSHTransport(time.Second)
	secret := CredentialSecret{Password: testSSHPassword}
	validTarget := server.target(server.fingerprint())
	before := server.connectionCount()

	badTargets := []RemoteTarget{
		{Address: "", Port: server.port, Username: testSSHUser, ExpectedFingerprint: server.fingerprint()},
		{Address: "host\nname", Port: server.port, Username: testSSHUser, ExpectedFingerprint: server.fingerprint()},
		{Address: server.address, Port: 0, Username: testSSHUser, ExpectedFingerprint: server.fingerprint()},
		{Address: server.address, Port: 65536, Username: testSSHUser, ExpectedFingerprint: server.fingerprint()},
		{Address: server.address, Port: server.port, Username: "", ExpectedFingerprint: server.fingerprint()},
		{Address: server.address, Port: server.port, Username: "user\x00name", ExpectedFingerprint: server.fingerprint()},
	}
	for _, target := range badTargets {
		if _, err := transport.Run(context.Background(), target, secret, "emit", 1024); err == nil {
			t.Fatalf("target %#v: expected validation error", target)
		}
	}
	for _, command := range []string{"", "echo\x00bad"} {
		if _, err := transport.Run(context.Background(), validTarget, secret, command, 1024); err == nil {
			t.Fatalf("command %q: expected validation error", command)
		}
	}
	if got := server.connectionCount(); got != before {
		t.Fatalf("invalid input dialed server: connections %d -> %d", before, got)
	}
}
