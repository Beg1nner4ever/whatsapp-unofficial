package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/api"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/config"
)

// ErrNotRunning means nothing is listening on the control socket.
var ErrNotRunning = errors.New("daemon is not running")

// Client talks to the daemon over its Unix socket.
type Client struct {
	socket string
	http   *http.Client
}

func NewClient(socket string) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		Proxy: nil, // never route the control socket through a proxy
	}
	return &Client{socket: socket, http: &http.Client{Transport: transport, Timeout: 6 * time.Minute}}
}

func (c *Client) call(ctx context.Context, path string, req, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://daemon"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return ErrNotRunning
		}
		return err
	}
	defer httpResp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(httpResp.Body, 4<<20))
	if err != nil {
		return err
	}
	if httpResp.StatusCode != http.StatusOK {
		var e api.ErrorResponse
		if json.Unmarshal(data, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("daemon returned HTTP %d", httpResp.StatusCode)
	}
	if resp == nil {
		return nil
	}
	return json.Unmarshal(data, resp)
}

func (c *Client) Ping(ctx context.Context) (api.Status, error) {
	var st api.Status
	err := c.call(ctx, "/ping", struct{}{}, &st)
	return st, err
}

func (c *Client) Status(ctx context.Context) (api.Status, error) {
	var st api.Status
	err := c.call(ctx, "/status", struct{}{}, &st)
	return st, err
}

func (c *Client) Pair(ctx context.Context, phone string) (api.PairResponse, error) {
	var resp api.PairResponse
	err := c.call(ctx, "/pair", api.PairRequest{Phone: phone}, &resp)
	return resp, err
}

func (c *Client) Send(ctx context.Context, req api.SendRequest) (string, error) {
	var resp api.SendResponse
	err := c.call(ctx, "/send", req, &resp)
	return resp.Message, err
}

func (c *Client) Download(ctx context.Context, messageID, chatJID string) (api.DownloadResponse, error) {
	var resp api.DownloadResponse
	err := c.call(ctx, "/download", api.DownloadRequest{MessageID: messageID, ChatJID: chatJID}, &resp)
	return resp, err
}

func (c *Client) Logout(ctx context.Context) error {
	return c.call(ctx, "/logout", struct{}{}, nil)
}

// EnsureRunning returns a client for a running daemon, starting one in the
// background if needed. Concurrent callers are safe: the daemon itself takes
// an exclusive lock, so extra instances exit immediately.
func EnsureRunning(ctx context.Context, cfg config.Config) (*Client, error) {
	c := NewClient(cfg.Socket)
	if _, err := c.Ping(ctx); err == nil {
		return c, nil
	}
	if err := spawn(cfg); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
		if _, err := c.Ping(ctx); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("daemon did not start; see %s", cfg.LogFile)
}

// spawn starts "<this executable> daemon" detached from the caller, with
// output appended to the daemon log.
func spawn(cfg config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	logFile, err := os.OpenFile(cfg.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "daemon")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	// Don't wait: the daemon outlives this process. Release resources.
	return cmd.Process.Release()
}
