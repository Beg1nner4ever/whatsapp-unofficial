// Package daemon runs the single process that holds the WhatsApp session and
// serves a small JSON API on an owner-only Unix socket. MCP servers and the
// CLI start it on demand; it exits after a period without requests.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/api"
)

const maxRequestBytes = 1 << 20

// Server exposes a Backend over HTTP on a Unix socket.
type Server struct {
	backend     api.Backend
	idleTimeout time.Duration

	mu       sync.Mutex
	lastSeen time.Time
	// shutdown is closed when the daemon should exit (idle or logout).
	shutdown     chan struct{}
	shutdownOnce sync.Once
}

func NewServer(backend api.Backend, idleTimeout time.Duration) *Server {
	return &Server{backend: backend, idleTimeout: idleTimeout, lastSeen: time.Now(), shutdown: make(chan struct{})}
}

// Done is closed when the server wants the daemon to stop.
func (s *Server) Done() <-chan struct{} { return s.shutdown }

func (s *Server) stop() { s.shutdownOnce.Do(func() { close(s.shutdown) }) }

func (s *Server) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

func (s *Server) idleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastSeen)
}

// Handler returns the API routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", s.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.backend.Status())
	}))
	mux.HandleFunc("/status", s.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.backend.Status())
	}))
	mux.HandleFunc("/pair", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var req api.PairRequest
		if !decode(w, r, &req) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		resp, err := s.backend.StartPairing(ctx, req.Phone)
		reply(w, resp, err)
	}))
	mux.HandleFunc("/send", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var req api.SendRequest
		if !decode(w, r, &req) {
			return
		}
		msg, err := s.backend.Send(r.Context(), req)
		reply(w, api.SendResponse{Message: msg}, err)
	}))
	mux.HandleFunc("/download", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var req api.DownloadRequest
		if !decode(w, r, &req) {
			return
		}
		if req.MessageID == "" || req.ChatJID == "" {
			writeJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "message_id and chat_jid are required"})
			return
		}
		resp, err := s.backend.Download(r.Context(), req.MessageID, req.ChatJID)
		reply(w, resp, err)
	}))
	mux.HandleFunc("/logout", s.guard(func(w http.ResponseWriter, r *http.Request) {
		err := s.backend.Logout(r.Context())
		reply(w, struct{}{}, err)
		if err == nil {
			s.stop()
		}
	}))
	return mux
}

// guard enforces POST + JSON and records activity for the idle timer. The
// socket itself is owner-only, so no token is needed.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, api.ErrorResponse{Error: "method not allowed"})
			return
		}
		if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, api.ErrorResponse{Error: "Content-Type must be application/json"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		s.touch()
		next(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, api.ErrorResponse{Error: "invalid JSON body"})
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, api.ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// Listen creates the control socket with owner-only permissions. The caller
// must hold the daemon lock, so removing a stale socket file is safe.
func Listen(socketPath string) (net.Listener, error) {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("restrict socket: %w", err)
	}
	return ln, nil
}

// Serve runs the API until ctx is cancelled, the idle timeout passes, or a
// logout asks the daemon to stop.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute, // media upload/download
		IdleTimeout:       90 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	tick := time.NewTicker(min(s.idleTimeout/4, 30*time.Second))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return shutdownHTTP(srv)
		case <-s.shutdown:
			return shutdownHTTP(srv)
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-tick.C:
			if s.idleFor() >= s.idleTimeout {
				return shutdownHTTP(srv)
			}
		}
	}
}

func shutdownHTTP(srv *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
