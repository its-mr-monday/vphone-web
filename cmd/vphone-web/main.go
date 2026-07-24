// Command vphone-web is the single-binary server for the vphone-web platform:
// it loads configuration, initializes the SQLite database, constructs the VM
// manager, and serves the REST/WebSocket API alongside the embedded frontend.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/api"
	"github.com/cyberm-tech/vphone-web/internal/auth"
	"github.com/cyberm-tech/vphone-web/internal/cluster"
	"github.com/cyberm-tech/vphone-web/internal/config"
	"github.com/cyberm-tech/vphone-web/internal/db"
	"github.com/cyberm-tech/vphone-web/internal/ipsw"
	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/cyberm-tech/vphone-web/web"
)

// version is the reported build version (override via -ldflags).
var version = "1.0"

func main() {
	configPath := flag.String("config", config.DefaultPath(), "path to config.toml")
	devProxy := flag.String("dev-proxy", "", "reverse-proxy non-API routes to this Vite origin (e.g. http://localhost:5173)")
	logLevel := flag.String("log-level", "info", "log level: debug|info|warn|error")
	agentMode := flag.Bool("agent", false, "run as a worker agent: print the control-link connection details on startup")
	envFile := flag.String("env-file", ".env", "path to a .env file for VPHONE_* variables")
	flag.Parse()

	logger := newLogger(*logLevel)
	slog.SetDefault(logger)

	// Load .env (VPHONE_SYSTEM_PASSWORD etc.) before config so it can supply env.
	if err := config.LoadDotEnv(*envFile); err != nil {
		logger.Warn("failed to read env file", "path", *envFile, "err", err)
	}

	if err := run(*configPath, *devProxy, *agentMode, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath, devProxy string, agentMode bool, logger *slog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if devProxy != "" {
		cfg.Server.DevProxy = devProxy
	}
	logger.Info("configuration loaded",
		"config", configPath,
		"vm_root", cfg.Paths.VMRoot,
		"vphone_cli", cfg.Paths.VphoneCLI,
		"dev_proxy", cfg.Server.DevProxy,
	)

	sqlDB, err := db.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	// Background job queue (provisioning steps, IPSW downloads, snapshots).
	queue, err := jobs.NewQueue(sqlDB, cfg.Limits.MaxConcurrentJobs, logger)
	if err != nil {
		return err
	}
	defer queue.Close()

	// IPSW library.
	library, err := ipsw.New(sqlDB, cfg.Paths.IPSWDir, queue, logger)
	if err != nil {
		return err
	}

	vm.SetVNCPassword(cfg.Guest.VNCPassword)
	vm.SetGuestSSH(cfg.Guest.SSHUser, cfg.Guest.SSHPassword)

	mgr, err := vm.NewManager(sqlDB, vm.Options{
		VphoneCLIDir:     cfg.Paths.VphoneCLI,
		VMRoot:           cfg.Paths.VMRoot,
		PortBase:         cfg.Ports.Base,
		PortBlockSize:    cfg.Ports.BlockSize,
		MaxConcurrentVMs: cfg.Limits.MaxConcurrentVMs,
		HeadlessVMs:      cfg.Server.HeadlessVMs,
		Jobs:             queue,
		IPSWPath:         library.PathOf,
		Logger:           logger,
	})
	if err != nil {
		return err
	}
	defer mgr.Shutdown()

	// Access control (disabled by default; see [auth] config).
	authSvc, oidcProv, samlProv, err := buildAuth(sqlDB, cfg, logger)
	if err != nil {
		return err
	}

	// Clustering: controller-side node registry with health monitoring.
	heartbeat := 10 * time.Second
	if cfg.Cluster.HeartbeatInterval != "" {
		if d, perr := time.ParseDuration(cfg.Cluster.HeartbeatInterval); perr == nil {
			heartbeat = d
		}
	}
	clusterMgr := cluster.NewManager(sqlDB, heartbeat, logger)
	clusterMgr.Start()
	defer clusterMgr.Stop()

	// Agent mode: require the system password and print how to register this node.
	if agentMode {
		if cfg.Cluster.SystemPassword == "" {
			return errIf("--agent requires a system password (set VPHONE_SYSTEM_PASSWORD or [cluster] system_password)")
		}
		printAgentBanner(cfg, logger)
	}

	staticFS, err := web.Dist()
	if err != nil {
		return err
	}

	srv := api.NewServer(cfg, api.Deps{
		VMs: mgr, Jobs: queue, IPSW: library, Auth: authSvc,
		OIDC: oidcProv, SAML: samlProv,
		Cluster: clusterMgr, Version: version, Log: logger,
	})
	handler := srv.Router(staticFS)

	addr := netAddr(cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		// No write timeout: WebSocket proxies (VNC) are long-lived.
	}

	// TLS: serve HTTPS when a cert/key is configured, or generate a self-signed
	// certificate on demand. The same setting secures the --agent control link.
	tlsEnabled := cfg.Server.TLSEnabled
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
		if cfg.Server.UseSelfSignedTLS() {
			cert, gerr := selfSignedCert(cfg.Server.Host)
			if gerr != nil {
				return fmt.Errorf("generate self-signed cert: %w", gerr)
			}
			httpServer.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			logger.Warn("serving HTTPS with a self-signed certificate (clients must trust it or skip verification)")
		}
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("vphone-web listening", "addr", scheme+"://"+addr)
		var err error
		if tlsEnabled {
			// Empty cert/key args use httpServer.TLSConfig (self-signed) when set.
			err = httpServer.ListenAndServeTLS(cfg.Server.TLSCert, cfg.Server.TLSKey)
		} else {
			err = httpServer.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-sigCh:
		logger.Info("shutting down", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpServer.Shutdown(ctx)
}

// selfSignedCert generates an in-memory self-signed ECDSA certificate covering
// localhost and this host's LAN addresses, valid for one year. Used when HTTPS
// is enabled without a provided cert/key (labs, worker agents).
func selfSignedCert(host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := crand.Int(crand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "vphone-web"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	if host != "" && host != "0.0.0.0" {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, host)
		}
	}
	for _, a := range lanAddrs() {
		if ip := net.ParseIP(a); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		}
	}
	der, err := x509.CreateCertificate(crand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return tls.X509KeyPair(certPEM, keyPEM)
}

// netAddr joins host and port into a listen address.
func netAddr(host string, port int) string {
	if host == "" {
		host = "0.0.0.0"
	}
	return host + ":" + strconv.Itoa(port)
}

// errIf returns an error carrying msg (small helper for readability).
func errIf(msg string) error { return errors.New(msg) }

// printAgentBanner prints how a controller can register this worker node.
func printAgentBanner(cfg config.Config, logger *slog.Logger) {
	addrs := lanAddrs()
	port := cfg.Server.Port
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  ┌───────────────────────── vphone-web agent ─────────────────────────")
	fmt.Fprintln(os.Stderr, "  │ This worker is ready to join a controller.")
	fmt.Fprintln(os.Stderr, "  │ In the controller UI → Nodes → Add node, enter:")
	if len(addrs) == 0 {
		fmt.Fprintf(os.Stderr, "  │   Address:         <this-host-ip>:%d\n", port)
	}
	for _, a := range addrs {
		fmt.Fprintf(os.Stderr, "  │   Address:         %s:%d\n", a, port)
	}
	fmt.Fprintln(os.Stderr, "  │   System password: (your VPHONE_SYSTEM_PASSWORD)")
	if cfg.Server.TLSEnabled {
		fmt.Fprintln(os.Stderr, "  │   TLS (HTTPS):     enabled — this agent serves HTTPS.")
		if cfg.Server.UseSelfSignedTLS() {
			fmt.Fprintln(os.Stderr, "  │                    (self-signed — accept the certificate fingerprint once)")
		}
	}
	fmt.Fprintln(os.Stderr, "  └─────────────────────────────────────────────────────────────────────")
	fmt.Fprintln(os.Stderr, "")
	logger.Info("agent mode enabled", "port", port, "addresses", addrs)
}

// lanAddrs returns this host's non-loopback IPv4 addresses.
func lanAddrs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				out = append(out, ipnet.IP.String())
			}
		}
	}
	return out
}

// buildAuth constructs the auth service from config and bootstraps the initial
// admin when auth is enabled and no users exist. External providers (LDAP/OIDC/
// SAML) are wired in as they are implemented.
func buildAuth(sqlDB *sql.DB, cfg config.Config, logger *slog.Logger) (*auth.Service, *auth.OIDCProvider, *auth.SAMLProvider, error) {
	ttl := 12 * time.Hour
	if cfg.Auth.SessionTTL != "" {
		if d, err := time.ParseDuration(cfg.Auth.SessionTTL); err == nil {
			ttl = d
		}
	}
	roleMap := make(map[string]auth.Role, len(cfg.Auth.RoleMap))
	for group, role := range cfg.Auth.RoleMap {
		roleMap[group] = auth.Role(role)
	}

	// External providers (added when enabled). LDAP is a password-grant
	// Authenticator; OIDC/SAML register HTTP redirect handlers on the router.
	var providers []auth.Authenticator
	if cfg.Auth.LDAP.Enabled {
		ldapProv, err := auth.NewLDAP(auth.LDAPOptions{
			URL:          cfg.Auth.LDAP.URL,
			BindDN:       cfg.Auth.LDAP.BindDN,
			BindPassword: cfg.Auth.LDAP.BindPassword,
			BaseDN:       cfg.Auth.LDAP.BaseDN,
			UserFilter:   cfg.Auth.LDAP.UserFilter,
			GroupFilter:  cfg.Auth.LDAP.GroupFilter,
			Insecure:     cfg.Auth.LDAP.Insecure,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("ldap provider: %w", err)
		}
		providers = append(providers, ldapProv)
		logger.Info("LDAP auth provider enabled", "url", cfg.Auth.LDAP.URL)
	}

	// OIDC and SAML are redirect flows handled by the API layer, not password
	// Authenticators — construct them separately and hand them to the server.
	var oidcProv *auth.OIDCProvider
	if cfg.Auth.OIDC.Enabled {
		p, err := auth.NewOIDC(context.Background(), auth.OIDCOptions{
			Issuer:       cfg.Auth.OIDC.Issuer,
			ClientID:     cfg.Auth.OIDC.ClientID,
			ClientSecret: cfg.Auth.OIDC.ClientSecret,
			RedirectURL:  cfg.Auth.OIDC.RedirectURL,
			GroupsClaim:  cfg.Auth.OIDC.GroupsClaim,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("oidc provider: %w", err)
		}
		oidcProv = p
		logger.Info("OIDC auth provider enabled", "issuer", cfg.Auth.OIDC.Issuer)
	}

	var samlProv *auth.SAMLProvider
	if cfg.Auth.SAML.Enabled {
		p, err := auth.NewSAML(context.Background(), auth.SAMLOptions{
			IDPMetadataURL: cfg.Auth.SAML.IDPMetadataURL,
			EntityID:       cfg.Auth.SAML.EntityID,
			ACSURL:         cfg.Auth.SAML.ACSURL,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("saml provider: %w", err)
		}
		samlProv = p
		logger.Info("SAML auth provider enabled", "idp_metadata", cfg.Auth.SAML.IDPMetadataURL)
	}

	svc := auth.NewService(sqlDB, auth.Options{
		Enabled:     cfg.Auth.Enabled,
		SessionTTL:  ttl,
		RoleMap:     roleMap,
		DefaultRole: auth.Role(cfg.Auth.DefaultRole),
		Providers:   providers,
		Logger:      logger,
	})
	if err := svc.BootstrapAdmin(cfg.Auth.BootstrapAdmin, cfg.Auth.BootstrapPassword); err != nil {
		return nil, nil, nil, err
	}
	if cfg.Auth.Enabled {
		logger.Info("access control enabled", "providers", svc.Providers())
	}
	return svc, oidcProv, samlProv, nil
}

// newLogger builds a text slog logger at the requested level.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
