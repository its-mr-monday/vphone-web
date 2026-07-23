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
	// A valid cluster system password acts as an admin (controller proxying).
	r.Use(s.controllerTrust)

	// Convenience wrappers so role gating is a no-op when auth is disabled.
	admin := s.auth.RequireRole(auth.RoleAdmin)
	user := s.auth.RequireAuth
	// remote adds transparent proxying to the owning node for per-VM routes.
	remote := s.routeRemoteVM

	r.Route("/api/v1", func(r chi.Router) {
		// Auth endpoints (open — the login flow itself).
		r.Route("/auth", func(r chi.Router) {
			r.Post("/login", s.login)
			r.Post("/logout", s.logout)
			r.Get("/me", s.me)
			r.Get("/providers", s.providers)
			// Redirect-based SSO flows (registered only when configured).
			if s.oidc != nil {
				r.Get("/oidc/login", s.oidcLogin)
				r.Get("/oidc/callback", s.oidcCallback)
			}
			if s.saml != nil {
				r.Get("/saml/login", s.samlLogin)
				r.Post("/saml/acs", s.samlACS)
			}
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
			r.Get("/nodes/{id}/ipsws", s.nodeIPSWs)
		})

		r.Route("/vms", func(r chi.Router) {
			// Any authenticated user may view and operate VMs.
			r.With(user).Get("/", s.listVMs)
			// Creating/importing VMs is an admin action.
			r.With(admin).Post("/", s.createVM)
			r.With(admin).Post("/import", s.importVM)
			r.With(admin).Post("/import-bundle", s.importBundle)
			// Per-VM routes: each gate runs, then `remote` proxies to the owning
			// node when the id is remote (else the local handler runs).
			r.Route("/{id}", func(r chi.Router) {
				r.With(user, remote).Get("/", s.getVM)
				r.With(admin, remote).Patch("/", s.updateVM)
				r.With(admin, remote).Get("/export", s.exportVM)
				r.With(admin, remote).Delete("/", s.deleteVM)
				r.With(user, remote).Post("/boot", s.bootVM)
				r.With(user, remote).Post("/stop", s.stopVM)
				r.With(user, remote).Post("/restart", s.restartVM)
				r.With(user, remote).Get("/vnc", s.vncWS)
				r.With(user, remote).Get("/terminal", s.terminalWS)
				r.With(user, remote).Get("/info", s.guestInfo)
				r.With(user, remote).Get("/frida", s.fridaStatus)
				r.With(user, remote).Get("/frida/processes", s.fridaProcesses)
				r.With(admin, remote).Post("/frida/install", s.fridaInstall)
				r.With(user, remote).Post("/frida/start", s.fridaStart)
				r.With(user, remote).Post("/frida/stop", s.fridaStop)
				r.With(admin, remote).Post("/frida/port", s.fridaSetPort)
				r.With(user, remote).Post("/screenshot", s.screenshot)
				r.With(user, remote).Post("/touch", s.touch)
				r.With(user, remote).Post("/key", s.key)
				r.With(user, remote).Get("/snapshots", s.listSnapshots)
				r.With(user, remote).Post("/snapshots", s.createSnapshot)
				r.With(user, remote).Post("/snapshots/{name}/restore", s.restoreSnapshot)
				r.With(admin, remote).Delete("/snapshots/{name}", s.deleteSnapshot)
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
			// Per-job routes proxy to the owning node when the job is remote.
			r.Route("/{id}", func(r chi.Router) {
				r.With(user, s.routeRemoteJob).Get("/", s.getJob)
				r.With(admin, s.routeRemoteJob).Post("/cancel", s.cancelJob)
				r.With(user, s.routeRemoteJob).Get("/logs", s.jobLogsWS)
			})
		})

		r.With(user).Get("/system/status", s.systemStatusHandler)
		r.With(admin).Get("/system/config", s.systemConfigHandler)
		r.With(admin).Get("/system/interfaces", s.systemInterfacesHandler)
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
