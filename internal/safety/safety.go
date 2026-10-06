// Package safety holds the filesystem guards shared by the daemon and the MCP
// server: private directories, outbox containment for outgoing files, and
// sanitized paths for downloaded media whose names come from other people.
package safety

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const maxFilenameRunes = 100

// EnsurePrivateDir creates dir (and parents) and restricts it to the owner,
// including directories created earlier with looser permissions.
func EnsurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("restrict %s: %w", dir, err)
	}
	return nil
}

// IsWithin reports whether target is base itself or lies beneath it.
func IsWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ResolveOutboxPath maps a requested path to a regular file inside outboxDir.
// Relative paths are resolved against the outbox, and symlinks are resolved
// before the containment check so a link pointing outside is rejected.
func ResolveOutboxPath(outboxDir, requested string) (string, error) {
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
		return "", fmt.Errorf("media file not found: %s", requested)
	}
	if !IsWithin(base, real) || real == base {
		return "", fmt.Errorf("files can only be sent from the outbox directory %s; copy the file there first", outboxDir)
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

// SanitizePathComponent turns an identifier (chat JID, message ID) into a
// single safe path segment.
func SanitizePathComponent(s string) string {
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

// SafeMediaFilename builds a local filename for downloaded media. The
// sender-supplied name is reduced to its base name and stripped of anything
// that could escape the media directory or hide the file; the message ID
// prefix keeps names unique.
func SafeMediaFilename(messageID, name string) string {
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
	return SanitizePathComponent(messageID) + "_" + clean
}

// MediaLocalPath returns the directory and full path where media for a
// message is stored, guaranteeing the result stays inside mediaDir.
func MediaLocalPath(mediaDir, chatJID, messageID, filename string) (string, string, error) {
	chatDir := filepath.Join(mediaDir, SanitizePathComponent(chatJID))
	full := filepath.Join(chatDir, SafeMediaFilename(messageID, filename))
	if !IsWithin(mediaDir, full) || filepath.Dir(full) != chatDir || chatDir == mediaDir {
		return "", "", fmt.Errorf("refusing unsafe media path for message %s", messageID)
	}
	return chatDir, full, nil
}

// WriteNewPrivateFile creates path with owner-only permissions. It never
// overwrites an existing file and never follows a symlink (O_EXCL).
func WriteNewPrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}
