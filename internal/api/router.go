package api

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"

	"github.com/cyberm-tech/vphone-web/internal/auth"
	"github.com/go-chi/chi/v5"
)

// Router builds the top-level HTTP handler: the /api/v1 surface plus frontend
// serving. When cfg.Server.DevProxy is set, non-API routes are reverse-proxied
// to the Vite dev server; otherwise the embedded static build is served with
// SPA fallback to index.html.
func (s *Server) Router(staticFS fs.FS) http.Handler {
	r := chi.NewRouter()
	r.Use(requestLogger(s.log))
	r.Use(recoverer(s.log))
	// Resolve the session (if any) into the request context; never rejects.
	r.Use(s.auth.Middleware)

	// Convenience wrappers so role gating is a no-op when auth is disabled.
	admin := s.auth.RequireRole(auth.RoleAdmin)
	user := s.auth.RequireAuth

	r.Route("/api/v1", func(r chi.Router) {
		// Auth endpoints (open — the login flow itself).
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", s.login)
			r.Post("/logout", s.logout)
			r.Get("/me", s.me)
			r.Get("/providers", s.providers)
		})

		// Agent control surface — authenticated by the cluster system password,
		// NOT the user session (this is the controller↔worker link).
		r.With(s.requireSystemPassword).Get("/agent/health", s.agentHealth)

		// User administration (admin only).
		r.Group(func(r chi.Router) {
			r.Use(admin)
			r.Get("/users", s.listUsers)
			r.Post("/users", s.createUser)
			r.Patch("/users/{id}", s.updateUser)
			r.Delete("/users/{id}", s.deleteUser)
		})

		// Cluster node registry (admin only).
		r.Group(func(r chi.Router) {
			r.Use(admin)
			r.Get("/nodes", s.listNodes)
			r.Post("/nodes", s.registerNode)
			r.Delete("/nodes/{id}", s.deleteNode)
		})

		r.Route("/vms", func(r chi.Router) {
			// Any authenticated user may view and operate VMs.
			r.With(user).Get("/", s.listVMs)
			// Creating/importing VMs is an admin action.
			r.With(admin).Post("/", s.createVM)
			r.With(admin).Post("/import", s.importVM)
			r.Route("/{id}", func(r chi.Router) {
				r.With(user).Get("/", s.getVM)
				r.With(admin).Delete("/", s.deleteVM)
				r.With(user).Post("/boot", s.bootVM)
				r.With(user).Post("/stop", s.stopVM)
				r.With(user).Post("/restart", s.restartVM)
				r.With(user).Get("/vnc", s.vncWS)
				r.With(user).Get("/terminal", s.terminalWS)
				r.With(user).Post("/screenshot", s.screenshot)
				r.With(user).Post("/touch", s.touch)
				r.With(user).Post("/key", s.key)
				r.With(user).Get("/snapshots", s.listSnapshots)
				r.With(user).Post("/snapshots", s.createSnapshot)
				r.With(user).Post("/snapshots/{name}/restore", s.restoreSnapshot)
				r.With(admin).Delete("/snapshots/{name}", s.deleteSnapshot)
			})
		})

		// IPSW library management is admin-only; listing is available to users.
		r.Route("/ipsws", func(r chi.Router) {
			r.With(user).Get("/", s.listIPSWs)
			r.With(admin).Post("/", s.registerIPSW)
			r.With(admin).Post("/download", s.downloadIPSW)
			r.With(admin).Post("/upload", s.uploadIPSW)
			r.With(admin).Delete("/{id}", s.deleteIPSW)
		})

		r.Route("/jobs", func(r chi.Router) {
			r.With(user).Get("/", s.listJobs)
			r.Route("/{id}", func(r chi.Router) {
				r.With(user).Get("/", s.getJob)
				r.With(admin).Post("/cancel", s.cancelJob)
				r.With(user).Get("/logs", s.jobLogsWS)
			})
		})

		r.With(user).Get("/system/status", s.systemStatusHandler)
		r.With(admin).Get("/system/config", s.systemConfigHandler)
	})

	if s.cfg.Server.DevProxy != "" {
		r.NotFound(s.devProxyHandler())
	} else {
		r.NotFound(s.staticHandler(staticFS))
	}

	return r
}

// devProxyHandler reverse-proxies non-API requests to the Vite dev server,
// preserving WebSocket upgrades for HMR.
func (s *Server) devProxyHandler() http.HandlerFunc {
	target, err := url.Parse(s.cfg.Server.DevProxy)
	if err != nil {
		s.log.Error("invalid dev_proxy url; serving 502", "url", s.cfg.Server.DevProxy, "err", err)
		return func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "invalid dev proxy configuration", http.StatusBadGateway)
		}
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		s.log.Warn("dev proxy error", "path", r.URL.Path, "err", err)
		http.Error(w, "dev server unreachable — is `vite` running on "+s.cfg.Server.DevProxy+"?", http.StatusBadGateway)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		rp.ServeHTTP(w, r)
	}
}

// staticHandler serves the embedded frontend build, falling back to index.html
// for client-side routes (SPA history mode).
func (s *Server) staticHandler(staticFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(staticFS))
	return func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if clean == "" {
			clean = "index.html"
		}
		if f, err := staticFS.Open(clean); err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for unknown non-asset paths.
		index, err := staticFS.Open("index.html")
		if err != nil {
			http.Error(w, "frontend not built", http.StatusNotFound)
			return
		}
		defer index.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.Copy(w, index)
	}
}
