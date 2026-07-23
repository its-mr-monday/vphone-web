package auth

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/cyberm-tech/vphone-web/internal/db"
	"github.com/go-ldap/ldap/v3"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	return sqlDB
}

// fakeLDAP is an in-memory directory implementing ldapConn for tests — no server.
type fakeLDAP struct {
	// users: username -> (dn, password, mail, memberOf)
	userDN   string
	password string
	mail     string
	memberOf []string
	// bind tracking
	lastBindDN string
	serviceOK  bool // whether service bind should succeed
}

func (f *fakeLDAP) Bind(dn, pw string) error {
	f.lastBindDN = dn
	// Service/anon bind: accept the configured service account.
	if dn == "cn=svc,dc=corp" {
		if pw == "svcpass" {
			return nil
		}
		return fmt.Errorf("service bind failed")
	}
	// User bind: verify password against the fake user.
	if dn == f.userDN {
		if pw == f.password {
			return nil
		}
		return fmt.Errorf("invalid password")
	}
	return fmt.Errorf("unknown dn %q", dn)
}

func (f *fakeLDAP) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	// User search returns the single fake user entry.
	entry := &ldap.Entry{
		DN: f.userDN,
		Attributes: []*ldap.EntryAttribute{
			{Name: "mail", Values: []string{f.mail}},
			{Name: "memberOf", Values: f.memberOf},
		},
	}
	return &ldap.SearchResult{Entries: []*ldap.Entry{entry}}, nil
}

func (f *fakeLDAP) Close() error { return nil }

func newTestLDAP(f *fakeLDAP) *ldapProvider {
	return &ldapProvider{
		opts: LDAPOptions{
			URL:        "ldap://fake",
			BindDN:     "cn=svc,dc=corp",
			BindPassword: "svcpass",
			BaseDN:     "dc=corp",
			UserFilter: "(uid=%s)",
		},
		dial: func(string) (ldapConn, error) { return f, nil },
	}
}

func TestLDAPAuthenticateSuccess(t *testing.T) {
	f := &fakeLDAP{
		userDN:   "uid=alice,ou=people,dc=corp",
		password: "s3cret",
		mail:     "alice@corp.example",
		memberOf: []string{"cn=vphone-admins,ou=groups,dc=corp", "CN=all-staff,ou=groups,dc=corp"},
	}
	p := newTestLDAP(f)

	ident, err := p.Authenticate(context.Background(), "alice", "s3cret")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if ident.Username != "alice" || ident.Email != "alice@corp.example" {
		t.Fatalf("unexpected identity: %+v", ident)
	}
	want := map[string]bool{"vphone-admins": true, "all-staff": true}
	if len(ident.Groups) != 2 {
		t.Fatalf("groups = %v, want 2", ident.Groups)
	}
	for _, g := range ident.Groups {
		if !want[g] {
			t.Fatalf("unexpected group %q in %v", g, ident.Groups)
		}
	}
}

func TestLDAPAuthenticateWrongPassword(t *testing.T) {
	f := &fakeLDAP{userDN: "uid=alice,dc=corp", password: "s3cret"}
	p := newTestLDAP(f)
	if _, err := p.Authenticate(context.Background(), "alice", "wrong"); err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestLDAPRoleMappingViaService(t *testing.T) {
	// End-to-end through the Service: LDAP group → role via RoleMap.
	db := newTestDB(t)
	f := &fakeLDAP{
		userDN:   "uid=bob,dc=corp",
		password: "pw123456",
		mail:     "bob@corp.example",
		memberOf: []string{"cn=vphone-admins,dc=corp"},
	}
	svc := NewService(db, Options{
		Enabled:     true,
		RoleMap:     map[string]Role{"vphone-admins": RoleAdmin},
		DefaultRole: RoleUser,
		Providers:   []Authenticator{newTestLDAP(f)},
	})
	u, token, err := svc.Login(context.Background(), "bob", "pw123456", "test", "127.0.0.1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if u.Role != RoleAdmin {
		t.Fatalf("role = %q, want vphone-admin", u.Role)
	}
	if u.Provider != "ldap" {
		t.Fatalf("provider = %q, want ldap", u.Provider)
	}
	if token == "" {
		t.Fatal("expected a session token")
	}
	// Second login should update, not duplicate, the user.
	if _, _, err := svc.Login(context.Background(), "bob", "pw123456", "test", "127.0.0.1"); err != nil {
		t.Fatalf("second login: %v", err)
	}
	users, _ := svc.ListUsers()
	count := 0
	for _, x := range users {
		if x.Username == "bob" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 bob user, got %d", count)
	}
}
