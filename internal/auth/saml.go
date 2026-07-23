package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

// SAMLOptions configure the SAML 2.0 provider.
type SAMLOptions struct {
	IDPMetadataURL string // URL of the IdP's SAML metadata
	EntityID       string // this SP's entity ID (metadata URL by convention)
	ACSURL         string // Assertion Consumer Service URL (our callback)
	// Attribute names to read from the assertion. Sensible defaults are applied.
	UsernameAttr string
	EmailAttr    string
	GroupsAttr   string
}

// SAMLProvider implements SP-initiated (and IdP-initiated) browser SSO. Like
// OIDCProvider it is driven by the API layer around an IdP redirect + POST-back.
type SAMLProvider struct {
	sp   saml.ServiceProvider
	opts SAMLOptions
}

// NewSAML fetches the IdP metadata and builds the service provider. No SP
// signing key is required for sign-in-only (assertions are verified against the
// IdP certificate in the metadata).
func NewSAML(ctx context.Context, opts SAMLOptions) (*SAMLProvider, error) {
	if opts.IDPMetadataURL == "" || opts.ACSURL == "" {
		return nil, fmt.Errorf("saml requires idp_metadata_url and acs_url")
	}
	if opts.UsernameAttr == "" {
		opts.UsernameAttr = "uid"
	}
	if opts.EmailAttr == "" {
		opts.EmailAttr = "mail"
	}
	if opts.GroupsAttr == "" {
		opts.GroupsAttr = "groups"
	}
	mdURL, err := url.Parse(opts.IDPMetadataURL)
	if err != nil {
		return nil, fmt.Errorf("saml idp_metadata_url: %w", err)
	}
	acs, err := url.Parse(opts.ACSURL)
	if err != nil {
		return nil, fmt.Errorf("saml acs_url: %w", err)
	}
	idpMeta, err := samlsp.FetchMetadata(ctx, http.DefaultClient, *mdURL)
	if err != nil {
		return nil, fmt.Errorf("saml fetch metadata: %w", err)
	}
	entityID := opts.EntityID
	if entityID == "" {
		entityID = opts.ACSURL
	}
	sp := saml.ServiceProvider{
		EntityID:          entityID,
		AcsURL:            *acs,
		IDPMetadata:       idpMeta,
		AllowIDPInitiated: true, // avoid a server-side request-ID store
	}
	return &SAMLProvider{sp: sp, opts: opts}, nil
}

// Name identifies the provider.
func (p *SAMLProvider) Name() string { return "saml" }

// LoginURL builds the IdP SSO redirect URL (HTTP-Redirect binding).
func (p *SAMLProvider) LoginURL(relayState string) (string, error) {
	u, err := p.sp.MakeRedirectAuthenticationRequest(relayState)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// ParseResponse validates a POSTed SAML response and extracts the identity.
func (p *SAMLProvider) ParseResponse(r *http.Request) (*ExternalIdentity, error) {
	assertion, err := p.sp.ParseResponse(r, nil)
	if err != nil {
		return nil, fmt.Errorf("saml parse response: %w", err)
	}
	return p.identityFromAssertion(assertion), nil
}

// identityFromAssertion maps assertion attributes to an ExternalIdentity.
func (p *SAMLProvider) identityFromAssertion(a *saml.Assertion) *ExternalIdentity {
	attrs := map[string][]string{}
	for _, st := range a.AttributeStatements {
		for _, at := range st.Attributes {
			var vals []string
			for _, v := range at.Values {
				if v.Value != "" {
					vals = append(vals, v.Value)
				}
			}
			// Index by both Name and FriendlyName so config can use either.
			if at.Name != "" {
				attrs[at.Name] = append(attrs[at.Name], vals...)
			}
			if at.FriendlyName != "" {
				attrs[at.FriendlyName] = append(attrs[at.FriendlyName], vals...)
			}
		}
	}
	first := func(key string) string {
		if v := attrs[key]; len(v) > 0 {
			return v[0]
		}
		return ""
	}

	username := first(p.opts.UsernameAttr)
	email := first(p.opts.EmailAttr)
	if username == "" {
		username = email
	}
	// Fall back to the Subject NameID.
	if username == "" && a.Subject != nil && a.Subject.NameID != nil {
		username = a.Subject.NameID.Value
	}
	username = strings.TrimSpace(username)

	return &ExternalIdentity{
		Username: username,
		Email:    email,
		Groups:   attrs[p.opts.GroupsAttr],
	}
}
