package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// StoredMessage holds a message.
type StoredMessage struct {
	ID        string    `json:"id"`
	Chat      string    `json:"chat"`
	Sender    string    `json:"sender"`
	PushName  string    `json:"push_name"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
	IsFromMe  bool      `json:"is_from_me"`
	IsGroup   bool      `json:"is_group"`
}

// ChatInfo holds basic chat info.
type ChatInfo struct {
	JID      string `json:"jid"`
	Name     string `json:"name"`
	IsGroup  bool   `json:"is_group"`
	LastMsg  string `json:"last_message,omitempty"`
	LastTime string `json:"last_time,omitempty"`
}

// Client wraps whatsmeow with persistent message storage.
type Client struct {
	WM        *whatsmeow.Client
	Container *sqlstore.Container
	db        *sql.DB
	connected chan struct{}
	names     map[string]string // cached JID -> display name
	namesMu   sync.RWMutex
}

const dsn = "file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

// NewClient creates the WhatsApp client with a SQLite session store.
func NewClient(dbPath string) (*Client, error) {
	store.SetOSInfo("Whasapo MCP", [3]uint32{0, 1, 0})

	connStr := fmt.Sprintf(dsn, dbPath)

	container, err := sqlstore.New(context.Background(), "sqlite", connStr, waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	// Open a separate connection for message storage
	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("open message db: %w", err)
	}

	if err := initMessageTable(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init messages: %w", err)
	}

	wm := whatsmeow.NewClient(deviceStore, waLog.Noop)

	c := &Client{
		WM:        wm,
		Container: container,
		db:        db,
		connected: make(chan struct{}),
		names:     make(map[string]string),
	}

	wm.AddEventHandler(c.eventHandler)
	return c, nil
}

func initMessageTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		id TEXT NOT NULL,
		chat TEXT NOT NULL,
		sender TEXT NOT NULL,
		push_name TEXT NOT NULL DEFAULT '',
		text TEXT NOT NULL,
		timestamp INTEGER NOT NULL,
		is_from_me INTEGER NOT NULL DEFAULT 0,
		is_group INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (id, chat)
	)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_chat_ts ON messages(chat, timestamp DESC)`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_ts ON messages(timestamp DESC)`)
	return err
}

// IsPaired returns true if we have a stored session.
func (c *Client) IsPaired() bool {
	return c.WM.Store.ID != nil
}

// Connect connects to WhatsApp and waits for the connection to be ready.
func (c *Client) Connect(ctx context.Context) error {
	if err := c.WM.Connect(); err != nil {
		return err
	}
	select {
	case <-c.connected:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for WhatsApp connection")
	}
}

// Disconnect cleanly disconnects.
func (c *Client) Disconnect() {
	c.WM.Disconnect()
	if c.db != nil {
		c.db.Close()
	}
}

func (c *Client) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		select {
		case <-c.connected:
		default:
			close(c.connected)
		}
	case *events.Message:
		text := extractText(v.Message)
		if text == "" {
			return
		}
		// Cache the push name
		if v.Info.PushName != "" {
			c.namesMu.Lock()
			c.names[v.Info.Sender.String()] = v.Info.PushName
			if v.Info.IsGroup {
				// For groups, cache the chat JID with the group name from push events
				// The actual group name comes from contact store, but push_name helps
			}
			c.namesMu.Unlock()
		}
		c.storeMessage(StoredMessage{
			ID:        string(v.Info.ID),
			Chat:      v.Info.Chat.String(),
			Sender:    v.Info.Sender.String(),
			PushName:  v.Info.PushName,
			Text:      text,
			Timestamp: v.Info.Timestamp,
			IsFromMe:  v.Info.IsFromMe,
			IsGroup:   v.Info.IsGroup,
		})
	}
}

func (c *Client) storeMessage(msg StoredMessage) {
	isFromMe := 0
	if msg.IsFromMe {
		isFromMe = 1
	}
	isGroup := 0
	if msg.IsGroup {
		isGroup = 1
	}
	_, err := c.db.Exec(
		`INSERT OR IGNORE INTO messages (id, chat, sender, push_name, text, timestamp, is_from_me, is_group)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		msg.ID, msg.Chat, msg.Sender, msg.PushName, msg.Text,
		msg.Timestamp.Unix(), isFromMe, isGroup,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "whasapo: failed to store message: %v\n", err)
	}
}

func extractText(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if t := msg.GetConversation(); t != "" {
		return t
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if img := msg.GetImageMessage(); img != nil {
		return "[image] " + img.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		return "[video] " + vid.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		return "[document] " + doc.GetFileName()
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return "[audio]"
	}
	if stk := msg.GetStickerMessage(); stk != nil {
		return "[sticker]"
	}
	return ""
}

// SendMessage sends a text message to a JID string.
func (c *Client) SendMessage(ctx context.Context, jidStr, text string) error {
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return fmt.Errorf("parse JID %q: %w", jidStr, err)
	}
	_, err = c.WM.SendMessage(ctx, jid, &waE2E.Message{
		Conversation: proto.String(text),
	})
	return err
}

// GetMessages returns messages from SQLite, optionally filtered by chat.
func (c *Client) GetMessages(chatFilter string, limit int) []StoredMessage {
	var rows *sql.Rows
	var err error
	if chatFilter != "" {
		rows, err = c.db.Query(
			`SELECT id, chat, sender, push_name, text, timestamp, is_from_me, is_group
			 FROM messages WHERE chat = ? ORDER BY timestamp DESC LIMIT ?`,
			chatFilter, limit,
		)
	} else {
		rows, err = c.db.Query(
			`SELECT id, chat, sender, push_name, text, timestamp, is_from_me, is_group
			 FROM messages ORDER BY timestamp DESC LIMIT ?`,
			limit,
		)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var ts int64
		var fromMe, group int
		if err := rows.Scan(&m.ID, &m.Chat, &m.Sender, &m.PushName, &m.Text, &ts, &fromMe, &group); err != nil {
			continue
		}
		m.Timestamp = time.Unix(ts, 0)
		m.IsFromMe = fromMe == 1
		m.IsGroup = group == 1
		msgs = append(msgs, m)
	}
	// Reverse to chronological order (query returns newest first)
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs
}

// GetChats returns known chats from stored messages + joined groups.
func (c *Client) GetChats(ctx context.Context) ([]ChatInfo, error) {
	// Query distinct chats with their most recent message
	rows, err := c.db.Query(`
		SELECT chat, push_name, text, timestamp, is_group
		FROM messages
		WHERE (chat, timestamp) IN (
			SELECT chat, MAX(timestamp) FROM messages GROUP BY chat
		)
		ORDER BY timestamp DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := make(map[string]bool)
	var chats []ChatInfo
	for rows.Next() {
		var chat, pushName, text string
		var ts int64
		var isGroup int
		if err := rows.Scan(&chat, &pushName, &text, &ts, &isGroup); err != nil {
			continue
		}
		seen[chat] = true
		name := c.resolveNameCached(ctx, chat)
		chats = append(chats, ChatInfo{
			JID:      chat,
			Name:     name,
			IsGroup:  isGroup == 1,
			LastMsg:  truncate(text, 100),
			LastTime: time.Unix(ts, 0).Format(time.RFC3339),
		})
	}

	// Also add joined groups not yet seen
	groups, err := c.WM.GetJoinedGroups(ctx)
	if err == nil {
		for _, g := range groups {
			jidStr := g.JID.String()
			if !seen[jidStr] {
				seen[jidStr] = true
				c.namesMu.Lock()
				c.names[jidStr] = g.Name
				c.namesMu.Unlock()
				chats = append(chats, ChatInfo{
					JID:     jidStr,
					Name:    g.Name,
					IsGroup: true,
				})
			}
		}
	}

	return chats, nil
}

// SearchContacts searches contacts by name or phone number.
func (c *Client) SearchContacts(ctx context.Context, query string) ([]ChatInfo, error) {
	contacts, err := c.WM.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	lowerQuery := toLower(query)
	var results []ChatInfo
	for jid, info := range contacts {
		name := contactName(info)
		if containsLower(toLower(name), lowerQuery) || containsLower(jid.User, lowerQuery) {
			results = append(results, ChatInfo{
				JID:     jid.String(),
				Name:    name,
				IsGroup: jid.Server == types.GroupServer,
			})
		}
	}
	return results, nil
}

// resolveNameCached returns a display name without making network calls.
func (c *Client) resolveNameCached(ctx context.Context, jidStr string) string {
	// Check our push name cache first
	c.namesMu.RLock()
	if name, ok := c.names[jidStr]; ok {
		c.namesMu.RUnlock()
		return name
	}
	c.namesMu.RUnlock()

	// Try the contact store (local, no network call)
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return jidStr
	}
	contact, err := c.WM.Store.Contacts.GetContact(ctx, jid)
	if err == nil && contact.Found {
		name := contactName(contact)
		if name != "" {
			c.namesMu.Lock()
			c.names[jidStr] = name
			c.namesMu.Unlock()
			return name
		}
	}

	return jidStr
}

func contactName(c types.ContactInfo) string {
	if c.FullName != "" {
		return c.FullName
	}
	if c.PushName != "" {
		return c.PushName
	}
	if c.BusinessName != "" {
		return c.BusinessName
	}
	if c.FirstName != "" {
		return c.FirstName
	}
	return ""
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func containsLower(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
