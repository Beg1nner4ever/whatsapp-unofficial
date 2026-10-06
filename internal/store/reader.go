package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	MaxLimit   = 100
	MaxContext = 20
)

// Reader gives the MCP server read-only access to synced messages. It also
// reads the whatsmeow session database (read-only) for contact names and the
// LID <-> phone number map; that database is optional.
type Reader struct {
	MessagesPath string
	SessionPath  string
}

// Message is a stored message as returned to tools.
type Message struct {
	Timestamp time.Time `json:"timestamp"`
	Sender    string    `json:"sender"`
	ChatName  string    `json:"chat_name,omitempty"`
	Content   string    `json:"content"`
	IsFromMe  bool      `json:"is_from_me"`
	ChatJID   string    `json:"chat_jid"`
	ID        string    `json:"id"`
	MediaType string    `json:"media_type,omitempty"`
}

// Chat is a chat as returned to tools.
type Chat struct {
	JID             string     `json:"jid"`
	Name            string     `json:"name,omitempty"`
	IsGroup         bool       `json:"is_group"`
	LastMessageTime *time.Time `json:"last_message_time,omitempty"`
	LastMessage     string     `json:"last_message,omitempty"`
	LastSender      string     `json:"last_sender,omitempty"`
	LastIsFromMe    bool       `json:"last_is_from_me,omitempty"`
}

// Contact is a person found in chats or the address book.
type Contact struct {
	PhoneNumber string `json:"phone_number"`
	Name        string `json:"name,omitempty"`
	JID         string `json:"jid"`
}

// MessageContext is a message with its neighbours in the same chat.
type MessageContext struct {
	Message Message   `json:"message"`
	Before  []Message `json:"before"`
	After   []Message `json:"after"`
}

// session is an open pair of read-only connections for one tool call.
type session struct {
	msgs *sql.DB
	sess *sql.DB // nil when the session database is unavailable
}

func (r *Reader) open() (*session, error) {
	if !fileExists(r.MessagesPath) {
		return nil, ErrNotSynced
	}
	msgs, err := sql.Open("sqlite", SQLiteDSN(r.MessagesPath, true))
	if err != nil {
		return nil, fmt.Errorf("open message database: %w", err)
	}
	s := &session{msgs: msgs}
	if r.SessionPath != "" && fileExists(r.SessionPath) {
		if sess, err := sql.Open("sqlite", SQLiteDSN(r.SessionPath, true)); err == nil {
			s.sess = sess
		}
	}
	return s, nil
}

func (s *session) close() {
	s.msgs.Close()
	if s.sess != nil {
		s.sess.Close()
	}
}

func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

var nonDigits = regexp.MustCompile(`\D`)

// NormalizePhone strips everything but digits ("+33 6 12" -> "33612").
func NormalizePhone(s string) string { return nonDigits.ReplaceAllString(s, "") }

func userPart(jid string) string {
	if i := strings.IndexByte(jid, '@'); i >= 0 {
		jid = jid[:i]
	}
	if i := strings.IndexByte(jid, ':'); i >= 0 { // device suffix
		jid = jid[:i]
	}
	return jid
}

func server(jid string) string {
	if i := strings.IndexByte(jid, '@'); i >= 0 {
		return jid[i+1:]
	}
	return ""
}

// pnForLID maps a LID user to a phone number user via whatsmeow's map.
func (s *session) pnForLID(lidUser string) string {
	if s.sess == nil {
		return ""
	}
	var pn string
	_ = s.sess.QueryRow("SELECT pn FROM whatsmeow_lid_map WHERE lid = ?", lidUser).Scan(&pn)
	return pn
}

func (s *session) lidForPN(pnUser string) string {
	if s.sess == nil {
		return ""
	}
	var lid string
	_ = s.sess.QueryRow("SELECT lid FROM whatsmeow_lid_map WHERE pn = ?", pnUser).Scan(&lid)
	return lid
}

// identities returns every stored form of the person behind id (a phone
// number, a bare user, or a JID): bare user, phone JID and LID JID. Earlier
// versions stored senders as bare users; WhatsApp now also uses LIDs.
func (s *session) identities(id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	var pn, lid string
	switch server(id) {
	case "lid":
		lid = userPart(id)
		pn = s.pnForLID(lid)
	case "s.whatsapp.net", "":
		pn = userPart(id)
		if server(id) == "" {
			pn = NormalizePhone(pn)
		}
		lid = s.lidForPN(pn)
	default: // groups, broadcasts: only exact matches make sense
		return []string{id}
	}
	var out []string
	if pn != "" {
		out = append(out, pn, pn+"@s.whatsapp.net")
	}
	if lid != "" {
		out = append(out, lid, lid+"@lid")
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func toArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// displayName resolves a human-readable name for a sender: address book
// contact first, then chat name, else the phone number / id.
func (s *session) displayName(sender string) string {
	ids := s.identities(sender)
	if s.sess != nil {
		for _, id := range ids {
			if !strings.Contains(id, "@") {
				continue
			}
			var full, first, push, business sql.NullString
			err := s.sess.QueryRow(
				"SELECT full_name, first_name, push_name, business_name FROM whatsmeow_contacts WHERE their_jid = ? LIMIT 1", id,
			).Scan(&full, &first, &push, &business)
			if err == nil {
				for _, n := range []sql.NullString{full, first, business, push} {
					if n.String != "" {
						return n.String
					}
				}
			}
		}
	}
	if len(ids) > 0 {
		var name sql.NullString
		q := "SELECT name FROM chats WHERE jid IN (" + placeholders(len(ids)) + ") AND name <> '' LIMIT 1"
		if err := s.msgs.QueryRow(q, toArgs(ids)...).Scan(&name); err == nil && name.String != "" {
			return name.String
		}
	}
	for _, id := range ids {
		if server(id) == "s.whatsapp.net" {
			return "+" + userPart(id)
		}
	}
	return sender
}

const messageColumns = `messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, messages.chat_jid, messages.id, messages.media_type`

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var ts, sender, chatName, content, chatJID, id, mediaType sql.NullString
		var fromMe sql.NullBool
		if err := rows.Scan(&ts, &sender, &chatName, &content, &fromMe, &chatJID, &id, &mediaType); err != nil {
			return nil, err
		}
		t, _ := parseTS(ts.String)
		out = append(out, Message{
			Timestamp: t, Sender: sender.String, ChatName: chatName.String, Content: content.String,
			IsFromMe: fromMe.Bool, ChatJID: chatJID.String, ID: id.String, MediaType: mediaType.String,
		})
	}
	return out, rows.Err()
}

// quote JSON-encodes untrusted text so it cannot forge extra lines.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// formatMessage renders one message per line. Chat names, sender names,
// identifiers and message text are untrusted and therefore JSON-quoted.
func (s *session) formatMessage(m Message, showChat bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] ", m.Timestamp.Local().Format("2006-01-02 15:04:05"))
	if showChat && m.ChatName != "" {
		fmt.Fprintf(&b, "Chat: %s ", quote(m.ChatName))
	}
	sender := "me"
	if !m.IsFromMe {
		sender = quote(s.displayName(m.Sender))
	}
	fmt.Fprintf(&b, "From: %s: ", sender)
	if m.MediaType != "" {
		fmt.Fprintf(&b, "[%s - Message ID: %s - Chat JID: %s] ", m.MediaType, quote(m.ID), quote(m.ChatJID))
	}
	b.WriteString(quote(m.Content))
	b.WriteByte('\n')
	return b.String()
}

func (s *session) formatList(msgs []Message) string {
	if len(msgs) == 0 {
		return "No messages to display."
	}
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(s.formatMessage(m, true))
	}
	return b.String()
}

// ListMessagesParams filters ListMessages. Zero values mean "no filter".
type ListMessagesParams struct {
	After, Before               *time.Time
	SenderPhoneNumber, ChatJID  string
	Query                       string
	Limit, Page                 int
	IncludeContext              bool
	ContextBefore, ContextAfter int
}

// ListMessages returns matching messages (newest first), formatted as text.
func (r *Reader) ListMessages(p ListMessagesParams) (string, error) {
	s, err := r.open()
	if err != nil {
		return "", err
	}
	defer s.close()

	limit := clamp(p.Limit, 1, MaxLimit)
	page := clamp(p.Page, 0, 1<<20)
	var where []string
	var args []any
	if p.After != nil {
		where = append(where, "messages.timestamp > ?")
		args = append(args, formatTS(*p.After))
	}
	if p.Before != nil {
		where = append(where, "messages.timestamp < ?")
		args = append(args, formatTS(*p.Before))
	}
	if p.SenderPhoneNumber != "" {
		ids := s.identities(p.SenderPhoneNumber)
		if len(ids) == 0 {
			return "No messages to display.", nil
		}
		where = append(where, "messages.sender IN ("+placeholders(len(ids))+")")
		args = append(args, toArgs(ids)...)
	}
	if p.ChatJID != "" {
		where = append(where, "messages.chat_jid = ?")
		args = append(args, p.ChatJID)
	}
	if p.Query != "" {
		where = append(where, "LOWER(messages.content) LIKE LOWER(?) ESCAPE '\\'")
		args = append(args, "%"+escapeLike(p.Query)+"%")
	}
	q := "SELECT " + messageColumns + " FROM messages JOIN chats ON messages.chat_jid = chats.jid"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY messages.timestamp DESC LIMIT ? OFFSET ?"
	args = append(args, limit, page*limit)

	rows, err := s.msgs.Query(q, args...)
	if err != nil {
		return "", err
	}
	msgs, err := scanMessages(rows)
	if err != nil {
		return "", err
	}
	if !p.IncludeContext {
		return s.formatList(msgs), nil
	}
	var withContext []Message
	for _, m := range msgs {
		ctx, err := s.context(m.ID, m.ChatJID, clamp(p.ContextBefore, 0, MaxContext), clamp(p.ContextAfter, 0, MaxContext))
		if err != nil {
			return "", err
		}
		withContext = append(withContext, ctx.Before...)
		withContext = append(withContext, ctx.Message)
		withContext = append(withContext, ctx.After...)
	}
	return s.formatList(withContext), nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// GetMessageContext returns a message and its neighbours. chatJID is optional
// but disambiguates message IDs that occur in several chats.
func (r *Reader) GetMessageContext(messageID, chatJID string, before, after int) (MessageContext, error) {
	s, err := r.open()
	if err != nil {
		return MessageContext{}, err
	}
	defer s.close()
	return s.context(messageID, chatJID, clamp(before, 0, MaxContext), clamp(after, 0, MaxContext))
}

func (s *session) context(messageID, chatJID string, before, after int) (MessageContext, error) {
	q := "SELECT " + messageColumns + ", CAST(messages.timestamp AS TEXT) FROM messages JOIN chats ON messages.chat_jid = chats.jid WHERE messages.id = ?"
	args := []any{messageID}
	if chatJID != "" {
		q += " AND messages.chat_jid = ?"
		args = append(args, chatJID)
	}
	var ts, sender, chatName, content, cj, id, mediaType, rawTS sql.NullString
	var fromMe sql.NullBool
	err := s.msgs.QueryRow(q+" LIMIT 1", args...).Scan(&ts, &sender, &chatName, &content, &fromMe, &cj, &id, &mediaType, &rawTS)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageContext{}, fmt.Errorf("message %s not found", messageID)
	}
	if err != nil {
		return MessageContext{}, err
	}
	t, _ := parseTS(ts.String)
	target := Message{Timestamp: t, Sender: sender.String, ChatName: chatName.String, Content: content.String,
		IsFromMe: fromMe.Bool, ChatJID: cj.String, ID: id.String, MediaType: mediaType.String}

	base := "SELECT " + messageColumns + " FROM messages JOIN chats ON messages.chat_jid = chats.jid WHERE messages.chat_jid = ? AND messages.timestamp "
	rows, err := s.msgs.Query(base+"< ? ORDER BY messages.timestamp DESC LIMIT ?", cj.String, rawTS.String, before)
	if err != nil {
		return MessageContext{}, err
	}
	beforeMsgs, err := scanMessages(rows)
	if err != nil {
		return MessageContext{}, err
	}
	// Return the earlier messages in chronological order.
	for i, j := 0, len(beforeMsgs)-1; i < j; i, j = i+1, j-1 {
		beforeMsgs[i], beforeMsgs[j] = beforeMsgs[j], beforeMsgs[i]
	}
	rows, err = s.msgs.Query(base+"> ? ORDER BY messages.timestamp ASC LIMIT ?", cj.String, rawTS.String, after)
	if err != nil {
		return MessageContext{}, err
	}
	afterMsgs, err := scanMessages(rows)
	if err != nil {
		return MessageContext{}, err
	}
	return MessageContext{Message: target, Before: beforeMsgs, After: afterMsgs}, nil
}

const chatColumns = `chats.jid, chats.name, chats.last_message_time, m.content, m.sender, m.is_from_me`
const lastMessageJoin = ` LEFT JOIN messages m ON m.rowid = (
	SELECT rowid FROM messages WHERE chat_jid = chats.jid ORDER BY timestamp DESC LIMIT 1)`

func scanChats(rows *sql.Rows, includeLast bool) ([]Chat, error) {
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var jid, name, ts, content, sender sql.NullString
		var fromMe sql.NullBool
		if err := rows.Scan(&jid, &name, &ts, &content, &sender, &fromMe); err != nil {
			return nil, err
		}
		c := Chat{JID: jid.String, Name: name.String, IsGroup: strings.HasSuffix(jid.String, "@g.us")}
		if t, err := parseTS(ts.String); err == nil {
			c.LastMessageTime = &t
		}
		if includeLast {
			c.LastMessage, c.LastSender, c.LastIsFromMe = content.String, sender.String, fromMe.Bool
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListChats returns chats, optionally filtered by name or JID.
func (r *Reader) ListChats(query string, limit, page int, includeLast bool, sortBy string) ([]Chat, error) {
	s, err := r.open()
	if err != nil {
		return nil, err
	}
	defer s.close()
	limit = clamp(limit, 1, MaxLimit)
	page = clamp(page, 0, 1<<20)

	q := "SELECT " + chatColumns + " FROM chats" + lastMessageJoin
	var args []any
	if query != "" {
		q += ` WHERE (LOWER(chats.name) LIKE LOWER(?) ESCAPE '\' OR chats.jid LIKE ? ESCAPE '\')`
		pattern := "%" + escapeLike(query) + "%"
		args = append(args, pattern, pattern)
	}
	if sortBy == "name" {
		q += " ORDER BY chats.name"
	} else {
		q += " ORDER BY chats.last_message_time DESC"
	}
	q += " LIMIT ? OFFSET ?"
	args = append(args, limit, page*limit)
	rows, err := s.msgs.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanChats(rows, includeLast)
}

// GetChat returns one chat by JID, or nil if unknown.
func (r *Reader) GetChat(jid string, includeLast bool) (*Chat, error) {
	s, err := r.open()
	if err != nil {
		return nil, err
	}
	defer s.close()
	rows, err := s.msgs.Query("SELECT "+chatColumns+" FROM chats"+lastMessageJoin+" WHERE chats.jid = ?", jid)
	if err != nil {
		return nil, err
	}
	chats, err := scanChats(rows, includeLast)
	if err != nil || len(chats) == 0 {
		return nil, err
	}
	return &chats[0], nil
}

// GetDirectChatByContact finds the one-to-one chat with a phone number,
// whether WhatsApp keyed it by phone number or by LID.
func (r *Reader) GetDirectChatByContact(phone string) (*Chat, error) {
	s, err := r.open()
	if err != nil {
		return nil, err
	}
	defer s.close()
	var jids []string
	for _, id := range s.identities(NormalizePhone(phone)) {
		if strings.Contains(id, "@") {
			jids = append(jids, id)
		}
	}
	if len(jids) == 0 {
		return nil, nil
	}
	q := "SELECT " + chatColumns + " FROM chats" + lastMessageJoin + " WHERE chats.jid IN (" + placeholders(len(jids)) + ") ORDER BY chats.last_message_time DESC LIMIT 1"
	rows, err := s.msgs.Query(q, toArgs(jids)...)
	if err != nil {
		return nil, err
	}
	chats, err := scanChats(rows, true)
	if err != nil || len(chats) == 0 {
		return nil, err
	}
	return &chats[0], nil
}

// GetContactChats lists chats (direct and groups) involving a contact.
func (r *Reader) GetContactChats(jid string, limit, page int) ([]Chat, error) {
	s, err := r.open()
	if err != nil {
		return nil, err
	}
	defer s.close()
	ids := s.identities(jid)
	if len(ids) == 0 {
		return nil, nil
	}
	in := placeholders(len(ids))
	q := "SELECT " + chatColumns + " FROM chats" + lastMessageJoin + ` WHERE chats.jid IN (` + in + `)
		OR chats.jid IN (SELECT DISTINCT chat_jid FROM messages WHERE sender IN (` + in + `))
		ORDER BY chats.last_message_time DESC LIMIT ? OFFSET ?`
	args := append(toArgs(ids), toArgs(ids)...)
	args = append(args, clamp(limit, 1, MaxLimit), clamp(page, 0, 1<<20)*clamp(limit, 1, MaxLimit))
	rows, err := s.msgs.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return scanChats(rows, true)
}

// GetLastInteraction returns the most recent message with a contact, formatted.
func (r *Reader) GetLastInteraction(jid string) (string, error) {
	s, err := r.open()
	if err != nil {
		return "", err
	}
	defer s.close()
	ids := s.identities(jid)
	if len(ids) == 0 {
		return "No messages to display.", nil
	}
	in := placeholders(len(ids))
	q := "SELECT " + messageColumns + " FROM messages JOIN chats ON messages.chat_jid = chats.jid WHERE messages.sender IN (" + in + ") OR messages.chat_jid IN (" + in + ") ORDER BY messages.timestamp DESC LIMIT 1"
	rows, err := s.msgs.Query(q, append(toArgs(ids), toArgs(ids)...)...)
	if err != nil {
		return "", err
	}
	msgs, err := scanMessages(rows)
	if err != nil {
		return "", err
	}
	return s.formatList(msgs), nil
}

// SearchContacts finds people by name or number across one-to-one chats and
// the WhatsApp address book.
func (r *Reader) SearchContacts(query string) ([]Contact, error) {
	s, err := r.open()
	if err != nil {
		return nil, err
	}
	defer s.close()
	pattern := "%" + escapeLike(query) + "%"
	seen := map[string]bool{}
	var out []Contact
	add := func(jid, name string) {
		if pn := s.phoneJID(jid); pn != "" {
			jid = pn
		}
		if seen[jid] || len(out) >= 50 {
			return
		}
		seen[jid] = true
		c := Contact{JID: jid, Name: name}
		if server(jid) == "s.whatsapp.net" {
			c.PhoneNumber = userPart(jid)
		}
		out = append(out, c)
	}

	if s.sess != nil {
		rows, err := s.sess.Query(`SELECT their_jid, COALESCE(NULLIF(full_name,''), NULLIF(first_name,''), NULLIF(business_name,''), push_name, '')
			FROM whatsmeow_contacts
			WHERE LOWER(full_name) LIKE LOWER(?) ESCAPE '\' OR LOWER(first_name) LIKE LOWER(?) ESCAPE '\'
			   OR LOWER(push_name) LIKE LOWER(?) ESCAPE '\' OR LOWER(business_name) LIKE LOWER(?) ESCAPE '\'
			   OR their_jid LIKE ? ESCAPE '\'
			ORDER BY full_name LIMIT 50`, pattern, pattern, pattern, pattern, pattern)
		if err == nil {
			for rows.Next() {
				var jid, name string
				if rows.Scan(&jid, &name) == nil {
					add(jid, name)
				}
			}
			rows.Close()
		}
	}

	rows, err := s.msgs.Query(`SELECT jid, COALESCE(name, '') FROM chats
		WHERE (LOWER(name) LIKE LOWER(?) ESCAPE '\' OR LOWER(jid) LIKE LOWER(?) ESCAPE '\')
		  AND jid NOT LIKE '%@g.us' AND jid NOT LIKE '%@broadcast' AND jid NOT LIKE '%@newsletter'
		ORDER BY name, jid LIMIT 50`, pattern, pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var jid, name string
		if err := rows.Scan(&jid, &name); err != nil {
			return nil, err
		}
		add(jid, name)
	}
	return out, rows.Err()
}

// phoneJID returns the phone-number JID for a LID JID when known.
func (s *session) phoneJID(jid string) string {
	if server(jid) != "lid" {
		return ""
	}
	if pn := s.pnForLID(userPart(jid)); pn != "" {
		return pn + "@s.whatsapp.net"
	}
	return ""
}
