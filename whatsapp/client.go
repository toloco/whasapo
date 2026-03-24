package whatsapp

import (
	"context"
	"fmt"
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

// StoredMessage holds a received message in memory.
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

// Client wraps whatsmeow with message storage.
type Client struct {
	WM        *whatsmeow.Client
	Container *sqlstore.Container
	messages  []StoredMessage
	mu        sync.RWMutex
	maxMsgs   int
	connected chan struct{}
}

// NewClient creates the WhatsApp client with a SQLite session store.
func NewClient(dbPath string) (*Client, error) {
	store.SetOSInfo("Whasapo MCP", [3]uint32{0, 1, 0})

	container, err := sqlstore.New(context.Background(), "sqlite",
		fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath),
		waLog.Noop,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	wm := whatsmeow.NewClient(deviceStore, waLog.Noop)

	c := &Client{
		WM:        wm,
		Container: container,
		maxMsgs:   5000,
		connected: make(chan struct{}),
	}

	wm.AddEventHandler(c.eventHandler)
	return c, nil
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
	// Wait for Connected event or timeout
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
}

func (c *Client) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		select {
		case <-c.connected:
			// already closed
		default:
			close(c.connected)
		}
	case *events.Message:
		text := extractText(v.Message)
		if text == "" {
			return
		}
		msg := StoredMessage{
			ID:        string(v.Info.ID),
			Chat:      v.Info.Chat.String(),
			Sender:    v.Info.Sender.String(),
			PushName:  v.Info.PushName,
			Text:      text,
			Timestamp: v.Info.Timestamp,
			IsFromMe:  v.Info.IsFromMe,
			IsGroup:   v.Info.IsGroup,
		}
		c.mu.Lock()
		c.messages = append(c.messages, msg)
		if len(c.messages) > c.maxMsgs {
			c.messages = c.messages[len(c.messages)-c.maxMsgs:]
		}
		c.mu.Unlock()
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

// GetMessages returns stored messages, optionally filtered by chat JID.
func (c *Client) GetMessages(chatFilter string, limit int) []StoredMessage {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []StoredMessage
	for i := len(c.messages) - 1; i >= 0 && len(result) < limit; i-- {
		m := c.messages[i]
		if chatFilter == "" || m.Chat == chatFilter {
			result = append(result, m)
		}
	}
	// Reverse to chronological order
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

// GetChats returns known chats from contacts + recent messages.
func (c *Client) GetChats(ctx context.Context) ([]ChatInfo, error) {
	seen := make(map[string]bool)
	var chats []ChatInfo

	// From recent messages (most relevant)
	c.mu.RLock()
	for i := len(c.messages) - 1; i >= 0; i-- {
		m := c.messages[i]
		if seen[m.Chat] {
			continue
		}
		seen[m.Chat] = true
		name := c.resolveName(ctx, m.Chat)
		jid, _ := types.ParseJID(m.Chat)
		chats = append(chats, ChatInfo{
			JID:     m.Chat,
			Name:    name,
			IsGroup: jid.Server == types.GroupServer,
			LastMsg: truncate(m.Text, 100),
			LastTime: m.Timestamp.Format(time.RFC3339),
		})
	}
	c.mu.RUnlock()

	// Also add joined groups
	groups, err := c.WM.GetJoinedGroups(ctx)
	if err == nil {
		for _, g := range groups {
			jidStr := g.JID.String()
			if !seen[jidStr] {
				seen[jidStr] = true
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
	var results []ChatInfo
	for jid, info := range contacts {
		name := contactName(info)
		if containsCI(name, query) || containsCI(jid.User, query) {
			results = append(results, ChatInfo{
				JID:     jid.String(),
				Name:    name,
				IsGroup: jid.Server == types.GroupServer,
			})
		}
	}
	return results, nil
}

func (c *Client) resolveName(ctx context.Context, jidStr string) string {
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return jidStr
	}
	if jid.Server == types.GroupServer {
		info, err := c.WM.GetGroupInfo(ctx, jid)
		if err == nil {
			return info.Name
		}
	}
	contact, err := c.WM.Store.Contacts.GetContact(ctx, jid)
	if err == nil && contact.Found {
		return contactName(contact)
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

func containsCI(s, sub string) bool {
	if sub == "" {
		return true
	}
	ls := toLower(s)
	lsub := toLower(sub)
	return len(ls) >= len(lsub) && contains(ls, lsub)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, sub string) bool {
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
