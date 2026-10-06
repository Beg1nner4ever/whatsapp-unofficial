package wa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"

	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/api"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/safety"
	"github.com/Beg1nner4ever/whatsapp-unofficial/internal/store"
)

// extractText returns the text of a message, including media captions.
func extractText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	switch {
	case msg.GetConversation() != "":
		return msg.GetConversation()
	case msg.GetExtendedTextMessage() != nil:
		return msg.GetExtendedTextMessage().GetText()
	case msg.GetImageMessage() != nil:
		return msg.GetImageMessage().GetCaption()
	case msg.GetVideoMessage() != nil:
		return msg.GetVideoMessage().GetCaption()
	case msg.GetDocumentMessage() != nil:
		return msg.GetDocumentMessage().GetCaption()
	}
	return ""
}

// extractMedia returns what is needed to download a message's media later.
// Filenames are stored as received (they are sanitized at download time).
func extractMedia(msg *waE2E.Message, messageID string) store.MediaInfo {
	if msg == nil {
		return store.MediaInfo{}
	}
	if m := msg.GetImageMessage(); m != nil {
		return store.MediaInfo{Type: "image", Filename: messageID + ".jpg", URL: m.GetURL(), MediaKey: m.GetMediaKey(),
			FileSHA256: m.GetFileSHA256(), FileEncSHA256: m.GetFileEncSHA256(), FileLength: m.GetFileLength()}
	}
	if m := msg.GetVideoMessage(); m != nil {
		return store.MediaInfo{Type: "video", Filename: messageID + ".mp4", URL: m.GetURL(), MediaKey: m.GetMediaKey(),
			FileSHA256: m.GetFileSHA256(), FileEncSHA256: m.GetFileEncSHA256(), FileLength: m.GetFileLength()}
	}
	if m := msg.GetAudioMessage(); m != nil {
		return store.MediaInfo{Type: "audio", Filename: messageID + ".ogg", URL: m.GetURL(), MediaKey: m.GetMediaKey(),
			FileSHA256: m.GetFileSHA256(), FileEncSHA256: m.GetFileEncSHA256(), FileLength: m.GetFileLength()}
	}
	if m := msg.GetDocumentMessage(); m != nil {
		name := m.GetFileName()
		if name == "" {
			name = messageID
		}
		return store.MediaInfo{Type: "document", Filename: name, URL: m.GetURL(), MediaKey: m.GetMediaKey(),
			FileSHA256: m.GetFileSHA256(), FileEncSHA256: m.GetFileEncSHA256(), FileLength: m.GetFileLength()}
	}
	return store.MediaInfo{}
}

// mediaDownloader implements whatsmeow.DownloadableMessage from stored data.
type mediaDownloader struct {
	url, directPath                     string
	mediaKey, fileSHA256, fileEncSHA256 []byte
	fileLength                          uint64
	mediaType                           whatsmeow.MediaType
}

func (d *mediaDownloader) GetDirectPath() string             { return d.directPath }
func (d *mediaDownloader) GetURL() string                    { return d.url }
func (d *mediaDownloader) GetMediaKey() []byte               { return d.mediaKey }
func (d *mediaDownloader) GetFileLength() uint64             { return d.fileLength }
func (d *mediaDownloader) GetFileSHA256() []byte             { return d.fileSHA256 }
func (d *mediaDownloader) GetFileEncSHA256() []byte          { return d.fileEncSHA256 }
func (d *mediaDownloader) GetMediaType() whatsmeow.MediaType { return d.mediaType }

// directPathFromURL extracts "/v/t62..." from a WhatsApp media URL.
func directPathFromURL(u string) string {
	parts := strings.SplitN(u, ".net/", 2)
	if len(parts) < 2 {
		return ""
	}
	return "/" + strings.SplitN(parts[1], "?", 2)[0]
}

var waMediaTypes = map[string]whatsmeow.MediaType{
	"image":    whatsmeow.MediaImage,
	"video":    whatsmeow.MediaVideo,
	"audio":    whatsmeow.MediaAudio,
	"document": whatsmeow.MediaDocument,
}

// Download fetches a message's media into the media directory. The stored
// filename is sender-controlled, so it is sanitized, confined to the media
// directory, and written without overwriting or following symlinks.
func (c *Client) Download(ctx context.Context, messageID, chatJID string) (api.DownloadResponse, error) {
	info, err := c.msgs.GetMediaInfo(messageID, chatJID)
	if err != nil {
		return api.DownloadResponse{}, err
	}
	waType, ok := waMediaTypes[info.Type]
	if !ok {
		return api.DownloadResponse{}, errors.New("not a media message")
	}
	chatDir, localPath, err := safety.MediaLocalPath(c.cfg.MediaDir, chatJID, messageID, info.Filename)
	if err != nil {
		return api.DownloadResponse{}, err
	}
	resp := api.DownloadResponse{MediaType: info.Type, Filename: filepath.Base(localPath), Path: localPath}

	if st, err := os.Lstat(localPath); err == nil {
		if !st.Mode().IsRegular() {
			return api.DownloadResponse{}, fmt.Errorf("refusing to use non-regular file at %s", localPath)
		}
		return resp, nil
	}
	if info.URL == "" || len(info.MediaKey) == 0 || len(info.FileSHA256) == 0 || len(info.FileEncSHA256) == 0 || info.FileLength == 0 {
		return api.DownloadResponse{}, errors.New("incomplete media information for download")
	}
	if err := safety.EnsurePrivateDir(chatDir); err != nil {
		return api.DownloadResponse{}, err
	}
	data, err := c.cli.Download(ctx, &mediaDownloader{
		url: info.URL, directPath: directPathFromURL(info.URL), mediaKey: info.MediaKey,
		fileSHA256: info.FileSHA256, fileEncSHA256: info.FileEncSHA256, fileLength: info.FileLength, mediaType: waType,
	})
	if err != nil {
		return api.DownloadResponse{}, fmt.Errorf("download media: %w", err)
	}
	if err := safety.WriteNewPrivateFile(localPath, data); err != nil {
		return api.DownloadResponse{}, fmt.Errorf("save media: %w", err)
	}
	return resp, nil
}

// Send sends a text message, or a file from the outbox (optionally as a
// voice note, converting with ffmpeg when needed).
func (c *Client) Send(ctx context.Context, req api.SendRequest) (string, error) {
	if !c.cli.IsConnected() || !c.cli.IsLoggedIn() {
		return "", errors.New("not connected to WhatsApp")
	}
	to, err := parseRecipient(req.Recipient)
	if err != nil {
		return "", err
	}
	if req.MediaPath == "" {
		if strings.TrimSpace(req.Message) == "" {
			return "", errors.New("message is empty")
		}
		if _, err := c.cli.SendMessage(ctx, to, &waE2E.Message{Conversation: proto.String(req.Message)}); err != nil {
			return "", fmt.Errorf("send message: %w", err)
		}
		return "Message sent to " + to.String(), nil
	}

	path, err := safety.ResolveOutboxPath(c.cfg.OutboxDir, req.MediaPath)
	if err != nil {
		return "", err
	}
	if req.AsVoice && !strings.EqualFold(filepath.Ext(path), ".ogg") {
		converted, err := convertToOpus(ctx, path, c.cfg.OutboxDir)
		if err != nil {
			return "", err
		}
		defer os.Remove(converted)
		path = converted
	}
	msg, err := c.buildMediaMessage(ctx, path, req.Message, req.AsVoice)
	if err != nil {
		return "", err
	}
	if _, err := c.cli.SendMessage(ctx, to, msg); err != nil {
		return "", fmt.Errorf("send media: %w", err)
	}
	return fmt.Sprintf("Sent %s to %s", filepath.Base(req.MediaPath), to.String()), nil
}

func (c *Client) buildMediaMessage(ctx context.Context, path, caption string, asVoice bool) (*waE2E.Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read media file: %w", err)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	mediaType, mime := whatsmeow.MediaDocument, "application/octet-stream"
	switch ext {
	case "jpg", "jpeg":
		mediaType, mime = whatsmeow.MediaImage, "image/jpeg"
	case "png":
		mediaType, mime = whatsmeow.MediaImage, "image/png"
	case "gif":
		mediaType, mime = whatsmeow.MediaImage, "image/gif"
	case "webp":
		mediaType, mime = whatsmeow.MediaImage, "image/webp"
	case "ogg":
		mediaType, mime = whatsmeow.MediaAudio, "audio/ogg; codecs=opus"
	case "mp4":
		mediaType, mime = whatsmeow.MediaVideo, "video/mp4"
	case "mov":
		mediaType, mime = whatsmeow.MediaVideo, "video/quicktime"
	case "pdf":
		mime = "application/pdf"
	}
	if asVoice && mediaType != whatsmeow.MediaAudio {
		return nil, errors.New("voice messages must be .ogg (Opus) files")
	}

	up, err := c.cli.Upload(ctx, data, mediaType)
	if err != nil {
		return nil, fmt.Errorf("upload media: %w", err)
	}
	switch mediaType {
	case whatsmeow.MediaImage:
		return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}}, nil
	case whatsmeow.MediaVideo:
		return &waE2E.Message{VideoMessage: &waE2E.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}}, nil
	case whatsmeow.MediaAudio:
		seconds, waveform, err := analyzeOggOpus(data)
		if err != nil {
			return nil, fmt.Errorf("analyze Ogg Opus file: %w", err)
		}
		return &waE2E.Message{AudioMessage: &waE2E.AudioMessage{
			Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
			Seconds: proto.Uint32(seconds), PTT: proto.Bool(asVoice), Waveform: waveform,
		}}, nil
	default:
		return &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{
			Title: proto.String(filepath.Base(path)), FileName: proto.String(filepath.Base(path)), Caption: proto.String(caption),
			Mimetype: proto.String(mime), URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}}, nil
	}
}

// convertToOpus converts an audio file to Ogg Opus inside dir using ffmpeg.
// The input path has already been confined to the outbox and is absolute, so
// it cannot be mistaken for an ffmpeg option.
func convertToOpus(ctx context.Context, input, dir string) (string, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", errors.New("ffmpeg is required to convert audio to a voice message; install it or send an .ogg Opus file")
	}
	out, err := os.CreateTemp(dir, ".voice-*.ogg")
	if err != nil {
		return "", err
	}
	out.Close()
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-y", "-i", input,
		"-c:a", "libopus", "-b:a", "32k", "-ar", "24000", "-application", "voip",
		"-vbr", "on", "-compression_level", "10", "-frame_duration", "60", out.Name())
	if output, err := cmd.CombinedOutput(); err != nil {
		os.Remove(out.Name())
		return "", fmt.Errorf("ffmpeg conversion failed: %v: %s", err, lastLine(string(output)))
	}
	return out.Name(), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
