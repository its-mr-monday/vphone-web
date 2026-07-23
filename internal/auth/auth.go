// Package auth implements access control for vphone-web: users, roles, sessions,
// and pluggable authentication providers (local datastore now; LDAP/OIDC/SAML as
// Authenticator implementations later). Auth is opt-in via config; when disabled
// the middleware is a no-op so single-user installs are unchanged.
package auth

import (
	"context"
	"time"
)

// Role is a coarse authorization level. Start with two; extensible.
type Role string

const (
	RoleAdmin Role = "vphone-admin"
	RoleUser  Role = "vphone-user"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r == RoleAdmin || r == RoleUser }

// AtLeast reports whether this role satisfies the required role (admin ≥ user).
func (r Role) AtLeast(required Role) bool {
	if required == RoleUser {
		return r == RoleUser || r == RoleAdmin
	}
	return r == RoleAdmin
}

// User is an account. External-provider users have an empty PasswordHash.
type User struct {
	ID         string    `json:"id"`
	Username   string    `json:"username"`
	Email      string    `json:"email,omitempty"`
	Role       Role      `json:"role"`
	Provider   string    `json:"provider"`
	Disabled   bool      `json:"disabled"`
	MustChange bool      `json:"must_change,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	PasswordHash string `json:"-"` // never serialized
}

// Session is a login session backing an httpOnly cookie token.
type Session struct {
	Token     string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// ExternalIdentity is what an external Authenticator returns on success. Groups
// drive role mapping.
type ExternalIdentity struct {
	Username string
	Email    string
	Groups   []string
}

// Authenticator verifies credentials against one source. Local is implemented
// here; LDAP/OIDC/SAML implement this interface in their own files.
type Authenticator interface {
	Name() string
	// Authenticate returns the external identity on success, or an error.
	Authenticate(ctx context.Context, username, password string) (*ExternalIdentity, error)
}
