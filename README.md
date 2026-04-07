# Whasapo

WhatsApp MCP server. Send and read WhatsApp messages from any AI assistant that supports [MCP](https://modelcontextprotocol.io/) — Claude desktop, Claude Code, OpenClaw, and more.

## Install

```bash
curl -sSL https://raw.githubusercontent.com/toloco/whasapo/main/install.sh | bash
```

This will:
1. Download the latest binary
2. Configure the Claude desktop app
3. Walk you through linking your WhatsApp account (QR code scan)

After install, **restart your AI app**.

## Setup by app

### Claude Desktop

The installer configures this automatically. If you need to do it manually, add to `~/Library/Application Support/Claude/claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "~/.whasapo/whasapo",
      "args": ["serve"]
    }
  }
}
```

Restart Claude Desktop. The WhatsApp tools will be available immediately.

### Claude Code

Add the MCP server to your project or global settings:

```bash
claude mcp add whatsapp ~/.whasapo/whasapo serve
```

Or add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "~/.whasapo/whasapo",
      "args": ["serve"]
    }
  }
}
```

### OpenClaw

In OpenClaw settings, add a new MCP server:

- **Name:** whatsapp
- **Command:** `~/.whasapo/whasapo`
- **Arguments:** `serve`

Or add to your OpenClaw config file:

```json
{
  "mcpServers": {
    "whatsapp": {
      "command": "~/.whasapo/whasapo",
      "args": ["serve"]
    }
  }
}
```

### Any MCP-compatible client

Whasapo is a standard MCP server using stdio transport. Point any MCP client at:

```
command: ~/.whasapo/whasapo
args: serve
```

## What can you do with it?

Ask your AI assistant things like:

- "Show me my recent WhatsApp messages"
- "Send a WhatsApp message to John saying I'll be 10 minutes late"
- "What messages did I get in the family group?"
- "Find my contact named Sarah"
- "Reply to Mom's last message saying thanks"
- "Summarize what I missed in the work group chat"

## Available tools

| Tool | Description |
|------|-------------|
| `send_message` | Send a text message to a contact or group |
| `list_chats` | List recent chats with last message preview |
| `get_messages` | Get messages, optionally filtered by chat |
| `search_contacts` | Search contacts by name or phone number |

## Commands

```
whasapo pair        Link your WhatsApp account (QR code)
whasapo serve       Start the MCP server (your AI app does this automatically)
whasapo status      Check if everything is working
whasapo update      Update to the latest version
whasapo uninstall   Remove whasapo completely
whasapo version     Print version
```

## Troubleshooting

**"Claude doesn't show WhatsApp tools"**
Restart the app after installing.

**"Can't be opened because Apple cannot check it for malicious software"**
Run this, then try again:
```bash
xattr -d com.apple.quarantine ~/.whasapo/whasapo
```

**"Connection failed" or "not paired"**
Your WhatsApp link may have expired. Re-pair:
```bash
whasapo pair
```

**"No messages found"**
Messages from before the first install won't appear. Once installed, messages are stored persistently and survive restarts.

## Uninstall

```bash
whasapo uninstall
```

Or remotely:
```bash
curl -sSL https://raw.githubusercontent.com/toloco/whasapo/main/install.sh | bash -s -- --uninstall
```

## Build from source

Requires Go 1.23+.

```bash
make build          # build for your machine → bin/whasapo
make release        # universal macOS binary → dist/whasapo-VERSION-macos.zip
```

## How it works

Whasapo is an [MCP server](https://modelcontextprotocol.io/) that connects to WhatsApp using the [whatsmeow](https://github.com/tulir/whatsmeow) library — the same protocol the official WhatsApp apps use.

Your WhatsApp session is stored locally in `~/.whasapo/session.db`. Messages are persisted in SQLite so they survive restarts. No data is sent to any third-party server.
