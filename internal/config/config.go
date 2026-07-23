// Package config loads and validates the vphone-web server configuration.
//
// Configuration is read from a TOML file (default: ~/.config/vphone-web/config.toml)
// with sane built-in defaults so the server runs with zero configuration. Any field
// may be overridden by an environment variable (see envOverrides).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the fully-resolved server configuration.
type Config struct {
	Server ServerConfig `toml:"server"`
	Paths  PathsConfig  `toml:"paths"`
	Ports  PortsConfig  `toml:"ports"`
	Limits LimitsConfig `toml:"limits"`
	Guest  GuestConfig  `toml:"guest"`
}

// GuestConfig holds credentials/settings for talking to booted guest VMs.
type GuestConfig struct {
	// VNCPassword is the RFB password the guest TrollVNC server requires.
	VNCPassword string `toml:"vnc_password"`
	// SSHUser/SSHPassword are the dropbear credentials for the terminal.
	SSHUser     string `toml:"ssh_user"`
	SSHPassword string `toml:"ssh_password"`
}

// ServerConfig holds HTTP listener settings.
type ServerConfig struct {
	Host string `toml:"host"`
	Port int    `toml:"port"`
	// DevProxy, when set, is the origin of a running Vite dev server (e.g.
	// "http://localhost:5173"). Unknown (non-API) routes are reverse-proxied there.
	// Empty in production, where the embedded frontend is served instead.
	DevProxy string `toml:"dev_proxy"`
}

// PathsConfig holds filesystem locations.
type PathsConfig struct {
	VphoneCLI string `toml:"vphone_cli"`
	VMRoot    string `toml:"vm_root"`
	IPSWDir   string `toml:"ipsw_dir"`
	// DataDir holds the SQLite database and other server state.
	DataDir string `toml:"data_dir"`
}

// PortsConfig controls per-VM port block allocation.
type PortsConfig struct {
	Base      int `toml:"base"`
	BlockSize int `toml:"block_size"`
}

// LimitsConfig caps concurrent resource usage.
type LimitsConfig struct {
	MaxConcurrentVMs  int `toml:"max_concurrent_vms"`
	MaxConcurrentJobs int `toml:"max_concurrent_jobs"`
}

// Default returns a Config populated with the built-in defaults. Paths are
// expanded to absolute locations under the user's home directory.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Host:     "0.0.0.0",
			Port:     8080,
			DevProxy: "",
		},
		Paths: PathsConfig{
			VphoneCLI: "./vphone-cli",
			VMRoot:    "~/.vphone-web/vms",
			IPSWDir:   "~/.vphone-web/ipsws",
			DataDir:   "~/.vphone-web",
		},
		Ports: PortsConfig{
			Base:      10000,
			BlockSize: 10,
		},
		Limits: LimitsConfig{
			MaxConcurrentVMs:  4,
			MaxConcurrentJobs: 2,
		},
		Guest: GuestConfig{
			VNCPassword: "alpine",
			SSHUser:     "root",
			SSHPassword: "alpine",
		},
	}
}

// DefaultPath returns the default config file location.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "vphone-web", "config.toml")
}

// Load reads configuration from path, layering it over the defaults. A missing
// file is not an error — the defaults are used. Environment variable overrides
// are applied last. All path fields are expanded to absolute paths.
func Load(path string) (Config, error) {
	cfg := Default()

	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return cfg, fmt.Errorf("stat config %s: %w", path, err)
		}
	}

	cfg.applyEnvOverrides()

	if err := cfg.expandPaths(); err != nil {
		return cfg, err
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// applyEnvOverrides layers VPHONE_WEB_* environment variables over the config.
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("VPHONE_WEB_HOST"); v != "" {
		c.Server.Host = v
	}
	if v := os.Getenv("VPHONE_WEB_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Server.Port = n
		}
	}
	if v := os.Getenv("VPHONE_WEB_DEV_PROXY"); v != "" {
		c.Server.DevProxy = v
	}
	if v := os.Getenv("VPHONE_WEB_VPHONE_CLI"); v != "" {
		c.Paths.VphoneCLI = v
	}
	if v := os.Getenv("VPHONE_WEB_VM_ROOT"); v != "" {
		c.Paths.VMRoot = v
	}
	if v := os.Getenv("VPHONE_WEB_IPSW_DIR"); v != "" {
		c.Paths.IPSWDir = v
	}
	if v := os.Getenv("VPHONE_WEB_DATA_DIR"); v != "" {
		c.Paths.DataDir = v
	}
}

// expandPaths turns ~-prefixed and relative paths into absolute paths.
func (c *Config) expandPaths() error {
	for _, p := range []*string{&c.Paths.VphoneCLI, &c.Paths.VMRoot, &c.Paths.IPSWDir, &c.Paths.DataDir} {
		expanded, err := expandPath(*p)
		if err != nil {
			return err
		}
		*p = expanded
	}
	return nil
}

// expandPath resolves ~ to the home directory and makes the result absolute.
func expandPath(p string) (string, error) {
	if p == "" {
		return p, nil
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, fmt.Errorf("resolve home dir: %w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, fmt.Errorf("absolute path %s: %w", p, err)
	}
	return abs, nil
}

// validate checks invariants that would otherwise fail at runtime.
func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port %d out of range", c.Server.Port)
	}
	if c.Ports.Base < 1024 || c.Ports.Base > 65535 {
		return fmt.Errorf("ports.base %d out of range", c.Ports.Base)
	}
	if c.Ports.BlockSize < 4 {
		return fmt.Errorf("ports.block_size %d too small (need >= 4)", c.Ports.BlockSize)
	}
	if c.Limits.MaxConcurrentVMs < 1 {
		return fmt.Errorf("limits.max_concurrent_vms must be >= 1")
	}
	if c.Limits.MaxConcurrentJobs < 1 {
		return fmt.Errorf("limits.max_concurrent_jobs must be >= 1")
	}
	return nil
}

// DBPath returns the path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.Paths.DataDir, "vphone-web.db")
}
