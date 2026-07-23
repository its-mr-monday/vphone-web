package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// SessionCookie is the cookie name carrying the session token.
const SessionCookie = "vphone_session"

type ctxKey int

const userKey ctxKey = 0

// UserFrom returns the authenticated user from the request context, if any.
func UserFrom(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(userKey).(*User)
	return u, ok
}

// Middleware resolves the session cookie (if present) and attaches the user to
// the request context. It never rejects — use RequireAuth/RequireRole to gate.
// When auth is disabled it is a pass-through.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Enabled {
			if c, err := r.Cookie(SessionCookie); err == nil {
				if u, err := s.ResolveSession(c.Value); err == nil {
					r = r.WithContext(context.WithValue(r.Context(), userKey, &u))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAuth rejects unauthenticated requests when auth is enabled.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.opts.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := UserFrom(r.Context()); !ok {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireRole rejects requests whose user lacks the required role. Implies auth.
func (s *Service) RequireRole(required Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.opts.Enabled {
				next.ServeHTTP(w, r)
				return
			}
			u, ok := UserFrom(r.Context())
			if !ok {
				writeJSONError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if !u.Role.AtLeast(required) {
				writeJSONError(w, http.StatusForbidden, "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SetSessionCookie writes the session cookie on login.
func (s *Service) SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		Expires:  time.Now().Add(s.opts.SessionTTL),
		MaxAge:   int(s.opts.SessionTTL.Seconds()),
	})
}

// ClearSessionCookie removes the session cookie on logout.
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
