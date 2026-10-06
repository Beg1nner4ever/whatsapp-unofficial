// Package wa wraps the whatsmeow client: it keeps the message store in sync,
// pairs new devices, and sends or downloads media under the safety rules.
package wa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/api"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/config"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/store"
)

// Client is the daemon's WhatsApp connection. It implements api.Backend.
type Client struct {
	cfg     config.Config
	cli     *whatsmeow.Client
	msgs    *store.Writer
	log     waLog.Logger
	version string

	mu      sync.Mutex
	pairing bool
	qr      string
	qrReady chan struct{}
}

// Open loads the session store and message database. It does not connect.
func Open(ctx context.Context, cfg config.Config, version string, logger waLog.Logger) (*Client, error) {
	container, err := sqlstore.New(ctx, "sqlite", store.SQLiteDSN(cfg.SessionDB, false), logger.Sub("Database"))
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("load device: %w", err)
	}
	msgs, err := store.OpenWriter(cfg.MessagesDB)
	if err != nil {
		return nil, err
	}
	c := &Client{cfg: cfg, cli: whatsmeow.NewClient(device, logger), msgs: msgs, log: logger, version: version}
	c.cli.AddEventHandler(c.handleEvent)
	return c, nil
}

// Close disconnects and closes the message database.
func (c *Client) Close() {
	c.cli.Disconnect()
	c.msgs.Close()
}

// LoggedIn reports whether this device is linked to an account.
func (c *Client) LoggedIn() bool { return c.cli.Store.ID != nil }

// Connect connects a linked device.
func (c *Client) Connect() error {
	if !c.LoggedIn() {
		return errors.New("not linked to a WhatsApp account")
	}
	return c.cli.Connect()
}

func (c *Client) Status() api.Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := api.Status{
		LoggedIn:  c.LoggedIn(),
		Connected: c.cli.IsConnected() && c.cli.IsLoggedIn(),
		Pairing:   c.pairing,
		Version:   c.version,
	}
	if c.LoggedIn() {
		st.Account = c.cli.Store.ID.User
	}
	if c.pairing {
		st.QR = c.qr
	}
	return st
}

// pairDisplayName must be "Browser (OS)" with values WhatsApp accepts.
const pairDisplayName = "Chrome (Linux)"

// StartPairing links this device to an account. With a phone number it
// returns an 8-character code to type into WhatsApp; without one it returns
// the current QR payload. Calling it again while pairing is in progress
// reuses the same session.
func (c *Client) StartPairing(ctx context.Context, phone string) (api.PairResponse, error) {
	if c.LoggedIn() {
		return api.PairResponse{}, errors.New("already linked to a WhatsApp account; use logout first to link a different one")
	}
	phone = store.NormalizePhone(phone)
	if phone != "" && (len(phone) < 8 || len(phone) > 15) {
		return api.PairResponse{}, errors.New("phone number must be in international format, e.g. 33612345678 (country code + number)")
	}

	c.mu.Lock()
	if !c.pairing {
		qrChan, err := c.cli.GetQRChannel(context.Background())
		if err != nil {
			c.mu.Unlock()
			return api.PairResponse{}, fmt.Errorf("start pairing: %w", err)
		}
		if err := c.cli.Connect(); err != nil {
			c.mu.Unlock()
			return api.PairResponse{}, fmt.Errorf("connect: %w", err)
		}
		c.pairing = true
		c.qr = ""
		c.qrReady = make(chan struct{})
		go c.consumeQR(qrChan, c.qrReady)
	}
	ready := c.qrReady
	c.mu.Unlock()

	select {
	case <-ready:
	case <-ctx.Done():
		return api.PairResponse{}, ctx.Err()
	case <-time.After(30 * time.Second):
		return api.PairResponse{}, errors.New("timed out waiting for WhatsApp to start pairing")
	}

	c.mu.Lock()
	resp := api.PairResponse{QR: c.qr}
	stillPairing := c.pairing
	c.mu.Unlock()
	if !stillPairing {
		return api.PairResponse{}, errors.New("pairing ended before it could start; try again")
	}
	if phone == "" {
		return resp, nil
	}
	code, err := c.cli.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, pairDisplayName)
	if err != nil {
		return api.PairResponse{}, fmt.Errorf("request pairing code: %w", err)
	}
	return api.PairResponse{Code: code}, nil
}

func (c *Client) consumeQR(ch <-chan whatsmeow.QRChannelItem, ready chan struct{}) {
	signaled := false
	signal := func() {
		if !signaled {
			close(ready)
			signaled = true
		}
	}
	for item := range ch {
		c.mu.Lock()
		switch item.Event {
		case "code":
			c.qr = item.Code
		case "success":
			c.pairing = false
			c.qr = ""
			c.log.Infof("Device linked successfully")
		default: // timeout, error, unavailable client version, ...
			c.pairing = false
			c.qr = ""
			c.log.Warnf("Pairing ended: %s", item.Event)
		}
		c.mu.Unlock()
		signal()
	}
	c.mu.Lock()
	c.pairing = false
	c.mu.Unlock()
	signal()
}

// Logout unlinks this device from the account.
func (c *Client) Logout(ctx context.Context) error {
	if !c.LoggedIn() {
		return nil
	}
	return c.cli.Logout(ctx)
}

func (c *Client) handleEvent(evt any) {
	switch v := evt.(type) {
	case *events.Message:
		c.handleMessage(v)
	case *events.HistorySync:
		c.handleHistorySync(v)
	case *events.Connected:
		c.log.Infof("Connected to WhatsApp")
	case *events.LoggedOut:
		c.log.Warnf("Device was unlinked from WhatsApp; link it again to continue syncing")
	}
}

// normalizeJID prefers the phone-number form of a LID when the mapping is
// known, so the same person isn't split across two identifiers.
func (c *Client) normalizeJID(jid types.JID) types.JID {
	jid = jid.ToNonAD()
	if jid.Server == types.HiddenUserServer {
		if pn, err := c.cli.Store.LIDs.GetPNForLID(context.Background(), jid); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD()
		}
	}
	return jid
}

// chatName finds a display name for a chat, preferring the stored one.
func (c *Client) chatName(jid types.JID, conversationName string) string {
	key := jid.String()
	if name, ok := c.msgs.ChatName(key); ok {
		return name
	}
	ctx := context.Background()
	if jid.Server == types.GroupServer {
		if conversationName != "" {
			return conversationName
		}
		if info, err := c.cli.GetGroupInfo(ctx, jid); err == nil && info.Name != "" {
			return info.Name
		}
		return "Group " + jid.User
	}
	if contact, err := c.cli.Store.Contacts.GetContact(ctx, jid); err == nil {
		for _, n := range []string{contact.FullName, contact.FirstName, contact.BusinessName, contact.PushName} {
			if n != "" {
				return n
			}
		}
	}
	if conversationName != "" {
		return conversationName
	}
	if jid.Server == types.DefaultUserServer {
		return "+" + jid.User
	}
	return jid.User
}

func (c *Client) handleMessage(msg *events.Message) {
	chat := c.normalizeJID(msg.Info.Chat)
	sender := c.normalizeJID(msg.Info.Sender)
	if msg.Info.IsFromMe && c.cli.Store.ID != nil {
		sender = c.cli.Store.ID.ToNonAD()
	}

	content := extractText(msg.Message)
	media := extractMedia(msg.Message, msg.Info.ID)
	if content == "" && media.Type == "" {
		return
	}
	if err := c.msgs.StoreChat(chat.String(), c.chatName(chat, ""), msg.Info.Timestamp); err != nil {
		c.log.Warnf("Failed to store chat: %v", err)
	}
	err := c.msgs.StoreMessage(store.MessageRecord{
		ID: msg.Info.ID, ChatJID: chat.String(), Sender: sender.String(), Content: content,
		Timestamp: msg.Info.Timestamp, IsFromMe: msg.Info.IsFromMe, Media: media,
	})
	if err != nil {
		c.log.Warnf("Failed to store message: %v", err)
	}
}

func (c *Client) handleHistorySync(hs *events.HistorySync) {
	stored := 0
	for _, conv := range hs.Data.GetConversations() {
		rawJID, err := types.ParseJID(conv.GetID())
		if err != nil {
			continue
		}
		chat := c.normalizeJID(rawJID)
		convName := conv.GetDisplayName()
		if convName == "" {
			convName = conv.GetName()
		}
		var latest time.Time
		for _, hm := range conv.GetMessages() {
			wm := hm.GetMessage()
			if wm == nil || wm.GetMessage() == nil || wm.GetMessageTimestamp() == 0 {
				continue
			}
			ts := time.Unix(int64(wm.GetMessageTimestamp()), 0)
			content := extractText(wm.GetMessage())
			key := wm.GetKey()
			media := extractMedia(wm.GetMessage(), key.GetID())
			if content == "" && media.Type == "" {
				continue
			}

			isFromMe := key.GetFromMe()
			var sender types.JID
			switch {
			case isFromMe && c.cli.Store.ID != nil:
				sender = c.cli.Store.ID.ToNonAD()
			case key.GetParticipant() != "":
				if p, err := types.ParseJID(key.GetParticipant()); err == nil {
					sender = c.normalizeJID(p)
				}
			case wm.GetParticipant() != "":
				if p, err := types.ParseJID(wm.GetParticipant()); err == nil {
					sender = c.normalizeJID(p)
				}
			default:
				sender = chat
			}

			if ts.After(latest) {
				latest = ts
				if err := c.msgs.StoreChat(chat.String(), c.chatName(chat, convName), latest); err != nil {
					c.log.Warnf("Failed to store chat: %v", err)
				}
			}
			err := c.msgs.StoreMessage(store.MessageRecord{
				ID: key.GetID(), ChatJID: chat.String(), Sender: sender.String(), Content: content,
				Timestamp: ts, IsFromMe: isFromMe, Media: media,
			})
			if err != nil {
				c.log.Warnf("Failed to store history message: %v", err)
				continue
			}
			stored++
		}
	}
	c.log.Infof("History sync: stored %d messages from %d conversations", stored, len(hs.Data.GetConversations()))
}

// parseRecipient accepts a JID or a phone number in any common format.
func parseRecipient(recipient string) (types.JID, error) {
	recipient = strings.TrimSpace(recipient)
	if strings.Contains(recipient, "@") {
		jid, err := types.ParseJID(recipient)
		if err != nil {
			return types.JID{}, fmt.Errorf("invalid recipient JID: %w", err)
		}
		return jid, nil
	}
	digits := store.NormalizePhone(recipient)
	if len(digits) < 8 || len(digits) > 15 {
		return types.JID{}, errors.New("recipient must be a JID or a phone number with country code, e.g. 33612345678")
	}
	return types.NewJID(digits, types.DefaultUserServer), nil
}
