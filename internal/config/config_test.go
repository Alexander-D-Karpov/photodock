package config

import (
	"path/filepath"
	"testing"
)

// setEnv clears PhotoDock env vars, applies overrides, and restores originals via t.Setenv.
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	keys := []string{"MEDIA_ROOT", "DATABASE_URL", "CACHE_DIR", "LISTEN_ADDR", "ADMIN_USER", "ADMIN_PASS"}
	for _, k := range keys {
		if v, ok := kv[k]; ok {
			t.Setenv(k, v)
		} else {
			t.Setenv(k, "")
		}
	}
}

func TestLoadRequiredVars(t *testing.T) {
	// Missing MEDIA_ROOT.
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_PASS": "p"})
	if _, err := Load(); err == nil {
		t.Error("expected error when MEDIA_ROOT is missing")
	}

	// Missing DATABASE_URL.
	setEnv(t, map[string]string{"MEDIA_ROOT": "/tmp/m", "ADMIN_PASS": "p"})
	if _, err := Load(); err == nil {
		t.Error("expected error when DATABASE_URL is missing")
	}

	// Missing ADMIN_PASS.
	setEnv(t, map[string]string{"MEDIA_ROOT": "/tmp/m", "DATABASE_URL": "postgres://x"})
	if _, err := Load(); err == nil {
		t.Error("expected error when ADMIN_PASS is missing")
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"MEDIA_ROOT":   "/tmp/media",
		"DATABASE_URL": "postgres://user@localhost/db",
		"ADMIN_PASS":   "secret",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q; want default :8080", cfg.ListenAddr)
	}
	if cfg.AdminUser != "admin" {
		t.Errorf("AdminUser = %q; want default admin", cfg.AdminUser)
	}
	if !filepath.IsAbs(cfg.MediaRoot) {
		t.Errorf("MediaRoot = %q; want absolute path", cfg.MediaRoot)
	}
	// CacheDir defaults to MEDIA_ROOT/.photodock_cache.
	wantCache := filepath.Join(cfg.MediaRoot, ".photodock_cache")
	if cfg.CacheDir != wantCache {
		t.Errorf("CacheDir = %q; want %q", cfg.CacheDir, wantCache)
	}
}

func TestLoadOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"MEDIA_ROOT":   "/tmp/media",
		"DATABASE_URL": "postgres://user@localhost/db",
		"ADMIN_PASS":   "secret",
		"ADMIN_USER":   "root",
		"LISTEN_ADDR":  ":9000",
		"CACHE_DIR":    "/var/cache/photodock",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ListenAddr != ":9000" {
		t.Errorf("ListenAddr = %q; want :9000", cfg.ListenAddr)
	}
	if cfg.AdminUser != "root" {
		t.Errorf("AdminUser = %q; want root", cfg.AdminUser)
	}
	if cfg.CacheDir != "/var/cache/photodock" {
		t.Errorf("CacheDir = %q; want /var/cache/photodock", cfg.CacheDir)
	}
}
