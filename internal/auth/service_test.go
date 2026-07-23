package auth

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cyberm-tech/vphone-web/internal/db"
)

func testService(t *testing.T) *Service {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return NewService(sqlDB, Options{Enabled: true})
}

func TestRoleAtLeast(t *testing.T) {
	if !RoleAdmin.AtLeast(RoleUser) || !RoleAdmin.AtLeast(RoleAdmin) {
		t.Error("admin should satisfy user and admin")
	}
	if RoleUser.AtLeast(RoleAdmin) {
		t.Error("user should NOT satisfy admin")
	}
	if !RoleUser.AtLeast(RoleUser) {
		t.Error("user should satisfy user")
	}
}

func TestLocalLoginAndSession(t *testing.T) {
	s := testService(t)
	if _, err := s.CreateLocalUser("alice", "s3cret!", RoleAdmin, false); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Wrong password rejected.
	if _, _, err := s.Login(context.Background(), "alice", "nope", "", ""); err == nil {
		t.Fatal("expected login failure with wrong password")
	}

	// Correct password → session.
	u, token, err := s.Login(context.Background(), "alice", "s3cret!", "ua", "ip")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.Role != RoleAdmin || token == "" {
		t.Fatalf("unexpected login result: %+v token=%q", u, token)
	}

	// Session resolves to the same user.
	got, err := s.ResolveSession(token)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("session user mismatch")
	}

	// Logout invalidates it.
	s.Logout(token)
	if _, err := s.ResolveSession(token); err != ErrNotFound {
		t.Fatalf("expected session gone after logout, got %v", err)
	}
}

func TestBootstrapAdminOnce(t *testing.T) {
	s := testService(t)
	if err := s.BootstrapAdmin("admin", "changeme"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	users, _ := s.ListUsers()
	if len(users) != 1 || users[0].Role != RoleAdmin || !users[0].MustChange {
		t.Fatalf("expected one must-change admin, got %+v", users)
	}
	// Second bootstrap is a no-op (users already exist).
	if err := s.BootstrapAdmin("admin2", "x"); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	users, _ = s.ListUsers()
	if len(users) != 1 {
		t.Fatalf("bootstrap should not add a second admin")
	}
}

func TestDisabledUserCannotLogin(t *testing.T) {
	s := testService(t)
	u, _ := s.CreateLocalUser("bob", "password1", RoleUser, false)
	if err := s.SetDisabled(u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login(context.Background(), "bob", "password1", "", ""); err == nil {
		t.Fatal("disabled user should not log in")
	}
}
