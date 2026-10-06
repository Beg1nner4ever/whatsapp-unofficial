// Package api defines the messages exchanged between the MCP server (and
// CLI) and the daemon over the control socket, plus the Backend interface the
// daemon serves.
package api

import "context"

// Status describes the daemon's WhatsApp connection.
type Status struct {
	LoggedIn  bool   `json:"logged_in"`
	Connected bool   `json:"connected"`
	Pairing   bool   `json:"pairing"`
	Account   string `json:"account,omitempty"` // own phone number when linked
	QR        string `json:"qr,omitempty"`      // current QR payload while pairing
	Version   string `json:"version"`
}

type PairRequest struct {
	Phone string `json:"phone,omitempty"` // digits only; empty = QR pairing
}

type PairResponse struct {
	Code string `json:"code,omitempty"` // 8-character phone pairing code
	QR   string `json:"qr,omitempty"`   // QR payload for terminal rendering
}

type SendRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message,omitempty"`
	MediaPath string `json:"media_path,omitempty"`
	AsVoice   bool   `json:"as_voice,omitempty"`
}

type SendResponse struct {
	Message string `json:"message"`
}

type DownloadRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

type DownloadResponse struct {
	MediaType string `json:"media_type"`
	Filename  string `json:"filename"`
	Path      string `json:"path"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// Backend is what the daemon exposes. The WhatsApp client implements it;
// tests use fakes.
type Backend interface {
	Status() Status
	StartPairing(ctx context.Context, phone string) (PairResponse, error)
	Send(ctx context.Context, req SendRequest) (string, error)
	Download(ctx context.Context, messageID, chatJID string) (DownloadResponse, error)
	Logout(ctx context.Context) error
}
