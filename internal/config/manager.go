package config

import (
	"fmt"
	"sync"
)

// ConfigManager provides thread-safe access to live configuration.
type ConfigManager interface {
	Get() *Config
	Set(cfg *Config)
	Reload(path string) error
}

// RWMutexManager provides thread-safe read-heavy config access using RWMutex.
type RWMutexManager struct {
	mu  sync.RWMutex
	cfg *Config
}

// NewManager constructs a manager with an initial config.
func NewManager(initial *Config) *RWMutexManager {
	return &RWMutexManager{cfg: initial.Clone()}
}

// NewRWMutexManager constructs a manager with an initial config.
func NewRWMutexManager(initial *Config) *RWMutexManager {
	return NewManager(initial)
}

// Get returns the current config snapshot under a shared lock.
//
// Callers should treat the returned config as read-only; mutations must be made
// through Set/Reload to keep write-contention low for read-heavy workloads.
func (m *RWMutexManager) Get() *Config {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Set updates the current config pointer under an exclusive lock.
func (m *RWMutexManager) Set(cfg *Config) {
	if m == nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg.Clone()
}

// Reload loads config from path and atomically swaps it into place.
func (m *RWMutexManager) Reload(path string) error {
	if m == nil {
		return fmt.Errorf("config manager is nil")
	}
	if path == "" {
		return fmt.Errorf("config reload path is required")
	}

	loaded, err := Load(path)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = loaded.Clone()
	return nil
}

var _ ConfigManager = (*RWMutexManager)(nil)
