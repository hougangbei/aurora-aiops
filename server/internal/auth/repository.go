package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrUserNotFound   = errors.New("user not found")
	ErrUserExists     = errors.New("user already exists")
	ErrSessionMissing = errors.New("session missing")
)

// Repository persists platform users and sessions.
type Repository interface {
	CreateUser(context.Context, User) error
	FindUserByUsername(context.Context, string) (User, error)
	CreateSession(context.Context, Session) error
	FindSessionByDigest(context.Context, string, time.Time) (User, error)
	FindSessionByUserID(context.Context, string) (Session, error)
	DeleteSession(context.Context, string) error
	CountUsers(context.Context) (int, error)
}

type sqlRepository struct {
	db *sql.DB
}

// NewRepository returns a Repository backed by the given database.
func NewRepository(db *sql.DB) Repository {
	return &sqlRepository{db: db}
}

func (r *sqlRepository) CreateUser(ctx context.Context, user User) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, role, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, string(user.Role),
		boolToInt(user.Enabled),
		user.CreatedAt.UTC().Format(time.RFC3339Nano),
		user.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrUserExists
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (r *sqlRepository) FindUserByUsername(ctx context.Context, username string) (User, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, role, enabled, created_at, updated_at
		FROM users WHERE username = ?`, username)
	return r.scanUser(row)
}

func (r *sqlRepository) CountUsers(ctx context.Context) (int, error) {
	var count int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func (r *sqlRepository) CreateSession(ctx context.Context, session Session) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (digest, user_id, expires_at, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)`,
		session.Digest, session.UserID,
		session.ExpiresAt.UTC().Format(time.RFC3339Nano),
		session.CreatedAt.UTC().Format(time.RFC3339Nano),
		session.LastSeenAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// FindSessionByDigest returns the user owning a valid, unexpired session.
// Sessions that expired at or before now are treated as missing.
func (r *sqlRepository) FindSessionByDigest(ctx context.Context, digest string, now time.Time) (User, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.password_hash, u.role, u.enabled, u.created_at, u.updated_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.digest = ? AND s.expires_at > ? AND u.enabled = 1`,
		digest, now.UTC().Format(time.RFC3339Nano))
	return r.scanUser(row)
}

// FindSessionByUserID returns the most recent session digest for a user.
// It is used by tests and admin session management; it returns the stored
// digest, never the raw session value.
func (r *sqlRepository) FindSessionByUserID(ctx context.Context, userID string) (Session, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT digest, user_id, expires_at, created_at, last_seen_at
		FROM sessions WHERE user_id = ? ORDER BY created_at DESC LIMIT 1`, userID)
	var s Session
	var expiresAt, createdAt, lastSeenAt string
	if err := row.Scan(&s.Digest, &s.UserID, &expiresAt, &createdAt, &lastSeenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrSessionMissing
		}
		return Session{}, fmt.Errorf("scan session: %w", err)
	}
	var err error
	if s.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt); err != nil {
		return Session{}, fmt.Errorf("parse expires_at: %w", err)
	}
	if s.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return Session{}, fmt.Errorf("parse created_at: %w", err)
	}
	if s.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt); err != nil {
		return Session{}, fmt.Errorf("parse last_seen_at: %w", err)
	}
	return s, nil
}

func (r *sqlRepository) DeleteSession(ctx context.Context, digest string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE digest = ?`, digest); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func (r *sqlRepository) scanUser(row *sql.Row) (User, error) {
	var id, username, passwordHash, role, createdAtStr, updatedAtStr string
	var enabled int
	if err := row.Scan(&id, &username, &passwordHash, &role, &enabled, &createdAtStr, &updatedAtStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtStr)
	if err != nil {
		return User{}, fmt.Errorf("parse created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtStr)
	if err != nil {
		return User{}, fmt.Errorf("parse updated_at: %w", err)
	}
	return User{
		ID:           id,
		Username:     username,
		PasswordHash: passwordHash,
		Role:         Role(role),
		Enabled:      boolToBool(enabled),
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func boolToBool(v int) bool {
	return v == 1
}
