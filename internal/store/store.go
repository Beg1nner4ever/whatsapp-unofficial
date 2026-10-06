// Package store owns the message database. The daemon writes to it through
// Writer; the MCP server reads it through Reader with a read-only connection.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS chats (
	jid TEXT PRIMARY KEY,
	name TEXT,
	last_message_time TIMESTAMP
);
CREATE TABLE IF NOT EXISTS messages (
	id TEXT,
	chat_jid TEXT,
	sender TEXT,
	content TEXT,
	timestamp TIMESTAMP,
	is_from_me BOOLEAN,
	media_type TEXT,
	filename TEXT,
	url TEXT,
	media_key BLOB,
	file_sha256 BLOB,
	file_enc_sha256 BLOB,
	file_length INTEGER,
	PRIMARY KEY (id, chat_jid),
	FOREIGN KEY (chat_jid) REFERENCES chats(jid)
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_time ON messages(chat_jid, timestamp);
CREATE INDEX IF NOT EXISTS idx_messages_time ON messages(timestamp);
CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender);
`

// ErrNotSynced means the message database does not exist yet, i.e. the
// account has not been linked or the first sync has not run.
var ErrNotSynced = errors.New("no messages synced yet: link your WhatsApp account first (connection_status / pair_with_phone)")

// SQLiteDSN builds a modernc.org/sqlite DSN for path. The path is URL-encoded
// so directories with spaces or '?' (e.g. "Application Support") work.
func SQLiteDSN(path string, readOnly bool) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	if readOnly {
		q.Set("mode", "ro")
	} else {
		q.Add("_pragma", "foreign_keys(1)")
		q.Add("_pragma", "journal_mode(WAL)")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Timestamps are stored as UTC text in one fixed format so that string
// comparison and ORDER BY are chronological.
const tsFormat = "2006-01-02 15:04:05"

func formatTS(t time.Time) string { return t.UTC().Format(tsFormat) }

// parseTS accepts the canonical format plus the offset formats written by
// earlier versions (mattn/go-sqlite3 stored local times with an offset).
func parseTS(s string) (time.Time, error) {
	if t, err := time.ParseInLocation(tsFormat, s, time.UTC); err == nil {
		return t, nil
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}

// Writer is the daemon's read-write handle on the message database.
type Writer struct {
	db *sql.DB
}

// OpenWriter opens (creating if needed) the message database.
func OpenWriter(path string) (*Writer, error) {
	db, err := sql.Open("sqlite", SQLiteDSN(path, false))
	if err != nil {
		return nil, fmt.Errorf("open message database: %w", err)
	}
	// One writer connection avoids SQLITE_BUSY between our own goroutines.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create message tables: %w", err)
	}
	w := &Writer{db: db}
	if err := w.normalizeTimestamps(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate timestamps: %w", err)
	}
	return w, nil
}

// normalizeTimestamps rewrites timestamps left in older formats (databases
// migrated from the upstream bridge) into the canonical UTC format.
func (w *Writer) normalizeTimestamps() error {
	for _, q := range []struct{ table, col string }{
		{"messages", "timestamp"},
		{"chats", "last_message_time"},
	} {
		rows, err := w.db.Query(fmt.Sprintf("SELECT rowid, CAST(%s AS TEXT) FROM %s WHERE length(%s) <> %d", q.col, q.table, q.col, len(tsFormat)))
		if err != nil {
			return err
		}
		type fix struct {
			rowid int64
			ts    string
		}
		var fixes []fix
		for rows.Next() {
			var rowid int64
			var raw sql.NullString
			if err := rows.Scan(&rowid, &raw); err != nil {
				rows.Close()
				return err
			}
			if t, err := parseTS(raw.String); err == nil {
				fixes = append(fixes, fix{rowid, formatTS(t)})
			}
		}
		rows.Close()
		for _, f := range fixes {
			if _, err := w.db.Exec(fmt.Sprintf("UPDATE %s SET %s = ? WHERE rowid = ?", q.table, q.col), f.ts, f.rowid); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Writer) Close() error { return w.db.Close() }

// StoreChat upserts a chat and its last message time.
func (w *Writer) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := w.db.Exec(
		`INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)
		 ON CONFLICT(jid) DO UPDATE SET
		   name = excluded.name,
		   last_message_time = MAX(COALESCE(chats.last_message_time, ''), excluded.last_message_time)`,
		jid, name, formatTS(lastMessageTime),
	)
	return err
}

// ChatName returns the stored name for a chat, if any.
func (w *Writer) ChatName(jid string) (string, bool) {
	var name sql.NullString
	if err := w.db.QueryRow("SELECT name FROM chats WHERE jid = ?", jid).Scan(&name); err != nil || name.String == "" {
		return "", false
	}
	return name.String, true
}

// MessageRecord is a message as written by the daemon.
type MessageRecord struct {
	ID, ChatJID, Sender, Content string
	Timestamp                    time.Time
	IsFromMe                     bool
	Media                        MediaInfo
}

// MediaInfo is what is needed to download a message's media later.
type MediaInfo struct {
	Type, Filename, URL                 string
	MediaKey, FileSHA256, FileEncSHA256 []byte
	FileLength                          uint64
}

// StoreMessage upserts a message. Messages without text or media are skipped.
func (w *Writer) StoreMessage(m MessageRecord) error {
	if m.Content == "" && m.Media.Type == "" {
		return nil
	}
	_, err := w.db.Exec(
		`INSERT OR REPLACE INTO messages
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.ChatJID, m.Sender, m.Content, formatTS(m.Timestamp), m.IsFromMe,
		m.Media.Type, m.Media.Filename, m.Media.URL, m.Media.MediaKey, m.Media.FileSHA256, m.Media.FileEncSHA256, m.Media.FileLength,
	)
	return err
}

// GetMediaInfo loads the media metadata stored for a message.
func (w *Writer) GetMediaInfo(id, chatJID string) (MediaInfo, error) {
	var mi MediaInfo
	var mediaType, filename, u sql.NullString
	var length sql.NullInt64
	err := w.db.QueryRow(
		"SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mediaType, &filename, &u, &mi.MediaKey, &mi.FileSHA256, &mi.FileEncSHA256, &length)
	if errors.Is(err, sql.ErrNoRows) {
		return mi, fmt.Errorf("message %s not found in chat %s", id, chatJID)
	}
	if err != nil {
		return mi, err
	}
	mi.Type, mi.Filename, mi.URL = mediaType.String, filename.String, u.String
	if length.Valid && length.Int64 > 0 {
		mi.FileLength = uint64(length.Int64)
	}
	return mi, nil
}

// fileExists is used by the reader to avoid creating databases.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
