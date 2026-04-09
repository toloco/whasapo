package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// StoredMessage holds a message.
type StoredMessage struct {
	ID         string    `json:"id"`
	Chat       string    `json:"chat"`
	Sender     string    `json:"sender"`
	PushName   string    `json:"push_name"`
	Text       string    `json:"text"`
	Timestamp  time.Time `json:"timestamp"`
	IsFromMe   bool      `json:"is_from_me"`
	IsGroup    bool      `json:"is_group"`
	MediaType  string    `json:"media_type,omitempty"`
	mediaProto []byte    // not serialized to JSON, used for download
}

// ChatInfo holds basic chat info.
type ChatInfo struct {
	JID      string `json:"jid"`
	Name     string `json:"name"`
	IsGroup  bool   `json:"is_group"`
	LastMsg  string `json:"last_message,omitempty"`
	LastTime string `json:"last_time,omitempty"`
}

// ChatDetail holds detailed chat information.
type ChatDetail struct {
	JID          string            `json:"jid"`
	Name         string            `json:"name"`
	IsGroup      bool              `json:"is_group"`
	Topic        string            `json:"topic,omitempty"`
	Participants []ParticipantInfo `json:"participants,omitempty"`
	CreatedAt    string            `json:"created_at,omitempty"`
	MessageCount int              `json:"message_count"`
}

// ParticipantInfo holds group participant info.
type ParticipantInfo struct {
	JID     string `json:"jid"`
	Name    string `json:"name,omitempty"`
	IsAdmin bool   `json:"is_admin,omitempty"`
}

// Client wraps whatsmeow with persistent message storage.
type Client struct {
	WM        *whatsmeow.Client
	Container *sqlstore.Container
	db        *sql.DB
	connected chan struct{}
	ready     atomic.Bool
	loggedOut atomic.Bool
	names     map[string]string
	namesMu   sync.RWMutex
	mediaDir  string
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

	db, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("open message db: %w", err)
	}

	if err := initMessageTable(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("init messages: %w", err)
	}

	// Media directory next to the database
	mediaDir := filepath.Join(filepath.Dir(dbPath), "media")
	os.MkdirAll(mediaDir, 0700)

	wm := whatsmeow.NewClient(deviceStore, waLog.Noop)

	c := &Client{
		WM:        wm,
		Container: container,
		db:        db,
		connected: make(chan struct{}),
		names:     make(map[string]string),
		mediaDir:  mediaDir,
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
		media_type TEXT NOT NULL DEFAULT '',
		media_proto BLOB,
		PRIMARY KEY (id, chat)
	)`)
	if err != nil {
		return err
	}
	// Add columns if upgrading from older schema
	db.Exec(`ALTER TABLE messages ADD COLUMN media_type TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE messages ADD COLUMN media_proto BLOB`)
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

// IsReady returns true if connected to WhatsApp and session is valid.
func (c *Client) IsReady() bool {
	return c.ready.Load() && !c.loggedOut.Load()
}

// IsLoggedOut returns true if WhatsApp has invalidated the session.
func (c *Client) IsLoggedOut() bool {
	return c.loggedOut.Load()
}

func (c *Client) eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		c.ready.Store(true)
		fmt.Fprintf(os.Stderr, "whasapo: whatsapp connected\n")
		select {
		case <-c.connected:
		default:
			close(c.connected)
		}
	case *events.Disconnected:
		c.ready.Store(false)
		fmt.Fprintf(os.Stderr, "whasapo: whatsapp disconnected, will reconnect automatically\n")
	case *events.LoggedOut:
		c.ready.Store(false)
		c.loggedOut.Store(true)
		fmt.Fprintf(os.Stderr, "whasapo: session expired — run 'whasapo pair' to re-link\n")
	case *events.StreamReplaced:
		c.ready.Store(false)
		fmt.Fprintf(os.Stderr, "whasapo: connection replaced by another client\n")
	case *events.HistorySync:
		c.handleHistorySync(v.Data)
	case *events.Message:
		msg := c.messageToStored(v.Info, v.Message)
		if msg.Text == "" {
			return
		}
		if v.Info.PushName != "" {
			c.namesMu.Lock()
			c.names[v.Info.Sender.String()] = v.Info.PushName
			c.namesMu.Unlock()
		}
		c.storeMessage(msg)
	}
}

func (c *Client) messageToStored(info types.MessageInfo, msg *waE2E.Message) StoredMessage {
	text, mediaType := extractTextAndMedia(msg)
	sm := StoredMessage{
		ID:        string(info.ID),
		Chat:      info.Chat.String(),
		Sender:    info.Sender.String(),
		PushName:  info.PushName,
		Text:      text,
		Timestamp: info.Timestamp,
		IsFromMe:  info.IsFromMe,
		IsGroup:   info.IsGroup,
		MediaType: mediaType,
	}
	// Store raw proto for media messages so we can download later
	if mediaType != "" && msg != nil {
		if data, err := proto.Marshal(msg); err == nil {
			sm.mediaProto = data
		}
	}
	return sm
}

func (c *Client) handleHistorySync(data *waHistorySync.HistorySync) {
	if data == nil {
		return
	}
	count := 0
	for _, conv := range data.GetConversations() {
		chatJID := conv.GetID()
		if chatJID == "" {
			continue
		}
		isGroup := strings.HasSuffix(chatJID, "@g.us")
		for _, hm := range conv.GetMessages() {
			wmi := hm.GetMessage()
			if wmi == nil || wmi.GetMessage() == nil {
				continue
			}
			text, mediaType := extractTextAndMedia(wmi.GetMessage())
			if text == "" {
				continue
			}
			ts := int64(wmi.GetMessageTimestamp())
			if ts == 0 {
				continue
			}
			senderJID := chatJID
			pushName := ""
			isFromMe := false
			if wmi.GetKey().GetFromMe() {
				isFromMe = true
				if c.WM.Store.ID != nil {
					senderJID = c.WM.Store.ID.String()
				}
			} else if wmi.GetKey().GetParticipant() != "" {
				senderJID = wmi.GetKey().GetParticipant()
			}
			if wmi.GetPushName() != "" {
				pushName = wmi.GetPushName()
				c.namesMu.Lock()
				c.names[senderJID] = pushName
				c.namesMu.Unlock()
			}
			sm := StoredMessage{
				ID:        wmi.GetKey().GetID(),
				Chat:      chatJID,
				Sender:    senderJID,
				PushName:  pushName,
				Text:      text,
				Timestamp: time.Unix(ts, 0),
				IsFromMe:  isFromMe,
				IsGroup:   isGroup,
				MediaType: mediaType,
			}
			if mediaType != "" {
				if data, err := proto.Marshal(wmi.GetMessage()); err == nil {
					sm.mediaProto = data
				}
			}
			c.storeMessage(sm)
			count++
		}
	}
	if count > 0 {
		fmt.Fprintf(os.Stderr, "whasapo: synced %d historical messages\n", count)
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
		`INSERT INTO messages (id, chat, sender, push_name, text, timestamp, is_from_me, is_group, media_type, media_proto)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id, chat) DO UPDATE SET text = excluded.text, push_name = excluded.push_name, media_type = excluded.media_type,
		 media_proto = COALESCE(excluded.media_proto, messages.media_proto)`,
		msg.ID, msg.Chat, msg.Sender, msg.PushName, msg.Text,
		msg.Timestamp.Unix(), isFromMe, isGroup, msg.MediaType, msg.mediaProto,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "whasapo: failed to store message: %v\n", err)
	}
}

func extractTextAndMedia(msg *waE2E.Message) (text string, mediaType string) {
	if msg == nil {
		return "", ""
	}
	if t := msg.GetConversation(); t != "" {
		return t, ""
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		return ext.GetText(), ""
	}
	if img := msg.GetImageMessage(); img != nil {
		caption := img.GetCaption()
		if caption != "" {
			return "[image] " + caption, "image"
		}
		return "[image]", "image"
	}
	if vid := msg.GetVideoMessage(); vid != nil {
		caption := vid.GetCaption()
		if caption != "" {
			return "[video] " + caption, "video"
		}
		return "[video]", "video"
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		name := doc.GetFileName()
		if name != "" {
			return "[document] " + name, "document"
		}
		return "[document]", "document"
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return "[audio]", "audio"
	}
	if stk := msg.GetStickerMessage(); stk != nil {
		return "[sticker]", "sticker"
	}
	if loc := msg.GetLocationMessage(); loc != nil {
		name := loc.GetName()
		if name != "" {
			return "[location] " + name, ""
		}
		return fmt.Sprintf("[location] %.4f, %.4f", loc.GetDegreesLatitude(), loc.GetDegreesLongitude()), ""
	}
	if con := msg.GetContactMessage(); con != nil {
		if name := con.GetDisplayName(); name != "" {
			return "[contact] " + name, ""
		}
		return "[contact]", ""
	}
	if poll := msg.GetPollCreationMessage(); poll != nil {
		if name := poll.GetName(); name != "" {
			return "[poll] " + name, ""
		}
		return "[poll]", ""
	}
	if li := msg.GetListMessage(); li != nil {
		title := li.GetTitle()
		if title != "" {
			return "[list] " + title, ""
		}
		return "[list]", ""
	}
	return "[unsupported]", ""
}

// DownloadMedia downloads media from a message and saves it to disk.
// Returns the local file path.
func (c *Client) DownloadMedia(ctx context.Context, msgID, chatJID string) (string, error) {
	var mediaType string
	var mediaProto []byte
	err := c.db.QueryRow(
		`SELECT media_type, media_proto FROM messages WHERE id = ? AND chat = ?`,
		msgID, chatJID,
	).Scan(&mediaType, &mediaProto)
	if err != nil {
		return "", fmt.Errorf("message not found: %w", err)
	}
	if mediaType == "" {
		return "", fmt.Errorf("message has no downloadable media")
	}
	if len(mediaProto) == 0 {
		return "", fmt.Errorf("media metadata not available (message was stored before media download support)")
	}

	// Deserialize the stored protobuf
	var msg waE2E.Message
	if err := proto.Unmarshal(mediaProto, &msg); err != nil {
		return "", fmt.Errorf("failed to decode media info: %w", err)
	}

	// Get the downloadable message and determine extension
	var data []byte
	var ext string
	switch mediaType {
	case "image":
		if im := msg.GetImageMessage(); im != nil {
			data, err = c.WM.Download(ctx, im)
			ext = extensionFromMime(im.GetMimetype(), ".jpg")
		}
	case "video":
		if vm := msg.GetVideoMessage(); vm != nil {
			data, err = c.WM.Download(ctx, vm)
			ext = extensionFromMime(vm.GetMimetype(), ".mp4")
		}
	case "audio":
		if am := msg.GetAudioMessage(); am != nil {
			data, err = c.WM.Download(ctx, am)
			ext = extensionFromMime(am.GetMimetype(), ".ogg")
		}
	case "document":
		if dm := msg.GetDocumentMessage(); dm != nil {
			data, err = c.WM.Download(ctx, dm)
			ext = extensionFromMime(dm.GetMimetype(), ".bin")
			if fn := dm.GetFileName(); fn != "" {
				ext = filepath.Ext(fn)
			}
		}
	case "sticker":
		if sm := msg.GetStickerMessage(); sm != nil {
			data, err = c.WM.Download(ctx, sm)
			ext = ".webp"
		}
	default:
		return "", fmt.Errorf("unsupported media type: %s", mediaType)
	}

	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	if data == nil {
		return "", fmt.Errorf("no media content in message")
	}

	// Save to media directory
	filename := fmt.Sprintf("%s_%s%s", chatJID, msgID, ext)
	// Sanitize filename
	filename = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == '@' {
			return '_'
		}
		return r
	}, filename)
	filePath := filepath.Join(c.mediaDir, filename)

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to save: %w", err)
	}

	return filePath, nil
}

func extensionFromMime(mimeType, fallback string) string {
	exts, err := mime.ExtensionsByType(mimeType)
	if err == nil && len(exts) > 0 {
		return exts[0]
	}
	return fallback
}

// SendFile uploads and sends a file to a WhatsApp contact.
func (c *Client) SendFile(ctx context.Context, jidStr, filePath, caption string) error {
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return fmt.Errorf("parse JID %q: %w", jidStr, err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file: %w", err)
	}

	ext := filepath.Ext(filePath)
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	fileName := filepath.Base(filePath)

	// Determine media type from MIME
	var wmMediaType whatsmeow.MediaType
	var msg *waE2E.Message

	switch {
	case strings.HasPrefix(mimeType, "image/"):
		wmMediaType = whatsmeow.MediaImage
		resp, err := c.WM.Upload(ctx, data, wmMediaType)
		if err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:           proto.String(resp.URL),
				DirectPath:    proto.String(resp.DirectPath),
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    proto.Uint64(resp.FileLength),
				Mimetype:      proto.String(mimeType),
				Caption:       proto.String(caption),
			},
		}
	case strings.HasPrefix(mimeType, "video/"):
		wmMediaType = whatsmeow.MediaVideo
		resp, err := c.WM.Upload(ctx, data, wmMediaType)
		if err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{
			VideoMessage: &waE2E.VideoMessage{
				URL:           proto.String(resp.URL),
				DirectPath:    proto.String(resp.DirectPath),
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    proto.Uint64(resp.FileLength),
				Mimetype:      proto.String(mimeType),
				Caption:       proto.String(caption),
			},
		}
	case strings.HasPrefix(mimeType, "audio/"):
		wmMediaType = whatsmeow.MediaAudio
		resp, err := c.WM.Upload(ctx, data, wmMediaType)
		if err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{
			AudioMessage: &waE2E.AudioMessage{
				URL:           proto.String(resp.URL),
				DirectPath:    proto.String(resp.DirectPath),
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    proto.Uint64(resp.FileLength),
				Mimetype:      proto.String(mimeType),
			},
		}
	default:
		wmMediaType = whatsmeow.MediaDocument
		resp, err := c.WM.Upload(ctx, data, wmMediaType)
		if err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		msg = &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				URL:           proto.String(resp.URL),
				DirectPath:    proto.String(resp.DirectPath),
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    proto.Uint64(resp.FileLength),
				Mimetype:      proto.String(mimeType),
				FileName:      proto.String(fileName),
				Title:         proto.String(fileName),
			},
		}
	}

	_, err = c.WM.SendMessage(ctx, jid, msg)
	return err
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

// GetMessages returns messages from SQLite, optionally filtered by chat JID or search query.
func (c *Client) GetMessages(chatFilter, query string, limit int) []StoredMessage {
	var rows *sql.Rows
	var err error
	if chatFilter != "" && query != "" {
		rows, err = c.db.Query(
			`SELECT id, chat, sender, push_name, text, timestamp, is_from_me, is_group, media_type
			 FROM messages WHERE chat = ? AND (push_name LIKE ? OR text LIKE ?)
			 ORDER BY timestamp DESC LIMIT ?`,
			chatFilter, "%"+query+"%", "%"+query+"%", limit,
		)
	} else if chatFilter != "" {
		rows, err = c.db.Query(
			`SELECT id, chat, sender, push_name, text, timestamp, is_from_me, is_group, media_type
			 FROM messages WHERE chat = ? ORDER BY timestamp DESC LIMIT ?`,
			chatFilter, limit,
		)
	} else if query != "" {
		rows, err = c.db.Query(
			`SELECT id, chat, sender, push_name, text, timestamp, is_from_me, is_group, media_type
			 FROM messages WHERE push_name LIKE ? OR text LIKE ?
			 ORDER BY timestamp DESC LIMIT ?`,
			"%"+query+"%", "%"+query+"%", limit,
		)
	} else {
		rows, err = c.db.Query(
			`SELECT id, chat, sender, push_name, text, timestamp, is_from_me, is_group, media_type
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
		if err := rows.Scan(&m.ID, &m.Chat, &m.Sender, &m.PushName, &m.Text, &ts, &fromMe, &group, &m.MediaType); err != nil {
			continue
		}
		m.Timestamp = time.Unix(ts, 0)
		m.IsFromMe = fromMe == 1
		m.IsGroup = group == 1
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "whasapo: error reading messages: %v\n", err)
	}
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs
}

// GetChats returns known chats from stored messages + joined groups.
func (c *Client) GetChats(ctx context.Context) ([]ChatInfo, error) {
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
		if err := rows.Err(); err != nil {
			break
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

// GetChatDetail returns detailed info about a specific chat.
func (c *Client) GetChatDetail(ctx context.Context, chatJID string) (*ChatDetail, error) {
	jid, err := types.ParseJID(chatJID)
	if err != nil {
		return nil, fmt.Errorf("parse JID: %w", err)
	}

	detail := &ChatDetail{
		JID:  chatJID,
		Name: c.resolveNameCached(ctx, chatJID),
	}

	// Get message count
	c.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat = ?`, chatJID).Scan(&detail.MessageCount)

	if jid.Server == types.GroupServer {
		detail.IsGroup = true
		info, err := c.WM.GetGroupInfo(ctx, jid)
		if err == nil {
			detail.Name = info.Name
			detail.Topic = info.Topic
			if !info.GroupCreated.IsZero() {
				detail.CreatedAt = info.GroupCreated.Format(time.RFC3339)
			}
			for _, p := range info.Participants {
				pi := ParticipantInfo{
					JID:     p.JID.String(),
					IsAdmin: p.IsAdmin || p.IsSuperAdmin,
				}
				if p.DisplayName != "" {
					pi.Name = p.DisplayName
				} else {
					pi.Name = c.resolveNameCached(ctx, p.JID.String())
				}
				detail.Participants = append(detail.Participants, pi)
			}
		}
	}

	return detail, nil
}

// SearchContacts searches contacts by name or phone number.
func (c *Client) SearchContacts(ctx context.Context, query string) ([]ChatInfo, error) {
	contacts, err := c.WM.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	var results []ChatInfo
	lowerQuery := strings.ToLower(query)
	for jid, info := range contacts {
		name := contactName(info)
		if strings.Contains(strings.ToLower(name), lowerQuery) || strings.Contains(strings.ToLower(jid.User), lowerQuery) {
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
	c.namesMu.RLock()
	if name, ok := c.names[jidStr]; ok {
		c.namesMu.RUnlock()
		return name
	}
	c.namesMu.RUnlock()

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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
