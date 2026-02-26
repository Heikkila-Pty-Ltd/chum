// Package config loads and validates the CHUM TOML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Load reads and validates a CHUM TOML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	applyDefaults(&cfg, md)
	normalizePaths(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Reload reads and validates a CHUM TOML configuration file.
//
// This mirrors Load but is intentionally named to reflect runtime refresh paths.
func Reload(path string) (*Config, error) {
	return Load(path)
}

// LoadManager reads config from path and returns an RWMutex-backed thread-safe manager.
func LoadManager(path string) (ConfigManager, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("config path is required")
	}

	cfg, err := Reload(path)
	if err != nil {
		return nil, err
	}
	return NewRWMutexManager(cfg), nil
}

// normalizePaths expands "~" and trims whitespace for configured filesystem paths.
func normalizePaths(cfg *Config) {
	if cfg == nil {
		return
	}

	cfg.General.StateDB = ExpandHome(strings.TrimSpace(cfg.General.StateDB))
	cfg.Dispatch.LogDir = ExpandHome(strings.TrimSpace(cfg.Dispatch.LogDir))
	cfg.API.Security.AuditLog = ExpandHome(strings.TrimSpace(cfg.API.Security.AuditLog))

	for name := range cfg.Projects {
		project := cfg.Projects[name]
		project.MorselsDir = ExpandHome(strings.TrimSpace(project.MorselsDir))
		project.Workspace = ExpandHome(strings.TrimSpace(project.Workspace))
		cfg.Projects[name] = project
	}
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}
