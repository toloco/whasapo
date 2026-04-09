# Changelog

## v0.7.0

- **History sync** — past messages are loaded automatically on connect. No more empty inbox.
- **Send files** — new `send_file` tool for images, videos, documents, and audio.
- **Download media** — new `download_media` tool saves images/videos/docs to disk from messages.
- **Chat details** — new `get_chat` tool shows group info, participants, topic, and message count.
- **Tests** — 37 unit and integration tests covering message parsing, SQLite storage, and version comparison.
- **CI test pipeline** — tests run on every push and PR, and before releases.

## v0.6.0

- **Cross-platform** — now supports macOS, Linux (amd64/arm64), and Windows (amd64).
- **Linux installer** — `install.sh` detects OS and architecture automatically.
- **Windows installer** — new `install.ps1` PowerShell script.
- **Platform-specific paths** — data and Claude config paths follow OS conventions (macOS `~/Library`, Linux `~/.config`, Windows `%APPDATA%`).
- **CI builds all platforms** — releases include macOS universal, Linux amd64/arm64, and Windows amd64 binaries.

## v0.5.0

- **Persistent messages** — messages are stored in SQLite and survive server restarts. You can now see message history across sessions.
- **Disconnection handling** — detects when WhatsApp disconnects or the session expires. Auto-reconnects on transient failures, shows clear error messages when re-pairing is needed.
- **Graceful shutdown** — clean WhatsApp disconnect on SIGINT/SIGTERM.
- **More message types** — location, contact, poll, and list messages are now displayed instead of being silently dropped.
- **Better pairing flow** — auto-syncs contacts and exits automatically. No more "press Ctrl+C" confusion.
- **Fix self-update** — version comparison now uses proper numeric semver (0.10.0 > 0.2.0).
- **Improved search** — uses stdlib string functions, handles Unicode names correctly.
- **README** — setup instructions for Claude Desktop, Claude Code, and OpenClaw.
- **Code review fixes** — message edits are captured, proper error checking on DB reads, connection guards on all tool handlers.

## v0.3.0

- Self-update command (`whasapo update`).
- Background update check on server startup — notifies via stderr when a new version is available.

## v0.2.2

- Symlink `whasapo` to PATH during install so the command works without the full path.

## v0.2.1

- Fix SQLite BUSY errors during initial sync by enabling WAL mode and busy timeout.

## v0.2.0

- Single binary with subcommands: `pair`, `serve`, `status`, `update`, `uninstall`.
- One-line curl installer that downloads the binary, configures Claude Desktop, and walks through QR pairing.
- GitHub Actions CI for automated releases on version tags.
- Pure Go SQLite driver (no CGO required) — universal macOS binary (arm64 + amd64).
