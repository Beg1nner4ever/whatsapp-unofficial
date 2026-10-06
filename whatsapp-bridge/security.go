package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	defaultBridgeAddr = "127.0.0.1:8080"
	tokenFileName     = "bridge_token"
	maxRequestBytes   = 1 << 20
	maxFilenameRunes  = 100
)

// bridgeConfig holds runtime settings. Everything is driven by environment
// variables so installers and plugin wrappers can configure the bridge
// without editing code.
type bridgeConfig struct {
	storeDir  string // session DB, message DB, token, media
	outboxDir string // the only directory files may be sent from
	mediaDir  string // downloaded media
	addr      string
	allowSend bool
	token     string
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// loadConfig reads configuration from the environment, creates the private
// store layout and loads (or generates) the API token.
func loadConfig() (bridgeConfig, error) {
	storeDir := os.Getenv("WHATSAPP_STORE_DIR")
	if storeDir == "" {
		storeDir = "store"
	}
	storeDir, err := filepath.Abs(storeDir)
	if err != nil {
		return bridgeConfig{}, fmt.Errorf("resolve store dir: %w", err)
	}

	addr := os.Getenv("WHATSAPP_BRIDGE_ADDR")
	if addr == "" {
		addr = defaultBridgeAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return bridgeConfig{}, fmt.Errorf("invalid WHATSAPP_BRIDGE_ADDR %q: %w", addr, err)
	}
	if !isLoopbackHost(host) && !envBool("WHATSAPP_BRIDGE_ALLOW_NON_LOOPBACK") {
		return bridgeConfig{}, fmt.Errorf("refusing to listen on non-loopback address %q; set WHATSAPP_BRIDGE_ALLOW_NON_LOOPBACK=1 if you really need this", addr)
	}

	cfg := bridgeConfig{
		storeDir:  storeDir,
		outboxDir: filepath.Join(storeDir, "outbox"),
		mediaDir:  filepath.Join(storeDir, "media"),
		addr:      addr,
		allowSend: envBool("WHATSAPP_ALLOW_SEND"),
	}

	for _, dir := range []string{cfg.storeDir, cfg.outboxDir, cfg.mediaDir} {
		if err := ensurePrivateDir(dir); err != nil {
			return bridgeConfig{}, err
		}
	}
	hardenStoreFiles(cfg.storeDir)

	if envToken := strings.TrimSpace(os.Getenv("WHATSAPP_BRIDGE_TOKEN")); envToken != "" {
		if len(envToken) < 32 {
			return bridgeConfig{}, errors.New("WHATSAPP_BRIDGE_TOKEN must be at least 32 characters")
		}
		cfg.token = envToken
	} else {
		cfg.token, err = loadOrCreateToken(filepath.Join(cfg.storeDir, tokenFileName))
		if err != nil {
			return bridgeConfig{}, err
		}
	}
	return cfg, nil
}

// ensurePrivateDir creates dir (and parents) and restricts it to the owner,
// including directories created by earlier, more permissive versions.
func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict %s: %w", dir, err)
	}
	return nil
}

// hardenStoreFiles tightens permissions on databases left behind by earlier
// versions, which created them world-readable. whatsapp.db holds the session
// keys: anyone who can read it can take over the linked device.
func hardenStoreFiles(storeDir string) {
	matches, _ := filepath.Glob(filepath.Join(storeDir, "*.db*"))
	for _, m := range matches {
		_ = os.Chmod(m, 0o600)
	}
}

// loadOrCreateToken returns the bearer token stored at tokenPath, creating a
// random one (owner-readable only) on first run.
func loadOrCreateToken(tokenPath string) (string, error) {
	data, err := os.ReadFile(tokenPath)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) < 32 {
			return "", fmt.Errorf("token file %s is too short; delete it to regenerate", tokenPath)
		}
		_ = os.Chmod(tokenPath, 0o600)
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read token file: %w", err)
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(buf)

	f, err := os.OpenFile(tokenPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create token file: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(token + "\n"); err != nil {
		return "", fmt.Errorf("write token file: %w", err)
	}
	return token, nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// requireAPIAccess guards every bridge endpoint:
//   - browsers are rejected outright (Origin / Sec-Fetch-Site), which blocks
//     drive-by requests from web pages, including "simple" no-cors POSTs;
//   - the Host header must be loopback when bound to loopback (DNS rebinding);
//   - a bearer token is required (constant-time compare);
//   - only application/json bodies up to maxRequestBytes are accepted.
func requireAPIAccess(cfg bridgeConfig, next http.HandlerFunc) http.HandlerFunc {
	bindHost, _, _ := net.SplitHostPort(cfg.addr)
	checkHost := isLoopbackHost(bindHost)
	expected := []byte("Bearer " + cfg.token)

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "Browser requests are not allowed", http.StatusForbidden)
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" {
			http.Error(w, "Browser requests are not allowed", http.StatusForbidden)
			return
		}
		if checkHost {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
			}
			if !isLoopbackHost(host) {
				http.Error(w, "Invalid Host header", http.StatusForbidden)
				return
			}
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		next(w, r)
	}
}

// resolveOutboxPath maps a requested media path to a regular file inside the
// outbox directory. Relative paths are resolved against the outbox. Symlinks
// are resolved before the containment check, so a link pointing outside the
// outbox is rejected.
func resolveOutboxPath(outboxDir, requested string) (string, error) {
	if requested == "" {
		return "", errors.New("media path is empty")
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(outboxDir, requested)
	}
	base, err := filepath.EvalSymlinks(outboxDir)
	if err != nil {
		return "", fmt.Errorf("outbox directory unavailable: %w", err)
	}
	real, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return "", fmt.Errorf("media file not found: %w", err)
	}
	if !isWithin(base, real) {
		return "", fmt.Errorf("media files must be inside the outbox directory %s", outboxDir)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("media file not accessible: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("media path is not a regular file")
	}
	return real, nil
}

// isWithin reports whether target is base itself or lies beneath it.
func isWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// sanitizePathComponent turns an identifier (chat JID, message ID) into a
// single safe path segment.
func sanitizePathComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)), r == '@', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if out == "" {
		return "_"
	}
	return out
}

// safeMediaFilename builds a local filename for downloaded media. The
// sender-supplied name is reduced to its base name and stripped of anything
// that could escape the media directory or hide the file; the message ID
// prefix keeps names unique.
func safeMediaFilename(messageID, name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))

	var b strings.Builder
	count := 0
	for _, r := range name {
		if count >= maxFilenameRunes {
			break
		}
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.', r == '_', r == '-', r == ' ', r == '(', r == ')':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		count++
	}
	clean := strings.TrimSpace(strings.TrimLeft(b.String(), ". "))
	if clean == "" {
		clean = "file"
	}
	return sanitizePathComponent(messageID) + "_" + clean
}

// mediaLocalPath returns the directory and full path where media for a
// message is stored, guaranteeing the result stays inside mediaDir.
func mediaLocalPath(mediaDir, chatJID, messageID, filename string) (string, string, error) {
	chatDir := filepath.Join(mediaDir, sanitizePathComponent(chatJID))
	full := filepath.Join(chatDir, safeMediaFilename(messageID, filename))
	if !isWithin(mediaDir, full) || filepath.Dir(full) != chatDir {
		return "", "", fmt.Errorf("refusing unsafe media path for message %s", messageID)
	}
	return chatDir, full, nil
}
