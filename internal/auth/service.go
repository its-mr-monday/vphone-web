package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// Options configure the auth Service.
type Options struct {
	Enabled    bool
	SessionTTL time.Duration
	// RoleMap maps external-provider group names to roles.
	RoleMap map[string]Role
	// DefaultRole is assigned to external users whose groups match nothing.
	DefaultRole Role
	// External providers (LDAP/OIDC/SAML). Local auth is always available.
	Providers []Authenticator
	Logger    *slog.Logger
}

// Service orchestrates local + external authentication and sessions.
type Service struct {
	store *store
	opts  Options
	log   *slog.Logger
}

// NewService constructs the auth service.
func NewService(db *sql.DB, opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SessionTTL <= 0 {
		opts.SessionTTL = 12 * time.Hour
	}
	if opts.DefaultRole == "" {
		opts.DefaultRole = RoleUser
	}
	return &Service{store: &store{db: db}, opts: opts, log: opts.Logger}
}

// Enabled reports whether access control is on.
func (s *Service) Enabled() bool { return s.opts.Enabled }

// Providers returns the names of enabled auth sources (for the login page).
func (s *Service) Providers() []string {
	names := []string{"local"}
	for _, p := range s.opts.Providers {
		names = append(names, p.Name())
	}
	return names
}

// BootstrapAdmin creates the initial admin account if there are no users yet.
// The password is required on first setup; the account must change it on login.
func (s *Service) BootstrapAdmin(username, password string) error {
	if !s.opts.Enabled {
		return nil
	}
	n, err := s.store.countUsers()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if username == "" {
		username = "admin"
	}
	if password == "" {
		return fmt.Errorf("auth enabled but no users exist and bootstrap_password is empty")
	}
	if _, err := s.CreateLocalUser(username, password, RoleAdmin, true); err != nil {
		return err
	}
	s.log.Warn("bootstrap admin created; change the password on first login", "username", username)
	return nil
}

// Login authenticates a username/password against local then external providers,
// creating a session on success. Returns the user and an opaque session token.
func (s *Service) Login(ctx context.Context, username, password, userAgent, ip string) (User, string, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return User{}, "", fmt.Errorf("username and password required")
	}

	existing, err := s.store.userByName(username)
	switch {
	case err == nil && existing.Provider == "local":
		if existing.Disabled {
			return User{}, "", fmt.Errorf("account disabled")
		}
		if bcrypt.CompareHashAndPassword([]byte(existing.PasswordHash), []byte(password)) != nil {
			return User{}, "", fmt.Errorf("invalid credentials")
		}
		return s.startSession(existing, userAgent, ip)
	case err != nil && err != ErrNotFound:
		return User{}, "", err
	}

	// Try external providers (LDAP/OIDC/SAML password grant).
	for _, p := range s.opts.Providers {
		ident, aerr := p.Authenticate(ctx, username, password)
		if aerr != nil || ident == nil {
			continue
		}
		u, perr := s.provisionExternal(p.Name(), *ident)
		if perr != nil {
			return User{}, "", perr
		}
		if u.Disabled {
			return User{}, "", fmt.Errorf("account disabled")
		}
		return s.startSession(u, userAgent, ip)
	}

	return User{}, "", fmt.Errorf("invalid credentials")
}

// provisionExternal creates or updates a user from an external identity, mapping
// groups to a role.
func (s *Service) provisionExternal(provider string, ident ExternalIdentity) (User, error) {
	role := s.mapRole(ident.Groups)
	u, err := s.store.userByName(ident.Username)
	if err == ErrNotFound {
		now := time.Now()
		u = User{
			ID: uuid.NewString(), Username: ident.Username, Email: ident.Email,
			Role: role, Provider: provider, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.store.insertUser(u); err != nil {
			return User{}, err
		}
		return u, nil
	}
	if err != nil {
		return User{}, err
	}
	// Keep role/email in sync with the directory on each login.
	u.Role, u.Email, u.Provider = role, ident.Email, provider
	if err := s.store.updateUser(u); err != nil {
		return User{}, err
	}
	return u, nil
}

// mapRole resolves a role from external group memberships.
func (s *Service) mapRole(groups []string) Role {
	for _, g := range groups {
		if r, ok := s.opts.RoleMap[g]; ok && r.Valid() {
			return r
		}
	}
	return s.opts.DefaultRole
}

func (s *Service) startSession(u User, userAgent, ip string) (User, string, error) {
	token, err := randomToken()
	if err != nil {
		return User{}, "", err
	}
	now := time.Now()
	sess := Session{Token: token, UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(s.opts.SessionTTL)}
	if err := s.store.insertSession(sess, userAgent, ip); err != nil {
		return User{}, "", err
	}
	s.log.Info("login", "user", u.Username, "role", u.Role, "provider", u.Provider)
	return u, token, nil
}

// ResolveSession validates a session token and returns the user, applying a
// sliding expiry. Returns ErrNotFound if the session is missing/expired.
func (s *Service) ResolveSession(token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	sess, err := s.store.sessionByToken(token)
	if err != nil {
		return User{}, err
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.store.deleteSession(token)
		return User{}, ErrNotFound
	}
	u, err := s.store.userByID(sess.UserID)
	if err != nil {
		return User{}, err
	}
	if u.Disabled {
		_ = s.store.deleteSession(token)
		return User{}, ErrNotFound
	}
	// Slide the expiry when past the halfway point.
	if time.Until(sess.ExpiresAt) < s.opts.SessionTTL/2 {
		_ = s.store.touchSession(token, time.Now().Add(s.opts.SessionTTL))
	}
	return u, nil
}

// Logout revokes a session.
func (s *Service) Logout(token string) { _ = s.store.deleteSession(token) }

// SessionTTL exposes the configured TTL (for cookie MaxAge).
func (s *Service) SessionTTL() time.Duration { return s.opts.SessionTTL }

// --- user administration (admin-only at the API layer) ---

// CreateLocalUser creates a local (password) user.
func (s *Service) CreateLocalUser(username, password string, role Role, mustChange bool) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, fmt.Errorf("username required")
	}
	if !role.Valid() {
		return User{}, fmt.Errorf("invalid role %q", role)
	}
	if len(password) < 6 {
		return User{}, fmt.Errorf("password must be at least 6 characters")
	}
	if _, err := s.store.userByName(username); err == nil {
		return User{}, fmt.Errorf("user %q already exists", username)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	now := time.Now()
	u := User{
		ID: uuid.NewString(), Username: username, Role: role, Provider: "local",
		PasswordHash: string(hash), MustChange: mustChange, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.insertUser(u); err != nil {
		return User{}, err
	}
	return u, nil
}

// ListUsers returns all users.
func (s *Service) ListUsers() ([]User, error) { return s.store.listUsers() }

// SetRole changes a user's role.
func (s *Service) SetRole(id string, role Role) error {
	if !role.Valid() {
		return fmt.Errorf("invalid role")
	}
	u, err := s.store.userByID(id)
	if err != nil {
		return err
	}
	u.Role = role
	return s.store.updateUser(u)
}

// SetPassword sets a local user's password (clears must-change).
func (s *Service) SetPassword(id, password string) error {
	if len(password) < 6 {
		return fmt.Errorf("password must be at least 6 characters")
	}
	u, err := s.store.userByID(id)
	if err != nil {
		return err
	}
	if u.Provider != "local" {
		return fmt.Errorf("cannot set password for %s user", u.Provider)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	u.MustChange = false
	return s.store.updateUser(u)
}

// SetDisabled enables/disables a user.
func (s *Service) SetDisabled(id string, disabled bool) error {
	u, err := s.store.userByID(id)
	if err != nil {
		return err
	}
	u.Disabled = disabled
	return s.store.updateUser(u)
}

// DeleteUser removes a user and its sessions.
func (s *Service) DeleteUser(id string) error { return s.store.deleteUser(id) }

// PurgeExpiredSessions removes expired sessions (call periodically).
func (s *Service) PurgeExpiredSessions() { s.store.purgeExpired() }

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
