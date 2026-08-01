package auth

import "time"

// Role is the platform role of a user. Kubernetes identity stays separate;
// these roles only gate what platform APIs a user may call.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// User is a platform account. PasswordHash is never serialized to the API.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// Session holds the digest of a session token. Only the SHA-256 digest of the
// raw session value is ever persisted.
type Session struct {
	Digest     string
	UserID     string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}
