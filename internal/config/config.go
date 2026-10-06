// Package config resolves where whatsapp-unofficial keeps its data and which
// optional behaviours are enabled. Everything is driven by environment
// variables so plugin manifests and installers can configure the binary.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/safety"
)

const (
	EnvHome        = "WHATSAPP_UNOFFICIAL_HOME"
	EnvAllowSend   = "WHATSAPP_UNOFFICIAL_ALLOW_SEND"
	EnvIdleMinutes = "WHATSAPP_UNOFFICIAL_IDLE_MINUTES"

	defaultIdle = 15 * time.Minute
	// sun_path is 104 bytes on macOS and 108 on Linux, including the NUL.
	maxSocketPath = 100
)

// Config is the resolved runtime configuration.
type Config struct {
	Home        string // root data directory (0700)
	SessionDB   string // whatsmeow session store: device keys, contacts, LID map
	MessagesDB  string // chats and messages synced by the daemon
	OutboxDir   string // the only directory files may be sent from
	MediaDir    string // downloaded media
	Socket      string // daemon control socket
	LockFile    string // held by the running daemon
	LogFile     string // daemon log
	AllowSend   bool
	IdleTimeout time.Duration
}

// EnvBool reports whether an environment variable is set to a truthy value.
func EnvBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Load resolves the configuration without touching the filesystem.
func Load() (Config, error) {
	home := strings.TrimSpace(os.Getenv(EnvHome))
	if home == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return Config{}, fmt.Errorf("cannot determine a data directory (set %s): %w", EnvHome, err)
		}
		home = filepath.Join(base, "whatsapp-unofficial")
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data directory: %w", err)
	}

	idle := defaultIdle
	if v := strings.TrimSpace(os.Getenv(EnvIdleMinutes)); v != "" {
		minutes, err := strconv.Atoi(v)
		if err != nil || minutes < 1 {
			return Config{}, fmt.Errorf("%s must be a positive number of minutes, got %q", EnvIdleMinutes, v)
		}
		idle = time.Duration(minutes) * time.Minute
	}

	cfg := Config{
		Home:        home,
		SessionDB:   filepath.Join(home, "whatsapp.db"),
		MessagesDB:  filepath.Join(home, "messages.db"),
		OutboxDir:   filepath.Join(home, "outbox"),
		MediaDir:    filepath.Join(home, "media"),
		Socket:      socketPath(home),
		LockFile:    filepath.Join(home, "daemon.lock"),
		LogFile:     filepath.Join(home, "daemon.log"),
		AllowSend:   EnvBool(EnvAllowSend),
		IdleTimeout: idle,
	}
	if len(cfg.Socket) > maxSocketPath {
		return Config{}, fmt.Errorf("data directory path is too long for a control socket (%d bytes); set %s to a shorter path", len(cfg.Socket), EnvHome)
	}
	return cfg, nil
}

// socketPath puts the control socket in the data directory, or, when that
// path is too long for a Unix socket, in a short per-user private runtime
// directory ($XDG_RUNTIME_DIR, or $TMPDIR on macOS, both owner-only).
func socketPath(home string) string {
	p := filepath.Join(home, "daemon.sock")
	if len(p) <= maxSocketPath {
		return p
	}
	sum := sha256.Sum256([]byte(home))
	name := "whatsapp-unofficial-" + hex.EncodeToString(sum[:])[:12] + ".sock"
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, name)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(os.TempDir(), name)
	}
	return p
}

// EnsureDirs creates the private directory layout and tightens permissions on
// anything created by earlier or more permissive versions.
func (c Config) EnsureDirs() error {
	for _, dir := range []string{c.Home, c.OutboxDir, c.MediaDir} {
		if err := safety.EnsurePrivateDir(dir); err != nil {
			return err
		}
	}
	matches, _ := filepath.Glob(filepath.Join(c.Home, "*.db*"))
	for _, m := range append(matches, c.LogFile) {
		_ = os.Chmod(m, 0o600)
	}
	return nil
}
