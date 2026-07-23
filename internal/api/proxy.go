package api

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/cyberm-tech/vphone-web/internal/auth"
	"github.com/cyberm-tech/vphone-web/internal/cluster"
	"github.com/go-chi/chi/v5"
)

// controllerTrust treats a request carrying a valid cluster system password as
// an authenticated admin — the trusted controller proxying on a user's behalf.
// This is what lets a controller drive a worker's full VM API over the control
// link. No-op when no system password is configured (i.e. not a worker).
func (s *Server) controllerTrust(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := s.cfg.Cluster.SystemPassword
		if secret != "" {
			if h := r.Header.Get(cluster.SystemPasswordHeader); h != "" && subtleEqual(h, secret) {
				u := &auth.User{Username: "controller", Role: auth.RoleAdmin, Provider: "cluster"}
				r = r.WithContext(auth.ContextWithUser(r.Context(), u))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// routeRemoteVM proxies a per-VM request to the owning worker node when the id
// belongs to a remote node; local VMs pass through to the normal handlers. It is
// composed AFTER the role gate so controller-side permissions still apply.
func (s *Server) routeRemoteVM(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cluster != nil {
			if node, ok := s.cluster.NodeForVM(chi.URLParam(r, "id")); ok {
				s.proxyToNode(node, w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// proxyToNode reverse-proxies the current request — including WebSocket upgrades
// (VNC, terminal) — to a worker node's API over the pinned control link.
func (s *Server) proxyToNode(node cluster.Node, w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(node.BaseURL())
	if err != nil {
		writeError(w, http.StatusBadGateway, "invalid node address")
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: s.cluster.Transport(node),
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// The path is preserved; authenticate to the worker as the controller.
			req.Header.Set(cluster.SystemPasswordHeader, node.SystemPassword)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, e error) {
			s.log.Warn("node proxy failed", "node", node.Name, "err", e)
			writeError(w, http.StatusBadGateway, "node unreachable: "+e.Error())
		},
	}
	proxy.ServeHTTP(w, r)
}
