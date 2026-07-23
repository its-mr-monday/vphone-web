package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"time"
)

// oidcStateCookie carries the anti-CSRF state between the login redirect and the
// callback. It is short-lived and httpOnly.
const oidcStateCookie = "vphone_oidc_state"

// randState returns a random opaque state token.
func randState() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// oidcLogin handles GET /api/v1/auth/oidc/login — begin the authorization-code
// flow by redirecting the browser to the identity provider.
func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeError(w, http.StatusNotFound, "OIDC is not configured")
		return
	}
	state := randState()
	http.SetCookie(w, &http.Cookie{
		Name:     oidcStateCookie,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((10 * time.Minute).Seconds()),
	})
	http.Redirect(w, r, s.oidc.AuthCodeURL(state), http.StatusFound)
}

// oidcCallback handles GET /api/v1/auth/oidc/callback — verify state, exchange
// the code, provision the user, start a session, and redirect to the console.
func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeError(w, http.StatusNotFound, "OIDC is not configured")
		return
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		s.ssoFail(w, r, "identity provider returned an error: "+errMsg)
		return
	}
	// Validate anti-CSRF state.
	cookie, err := r.Cookie(oidcStateCookie)
	if err != nil || cookie.Value == "" || cookie.Value != r.URL.Query().Get("state") {
		s.ssoFail(w, r, "invalid or expired login state")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcStateCookie, Value: "", Path: "/", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		s.ssoFail(w, r, "missing authorization code")
		return
	}
	ident, err := s.oidc.Exchange(r.Context(), code)
	if err != nil {
		s.log.Warn("oidc exchange failed", "err", err)
		s.ssoFail(w, r, "single sign-on failed")
		return
	}
	u, token, err := s.auth.LoginExternal(s.oidc.Name(), *ident, r.UserAgent(), clientIP(r))
	if err != nil {
		s.ssoFail(w, r, err.Error())
		return
	}
	s.auth.SetSessionCookie(w, r, token)
	s.log.Info("oidc login", "user", u.Username, "role", u.Role)
	http.Redirect(w, r, "/", http.StatusFound)
}

// samlLogin handles GET /api/v1/auth/saml/login — redirect to the IdP SSO.
func (s *Server) samlLogin(w http.ResponseWriter, r *http.Request) {
	if s.saml == nil {
		writeError(w, http.StatusNotFound, "SAML is not configured")
		return
	}
	dest, err := s.saml.LoginURL("/")
	if err != nil {
		s.log.Warn("saml login url", "err", err)
		s.ssoFail(w, r, "single sign-on failed")
		return
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// samlACS handles POST /api/v1/auth/saml/acs — the Assertion Consumer Service.
func (s *Server) samlACS(w http.ResponseWriter, r *http.Request) {
	if s.saml == nil {
		writeError(w, http.StatusNotFound, "SAML is not configured")
		return
	}
	ident, err := s.saml.ParseResponse(r)
	if err != nil {
		s.log.Warn("saml acs parse failed", "err", err)
		s.ssoFail(w, r, "single sign-on failed")
		return
	}
	u, token, err := s.auth.LoginExternal(s.saml.Name(), *ident, r.UserAgent(), clientIP(r))
	if err != nil {
		s.ssoFail(w, r, err.Error())
		return
	}
	s.auth.SetSessionCookie(w, r, token)
	s.log.Info("saml login", "user", u.Username, "role", u.Role)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ssoFail redirects back to the login page with an error message in the query.
func (s *Server) ssoFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(msg), http.StatusFound)
}
