package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/go-ldap/ldap/v3"
)

// ldapConn is the subset of *ldap.Conn used by the provider, extracted so tests
// can inject a fake directory without a live server.
type ldapConn interface {
	Bind(username, password string) error
	Search(*ldap.SearchRequest) (*ldap.SearchResult, error)
	Close() error
}

// LDAPOptions configure the LDAP authenticator.
type LDAPOptions struct {
	URL          string // ldap://host:389 or ldaps://host:636
	BindDN       string // service account for the user search ("" → anonymous)
	BindPassword string
	BaseDN       string // search base for users (and groups)
	UserFilter   string // e.g. (uid=%s) or (sAMAccountName=%s); %s = username
	GroupFilter  string // optional, e.g. (member=%s); %s = the user's DN
	Insecure     bool   // skip TLS cert verification (labs / self-signed)
}

// ldapProvider authenticates by search-then-bind: bind a service account (or
// anonymously), find the user entry, then bind as that user to verify the
// password. Group membership (memberOf + optional group search) drives roles.
type ldapProvider struct {
	opts LDAPOptions
	// dial is overridable in tests to inject a fake connection.
	dial func(url string) (ldapConn, error)
}

// NewLDAP builds an LDAP Authenticator from options.
func NewLDAP(opts LDAPOptions) (Authenticator, error) {
	if opts.URL == "" || opts.BaseDN == "" || opts.UserFilter == "" {
		return nil, fmt.Errorf("ldap requires url, base_dn and user_filter")
	}
	if !strings.Contains(opts.UserFilter, "%s") {
		return nil, fmt.Errorf("ldap user_filter must contain %%s")
	}
	p := &ldapProvider{opts: opts}
	p.dial = func(url string) (ldapConn, error) {
		return ldap.DialURL(url, ldap.DialWithTLSConfig(&tls.Config{InsecureSkipVerify: opts.Insecure})) //nolint:gosec // operator opt-in for labs
	}
	return p, nil
}

func (p *ldapProvider) Name() string { return "ldap" }

// Authenticate verifies username/password against the directory and returns the
// resolved identity (email + group CNs).
func (p *ldapProvider) Authenticate(_ context.Context, username, password string) (*ExternalIdentity, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("missing credentials")
	}
	conn, err := p.dial(p.opts.URL)
	if err != nil {
		return nil, fmt.Errorf("ldap dial: %w", err)
	}
	defer conn.Close()

	// Bind as the service account (or anonymously) to run the user search.
	if p.opts.BindDN != "" {
		if err := conn.Bind(p.opts.BindDN, p.opts.BindPassword); err != nil {
			return nil, fmt.Errorf("ldap service bind: %w", err)
		}
	}

	filter := fmt.Sprintf(p.opts.UserFilter, ldap.EscapeFilter(username))
	res, err := conn.Search(ldap.NewSearchRequest(
		p.opts.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 0, false,
		filter, []string{"dn", "mail", "memberOf", "cn"}, nil,
	))
	if err != nil {
		return nil, fmt.Errorf("ldap search: %w", err)
	}
	if len(res.Entries) == 0 {
		return nil, fmt.Errorf("invalid credentials") // don't leak user existence
	}
	if len(res.Entries) > 1 {
		return nil, fmt.Errorf("user filter matched multiple entries")
	}
	entry := res.Entries[0]

	// Verify the password by binding as the resolved user DN.
	if err := conn.Bind(entry.DN, password); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	groups := groupCNs(entry.GetAttributeValues("memberOf"))

	// Optional explicit group search (e.g. for directories without memberOf).
	if p.opts.GroupFilter != "" && strings.Contains(p.opts.GroupFilter, "%s") {
		if p.opts.BindDN != "" {
			_ = conn.Bind(p.opts.BindDN, p.opts.BindPassword) // rebind service acct
		}
		gf := fmt.Sprintf(p.opts.GroupFilter, ldap.EscapeFilter(entry.DN))
		if gres, gerr := conn.Search(ldap.NewSearchRequest(
			p.opts.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
			gf, []string{"cn"}, nil,
		)); gerr == nil {
			for _, e := range gres.Entries {
				if cn := e.GetAttributeValue("cn"); cn != "" {
					groups = append(groups, cn)
				}
			}
		}
	}

	return &ExternalIdentity{
		Username: username,
		Email:    entry.GetAttributeValue("mail"),
		Groups:   dedupStrings(groups),
	}, nil
}

// groupCNs extracts the CN component from each group DN in a memberOf list.
func groupCNs(dns []string) []string {
	var out []string
	for _, dn := range dns {
		parsed, err := ldap.ParseDN(dn)
		if err != nil || len(parsed.RDNs) == 0 {
			continue
		}
		for _, attr := range parsed.RDNs[0].Attributes {
			if strings.EqualFold(attr.Type, "cn") {
				out = append(out, attr.Value)
			}
		}
	}
	return out
}

func dedupStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
