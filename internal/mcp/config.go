package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// Defaults for a standalone MCP server pointed at a local vphone-web instance.
const (
	DefaultURL     = "http://127.0.0.1:8099"
	DefaultTimeout = 120 * time.Second
)

// Config configures the standalone MCP server. It is deliberately independent of
// the vphone-web server's own configuration: the MCP server is a *client* and may
// drive a local instance or a remote one, so it carries its own settings.
type Config struct {
	Server ServerConfig `toml:"server"`
}

// ServerConfig identifies the vphone-web instance to drive.
type ServerConfig struct {
	// URL is the base URL of the vphone-web instance (e.g. http://127.0.0.1:8099).
	URL string `toml:"url"`
	// Token is an optional bearer token, for instances with access control on.
	Token string `toml:"token"`
	// Timeout bounds each API request as a Go duration string (e.g. "120s").
	Timeout string `toml:"timeout"`
}

// DefaultConfigPath is ~/.config/vphone-mcp/config.toml.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "vphone-mcp", "config.toml")
}

// LoadConfig reads the config file when it exists. A missing file is not an
// error — with no configuration at all the server targets a local vphone-web on
// its default port, so the common case needs zero setup.
//
// Environment variables VPHONE_MCP_URL / VPHONE_MCP_TOKEN override the file.
func LoadConfig(path string) (Config, error) {
	cfg := Config{Server: ServerConfig{URL: DefaultURL}}

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return cfg, fmt.Errorf("read %s: %w", path, err)
			}
		}
	}

	if v := os.Getenv("VPHONE_MCP_URL"); v != "" {
		cfg.Server.URL = v
	}
	if v := os.Getenv("VPHONE_MCP_TOKEN"); v != "" {
		cfg.Server.Token = v
	}
	if cfg.Server.URL == "" {
		cfg.Server.URL = DefaultURL
	}
	return cfg, nil
}

// RequestTimeout resolves the configured per-request timeout.
func (c Config) RequestTimeout() time.Duration {
	if c.Server.Timeout != "" {
		if d, err := time.ParseDuration(c.Server.Timeout); err == nil && d > 0 {
			return d
		}
	}
	return DefaultTimeout
}
