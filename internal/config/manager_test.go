package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRWMutexManagerGetSet(t *testing.T) {
	initial := &Config{General: General{LogLevel: "info"}}
	mgr := NewRWMutexManager(initial)

	got := mgr.Get()
	if got == nil {
		t.Fatal("expected initial config snapshot")
	}
	if got == initial {
		t.Fatal("expected manager to store cloned config on bootstrap")
	}
	if got.General.LogLevel != "info" {
		t.Fatalf("unexpected initial log level: %q", got.General.LogLevel)
	}
	if got != mgr.Get() {
		t.Fatal("expected repeated Get to return same live snapshot")
	}

	next := &Config{General: General{LogLevel: "debug"}}
	mgr.Set(next)
	next.General.LogLevel = "error"

	updated := mgr.Get()
	if updated == next {
		t.Fatal("expected manager to clone Set input")
	}
	if updated.General.LogLevel != "debug" {
		t.Fatalf("expected updated config value, got %q", updated.General.LogLevel)
	}
	// Set should still isolate callers who mutate their source object.
	next.General.LogLevel = "error"
	updatedAfterMutation := mgr.Get()
	if updatedAfterMutation.General.LogLevel != "debug" {
		t.Fatalf("expected Set to keep its own snapshot: got %q", updatedAfterMutation.General.LogLevel)
	}
}

func TestRWMutexManagerReload(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	mgr := NewRWMutexManager(nil)

	if err := mgr.Reload(path); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	cfg := mgr.Get()
	if cfg == nil {
		t.Fatal("expected config after reload")
	}

	if cfg.General.LogLevel == "" {
		t.Fatal("expected populated config from file")
	}
}

func TestRWMutexManagerReloadRequiresPath(t *testing.T) {
	mgr := NewRWMutexManager(&Config{})
	if err := mgr.Reload(""); err == nil {
		t.Fatal("expected error for empty reload path")
	}
}

func TestRWMutexManagerConcurrentReadWithWrites(t *testing.T) {
	mgr := NewRWMutexManager(&Config{General: General{MaxPerTick: 1}})

	const readers = 32
	const readsPerReader = 1000
	const writes = 100

	var wg sync.WaitGroup
	wg.Add(readers + 1)

	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < readsPerReader; j++ {
				cfg := mgr.Get()
				if cfg == nil {
					t.Error("got nil config during concurrent read")
					return
				}
				_ = cfg.General.MaxPerTick
			}
		}()
	}

	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			mgr.Set(&Config{General: General{MaxPerTick: i + 2}})
		}
	}()

	wg.Wait()

	if got := mgr.Get(); got == nil {
		t.Fatal("expected final non-nil config")
	}
}

func TestLoadManager(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	mgr, err := LoadManager(path)
	if err != nil {
		t.Fatalf("LoadManager failed: %v", err)
	}
	if mgr.Get() == nil {
		t.Fatal("expected non-nil config from LoadManager")
	}
}

func TestRWMutexManagerNilSafeMethods(t *testing.T) {
	var mgr *RWMutexManager

	if got := mgr.Get(); got != nil {
		t.Fatalf("Get on nil manager should return nil, got %#v", got)
	}

	if err := mgr.Reload(validConfig); err == nil {
		t.Fatal("expected error when reloading with nil manager")
	}

	mgr.Set(&Config{General: General{LogLevel: "info"}})
	if got := mgr.Get(); got != nil {
		t.Fatalf("Set on nil manager should not initialize config, got %#v", got)
	}
}

func TestRWMutexManagerReloadUsesWriterLock(t *testing.T) {
	mgr := NewRWMutexManager(&Config{})
	path := writeTestConfig(t, validConfig)

	mgr.mu.RLock()
	done := make(chan struct{})
	go func() {
		err := mgr.Reload(path)
		if err != nil {
			t.Error(err)
		}
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("reload completed while reader lock held; expected blocking")
	case <-time.After(20 * time.Millisecond):
	}

	mgr.mu.RUnlock()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("reload did not complete after releasing reader lock")
	}
}

func TestRWMutexManagerSetUsesExclusiveLock(t *testing.T) {
	mgr := NewRWMutexManager(&Config{})
	mgr.mu.RLock()

	done := make(chan struct{})
	go func() {
		mgr.Set(&Config{General: General{LogLevel: "debug"}})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("writer completed while reader lock held; expected blocking")
	case <-time.After(20 * time.Millisecond):
	}

	mgr.mu.RUnlock()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("writer did not complete after releasing reader lock")
	}
}

func TestRWMutexManagerGetUsesReadLock(t *testing.T) {
	mgr := NewRWMutexManager(&Config{General: General{LogLevel: "info"}})
	mgr.mu.Lock()

	done := make(chan struct{})
	go func() {
		_ = mgr.Get()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("reader completed while writer lock held; expected blocking")
	case <-time.After(20 * time.Millisecond):
	}

	mgr.mu.Unlock()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("reader did not complete after releasing writer lock")
	}
}

func BenchmarkRWMutexManagerGet(b *testing.B) {
	mgr := NewRWMutexManager(&Config{General: General{LogLevel: "info"}})
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cfg := mgr.Get()
			if cfg == nil {
				b.Fatal("nil config")
			}
		}
	})
}

func BenchmarkRWMutexManagerReadMostly(b *testing.B) {
	mgr := NewRWMutexManager(&Config{General: General{MaxPerTick: 1}})
	var writes atomic.Int64

	stop := make(chan struct{})
	defer close(stop)

	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				next := int(writes.Add(1))
				mgr.Set(&Config{General: General{MaxPerTick: next}})
			case <-stop:
				return
			}
		}
	}()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cfg := mgr.Get()
			if cfg == nil {
				b.Fatal("nil config")
			}
			_ = cfg.General.MaxPerTick
		}
	})
}

func BenchmarkRWMutexManagerReadMostlyWithReloads(b *testing.B) {
	workdir := b.TempDir()
	path := filepath.Join(workdir, "chum.toml")
	if err := os.WriteFile(path, []byte(validConfig), 0644); err != nil {
		b.Fatalf("seed config write failed: %v", err)
	}
	mgr := NewRWMutexManager(nil)
	if err := mgr.Reload(path); err != nil {
		b.Fatalf("initial reload failed: %v", err)
	}

	stop := make(chan struct{})
	defer close(stop)

	writeTicker := time.NewTicker(2 * time.Millisecond)
	defer writeTicker.Stop()
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	go func() {
		counter := 0
		for i := 0; ; i++ {
			select {
			case <-ticker.C:
				updated := strings.Replace(validConfig, "max_per_tick = 10", fmt.Sprintf("max_per_tick = %d", (i%20)+1), 1)
				if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
					b.Error(err)
					return
				}
				if err := mgr.Reload(path); err != nil {
					counter++
					if counter > 3 {
						b.Error(err)
						return
					}
				}
				_ = writeTicker
			case <-stop:
				return
			}
		}
	}()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cfg := mgr.Get()
			if cfg == nil {
				b.Fatal("nil config")
			}
			_ = cfg.General.MaxPerTick
		}
	})
}

func TestRWMutexManagerReloadConcurrentReaders(t *testing.T) {
	cfgTemplate := validConfig
	path := writeTestConfig(t, cfgTemplate)
	mgr := NewRWMutexManager(nil)

	if err := mgr.Reload(path); err != nil {
		t.Fatalf("initial reload failed: %v", err)
	}

	const iterations = 20
	const readers = 8

	var wg sync.WaitGroup
	wg.Add(readers + 1)

	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations*50; j++ {
				cfg := mgr.Get()
				if cfg == nil {
					t.Error("nil config during read")
					return
				}
				_ = cfg.General.MaxPerTick
			}
		}()
	}

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			content := strings.Replace(validConfig, "max_per_tick = 10", fmt.Sprintf("max_per_tick = %d", i+1), 1)

			reloadPath := writeTestConfig(t, content)
			if err := mgr.Reload(reloadPath); err != nil {
				t.Errorf("reload failed: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}
