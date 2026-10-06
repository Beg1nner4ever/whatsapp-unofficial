package safety

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveOutboxPath(t *testing.T) {
	outbox := filepath.Join(t.TempDir(), "outbox")
	if err := EnsurePrivateDir(outbox); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(outbox, "photo.jpg")
	if err := os.WriteFile(good, []byte("img"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(outbox, "innocent.jpg")); err != nil {
		t.Fatal(err)
	}

	realGood, _ := filepath.EvalSymlinks(good)
	for _, ok := range []string{good, "photo.jpg"} {
		got, err := ResolveOutboxPath(outbox, ok)
		if err != nil || got != realGood {
			t.Errorf("ResolveOutboxPath(%q) = %q, %v; want %q", ok, got, err, realGood)
		}
	}
	for _, bad := range []string{secret, "../outbox/../x", "innocent.jpg", outbox, ".", "missing.jpg", ""} {
		if got, err := ResolveOutboxPath(outbox, bad); err == nil {
			t.Errorf("ResolveOutboxPath(%q) = %q, want error", bad, got)
		}
	}
}

func TestSafeMediaFilename(t *testing.T) {
	cases := map[string]string{
		"report.pdf": "MSG1_report.pdf",
		"../../../../Library/LaunchAgents/x.plist": "MSG1_x.plist",
		`..\..\evil.bat`:  "MSG1_evil.bat",
		"..":              "MSG1_file",
		".zshrc":          "MSG1_zshrc",
		"":                "MSG1_file",
		"a\x00b\nc.txt":   "MSG1_a_b_c.txt",
		"facture été.pdf": "MSG1_facture été.pdf",
	}
	for in, want := range cases {
		if got := SafeMediaFilename("MSG1", in); got != want {
			t.Errorf("SafeMediaFilename(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeMediaFilename("M", strings.Repeat("a", 500)+".pdf"); len([]rune(got)) > maxFilenameRunes+2 {
		t.Errorf("long filename not truncated: %d runes", len([]rune(got)))
	}
}

func TestMediaLocalPathStaysInMediaDir(t *testing.T) {
	mediaDir := t.TempDir()
	for _, h := range []struct{ chat, id, name string }{
		{"123@s.whatsapp.net", "ID1", "../../../../Library/LaunchAgents/evil.plist"},
		{"../../..", "ID2", "x.jpg"},
		{"123@s.whatsapp.net", "../../ID3", "x.jpg"},
		{"..", "..", ".."},
		{"", "", ""},
	} {
		dir, full, err := MediaLocalPath(mediaDir, h.chat, h.id, h.name)
		if err != nil {
			continue
		}
		if !IsWithin(mediaDir, full) || filepath.Dir(full) != dir || dir == mediaDir {
			t.Errorf("MediaLocalPath(%q, %q, %q) = %q escapes %q", h.chat, h.id, h.name, full, mediaDir)
		}
	}
}

func TestWriteNewPrivateFile(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteNewPrivateFile(victim, []byte("evil")); err == nil {
		t.Fatal("overwrote an existing file")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "target"), link); err != nil {
		t.Fatal(err)
	}
	if err := WriteNewPrivateFile(link, []byte("evil")); err == nil {
		t.Fatal("followed a dangling symlink")
	}
	fresh := filepath.Join(dir, "fresh")
	if err := WriteNewPrivateFile(fresh, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(fresh); info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}
