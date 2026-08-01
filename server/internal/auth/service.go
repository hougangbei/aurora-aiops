package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrInvalidCredentials is returned for both unknown users and wrong
	// passwords so callers cannot distinguish which part failed.
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUserDisabled       = errors.New("user disabled")
	ErrInvalidSession     = errors.New("invalid session")
)

// Service implements platform account login, session authentication, and
// bootstrap-admin creation.
type Service struct {
	repo       Repository
	sessionTTL time.Duration
	now        func() time.Time
}

// NewService returns a Service. sessionTTL bounds session lifetime; now is
// injectable for deterministic expiry tests.
func NewService(repo Repository, sessionTTL time.Duration, now func() time.Time) *Service {
	return &Service{repo: repo, sessionTTL: sessionTTL, now: now}
}

// Login verifies username and password and returns the raw session value plus
// the authenticated user. The raw value is returned to the caller exactly once
// and must be stored in an HttpOnly Cookie; only its digest is persisted.
func (s *Service) Login(ctx context.Context, username, password string) (rawSession string, user User, err error) {
	username = strings.TrimSpace(username)
	if len(username) < 3 || len(username) > 64 {
		return "", User{}, ErrInvalidCredentials
	}
	if len(password) < 8 || len(password) > 128 {
		return "", User{}, ErrInvalidCredentials
	}

	user, err = s.repo.FindUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return "", User{}, ErrInvalidCredentials
		}
		return "", User{}, err
	}
	if !user.Enabled {
		return "", User{}, ErrUserDisabled
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", User{}, ErrInvalidCredentials
	}

	raw, err := generateSessionValue()
	if err != nil {
		return "", User{}, fmt.Errorf("generate session: %w", err)
	}
	now := s.now().UTC()
	session := Session{
		Digest:     sessionDigest(raw),
		UserID:     user.ID,
		ExpiresAt:  now.Add(s.sessionTTL),
		CreatedAt:  now,
		LastSeenAt: now,
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return "", User{}, err
	}
	return raw, user, nil
}

// Authenticate resolves a raw session value to its user. Expired or unknown
// sessions both yield ErrInvalidSession.
func (s *Service) Authenticate(ctx context.Context, rawSession string) (User, error) {
	if strings.TrimSpace(rawSession) == "" {
		return User{}, ErrInvalidSession
	}
	user, err := s.repo.FindSessionByDigest(ctx, sessionDigest(rawSession), s.now())
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return User{}, ErrInvalidSession
		}
		return User{}, err
	}
	return user, nil
}

// SessionExpiry returns the expiry time for a session created at this moment.
// It mirrors the TTL applied inside Login so handlers can report the same
// expiry without a second database read.
func (s *Service) SessionExpiry() time.Time {
	return s.now().UTC().Add(s.sessionTTL)
}

// Logout removes the session identified by rawSession. Removing an already
// unknown session is not an error.
func (s *Service) Logout(ctx context.Context, rawSession string) error {
	if strings.TrimSpace(rawSession) == "" {
		return nil
	}
	return s.repo.DeleteSession(ctx, sessionDigest(rawSession))
}

// BootstrapAdmin creates the first admin account only when the users table is
// empty. When users already exist it is a no-op. Missing credentials on an
// empty database is an error and the message never includes the password.
func (s *Service) BootstrapAdmin(ctx context.Context, username, password string) error {
	count, err := s.repo.CountUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return errors.New("bootstrap admin requires KUBEJOJO_BOOTSTRAP_ADMIN_USER and KUBEJOJO_BOOTSTRAP_ADMIN_PASSWORD")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	now := s.now().UTC()
	return s.repo.CreateUser(ctx, User{
		ID:           "bootstrap-admin",
		Username:     username,
		PasswordHash: string(passwordHash),
		Role:         RoleAdmin,
		Enabled:      true,
		CreatedAt:    now,
		UpdatedAt:    now,
	})
}

// generateSessionValue returns a 32-byte random value encoded as
// URL-safe base64 without padding (43 characters).
func generateSessionValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sessionDigest returns the lowercase hex SHA-256 digest of a raw session
// value. This digest is the only representation stored in the database.
func sessionDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
