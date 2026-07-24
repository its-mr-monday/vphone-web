// Command vphone-mcp is a standalone Model Context Protocol server for
// vphone-web. It speaks MCP over stdio and drives a vphone-web instance through
// its REST API, letting an AI agent manage VMs and control the guest devices
// (screenshot, tap/swipe, hardware keys, shell exec).
//
// It is a separate binary from the server on purpose: it is a *client*, embeds no
// frontend, never opens the database, and can point at a local or a remote
// instance. Configuration is its own — ~/.config/vphone-mcp/config.toml — and is
// optional: with no config at all it targets a local server on the default port.
//
//	vphone-mcp                          # local instance (or whatever config says)
//	vphone-mcp -url http://lab-mini:8099 # an explicit remote instance
//	vphone-mcp -print-config             # write a starter config file
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/cyberm-tech/vphone-web/internal/mcp"
)

// version is the reported build version (override via -ldflags).
var version = "1.0"

const sampleConfig = `# vphone-mcp configuration.
#
# Optional: with no config file the MCP server targets a local vphone-web at
# ` + mcp.DefaultURL + `. Env vars VPHONE_MCP_URL / VPHONE_MCP_TOKEN override this file,
# and the -url / -token flags override both.

[server]
url = "` + mcp.DefaultURL + `"
# token   = ""       # bearer token, if the instance has access control enabled
# timeout = "120s"   # per-request timeout
`

func main() {
	configPath := flag.String("config", mcp.DefaultConfigPath(), "path to the vphone-mcp config file")
	baseURL := flag.String("url", "", "vphone-web base URL (overrides config)")
	token := flag.String("token", "", "bearer token (overrides config)")
	initConfig := flag.Bool("print-config", false, "print a starter config file and exit")
	flag.Parse()

	if *initConfig {
		fmt.Print(sampleConfig)
		return
	}

	// stdout is the MCP transport — all logging must go to stderr.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := mcp.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vphone-mcp: %v\n", err)
		os.Exit(1)
	}
	// Flags win over file and environment.
	if *baseURL != "" {
		cfg.Server.URL = *baseURL
	}
	if *token != "" {
		cfg.Server.Token = *token
	}

	source := "defaults"
	if _, statErr := os.Stat(*configPath); statErr == nil {
		source = filepath.Clean(*configPath)
	}
	log.Info("vphone-mcp on stdio", "target", cfg.Server.URL, "config", source)

	if err := mcp.New(cfg, version, log).Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "vphone-mcp: %v\n", err)
		os.Exit(1)
	}
}
