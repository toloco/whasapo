package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "modernc.org/sqlite"

	"github.com/toloco/whasapo/whatsapp"
)

var version = "dev"

var wa *whatsapp.Client

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "pair":
		cmdPair()
	case "serve":
		cmdServe()
	case "status":
		cmdStatus()
	case "uninstall":
		cmdUninstall()
	case "version", "--version", "-v":
		fmt.Printf("whasapo %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `whasapo — WhatsApp MCP server for Claude

Usage:
  whasapo pair        Link your WhatsApp account (scan QR code)
  whasapo serve       Start the MCP server (used by Claude)
  whasapo status      Check connection status
  whasapo uninstall   Remove whasapo and clean up
  whasapo version     Print version

Environment:
  WHASAPO_DB          Path to session database (default: ~/.whasapo/session.db)
`)
}

// --- pair command ---

func cmdPair() {
	dbPath := getDBPath()
	fmt.Println("=== WhatsApp Pairing ===")
	fmt.Println()

	store.SetOSInfo("Whasapo", [3]uint32{0, 1, 0})

	container, err := sqlstore.New(context.Background(), "sqlite",
		fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath),
		waLog.Stdout("DB", "WARN", true),
	)
	if err != nil {
		fatal("Failed to open database: %v", err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		fatal("Failed to get device: %v", err)
	}

	client := whatsmeow.NewClient(deviceStore, waLog.Stdout("Client", "WARN", true))

	if client.Store.ID != nil {
		fmt.Println("Already paired!")
		fmt.Println("WhatsApp ID:", client.Store.ID.String())
		fmt.Println()
		fmt.Println("To re-pair, run: whasapo uninstall")
		fmt.Println("Then run: whasapo pair")
		return
	}

	qrChan, err := client.GetQRChannel(context.Background())
	if err != nil {
		fatal("Failed to start pairing: %v", err)
	}

	if err := client.Connect(); err != nil {
		fatal("Failed to connect: %v", err)
	}

	fmt.Println("Open WhatsApp on your phone:")
	fmt.Println("  1. Go to Settings > Linked Devices")
	fmt.Println("  2. Tap 'Link a Device'")
	fmt.Println("  3. Scan the QR code below")
	fmt.Println()

	for evt := range qrChan {
		if evt.Event == "code" {
			qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
			fmt.Println()
			fmt.Println("Waiting for scan...")
		} else {
			if evt.Event == "success" {
				fmt.Println()
				fmt.Println("Paired successfully!")
				fmt.Println()
				fmt.Println("Syncing contacts... (wait 10 seconds then press Ctrl+C)")
			} else {
				fmt.Fprintf(os.Stderr, "Pairing failed: %s\n", evt.Event)
				client.Disconnect()
				os.Exit(1)
			}
			break
		}
	}

	// Wait for sync then exit
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	select {
	case <-c:
	case <-time.After(15 * time.Second):
		fmt.Println("Sync complete.")
	}
	client.Disconnect()

	fmt.Println()
	fmt.Println("Done! Restart the Claude desktop app to start using WhatsApp.")
}

// --- serve command ---

func cmdServe() {
	dbPath := getDBPath()

	var err error
	wa, err = whatsapp.NewClient(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "whasapo: failed to start: %v\n", err)
		os.Exit(1)
	}

	if !wa.IsPaired() {
		fmt.Fprintf(os.Stderr, "whasapo: not paired yet — run 'whasapo pair' first\n")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "whasapo: connecting to WhatsApp...\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := wa.Connect(ctx); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "whasapo: connection failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "whasapo: try running 'whasapo pair' to re-link your account\n")
		os.Exit(1)
	}
	cancel()
	fmt.Fprintf(os.Stderr, "whasapo: connected\n")
	defer wa.Disconnect()

	s := server.NewMCPServer(
		"whasapo",
		version,
		server.WithToolCapabilities(false),
	)

	s.AddTool(mcp.NewTool("send_message",
		mcp.WithDescription("Send a WhatsApp text message to a contact or group"),
		mcp.WithString("to",
			mcp.Required(),
			mcp.Description("Recipient JID (e.g. 1234567890@s.whatsapp.net for users, or 120363xxx@g.us for groups). Use list_chats or search_contacts to find JIDs."),
		),
		mcp.WithString("message",
			mcp.Required(),
			mcp.Description("Text message to send"),
		),
	), handleSendMessage)

	s.AddTool(mcp.NewTool("list_chats",
		mcp.WithDescription("List recent WhatsApp chats with last message preview. Returns chats from received messages and joined groups."),
	), handleListChats)

	s.AddTool(mcp.NewTool("get_messages",
		mcp.WithDescription("Get recent messages, optionally filtered by chat. Only includes messages received while the server is running."),
		mcp.WithString("chat",
			mcp.Description("Chat JID to filter by (omit for all chats)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of messages to return (default 50)"),
		),
	), handleGetMessages)

	s.AddTool(mcp.NewTool("search_contacts",
		mcp.WithDescription("Search WhatsApp contacts by name or phone number"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query (name or phone number)"),
		),
	), handleSearchContacts)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "whasapo: server error: %v\n", err)
		os.Exit(1)
	}
}

// --- status command ---

func cmdStatus() {
	dbPath := getDBPath()
	fmt.Printf("Database: %s\n", dbPath)

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("Status: not installed (no database)")
		os.Exit(1)
	}

	client, err := whatsapp.NewClient(dbPath)
	if err != nil {
		fmt.Printf("Status: error (%v)\n", err)
		os.Exit(1)
	}

	if !client.IsPaired() {
		fmt.Println("Status: not paired")
		fmt.Println("Run: whasapo pair")
		os.Exit(1)
	}

	fmt.Println("Status: paired")
	fmt.Println("Testing connection...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Connect(ctx); err != nil {
		fmt.Printf("Connection: failed (%v)\n", err)
		fmt.Println("Try running: whasapo pair")
		client.Disconnect()
		os.Exit(1)
	}
	fmt.Println("Connection: OK")
	client.Disconnect()
}

// --- uninstall command ---

func cmdUninstall() {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".whasapo")
	configFile := filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")

	fmt.Println("=== Uninstall Whasapo ===")
	fmt.Println()

	// Remove session data
	if _, err := os.Stat(dataDir); err == nil {
		fmt.Printf("Removing %s...\n", dataDir)
		os.RemoveAll(dataDir)
		fmt.Println("  Done.")
	} else {
		fmt.Println("No data directory found.")
	}

	// Remove from Claude config
	if data, err := os.ReadFile(configFile); err == nil {
		var config map[string]interface{}
		if json.Unmarshal(data, &config) == nil {
			if servers, ok := config["mcpServers"].(map[string]interface{}); ok {
				if _, exists := servers["whatsapp"]; exists {
					delete(servers, "whatsapp")
					if updated, err := json.MarshalIndent(config, "", "  "); err == nil {
						os.WriteFile(configFile, updated, 0644)
						fmt.Println("Removed WhatsApp from Claude desktop config.")
					}
				}
			}
		}
	}

	fmt.Println()
	fmt.Println("Uninstalled. Restart Claude desktop for changes to take effect.")
}

// --- tool handlers ---

func handleSendMessage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	to, _ := req.RequireString("to")
	msg, _ := req.RequireString("message")
	if to == "" || msg == "" {
		return mcp.NewToolResultError("Both 'to' and 'message' are required"), nil
	}
	if err := wa.SendMessage(ctx, to, msg); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to send: %v", err)), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Message sent to %s", to)), nil
}

func handleListChats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	chats, err := wa.GetChats(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to list chats: %v", err)), nil
	}
	if len(chats) == 0 {
		return mcp.NewToolResultText("No chats found yet. Messages will appear as they are received."), nil
	}
	data, _ := json.MarshalIndent(chats, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleGetMessages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	chat := req.GetString("chat", "")
	limit := int(req.GetFloat("limit", 50))
	if limit <= 0 {
		limit = 50
	}
	msgs := wa.GetMessages(chat, limit)
	if len(msgs) == 0 {
		return mcp.NewToolResultText("No messages yet. Messages are collected while the server is running."), nil
	}
	data, _ := json.MarshalIndent(msgs, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

func handleSearchContacts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, _ := req.RequireString("query")
	if query == "" {
		return mcp.NewToolResultError("'query' is required"), nil
	}
	contacts, err := wa.SearchContacts(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to search: %v", err)), nil
	}
	if len(contacts) == 0 {
		return mcp.NewToolResultText("No contacts found matching: " + query), nil
	}
	data, _ := json.MarshalIndent(contacts, "", "  ")
	return mcp.NewToolResultText(string(data)), nil
}

// --- helpers ---

func getDBPath() string {
	if v := os.Getenv("WHASAPO_DB"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".whasapo")
	os.MkdirAll(dir, 0700)
	return filepath.Join(dir, "session.db")
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
