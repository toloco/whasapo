package whatsapp

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// --- extractTextAndMedia tests ---

func TestExtractTextAndMedia_Conversation(t *testing.T) {
	msg := &waE2E.Message{Conversation: proto.String("hello")}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "hello", text)
	assertEqual(t, "", media)
}

func TestExtractTextAndMedia_ExtendedText(t *testing.T) {
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link msg")},
	}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "link msg", text)
	assertEqual(t, "", media)
}

func TestExtractTextAndMedia_Image(t *testing.T) {
	msg := &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{Caption: proto.String("sunset")},
	}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "[image] sunset", text)
	assertEqual(t, "image", media)
}

func TestExtractTextAndMedia_ImageNoCaption(t *testing.T) {
	msg := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{}}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "[image]", text)
	assertEqual(t, "image", media)
}

func TestExtractTextAndMedia_Video(t *testing.T) {
	msg := &waE2E.Message{
		VideoMessage: &waE2E.VideoMessage{Caption: proto.String("clip")},
	}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "[video] clip", text)
	assertEqual(t, "video", media)
}

func TestExtractTextAndMedia_Document(t *testing.T) {
	msg := &waE2E.Message{
		DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("report.pdf")},
	}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "[document] report.pdf", text)
	assertEqual(t, "document", media)
}

func TestExtractTextAndMedia_Audio(t *testing.T) {
	msg := &waE2E.Message{AudioMessage: &waE2E.AudioMessage{}}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "[audio]", text)
	assertEqual(t, "audio", media)
}

func TestExtractTextAndMedia_Sticker(t *testing.T) {
	msg := &waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "[sticker]", text)
	assertEqual(t, "sticker", media)
}

func TestExtractTextAndMedia_Location(t *testing.T) {
	lat := float64(51.5074)
	lng := float64(-0.1278)
	msg := &waE2E.Message{
		LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  &lat,
			DegreesLongitude: &lng,
		},
	}
	text, media := extractTextAndMedia(msg)
	assertEqual(t, "[location] 51.5074, -0.1278", text)
	assertEqual(t, "", media)
}

func TestExtractTextAndMedia_LocationWithName(t *testing.T) {
	msg := &waE2E.Message{
		LocationMessage: &waE2E.LocationMessage{Name: proto.String("Big Ben")},
	}
	text, _ := extractTextAndMedia(msg)
	assertEqual(t, "[location] Big Ben", text)
}

func TestExtractTextAndMedia_Contact(t *testing.T) {
	msg := &waE2E.Message{
		ContactMessage: &waE2E.ContactMessage{DisplayName: proto.String("John")},
	}
	text, _ := extractTextAndMedia(msg)
	assertEqual(t, "[contact] John", text)
}

func TestExtractTextAndMedia_ContactEmpty(t *testing.T) {
	msg := &waE2E.Message{ContactMessage: &waE2E.ContactMessage{}}
	text, _ := extractTextAndMedia(msg)
	assertEqual(t, "[contact]", text)
}

func TestExtractTextAndMedia_Poll(t *testing.T) {
	msg := &waE2E.Message{
		PollCreationMessage: &waE2E.PollCreationMessage{Name: proto.String("Lunch?")},
	}
	text, _ := extractTextAndMedia(msg)
	assertEqual(t, "[poll] Lunch?", text)
}

func TestExtractTextAndMedia_Nil(t *testing.T) {
	text, media := extractTextAndMedia(nil)
	assertEqual(t, "", text)
	assertEqual(t, "", media)
}

func TestExtractTextAndMedia_Empty(t *testing.T) {
	msg := &waE2E.Message{}
	text, _ := extractTextAndMedia(msg)
	assertEqual(t, "[unsupported]", text)
}

// --- contactName tests ---

func TestContactName_FullName(t *testing.T) {
	c := types.ContactInfo{FullName: "John Doe", PushName: "Johnny"}
	assertEqual(t, "John Doe", contactName(c))
}

func TestContactName_PushName(t *testing.T) {
	c := types.ContactInfo{PushName: "Johnny"}
	assertEqual(t, "Johnny", contactName(c))
}

func TestContactName_BusinessName(t *testing.T) {
	c := types.ContactInfo{BusinessName: "Acme Corp"}
	assertEqual(t, "Acme Corp", contactName(c))
}

func TestContactName_FirstName(t *testing.T) {
	c := types.ContactInfo{FirstName: "John"}
	assertEqual(t, "John", contactName(c))
}

func TestContactName_Empty(t *testing.T) {
	c := types.ContactInfo{}
	assertEqual(t, "", contactName(c))
}

// --- truncate tests ---

func TestTruncate_Short(t *testing.T) {
	assertEqual(t, "hi", truncate("hi", 10))
}

func TestTruncate_Exact(t *testing.T) {
	assertEqual(t, "hello", truncate("hello", 5))
}

func TestTruncate_Long(t *testing.T) {
	assertEqual(t, "hel...", truncate("hello world", 3))
}

func TestTruncate_Empty(t *testing.T) {
	assertEqual(t, "", truncate("", 10))
}

// --- extensionFromMime tests ---

func TestExtensionFromMime_Known(t *testing.T) {
	ext := extensionFromMime("image/png", ".bin")
	if ext != ".png" {
		t.Errorf("expected .png, got %s", ext)
	}
}

func TestExtensionFromMime_Unknown(t *testing.T) {
	assertEqual(t, ".bin", extensionFromMime("application/x-unknown-type-xyz", ".bin"))
}

func TestExtensionFromMime_Empty(t *testing.T) {
	assertEqual(t, ".dat", extensionFromMime("", ".dat"))
}

// --- SQLite integration tests ---

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	if err := initMessageTable(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestInitMessageTable_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	// Call again — should not error
	if err := initMessageTable(db); err != nil {
		t.Fatal(err)
	}
}

func TestStoreAndGetMessages(t *testing.T) {
	db := setupTestDB(t)
	c := &Client{db: db, names: make(map[string]string)}

	c.storeMessage(StoredMessage{
		ID: "msg1", Chat: "chat1@s.whatsapp.net", Sender: "sender1",
		PushName: "Alice", Text: "hello", Timestamp: time.Unix(1000, 0),
	})
	c.storeMessage(StoredMessage{
		ID: "msg2", Chat: "chat1@s.whatsapp.net", Sender: "sender2",
		PushName: "Bob", Text: "world", Timestamp: time.Unix(2000, 0),
	})
	c.storeMessage(StoredMessage{
		ID: "msg3", Chat: "chat2@g.us", Sender: "sender1",
		PushName: "Alice", Text: "group msg", Timestamp: time.Unix(3000, 0),
		IsGroup: true,
	})

	// Get all messages
	msgs := c.GetMessages("", "", 50)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	// Should be chronological
	assertEqual(t, "hello", msgs[0].Text)
	assertEqual(t, "world", msgs[1].Text)
	assertEqual(t, "group msg", msgs[2].Text)
}

func TestGetMessages_FilterByChat(t *testing.T) {
	db := setupTestDB(t)
	c := &Client{db: db, names: make(map[string]string)}

	c.storeMessage(StoredMessage{
		ID: "msg1", Chat: "chat1@s.whatsapp.net", Text: "hello", Timestamp: time.Unix(1000, 0),
	})
	c.storeMessage(StoredMessage{
		ID: "msg2", Chat: "chat2@g.us", Text: "group", Timestamp: time.Unix(2000, 0),
	})

	msgs := c.GetMessages("chat1@s.whatsapp.net", "", 50)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	assertEqual(t, "hello", msgs[0].Text)
}

func TestGetMessages_FilterByQuery(t *testing.T) {
	db := setupTestDB(t)
	c := &Client{db: db, names: make(map[string]string)}

	c.storeMessage(StoredMessage{
		ID: "msg1", Chat: "chat1@s.whatsapp.net", PushName: "Alice",
		Text: "hello", Timestamp: time.Unix(1000, 0),
	})
	c.storeMessage(StoredMessage{
		ID: "msg2", Chat: "chat1@s.whatsapp.net", PushName: "Bob",
		Text: "goodbye", Timestamp: time.Unix(2000, 0),
	})

	// Search by text
	msgs := c.GetMessages("", "good", 50)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	assertEqual(t, "goodbye", msgs[0].Text)

	// Search by push name
	msgs = c.GetMessages("", "Alice", 50)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	assertEqual(t, "hello", msgs[0].Text)
}

func TestGetMessages_Limit(t *testing.T) {
	db := setupTestDB(t)
	c := &Client{db: db, names: make(map[string]string)}

	for i := 0; i < 10; i++ {
		c.storeMessage(StoredMessage{
			ID: "msg" + string(rune('a'+i)), Chat: "chat1@s.whatsapp.net",
			Text: "msg", Timestamp: time.Unix(int64(1000+i), 0),
		})
	}

	msgs := c.GetMessages("", "", 3)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
}

func TestStoreMessage_Upsert(t *testing.T) {
	db := setupTestDB(t)
	c := &Client{db: db, names: make(map[string]string)}

	c.storeMessage(StoredMessage{
		ID: "msg1", Chat: "chat1@s.whatsapp.net", Text: "original",
		Timestamp: time.Unix(1000, 0),
	})
	c.storeMessage(StoredMessage{
		ID: "msg1", Chat: "chat1@s.whatsapp.net", Text: "edited",
		Timestamp: time.Unix(1000, 0),
	})

	msgs := c.GetMessages("", "", 50)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after upsert, got %d", len(msgs))
	}
	assertEqual(t, "edited", msgs[0].Text)
}

func TestStoreMessage_MediaType(t *testing.T) {
	db := setupTestDB(t)
	c := &Client{db: db, names: make(map[string]string)}

	c.storeMessage(StoredMessage{
		ID: "img1", Chat: "chat1@s.whatsapp.net", Text: "[image] sunset",
		Timestamp: time.Unix(1000, 0), MediaType: "image",
	})

	msgs := c.GetMessages("", "", 50)
	if len(msgs) != 1 {
		t.Fatal("expected 1 message")
	}
	assertEqual(t, "image", msgs[0].MediaType)
}

func TestDownloadMedia_NoProto(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()
	c := &Client{db: db, names: make(map[string]string), mediaDir: dir}

	c.storeMessage(StoredMessage{
		ID: "img1", Chat: "chat1@s.whatsapp.net", Text: "[image]",
		Timestamp: time.Unix(1000, 0), MediaType: "image",
	})

	_, err := c.DownloadMedia(nil, "img1", "chat1@s.whatsapp.net")
	if err == nil {
		t.Fatal("expected error for missing proto")
	}
	if !os.IsNotExist(err) && err.Error() != "media metadata not available (message was stored before media download support)" {
		// Accept the specific error message
	}
}

func TestDownloadMedia_NotFound(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()
	c := &Client{db: db, names: make(map[string]string), mediaDir: dir}

	_, err := c.DownloadMedia(nil, "nonexistent", "chat@s.whatsapp.net")
	if err == nil {
		t.Fatal("expected error for missing message")
	}
}

func TestDownloadMedia_NoMedia(t *testing.T) {
	db := setupTestDB(t)
	dir := t.TempDir()
	c := &Client{db: db, names: make(map[string]string), mediaDir: dir}

	c.storeMessage(StoredMessage{
		ID: "txt1", Chat: "chat1@s.whatsapp.net", Text: "plain text",
		Timestamp: time.Unix(1000, 0),
	})

	_, err := c.DownloadMedia(nil, "txt1", "chat1@s.whatsapp.net")
	if err == nil {
		t.Fatal("expected error for non-media message")
	}
}

// --- helper ---

func assertEqual(t *testing.T, expected, actual string) {
	t.Helper()
	if expected != actual {
		t.Errorf("expected %q, got %q", expected, actual)
	}
}
