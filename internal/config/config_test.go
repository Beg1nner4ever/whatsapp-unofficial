package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestLoadDefaultsAndPermissions(t *testing.T) {
	home := filepath.Join(shortTempDir(t), "wa")
	t.Setenv(EnvHome, home)
	t.Setenv(EnvAllowSend, "")
	t.Setenv(EnvIdleMinutes, "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowSend {
		t.Error("sending must be disabled by default")
	}
	if cfg.IdleTimeout != 15*time.Minute {
		t.Errorf("idle = %v", cfg.IdleTimeout)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("Load must not touch the filesystem")
	}

	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// A database left world-readable by the upstream bridge.
	if err := os.WriteFile(cfg.SessionDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{cfg.Home, cfg.OutboxDir, cfg.MediaDir} {
		if info, _ := os.Stat(p); info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, want 0700", p, info.Mode().Perm())
		}
	}
	if info, _ := os.Stat(cfg.SessionDB); info.Mode().Perm() != 0o600 {
		t.Errorf("session db mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestEnvParsing(t *testing.T) {
	t.Setenv(EnvHome, t.TempDir())
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Setenv(EnvAllowSend, v)
		if cfg, _ := Load(); !cfg.AllowSend {
			t.Errorf("%q should enable sending", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no", "${user_config.allow_send}"} {
		t.Setenv(EnvAllowSend, v)
		if cfg, _ := Load(); cfg.AllowSend {
			t.Errorf("%q should not enable sending", v)
		}
	}
	t.Setenv(EnvIdleMinutes, "0")
	if _, err := Load(); err == nil {
		t.Error("idle minutes 0 should be rejected")
	}
}

func TestLongHomeFallsBackToRuntimeDir(t *testing.T) {
	runtimeDir := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv(EnvHome, "/"+strings.Repeat("x", 120))
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(cfg.Socket) != runtimeDir || len(cfg.Socket) > maxSocketPath {
		t.Fatalf("socket = %q", cfg.Socket)
	}
	// Different homes must not share a socket.
	t.Setenv(EnvHome, "/"+strings.Repeat("y", 120))
	other, _ := Load()
	if other.Socket == cfg.Socket {
		t.Fatal("distinct data dirs mapped to the same socket")
	}
}
