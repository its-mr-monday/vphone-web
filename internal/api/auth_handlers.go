package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cyberm-tech/vphone-web/internal/auth"
	"github.com/go-chi/chi/v5"
)

// ssoProvider is a redirect-based login option shown on the login page.
type ssoProvider struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	LoginURL string `json:"login_url"`
}

// authStatus is the GET /api/v1/auth/me and /providers response.
type authStatus struct {
	Enabled   bool          `json:"enabled"`
	User      *auth.User    `json:"user,omitempty"`
	Providers []string      `json:"providers,omitempty"`
	SSO       []ssoProvider `json:"sso,omitempty"`
}

// ssoProviders lists the configured redirect-based login options.
func (s *Server) ssoProviders() []ssoProvider {
	var out []ssoProvider
	if s.oidc != nil {
		out = append(out, ssoProvider{Name: "oidc", Label: "Single Sign-On (OIDC)", LoginURL: "/api/v1/auth/oidc/login"})
	}
	if s.saml != nil {
		out = append(out, ssoProvider{Name: "saml", Label: "Single Sign-On (SAML)", LoginURL: "/api/v1/auth/saml/login"})
	}
	return out
}

// me handles GET /api/v1/auth/me — current user (200) or unauthenticated (200 with user null when disabled, 401 when enabled+no session).
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeJSON(w, http.StatusOK, authStatus{Enabled: false})
		return
	}
	u, ok := auth.UserFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, authStatus{Enabled: true, User: u})
}

// providers handles GET /api/v1/auth/providers — for the login page.
func (s *Server) providers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authStatus{
		Enabled:   s.auth.Enabled(),
		Providers: s.auth.Providers(),
		SSO:       s.ssoProviders(),
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// login handles POST /api/v1/auth/login.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled() {
		writeError(w, http.StatusBadRequest, "authentication is disabled")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u, token, err := s.auth.Login(r.Context(), req.Username, req.Password, r.UserAgent(), clientIP(r))
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.auth.SetSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, authStatus{Enabled: true, User: &u})
}

// logout handles POST /api/v1/auth/logout.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		s.auth.Logout(c.Value)
	}
	s.auth.ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- user administration (admin-only) ---

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.auth.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u, err := s.auth.CreateLocalUser(req.Username, req.Password, auth.Role(req.Role), false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

type updateUserRequest struct {
	Role     *string `json:"role,omitempty"`
	Password *string `json:"password,omitempty"`
	Disabled *bool   `json:"disabled,omitempty"`
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Role != nil {
		if err := s.auth.SetRole(id, auth.Role(*req.Role)); err != nil {
			writeError(w, statusFor(err), err.Error())
			return
		}
	}
	if req.Password != nil {
		if err := s.auth.SetPassword(id, *req.Password); err != nil {
			writeError(w, statusFor(err), err.Error())
			return
		}
	}
	if req.Disabled != nil {
		if err := s.auth.SetDisabled(id, *req.Disabled); err != nil {
			writeError(w, statusFor(err), err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.auth.DeleteUser(id); err != nil {
		writeError(w, statusFor(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func statusFor(err error) int {
	if errors.Is(err, auth.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusBadRequest
}

// clientIP extracts a best-effort client IP for session records.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}
