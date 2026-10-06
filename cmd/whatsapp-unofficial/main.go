// Command whatsapp-unofficial is an unofficial WhatsApp integration for AI
// agents: an MCP server, the daemon that holds the WhatsApp session, and CLI
// commands to link, inspect and unlink the account.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/config"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/daemon"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/mcpserver"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/safety"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/store"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/wa"
)

// version is set at build time with -ldflags "-X main.version=..."
var version = "dev"

const usage = `whatsapp-unofficial - unofficial WhatsApp integration for AI agents
(not affiliated with WhatsApp or Meta)

Usage:
  whatsapp-unofficial serve              run the MCP server on stdio (used by Claude Code / Codex)
  whatsapp-unofficial login [--phone N]  link your account (QR code, or pairing code with --phone)
  whatsapp-unofficial status             show link and daemon status
  whatsapp-unofficial logout [--wipe]    unlink this device; --wipe also deletes all local data
  whatsapp-unofficial daemon             run the background daemon (started automatically)
  whatsapp-unofficial version

Environment:
  WHATSAPP_UNOFFICIAL_HOME          data directory (default: OS config dir/whatsapp-unofficial)
  WHATSAPP_UNOFFICIAL_ALLOW_SEND    set to 1/true to enable the send tools (default: off)
  WHATSAPP_UNOFFICIAL_IDLE_MINUTES  daemon idle shutdown (default: 15)
`

func main() {
	safety.SetPrivateUmask()
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "serve":
		err = runServe(ctx)
	case "daemon":
		err = runDaemon(ctx)
	case "login":
		err = runLogin(ctx, args)
	case "status":
		err = runStatus(ctx)
	case "logout":
		err = runLogout(ctx, args)
	case "version", "--version", "-v":
		fmt.Println(version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func loadConfig() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return cfg, err
	}
	return cfg, cfg.EnsureDirs()
}

// runServe runs the MCP server. Nothing may be written to stdout except the
// MCP protocol.
func runServe(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return mcpserver.Run(ctx, mcpserver.Deps{
		Config:  cfg,
		Reader:  &store.Reader{MessagesPath: cfg.MessagesDB, SessionPath: cfg.SessionDB},
		Version: version,
		Daemon: func(ctx context.Context) (mcpserver.Daemon, error) {
			return daemon.EnsureRunning(ctx, cfg)
		},
	})
}

func runDaemon(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	lock, err := daemon.Lock(cfg.LockFile)
	if errors.Is(err, daemon.ErrLocked) {
		return nil // another daemon is already serving
	}
	if err != nil {
		return err
	}
	defer lock.Close()

	logger := waLog.Stdout("whatsapp", "INFO", false)
	client, err := wa.Open(ctx, cfg, version, logger)
	if err != nil {
		return err
	}
	defer client.Close()
	if client.LoggedIn() {
		if err := client.Connect(); err != nil {
			logger.Errorf("Connect failed: %v", err)
		}
	} else {
		logger.Infof("No account linked; waiting for pairing")
	}

	ln, err := daemon.Listen(cfg.Socket)
	if err != nil {
		return err
	}
	defer os.Remove(cfg.Socket)
	logger.Infof("Daemon %s listening (idle timeout %s, data %s)", version, cfg.IdleTimeout, cfg.Home)
	err = daemon.NewServer(client, cfg.IdleTimeout).Serve(ctx, ln)
	logger.Infof("Daemon stopping")
	return err
}

func runLogin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	phone := fs.String("phone", "", "link with a pairing code for this phone number (international format) instead of a QR code")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	dm, err := daemon.EnsureRunning(ctx, cfg)
	if err != nil {
		return err
	}
	st, err := dm.Status(ctx)
	if err != nil {
		return err
	}
	if st.LoggedIn {
		fmt.Printf("Already linked to +%s.\n", st.Account)
		return nil
	}

	resp, err := dm.Pair(ctx, *phone)
	if err != nil {
		return err
	}
	if resp.Code != "" {
		fmt.Printf("\nPairing code: %s\n\nOn your phone: WhatsApp > Linked devices > Link a device >\n\"Link with phone number instead\", then enter the code.\n\n", resp.Code)
	}
	lastQR := ""
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		st, err := dm.Status(ctx)
		if err != nil {
			return err
		}
		if st.LoggedIn {
			fmt.Printf("Linked to +%s. Your message history will sync over the next few minutes.\n", st.Account)
			return nil
		}
		if !st.Pairing {
			return errors.New("pairing ended without linking; run login again")
		}
		if resp.Code == "" && st.QR != "" && st.QR != lastQR {
			lastQR = st.QR
			fmt.Println("\nScan this QR code: WhatsApp > Linked devices > Link a device")
			qrterminal.GenerateHalfBlock(st.QR, qrterminal.L, os.Stdout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("timed out waiting for the phone to link")
}

func runStatus(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fmt.Printf("Version:      %s\nData:         %s\nOutbox:       %s\nSending:      %v\n", version, cfg.Home, cfg.OutboxDir, cfg.AllowSend)
	st, err := daemon.NewClient(cfg.Socket).Status(ctx)
	if errors.Is(err, daemon.ErrNotRunning) {
		fmt.Println("Daemon:       not running (starts automatically when an agent uses WhatsApp)")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("Daemon:       running (%s)\nLinked:       %v\n", st.Version, st.LoggedIn)
	if st.LoggedIn {
		fmt.Printf("Account:      +%s\nConnected:    %v\n", st.Account, st.Connected)
	}
	return nil
}

func runLogout(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	wipe := fs.Bool("wipe", false, "also delete all local data (session, messages, media, outbox)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	dm, err := daemon.EnsureRunning(ctx, cfg)
	if err != nil {
		return err
	}
	if err := dm.Logout(ctx); err != nil {
		return fmt.Errorf("unlink: %w", err)
	}
	fmt.Println("Unlinked this device from WhatsApp.")
	if !*wipe {
		return nil
	}
	// Wait for the daemon to release its lock before deleting its files.
	for i := 0; i < 50; i++ {
		if l, err := daemon.Lock(cfg.LockFile); err == nil {
			l.Close()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err := os.RemoveAll(cfg.Home); err != nil {
		return fmt.Errorf("delete %s: %w", cfg.Home, err)
	}
	fmt.Printf("Deleted %s.\n", cfg.Home)
	return nil
}
