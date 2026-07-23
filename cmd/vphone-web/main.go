// Command vphone-web is the single-binary server for the vphone-web platform:
// it loads configuration, initializes the SQLite database, constructs the VM
// manager, and serves the REST/WebSocket API alongside the embedded frontend.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/cyberm-tech/vphone-web/internal/api"
	"github.com/cyberm-tech/vphone-web/internal/auth"
	"github.com/cyberm-tech/vphone-web/internal/config"
	"github.com/cyberm-tech/vphone-web/internal/db"
	"github.com/cyberm-tech/vphone-web/internal/ipsw"
	"github.com/cyberm-tech/vphone-web/internal/jobs"
	"github.com/cyberm-tech/vphone-web/internal/vm"
	"github.com/cyberm-tech/vphone-web/web"
)

func main() {
	configPath := flag.String("config", config.DefaultPath(), "path to config.toml")
	devProxy := flag.String("dev-proxy", "", "reverse-proxy non-API routes to this Vite origin (e.g. http://localhost:5173)")
	logLevel := flag.String("log-level", "info", "log level: debug|info|warn|error")
	flag.Parse()

	logger := newLogger(*logLevel)
	slog.SetDefault(logger)

	if err := run(*configPath, *devProxy, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath, devProxy string, logger *slog.Logger) error {
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

	mgr, err := vm.NewManager(sqlDB, vm.Options{
		VphoneCLIDir:     cfg.Paths.VphoneCLI,
		VMRoot:           cfg.Paths.VMRoot,
		PortBase:         cfg.Ports.Base,
		PortBlockSize:    cfg.Ports.BlockSize,
		MaxConcurrentVMs: cfg.Limits.MaxConcurrentVMs,
		Jobs:             queue,
		IPSWPath:         library.PathOf,
		Logger:           logger,
	})
	if err != nil {
		return err
	}
	defer mgr.Shutdown()

	// Access control (disabled by default; see [auth] config).
	authSvc, err := buildAuth(sqlDB, cfg, logger)
	if err != nil {
		return err
	}

	staticFS, err := web.Dist()
	if err != nil {
		return err
	}

	srv := api.NewServer(cfg, mgr, queue, library, authSvc, logger)
	handler := srv.Router(staticFS)

	addr := netAddr(cfg.Server.Host, cfg.Server.Port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		// No write timeout: WebSocket proxies (VNC) are long-lived.
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("vphone-web listening", "addr", "http://"+addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// netAddr joins host and port into a listen address.
func netAddr(host string, port int) string {
	if host == "" {
		host = "0.0.0.0"
	}
	return host + ":" + strconv.Itoa(port)
}

// buildAuth constructs the auth service from config and bootstraps the initial
// admin when auth is enabled and no users exist. External providers (LDAP/OIDC/
// SAML) are wired in as they are implemented.
func buildAuth(sqlDB *sql.DB, cfg config.Config, logger *slog.Logger) (*auth.Service, error) {
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
	svc := auth.NewService(sqlDB, auth.Options{
		Enabled:     cfg.Auth.Enabled,
		SessionTTL:  ttl,
		RoleMap:     roleMap,
		DefaultRole: auth.Role(cfg.Auth.DefaultRole),
		Logger:      logger,
	})
	if err := svc.BootstrapAdmin(cfg.Auth.BootstrapAdmin, cfg.Auth.BootstrapPassword); err != nil {
		return nil, err
	}
	if cfg.Auth.Enabled {
		logger.Info("access control enabled", "providers", svc.Providers())
	}
	return svc, nil
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
