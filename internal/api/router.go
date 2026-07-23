package api

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"

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

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/vms", func(r chi.Router) {
			r.Get("/", s.listVMs)
			r.Post("/", s.createVM)
			r.Post("/import", s.importVM)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.getVM)
				r.Delete("/", s.deleteVM)
				r.Post("/boot", s.bootVM)
				r.Post("/stop", s.stopVM)
				r.Post("/restart", s.restartVM)
				r.Get("/vnc", s.vncWS)
				r.Get("/terminal", s.terminalWS)
				r.Post("/screenshot", s.screenshot)
				r.Post("/touch", s.touch)
				r.Post("/key", s.key)
				r.Get("/snapshots", s.listSnapshots)
				r.Post("/snapshots", s.createSnapshot)
				r.Post("/snapshots/{name}/restore", s.restoreSnapshot)
				r.Delete("/snapshots/{name}", s.deleteSnapshot)
			})
		})

		r.Route("/ipsws", func(r chi.Router) {
			r.Get("/", s.listIPSWs)
			r.Post("/", s.registerIPSW)
			r.Post("/download", s.downloadIPSW)
			r.Post("/upload", s.uploadIPSW)
			r.Delete("/{id}", s.deleteIPSW)
		})

		r.Route("/jobs", func(r chi.Router) {
			r.Get("/", s.listJobs)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.getJob)
				r.Post("/cancel", s.cancelJob)
				r.Get("/logs", s.jobLogsWS)
			})
		})

		r.Get("/system/status", s.systemStatusHandler)
		r.Get("/system/config", s.systemConfigHandler)
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
