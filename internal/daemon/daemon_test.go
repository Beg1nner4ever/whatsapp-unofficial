package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/api"
)

type fakeBackend struct {
	sent      []api.SendRequest
	loggedOut bool
}

func (f *fakeBackend) Status() api.Status {
	return api.Status{LoggedIn: true, Connected: true, Account: "33600000000"}
}
func (f *fakeBackend) StartPairing(ctx context.Context, phone string) (api.PairResponse, error) {
	if phone == "" {
		return api.PairResponse{QR: "qr-payload"}, nil
	}
	return api.PairResponse{Code: "ABCD-EFGH"}, nil
}
func (f *fakeBackend) Send(ctx context.Context, req api.SendRequest) (string, error) {
	if req.MediaPath == "/etc/passwd" {
		return "", errors.New("files can only be sent from the outbox directory")
	}
	f.sent = append(f.sent, req)
	return "sent", nil
}
func (f *fakeBackend) Download(ctx context.Context, id, chat string) (api.DownloadResponse, error) {
	return api.DownloadResponse{MediaType: "image", Filename: "x.jpg", Path: "/tmp/x.jpg"}, nil
}
func (f *fakeBackend) Logout(ctx context.Context) error { f.loggedOut = true; return nil }

// shortTempDir keeps socket paths under the ~104-byte sun_path limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func startServer(t *testing.T, b api.Backend, idle time.Duration) (*Server, *Client, string, chan error) {
	t.Helper()
	sock := filepath.Join(shortTempDir(t), "d.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(b, idle)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(cancel)
	return srv, NewClient(sock), sock, done
}

func TestRoundTripOverSocket(t *testing.T) {
	b := &fakeBackend{}
	_, c, sock, _ := startServer(t, b, time.Hour)
	ctx := context.Background()

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v, want 0600", info.Mode().Perm())
	}

	st, err := c.Status(ctx)
	if err != nil || !st.LoggedIn || st.Account != "33600000000" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	pr, err := c.Pair(ctx, "33600000000")
	if err != nil || pr.Code != "ABCD-EFGH" {
		t.Fatalf("pair = %+v, %v", pr, err)
	}
	if msg, err := c.Send(ctx, api.SendRequest{Recipient: "1", Message: "hi"}); err != nil || msg != "sent" {
		t.Fatalf("send = %q, %v", msg, err)
	}
	if _, err := c.Send(ctx, api.SendRequest{Recipient: "1", MediaPath: "/etc/passwd"}); err == nil || !strings.Contains(err.Error(), "outbox") {
		t.Fatalf("backend error not propagated: %v", err)
	}
	if d, err := c.Download(ctx, "m", "c"); err != nil || d.Path != "/tmp/x.jpg" {
		t.Fatalf("download = %+v, %v", d, err)
	}
}

func TestGuardRejectsNonJSONAndGET(t *testing.T) {
	srv := NewServer(&fakeBackend{}, time.Hour)
	h := srv.Handler()
	cases := []struct {
		method, ct string
		want       int
	}{
		{http.MethodGet, "application/json", http.StatusMethodNotAllowed},
		{http.MethodPost, "text/plain", http.StatusUnsupportedMediaType},
		{http.MethodPost, "application/json", http.StatusOK},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, "/status", strings.NewReader("{}"))
		req.Header.Set("Content-Type", tc.ct)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.ct, rec.Code, tc.want)
		}
	}
}

func TestIdleShutdown(t *testing.T) {
	_, _, _, done := startServer(t, &fakeBackend{}, 200*time.Millisecond)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not shut down after idle timeout")
	}
}

func TestLogoutStopsDaemon(t *testing.T) {
	b := &fakeBackend{}
	_, c, _, done := startServer(t, b, time.Hour)
	if err := c.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon kept running after logout")
	}
	if !b.loggedOut {
		t.Fatal("backend logout not called")
	}
}

func TestClientReportsNotRunning(t *testing.T) {
	c := NewClient(filepath.Join(shortTempDir(t), "nobody.sock"))
	if _, err := c.Ping(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "d.lock")
	first, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock err = %v, want ErrLocked", err)
	}
	first.Close()
	again, err := Lock(path)
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	again.Close()
}
