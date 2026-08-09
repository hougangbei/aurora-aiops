package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/hougangbei/aurora-aiops/server/internal/store"
)

func openTestRepo(t *testing.T) Repository {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return NewRepository(db)
}

func createAdminUser(t *testing.T, repo Repository) {
	t.Helper()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateUser(context.Background(), User{
		ID:           "user-1",
		Username:     "admin",
		PasswordHash: string(passwordHash),
		Role:         RoleAdmin,
		Enabled:      true,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLoginStoresOnlySessionDigest(t *testing.T) {
	repo := openTestRepo(t)
	createAdminUser(t, repo)

	svc := NewService(repo, 8*time.Hour, time.Now)
	raw, user, err := svc.Login(context.Background(), "admin", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != RoleAdmin {
		t.Fatalf("role=%s", user.Role)
	}

	stored, err := repo.FindSessionByUserID(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Digest == raw || strings.Contains(stored.Digest, raw) {
		t.Fatal("raw session was stored")
	}
	if len(raw) < 40 {
		t.Fatalf("session length=%d", len(raw))
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	repo := openTestRepo(t)
	createAdminUser(t, repo)
	svc := NewService(repo, 8*time.Hour, time.Now)

	if _, _, err := svc.Login(context.Background(), "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoginAcceptsSixCharacterPassword(t *testing.T) {
	repo := openTestRepo(t)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("123456"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateUser(context.Background(), User{
		ID: "user-six-character-password", Username: "admin", PasswordHash: string(passwordHash),
		Role: RoleAdmin, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, 8*time.Hour, time.Now)
	if _, _, err := svc.Login(context.Background(), "admin", "123456"); err != nil {
		t.Fatalf("six-character password should be accepted: %v", err)
	}
}

func TestLoginUnknownUserSameError(t *testing.T) {
	repo := openTestRepo(t)
	createAdminUser(t, repo)
	svc := NewService(repo, 8*time.Hour, time.Now)

	if _, _, err := svc.Login(context.Background(), "ghost", "whatever-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoginRejectsDisabledUser(t *testing.T) {
	repo := openTestRepo(t)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateUser(context.Background(), User{
		ID: "user-2", Username: "disabled", PasswordHash: string(passwordHash),
		Role: RoleViewer, Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, 8*time.Hour, time.Now)

	if _, _, err := svc.Login(context.Background(), "disabled", "correct-password"); !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("err=%v", err)
	}
}

func TestLoginValidatesInputLengths(t *testing.T) {
	repo := openTestRepo(t)
	createAdminUser(t, repo)
	svc := NewService(repo, 8*time.Hour, time.Now)

	if _, _, err := svc.Login(context.Background(), "  ", "correct-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("short username err=%v", err)
	}
	if _, _, err := svc.Login(context.Background(), "admin", "short"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("short password err=%v", err)
	}
}

func TestAuthenticateSucceedsAndDetectsExpiry(t *testing.T) {
	repo := openTestRepo(t)
	createAdminUser(t, repo)

	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	svc := NewService(repo, 8*time.Hour, func() time.Time { return now })

	raw, _, err := svc.Login(context.Background(), "admin", "correct-password")
	if err != nil {
		t.Fatal(err)
	}

	user, err := svc.Authenticate(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "admin" {
		t.Fatalf("username=%q", user.Username)
	}

	// Advance past the session TTL: authentication must fail.
	expired := NewService(repo, 8*time.Hour, func() time.Time { return now.Add(9 * time.Hour) })
	if _, err := expired.Authenticate(context.Background(), raw); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired err=%v", err)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	repo := openTestRepo(t)
	createAdminUser(t, repo)
	svc := NewService(repo, 8*time.Hour, time.Now)

	raw, _, err := svc.Login(context.Background(), "admin", "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Logout(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(context.Background(), raw); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("after logout err=%v", err)
	}
}

func TestRolesAreClosedEnum(t *testing.T) {
	for _, role := range []Role{RoleAdmin, RoleOperator, RoleViewer} {
		switch role {
		case RoleAdmin, RoleOperator, RoleViewer:
		default:
			t.Fatalf("unexpected role %q", role)
		}
	}
}

func TestBootstrapAdminCreatesOnlyWhenEmpty(t *testing.T) {
	repo := openTestRepo(t)
	svc := NewService(repo, 8*time.Hour, time.Now)

	if err := svc.BootstrapAdmin(context.Background(), "root", "SuperSecret123"); err != nil {
		t.Fatal(err)
	}
	user, err := repo.FindUserByUsername(context.Background(), "root")
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != RoleAdmin || !user.Enabled {
		t.Fatalf("user=%+v", user)
	}

	// Second bootstrap call must be a no-op when users exist.
	if err := svc.BootstrapAdmin(context.Background(), "other", "AnotherSecret456"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindUserByUsername(context.Background(), "other"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

func TestBootstrapAdminRequiresCredentials(t *testing.T) {
	repo := openTestRepo(t)
	svc := NewService(repo, 8*time.Hour, time.Now)

	if err := svc.BootstrapAdmin(context.Background(), "", "SuperSecret123"); err == nil {
		t.Fatal("expected error for empty username")
	}
	if err := svc.BootstrapAdmin(context.Background(), "root", ""); err == nil {
		t.Fatal("expected error for empty password")
	}
}
