# Whasapo

WhatsApp integration for the Claude desktop app. Send and read WhatsApp messages through Claude.

## Install

```bash
curl -sSL https://raw.githubusercontent.com/toloco/whasapo/main/install.sh | bash
```

This will:
1. Download the latest binary
2. Configure the Claude desktop app
3. Walk you through linking your WhatsApp account (QR code scan)

After install, **restart the Claude desktop app**.

## What can you do with it?

Ask Claude things like:

- "Show me my recent WhatsApp messages"
- "Send a WhatsApp message to John saying I'll be 10 minutes late"
- "What messages did I get in the family group?"
- "Find my contact named Sarah"

## Commands

```
whasapo pair        Link your WhatsApp account (QR code)
whasapo serve       Start the MCP server (Claude does this automatically)
whasapo status      Check if everything is working
whasapo uninstall   Remove whasapo completely
whasapo version     Print version
```

## Troubleshooting

**"Claude doesn't show WhatsApp tools"**
Restart the Claude desktop app after installing.

**"Can't be opened because Apple cannot check it for malicious software"**
Run this, then try again:
```bash
xattr -d com.apple.quarantine ~/.whasapo/whasapo
```

**"Connection failed" or "not paired"**
Your WhatsApp link may have expired. Re-pair:
```bash
~/.whasapo/whasapo pair
```

**"No messages found"**
Messages are only collected while Claude is running. You won't see old message history — only new messages that arrive after you open Claude.

## Uninstall

```bash
~/.whasapo/whasapo uninstall
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

Whasapo runs as an [MCP server](https://modelcontextprotocol.io/) that Claude launches automatically. It connects to WhatsApp using the [whatsmeow](https://github.com/tulir/whatsmeow) library (the same protocol the official WhatsApp apps use).

Your WhatsApp session is stored locally in `~/.whasapo/session.db`. No data is sent to any third-party server.
