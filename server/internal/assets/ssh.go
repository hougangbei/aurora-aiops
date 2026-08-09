package assets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"
)

const (
	defaultSSHDialTimeout = 10 * time.Second
	maxSSHOutputBytes     = int64(16 << 20)
	maxSSHUploadBytes     = int64(1 << 30)
	maxSSHEmptyReads      = 100
)

type RemoteTarget struct {
	Address             string
	Port                int
	Username            string
	ExpectedFingerprint string
}

type CommandResult struct {
	Stdout    string
	Stderr    string
	ExitCode  int
	Truncated bool
}

type HostKeyError struct {
	Expected string
	Actual   string
	Changed  bool
}

func (e *HostKeyError) Error() string {
	if e.Changed {
		return fmt.Sprintf("ssh host key changed (expected %s, actual %s)", e.Expected, e.Actual)
	}
	return fmt.Sprintf("ssh host key is untrusted (actual %s)", e.Actual)
}

func (e *HostKeyError) Unwrap() error {
	if e.Changed {
		return ErrHostKeyChanged
	}
	return ErrHostKeyUntrusted
}

type RemoteTransport interface {
	ProbeHostKey(context.Context, RemoteTarget) (string, error)
	Run(context.Context, RemoteTarget, CredentialSecret, string, int64) (CommandResult, error)
	Upload(context.Context, RemoteTarget, CredentialSecret, io.Reader, int64, string, fs.FileMode) error
}

type SSHTransport struct {
	dialTimeout time.Duration
}

var _ RemoteTransport = (*SSHTransport)(nil)

func NewSSHTransport(dialTimeout time.Duration) *SSHTransport {
	if dialTimeout <= 0 {
		dialTimeout = defaultSSHDialTimeout
	}
	return &SSHTransport{dialTimeout: dialTimeout}
}

func (t *SSHTransport) ProbeHostKey(ctx context.Context, target RemoteTarget) (string, error) {
	if err := validateRemoteTarget(target, true); err != nil {
		return "", err
	}

	var actual string
	var hostKeyErr *HostKeyError
	config := &ssh.ClientConfig{
		User: target.Username,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			actual = ssh.FingerprintSHA256(key)
			switch {
			case target.ExpectedFingerprint == "":
				hostKeyErr = &HostKeyError{Actual: actual}
				return hostKeyErr
			case target.ExpectedFingerprint != actual:
				hostKeyErr = &HostKeyError{
					Expected: target.ExpectedFingerprint,
					Actual:   actual,
					Changed:  true,
				}
				return hostKeyErr
			default:
				return nil
			}
		},
	}
	client, err := t.dial(ctx, target, config)
	if client != nil {
		_ = client.Close()
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return actual, ctxErr
	}
	if hostKeyErr != nil {
		return actual, hostKeyErr
	}
	// Reaching user authentication proves that key exchange and the pinned host
	// key check completed. ProbeHostKey intentionally has no user credential, so
	// an authentication failure after that point is a successful probe.
	if actual != "" {
		return actual, nil
	}
	return "", err
}

func (t *SSHTransport) Run(ctx context.Context, target RemoteTarget, secret CredentialSecret, command string, outputLimit int64) (CommandResult, error) {
	result := CommandResult{ExitCode: -1}
	if err := validateRemoteTarget(target, false); err != nil {
		return result, err
	}
	if command == "" {
		return result, fmt.Errorf("command must not be empty: %w", ErrInvalidInput)
	}
	if strings.ContainsRune(command, '\x00') {
		return result, fmt.Errorf("command contains a NUL byte: %w", ErrInvalidInput)
	}
	if outputLimit <= 0 || outputLimit > maxSSHOutputBytes {
		return result, fmt.Errorf("output limit must be between 1 and %d bytes: %w", maxSSHOutputBytes, ErrInvalidInput)
	}
	auth, err := authMethod(secret)
	if err != nil {
		return result, err
	}

	client, err := t.dial(ctx, target, verifiedClientConfig(target, auth))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, err
	}
	defer client.Close()
	stopCancellation := closeSSHClientOnCancellation(ctx, client)
	defer stopCancellation()

	session, err := client.NewSession()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, fmt.Errorf("create SSH session: %w", err)
	}
	defer session.Close()

	budget := &combinedOutputBudget{remaining: outputLimit}
	stdout := &boundedOutputWriter{budget: budget}
	stderr := &boundedOutputWriter{budget: budget}
	session.Stdout = stdout
	session.Stderr = stderr
	err = session.Run(command)
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.Truncated = budget.wasTruncated()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	if err == nil {
		result.ExitCode = 0
		return result, nil
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitStatus()
	}
	return result, err
}

func (t *SSHTransport) Upload(ctx context.Context, target RemoteTarget, secret CredentialSecret, source io.Reader, size int64, destination string, mode fs.FileMode) error {
	if err := validateRemoteTarget(target, false); err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("upload source must not be nil: %w", ErrInvalidInput)
	}
	if size < 0 || size > maxSSHUploadBytes {
		return fmt.Errorf("upload size must be between 0 and %d bytes: %w", maxSSHUploadBytes, ErrInvalidInput)
	}
	if mode.Perm() == 0 || mode&^fs.ModePerm != 0 {
		return fmt.Errorf("upload mode must contain only nonzero regular permission bits: %w", ErrInvalidInput)
	}
	if err := validateUploadPath(destination); err != nil {
		return err
	}
	auth, err := authMethod(secret)
	if err != nil {
		return err
	}

	client, err := t.dial(ctx, target, verifiedClientConfig(target, auth))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	defer client.Close()
	stopCancellation := closeSSHClientOnCancellation(ctx, client)
	defer stopCancellation()

	session, err := client.NewSession()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("create SSH upload session: %w", err)
	}
	defer session.Close()
	session.Stdout = io.Discard
	session.Stderr = io.Discard
	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("open SSH upload stdin: %w", err)
	}
	command := fmt.Sprintf("install -m %04o /dev/stdin %s", mode.Perm(), quotePOSIX(destination))
	if err := session.Start(command); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("start SSH upload command: %w", err)
	}

	var transferErr error
	if err := copyUploadExact(ctx, stdin, source, size); err != nil {
		transferErr = err
	} else {
		transferErr = verifyUploadSourceEOF(ctx, source, size)
	}
	closeErr := stdin.Close()
	waitErr := session.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if transferErr != nil || closeErr != nil || waitErr != nil {
		return errors.Join(transferErr, closeErr, waitErr)
	}
	return nil
}

func copyUploadExact(ctx context.Context, destination io.Writer, source io.Reader, declaredSize int64) error {
	const bufferSize = 32 << 10
	var buffer [bufferSize]byte
	remaining := declaredSize
	emptyReads := 0
	for remaining > 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		readSize := len(buffer)
		if remaining < int64(readSize) {
			readSize = int(remaining)
		}
		n, readErr := source.Read(buffer[:readSize])
		if n < 0 || n > readSize {
			return fmt.Errorf("upload source returned invalid read count %d: %w", n, ErrInvalidInput)
		}
		if n > 0 {
			emptyReads = 0
			written, writeErr := destination.Write(buffer[:n])
			if written < 0 || written > n {
				return fmt.Errorf("SSH upload stdin returned invalid write count %d: %w", written, io.ErrShortWrite)
			}
			remaining -= int64(written)
			if writeErr != nil {
				return fmt.Errorf("write SSH upload stdin: %w", writeErr)
			}
			if written != n {
				return fmt.Errorf("write SSH upload stdin: %w", io.ErrShortWrite)
			}
			if remaining == 0 {
				return nil
			}
		} else {
			emptyReads++
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return fmt.Errorf("upload source shorter than declared size %d: %w", declaredSize, io.ErrUnexpectedEOF)
			}
			return fmt.Errorf("read upload source: %w", readErr)
		}
		if emptyReads >= maxSSHEmptyReads {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("read upload source after %d empty reads: %w", maxSSHEmptyReads, io.ErrNoProgress)
		}
	}
	return nil
}

func verifyUploadSourceEOF(ctx context.Context, source io.Reader, declaredSize int64) error {
	var extra [1]byte
	for emptyReads := 0; ; emptyReads++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		n, err := source.Read(extra[:])
		if n > 0 {
			return fmt.Errorf("upload source exceeds declared size %d: %w", declaredSize, ErrInvalidInput)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("verify upload source length: %w", err)
		}
		if emptyReads+1 >= maxSSHEmptyReads {
			return fmt.Errorf("verify upload source length after %d empty reads: %w", maxSSHEmptyReads, io.ErrNoProgress)
		}
	}
}

func (t *SSHTransport) dial(ctx context.Context, target RemoteTarget, config *ssh.ClientConfig) (*ssh.Client, error) {
	dialer := net.Dialer{Timeout: t.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Address, strconv.Itoa(target.Port)))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("dial SSH target: %w", err)
	}
	deadline := time.Now().Add(t.dialTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set SSH handshake deadline: %w", err)
	}
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-handshakeDone:
		}
	}()
	clientConn, channels, requests, err := ssh.NewClientConn(conn, net.JoinHostPort(target.Address, strconv.Itoa(target.Port)), config)
	close(handshakeDone)
	if err != nil {
		_ = conn.Close()
		if ctxErr := elapsedContextError(ctx); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = clientConn.Close()
		return nil, fmt.Errorf("clear SSH connection deadline: %w", err)
	}
	return ssh.NewClient(clientConn, channels, requests), nil
}

func elapsedContextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
	}
	return nil
}

func verifiedClientConfig(target RemoteTarget, auth ssh.AuthMethod) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: target.Username,
		Auth: []ssh.AuthMethod{auth},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			actual := ssh.FingerprintSHA256(key)
			if target.ExpectedFingerprint == "" {
				return &HostKeyError{Actual: actual}
			}
			if target.ExpectedFingerprint != actual {
				return &HostKeyError{Expected: target.ExpectedFingerprint, Actual: actual, Changed: true}
			}
			return nil
		},
	}
}

func authMethod(secret CredentialSecret) (ssh.AuthMethod, error) {
	if secret.PrivateKey != "" {
		var (
			signer ssh.Signer
			err    error
		)
		if secret.Passphrase == "" {
			signer, err = ssh.ParsePrivateKey([]byte(secret.PrivateKey))
		} else {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(secret.PrivateKey), []byte(secret.Passphrase))
		}
		if err != nil {
			return nil, fmt.Errorf("parse SSH private key: %w", err)
		}
		return ssh.PublicKeys(signer), nil
	}
	if secret.Password == "" {
		return nil, fmt.Errorf("SSH password or private key is required: %w", ErrInvalidInput)
	}
	return ssh.Password(secret.Password), nil
}

func validateRemoteTarget(target RemoteTarget, allowUnconfirmedFingerprint bool) error {
	if target.Address == "" || containsControl(target.Address) {
		return fmt.Errorf("SSH address is empty or contains control characters: %w", ErrInvalidInput)
	}
	if target.Port < 1 || target.Port > 65535 {
		return fmt.Errorf("SSH port must be between 1 and 65535: %w", ErrInvalidInput)
	}
	if target.Username == "" || containsControl(target.Username) {
		return fmt.Errorf("SSH username is empty or contains control characters: %w", ErrInvalidInput)
	}
	if containsControl(target.ExpectedFingerprint) {
		return fmt.Errorf("SSH fingerprint contains control characters: %w", ErrInvalidInput)
	}
	if !allowUnconfirmedFingerprint && target.ExpectedFingerprint == "" {
		return &HostKeyError{}
	}
	return nil
}

func validateUploadPath(destination string) error {
	if destination == "" || containsControl(destination) || !strings.HasPrefix(destination, "/") {
		return fmt.Errorf("upload path must be absolute and contain no control characters: %w", ErrInvalidInput)
	}
	cleaned := path.Clean(destination)
	if cleaned != destination {
		return fmt.Errorf("upload path must already be clean: %w", ErrInvalidInput)
	}
	if !strings.HasPrefix(cleaned, "/tmp/aurora-aiops/") && !strings.HasPrefix(cleaned, "/opt/aurora-aiops/") {
		return fmt.Errorf("upload path is outside the allowed roots: %w", ErrInvalidInput)
	}
	return nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type combinedOutputBudget struct {
	mu        sync.Mutex
	remaining int64
	truncated bool
}

type boundedOutputWriter struct {
	budget *combinedOutputBudget
	buffer bytes.Buffer
}

func (w *boundedOutputWriter) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	keep := int64(len(p))
	if keep > w.budget.remaining {
		keep = w.budget.remaining
		w.budget.truncated = true
	}
	if keep > 0 {
		_, _ = w.buffer.Write(p[:keep])
		w.budget.remaining -= keep
	}
	return len(p), nil
}

func (w *boundedOutputWriter) String() string {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	return w.buffer.String()
}

func (b *combinedOutputBudget) wasTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func closeSSHClientOnCancellation(ctx context.Context, client *ssh.Client) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}
