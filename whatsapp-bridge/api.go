package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// bridgeAPI exposes the WhatsApp client over a local, authenticated REST API.
// The send and download operations are injected so the HTTP layer can be
// tested without a live WhatsApp connection.
type bridgeAPI struct {
	cfg      bridgeConfig
	send     func(recipient, message, mediaPath string) (bool, string)
	download func(messageID, chatJID string) (bool, string, string, string, error)
}

func (a *bridgeAPI) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/send", requireAPIAccess(a.cfg, a.handleSend))
	mux.HandleFunc("/api/download", requireAPIAccess(a.cfg, a.handleDownload))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *bridgeAPI) handleSend(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.allowSend {
		writeJSON(w, http.StatusForbidden, SendMessageResponse{
			Success: false,
			Message: "Sending is disabled. Start the bridge with WHATSAPP_ALLOW_SEND=1 to enable it.",
		})
		return
	}

	var req SendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}
	if req.Recipient == "" {
		http.Error(w, "Recipient is required", http.StatusBadRequest)
		return
	}
	if req.Message == "" && req.MediaPath == "" {
		http.Error(w, "Message or media path is required", http.StatusBadRequest)
		return
	}

	mediaPath := ""
	if req.MediaPath != "" {
		resolved, err := resolveOutboxPath(a.cfg.outboxDir, req.MediaPath)
		if err != nil {
			writeJSON(w, http.StatusForbidden, SendMessageResponse{Success: false, Message: err.Error()})
			return
		}
		mediaPath = resolved
	}

	fmt.Printf("Send request: recipient=%s media=%t\n", req.Recipient, mediaPath != "")
	success, message := a.send(req.Recipient, req.Message, mediaPath)

	status := http.StatusOK
	if !success {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, SendMessageResponse{Success: success, Message: message})
}

func (a *bridgeAPI) handleDownload(w http.ResponseWriter, r *http.Request) {
	var req DownloadMediaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}
	if req.MessageID == "" || req.ChatJID == "" {
		http.Error(w, "Message ID and Chat JID are required", http.StatusBadRequest)
		return
	}

	success, mediaType, filename, path, err := a.download(req.MessageID, req.ChatJID)
	if !success || err != nil {
		errMsg := "Unknown error"
		if err != nil {
			errMsg = err.Error()
		}
		writeJSON(w, http.StatusInternalServerError, DownloadMediaResponse{
			Success: false,
			Message: fmt.Sprintf("Failed to download media: %s", errMsg),
		})
		return
	}

	writeJSON(w, http.StatusOK, DownloadMediaResponse{
		Success:  true,
		Message:  fmt.Sprintf("Successfully downloaded %s media", mediaType),
		Filename: filename,
		Path:     path,
	})
}

// start binds the listener synchronously, so a busy port or bad address is
// reported at startup instead of failing silently in the background.
func (a *bridgeAPI) start() (*http.Server, error) {
	listener, err := net.Listen("tcp", a.cfg.addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", a.cfg.addr, err)
	}
	server := &http.Server{
		Handler:           a.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute, // media uploads/downloads can be slow
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("REST API server error: %v\n", err)
		}
	}()
	return server, nil
}
