package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef"

func testConfig(t *testing.T, allowSend bool) bridgeConfig {
	t.Helper()
	store := t.TempDir()
	cfg := bridgeConfig{
		storeDir:  store,
		outboxDir: filepath.Join(store, "outbox"),
		mediaDir:  filepath.Join(store, "media"),
		addr:      defaultBridgeAddr,
		allowSend: allowSend,
		token:     testToken,
	}
	for _, d := range []string{cfg.outboxDir, cfg.mediaDir} {
		if err := ensurePrivateDir(d); err != nil {
			t.Fatal(err)
		}
	}
	return cfg
}

type sendCall struct{ recipient, message, mediaPath string }

func newTestAPI(cfg bridgeConfig) (*bridgeAPI, *[]sendCall) {
	var calls []sendCall
	api := &bridgeAPI{
		cfg: cfg,
		send: func(recipient, message, mediaPath string) (bool, string) {
			calls = append(calls, sendCall{recipient, message, mediaPath})
			return true, "sent"
		},
		download: func(messageID, chatJID string) (bool, string, string, string, error) {
			return true, "image", "x.jpg", "/tmp/x.jpg", nil
		},
	}
	return api, &calls
}

func doRequest(h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func validHeaders() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + testToken,
		"Content-Type":  "application/json",
	}
}

func TestAPIAccessControl(t *testing.T) {
	cfg := testConfig(t, true)
	api, calls := newTestAPI(cfg)
	h := api.routes()
	body := `{"recipient":"123","message":"hi"}`

	withHeader := func(k, v string) map[string]string {
		h := validHeaders()
		if v == "" {
			delete(h, k)
		} else {
			h[k] = v
		}
		return h
	}

	cases := []struct {
		name    string
		method  string
		headers map[string]string
		host    string
		want    int
	}{
		{"valid request", http.MethodPost, validHeaders(), "", http.StatusOK},
		{"GET rejected", http.MethodGet, validHeaders(), "", http.StatusMethodNotAllowed},
		{"missing token", http.MethodPost, withHeader("Authorization", ""), "", http.StatusUnauthorized},
		{"wrong token", http.MethodPost, withHeader("Authorization", "Bearer nope"), "", http.StatusUnauthorized},
		{"browser origin", http.MethodPost, withHeader("Origin", "https://evil.example"), "", http.StatusForbidden},
		{"browser fetch metadata", http.MethodPost, withHeader("Sec-Fetch-Site", "cross-site"), "", http.StatusForbidden},
		{"text/plain CSRF body", http.MethodPost, withHeader("Content-Type", "text/plain"), "", http.StatusUnsupportedMediaType},
		{"DNS rebinding host", http.MethodPost, validHeaders(), "evil.example:8080", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/send", strings.NewReader(body))
			req.Host = "127.0.0.1:8080"
			if tc.host != "" {
				req.Host = tc.host
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	if len(*calls) != 1 {
		t.Fatalf("send called %d times, want exactly 1 (only the valid request)", len(*calls))
	}
}

func TestSendDisabledByDefault(t *testing.T) {
	cfg := testConfig(t, false)
	api, calls := newTestAPI(cfg)
	rec := doRequest(api.routes(), http.MethodPost, "/api/send", `{"recipient":"123","message":"hi"}`, validHeaders())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(*calls) != 0 {
		t.Fatal("send must not be called when sending is disabled")
	}
}

func TestSendMediaRestrictedToOutbox(t *testing.T) {
	cfg := testConfig(t, true)
	api, calls := newTestAPI(cfg)
	h := api.routes()

	inside := filepath.Join(cfg.outboxDir, "photo.jpg")
	if err := os.WriteFile(inside, []byte("img"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(cfg.outboxDir, "innocent.jpg")); err != nil {
		t.Fatal(err)
	}

	rejected := []string{
		secret,               // absolute path outside the outbox
		"../bridge_token",    // traversal out of the outbox
		"innocent.jpg",       // symlink pointing outside
		cfg.outboxDir,        // a directory, not a file
		"does-not-exist.jpg", // missing file
	}
	for _, p := range rejected {
		rec := doRequest(h, http.MethodPost, "/api/send", `{"recipient":"123","media_path":`+jsonString(p)+`}`, validHeaders())
		if rec.Code != http.StatusForbidden {
			t.Errorf("media_path %q: status = %d, want 403", p, rec.Code)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("send called for a rejected path: %+v", *calls)
	}

	for _, p := range []string{inside, "photo.jpg"} {
		rec := doRequest(h, http.MethodPost, "/api/send", `{"recipient":"123","media_path":`+jsonString(p)+`}`, validHeaders())
		if rec.Code != http.StatusOK {
			t.Fatalf("media_path %q: status = %d, want 200 (%s)", p, rec.Code, rec.Body.String())
		}
	}
	realInside, _ := filepath.EvalSymlinks(inside)
	for _, c := range *calls {
		if c.mediaPath != realInside {
			t.Errorf("send got media path %q, want resolved %q", c.mediaPath, realInside)
		}
	}
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
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
		if got := safeMediaFilename("MSG1", in); got != want {
			t.Errorf("safeMediaFilename(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 500) + ".pdf"
	if got := safeMediaFilename("M", long); len([]rune(got)) > maxFilenameRunes+2 {
		t.Errorf("long filename not truncated: %d runes", len([]rune(got)))
	}
}

func TestMediaLocalPathStaysInMediaDir(t *testing.T) {
	mediaDir := t.TempDir()
	hostile := []struct{ chat, id, name string }{
		{"123@s.whatsapp.net", "ID1", "../../../../Library/LaunchAgents/evil.plist"},
		{"../../..", "ID2", "x.jpg"},
		{"123@s.whatsapp.net", "../../ID3", "x.jpg"},
		{"..", "..", ".."},
	}
	for _, h := range hostile {
		dir, full, err := mediaLocalPath(mediaDir, h.chat, h.id, h.name)
		if err != nil {
			continue
		}
		if !isWithin(mediaDir, full) || filepath.Dir(full) != dir || dir == mediaDir {
			t.Errorf("mediaLocalPath(%q, %q, %q) = %q escapes %q", h.chat, h.id, h.name, full, mediaDir)
		}
	}
}

func TestWriteNewPrivateFileRefusesExistingAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "victim")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeNewPrivateFile(target, []byte("evil")); err == nil {
		t.Fatal("overwrote an existing file")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "new-target"), link); err != nil {
		t.Fatal(err)
	}
	if err := writeNewPrivateFile(link, []byte("evil")); err == nil {
		t.Fatal("followed a dangling symlink")
	}

	fresh := filepath.Join(dir, "fresh")
	if err := writeNewPrivateFile(fresh, []byte("ok")); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(fresh)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestTokenFileCreatedPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), tokenFileName)
	tok, err := loadOrCreateToken(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 {
		t.Fatalf("token length = %d, want 64", len(tok))
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token mode = %v, want 0600", info.Mode().Perm())
	}
	again, err := loadOrCreateToken(p)
	if err != nil || again != tok {
		t.Fatalf("token not stable across loads: %q vs %q (%v)", again, tok, err)
	}
}

func TestLoadConfigRefusesNonLoopback(t *testing.T) {
	t.Setenv("WHATSAPP_STORE_DIR", t.TempDir())
	t.Setenv("WHATSAPP_BRIDGE_ADDR", "0.0.0.0:8080")
	t.Setenv("WHATSAPP_BRIDGE_ALLOW_NON_LOOPBACK", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected non-loopback bind to be refused")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	store := t.TempDir()
	t.Setenv("WHATSAPP_STORE_DIR", store)
	t.Setenv("WHATSAPP_BRIDGE_ADDR", "")
	t.Setenv("WHATSAPP_ALLOW_SEND", "")
	t.Setenv("WHATSAPP_BRIDGE_TOKEN", "")
	// Simulate a database left world-readable by the upstream version.
	if err := os.WriteFile(filepath.Join(store, "whatsapp.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.addr != defaultBridgeAddr || cfg.allowSend {
		t.Fatalf("unexpected defaults: addr=%s allowSend=%v", cfg.addr, cfg.allowSend)
	}
	for _, p := range []string{store, cfg.outboxDir, cfg.mediaDir} {
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, want 0700", p, info.Mode().Perm())
		}
	}
	info, _ := os.Stat(filepath.Join(store, "whatsapp.db"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("existing db mode = %v, want 0600", info.Mode().Perm())
	}
}
