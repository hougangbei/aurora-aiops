package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/hougangbei/aurora-aiops/server/internal/audit"
)

const collectionFailureMessage = "asset collection failed"

type Service struct {
	repo      *Repository
	cipher    CredentialCipher
	remote    RemoteTransport
	collector *Collector
	audit     audit.Repository
	now       func() time.Time
}

func NewService(repo *Repository, cipher CredentialCipher, remote RemoteTransport, collector *Collector, auditRepo audit.Repository, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, cipher: cipher, remote: remote, collector: collector, audit: auditRepo, now: now}
}

func (s *Service) Create(ctx context.Context, actor string, input CreateServerInput) (Server, error) {
	name, address, username, port, err := validateServerFields(input.Name, input.Address, input.Username, input.SSHPort, true)
	if err != nil {
		return Server{}, err
	}
	if err := validateCredential(input.AuthType, input.Secret); err != nil {
		return Server{}, err
	}
	now := s.clock()
	server := Server{
		ID: uuid.NewString(), Name: name, Address: address, Username: username, SSHPort: port,
		Status: ServerPending, CreatedAt: now, UpdatedAt: now,
	}
	if !credentialCipherAvailable(s.cipher) {
		_ = s.auditRecord(ctx, actor, "asset.server.create", server.ID, "failure", auditPayload{AuthType: string(input.AuthType)})
		return Server{}, ErrEncryptionUnavailable
	}
	envelope, err := s.cipher.Encrypt(input.Secret)
	if err != nil {
		_ = s.auditRecord(ctx, actor, "asset.server.create", server.ID, "failure", auditPayload{AuthType: string(input.AuthType)})
		return Server{}, ErrEncryptionUnavailable
	}
	credential := StoredCredential{
		ID: uuid.NewString(), AuthType: input.AuthType, Envelope: envelope, CreatedAt: now, UpdatedAt: now,
	}
	created, err := s.repo.CreateServer(ctx, server, credential)
	if err != nil {
		s.auditRecord(ctx, actor, "asset.server.create", server.ID, "failure", auditPayload{AuthType: string(input.AuthType)})
		return Server{}, err
	}
	if !input.TestConnection {
		if err := s.auditRecord(ctx, actor, "asset.server.create", created.ID, "success", auditPayload{AuthType: string(input.AuthType)}); err != nil {
			return created, err
		}
		return created, nil
	}

	result, probeErr := s.probe(ctx, created)
	payload := auditPayload{AuthType: string(input.AuthType), Fingerprint: result.Fingerprint}
	if probeErr != nil {
		resultName := "failure"
		var hostKeyErr *HostKeyError
		if errors.As(probeErr, &hostKeyErr) {
			resultName = "host_key_confirmation_required"
		}
		_ = s.auditRecord(ctx, actor, "asset.server.create", created.ID, resultName, payload)
		return created, probeErr
	}
	if err := s.auditRecord(ctx, actor, "asset.server.create", created.ID, "success", payload); err != nil {
		return created, err
	}
	return created, nil
}

func (s *Service) Update(ctx context.Context, actor, id string, input UpdateServerInput) (Server, error) {
	current, err := s.repo.GetServer(ctx, id)
	if err != nil {
		return Server{}, err
	}
	name, address, username, port, err := validateServerFields(input.Name, input.Address, input.Username, input.SSHPort, true)
	if err != nil {
		return Server{}, err
	}

	var replacement *StoredCredential
	authType := current.CredentialAuthType
	credentialMutation := input.AuthType != nil || input.Secret != nil
	if credentialMutation {
		if input.AuthType != nil {
			authType = *input.AuthType
		}
		if input.Secret == nil {
			return Server{}, invalidInput("credential secret is required when changing authentication type")
		}
		if err := validateCredential(authType, *input.Secret); err != nil {
			return Server{}, err
		}
		if !credentialCipherAvailable(s.cipher) {
			_ = s.auditRecord(ctx, actor, "asset.server.update", id, "failure", auditPayload{AuthType: string(authType)})
			return Server{}, ErrEncryptionUnavailable
		}
		envelope, err := s.cipher.Encrypt(*input.Secret)
		if err != nil {
			_ = s.auditRecord(ctx, actor, "asset.server.update", id, "failure", auditPayload{AuthType: string(authType)})
			return Server{}, ErrEncryptionUnavailable
		}
		now := s.clock()
		replacement = &StoredCredential{
			ID: uuid.NewString(), AuthType: authType, Envelope: envelope, CreatedAt: now, UpdatedAt: now,
		}
	}
	current.Name = name
	current.Address = address
	current.Username = username
	current.SSHPort = port
	current.UpdatedAt = s.clock()
	updated, err := s.repo.UpdateServer(ctx, current, replacement)
	result := "success"
	if err != nil {
		result = "failure"
	}
	payload := auditPayload{}
	if credentialMutation {
		payload.AuthType = string(authType)
	}
	if auditErr := s.auditRecord(ctx, actor, "asset.server.update", id, result, payload); err == nil && auditErr != nil {
		return updated, auditErr
	}
	return updated, err
}

func (s *Service) Delete(ctx context.Context, actor, id string) error {
	err := s.repo.DeleteServer(ctx, id)
	result := "success"
	if err != nil {
		result = "failure"
	}
	if auditErr := s.auditRecord(ctx, actor, "asset.server.delete", id, result, auditPayload{}); err == nil && auditErr != nil {
		return auditErr
	}
	return err
}

func (s *Service) List(ctx context.Context) ([]Server, error) {
	return s.repo.ListServers(ctx)
}

func (s *Service) Get(ctx context.Context, id string) (Server, error) {
	return s.repo.GetServer(ctx, id)
}

func (s *Service) TestConnection(ctx context.Context, actor, id string) (ConnectionResult, error) {
	server, err := s.repo.GetServer(ctx, id)
	if err != nil {
		return ConnectionResult{}, err
	}
	result, err := s.probe(ctx, server)
	auditResult := "success"
	if err != nil {
		auditResult = "failure"
		var hostKeyErr *HostKeyError
		if errors.As(err, &hostKeyErr) {
			auditResult = "host_key_confirmation_required"
		}
	}
	if auditErr := s.auditRecord(ctx, actor, "asset.server.test_connection", id, auditResult, auditPayload{Fingerprint: result.Fingerprint}); err == nil && auditErr != nil {
		return result, auditErr
	}
	return result, err
}

func (s *Service) ConfirmHostKey(ctx context.Context, actor, id, fingerprint string) error {
	fingerprint, err := validateTextField("fingerprint", fingerprint)
	if err != nil {
		return err
	}
	server, err := s.repo.GetServer(ctx, id)
	if err != nil {
		return err
	}
	observed, probeErr := s.remote.ProbeHostKey(ctx, remoteTarget(server))
	var hostKeyErr *HostKeyError
	if observed == "" && errors.As(probeErr, &hostKeyErr) {
		observed = hostKeyErr.Actual
	}
	if probeErr != nil && !errors.As(probeErr, &hostKeyErr) {
		_ = s.auditRecord(ctx, actor, "asset.server.confirm_host_key", id, "failure", auditPayload{})
		return probeErr
	}
	if strings.TrimSpace(observed) == "" || observed != fingerprint {
		_ = s.auditRecord(ctx, actor, "asset.server.confirm_host_key", id, "failure", auditPayload{})
		return invalidInput("fingerprint does not match the probed host key")
	}
	err = s.repo.ConfirmHostKey(ctx, id, observed, s.clock())
	result := "success"
	if err != nil {
		result = "failure"
	}
	if auditErr := s.auditRecord(ctx, actor, "asset.server.confirm_host_key", id, result, auditPayload{Fingerprint: observed}); err == nil && auditErr != nil {
		return auditErr
	}
	return err
}

func (s *Service) Collect(ctx context.Context, actor, id string) (Snapshot, []SoftwareItem, error) {
	server, err := s.repo.GetServer(ctx, id)
	if err != nil {
		return Snapshot{}, nil, err
	}
	if !credentialCipherAvailable(s.cipher) {
		return Snapshot{}, nil, ErrEncryptionUnavailable
	}
	credential, err := s.repo.GetCredential(ctx, server.CredentialID)
	if err != nil {
		return Snapshot{}, nil, err
	}
	secret, err := s.cipher.Decrypt(credential.Envelope)
	if err != nil {
		_ = s.repo.MarkCollectionFailure(ctx, id, ServerError, collectionFailureMessage, s.clock())
		_ = s.auditRecord(ctx, actor, "asset.server.collect", id, "failure", auditPayload{})
		return Snapshot{}, nil, ErrEncryptionUnavailable
	}
	snapshot, software, collectErr := s.collector.Collect(ctx, server, secret)
	clearCredentialSecret(&secret)
	if collectErr != nil {
		markErr := s.repo.MarkCollectionFailure(ctx, id, ServerError, collectionFailureMessage, s.clock())
		_ = s.auditRecord(ctx, actor, "asset.server.collect", id, "failure", auditPayload{})
		if markErr != nil {
			return Snapshot{}, nil, markErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Snapshot{}, nil, ctxErr
		}
		return Snapshot{}, nil, errors.New(collectionFailureMessage)
	}
	if err := s.repo.SaveCollection(ctx, server, snapshot, software); err != nil {
		_ = s.auditRecord(ctx, actor, "asset.server.collect", id, "failure", auditPayload{})
		return Snapshot{}, nil, err
	}
	if err := s.auditRecord(ctx, actor, "asset.server.collect", id, "success", auditPayload{}); err != nil {
		return snapshot, software, err
	}
	return snapshot, software, nil
}

func (s *Service) LatestSnapshot(ctx context.Context, id string) (Snapshot, error) {
	return s.repo.LatestSnapshot(ctx, id)
}

func (s *Service) Software(ctx context.Context, id string) ([]SoftwareItem, error) {
	return s.repo.ListLatestSoftware(ctx, id)
}

func (s *Service) probe(ctx context.Context, server Server) (ConnectionResult, error) {
	if s.remote == nil {
		return ConnectionResult{}, errors.New("asset remote transport is unavailable")
	}
	fingerprint, err := s.remote.ProbeHostKey(ctx, remoteTarget(server))
	result := ConnectionResult{Fingerprint: fingerprint, Trusted: err == nil}
	var hostKeyErr *HostKeyError
	if errors.As(err, &hostKeyErr) {
		if result.Fingerprint == "" {
			result.Fingerprint = hostKeyErr.Actual
		}
		result.Changed = hostKeyErr.Changed
	}
	return result, err
}

func remoteTarget(server Server) RemoteTarget {
	return RemoteTarget{Address: server.Address, Port: server.SSHPort, Username: server.Username, ExpectedFingerprint: server.HostKeyFingerprint}
}

type auditPayload struct {
	AuthType    string `json:"authType,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func (s *Service) auditRecord(ctx context.Context, actor, action, target, result string, payload auditPayload) error {
	if s.audit == nil {
		return errors.New("asset audit repository is unavailable")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return errors.New("encode asset audit payload")
	}
	_, err = s.audit.Append(ctx, audit.Record{
		Actor: actor, Action: action, Target: target, Result: result, Payload: string(encoded), Timestamp: s.clock(),
	})
	if err != nil {
		return errors.New("append asset audit record")
	}
	return nil
}

func (s *Service) clock() time.Time {
	if s.now == nil {
		return time.Now().UTC()
	}
	return s.now().UTC()
}

func validateServerFields(name, address, username string, port int, zeroMeansDefault bool) (string, string, string, int, error) {
	var err error
	if name, err = validateTextField("name", name); err != nil {
		return "", "", "", 0, err
	}
	if address, err = validateTextField("address", address); err != nil {
		return "", "", "", 0, err
	}
	if username, err = validateTextField("username", username); err != nil {
		return "", "", "", 0, err
	}
	if port == 0 && zeroMeansDefault {
		port = 22
	}
	if port < 1 || port > 65535 {
		return "", "", "", 0, invalidInput("SSH port must be between 1 and 65535")
	}
	return name, address, username, port, nil
}

func validateTextField(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", invalidInput(field + " is required")
	}
	for _, r := range value {
		if r == '\x00' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", invalidInput(field + " contains unsupported characters")
		}
	}
	return value, nil
}

func validateCredential(authType CredentialAuthType, secret CredentialSecret) error {
	switch authType {
	case AuthPassword:
		if secret.Password == "" || secret.PrivateKey != "" || secret.Passphrase != "" {
			return invalidInput("password authentication requires only a password")
		}
	case AuthPrivateKey:
		if secret.PrivateKey == "" || secret.Password != "" {
			return invalidInput("private-key authentication requires a private key and optional passphrase")
		}
	default:
		return invalidInput("unsupported credential authentication type")
	}
	return nil
}

func invalidInput(message string) error {
	return fmt.Errorf("%s: %w", message, ErrInvalidInput)
}

func credentialCipherAvailable(value CredentialCipher) bool {
	if value == nil {
		return false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !rv.IsNil()
	default:
		return true
	}
}

func clearCredentialSecret(secret *CredentialSecret) {
	secret.Password = ""
	secret.PrivateKey = ""
	secret.Passphrase = ""
}
