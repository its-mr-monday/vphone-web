package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCOptions configure the OIDC/OAuth2 provider.
type OIDCOptions struct {
	Issuer       string // discovery base URL
	ClientID     string
	ClientSecret string
	RedirectURL  string // must match the callback route registered on the server
	GroupsClaim  string // ID-token claim holding group names (default "groups")
	Scopes       []string
}

// OIDCProvider implements the browser authorization-code flow. It is not an
// Authenticator (no password grant); the API layer drives it via AuthCodeURL +
// Exchange around an IdP redirect.
type OIDCProvider struct {
	opts     OIDCOptions
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
}

// NewOIDC performs OIDC discovery against the issuer and builds the provider.
func NewOIDC(ctx context.Context, opts OIDCOptions) (*OIDCProvider, error) {
	if opts.Issuer == "" || opts.ClientID == "" || opts.RedirectURL == "" {
		return nil, fmt.Errorf("oidc requires issuer, client_id and redirect_url")
	}
	if opts.GroupsClaim == "" {
		opts.GroupsClaim = "groups"
	}
	prov, err := oidc.NewProvider(ctx, opts.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	scopes := opts.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "profile", "email", "groups"}
	}
	return &OIDCProvider{
		opts:     opts,
		verifier: prov.Verifier(&oidc.Config{ClientID: opts.ClientID}),
		oauth: &oauth2.Config{
			ClientID:     opts.ClientID,
			ClientSecret: opts.ClientSecret,
			RedirectURL:  opts.RedirectURL,
			Endpoint:     prov.Endpoint(),
			Scopes:       scopes,
		},
	}, nil
}

// Name identifies the provider (used as the User.Provider value).
func (p *OIDCProvider) Name() string { return "oidc" }

// AuthCodeURL builds the IdP authorization URL for the given anti-CSRF state.
func (p *OIDCProvider) AuthCodeURL(state string) string {
	return p.oauth.AuthCodeURL(state)
}

// Exchange completes the flow: swap the code for tokens, verify the ID token,
// and extract the identity (subject/username, email, groups).
func (p *OIDCProvider) Exchange(ctx context.Context, code string) (*ExternalIdentity, error) {
	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("oidc token exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("oidc response missing id_token")
	}
	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("oidc verify id_token: %w", err)
	}

	// Decode the claims we care about; the groups claim key is configurable.
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc claims: %w", err)
	}
	return identityFromClaims(claims, p.opts.GroupsClaim, idToken.Subject), nil
}

// identityFromClaims maps OIDC ID-token claims to an ExternalIdentity. Username
// prefers preferred_username, then email, then the subject.
func identityFromClaims(claims map[string]any, groupsClaim, subject string) *ExternalIdentity {
	str := func(k string) string {
		if v, ok := claims[k].(string); ok {
			return v
		}
		return ""
	}
	username := str("preferred_username")
	email := str("email")
	if username == "" {
		username = email
	}
	if username == "" {
		username = subject
	}
	return &ExternalIdentity{
		Username: username,
		Email:    email,
		Groups:   toStringSlice(claims[groupsClaim]),
	}
}

// toStringSlice coerces a claim value that may be []any, []string, or a single
// space/comma-delimited string into a []string of group names.
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		sep := ","
		if !strings.Contains(t, ",") && strings.Contains(t, " ") {
			sep = " "
		}
		parts := strings.Split(t, sep)
		out := make([]string, 0, len(parts))
		for _, s := range parts {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
