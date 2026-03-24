#!/bin/bash
set -euo pipefail

# Whasapo installer
# Usage:
#   curl -sSL https://raw.githubusercontent.com/toloco/whasapo/main/install.sh | bash
#   ./install.sh                  (local install)
#   ./install.sh --uninstall      (remove whasapo)

REPO="toloco/whasapo"
INSTALL_DIR="$HOME/.whasapo"
BINARY="$INSTALL_DIR/whasapo"
CONFIG_FILE="$HOME/Library/Application Support/Claude/claude_desktop_config.json"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${BOLD}$*${NC}"; }
ok()    { echo -e "${GREEN}$*${NC}"; }
warn()  { echo -e "${YELLOW}$*${NC}"; }
error() { echo -e "${RED}Error: $*${NC}" >&2; }

# --- Uninstall ---

if [ "${1:-}" = "--uninstall" ]; then
    info "=== Uninstalling Whasapo ==="
    echo ""

    if [ -d "$INSTALL_DIR" ]; then
        # Keep session.db backup just in case
        if [ -f "$INSTALL_DIR/session.db" ]; then
            warn "Backing up session to $INSTALL_DIR.session.db.bak"
            cp "$INSTALL_DIR/session.db" "$HOME/.whasapo.session.db.bak"
        fi
        rm -rf "$INSTALL_DIR"
        ok "Removed $INSTALL_DIR"
    fi

    if [ -f "$CONFIG_FILE" ]; then
        python3 -c "
import json, sys
try:
    with open('$CONFIG_FILE', 'r') as f:
        config = json.load(f)
    if 'mcpServers' in config and 'whatsapp' in config['mcpServers']:
        del config['mcpServers']['whatsapp']
        with open('$CONFIG_FILE', 'w') as f:
            json.dump(config, f, indent=2)
        print('Removed WhatsApp from Claude config.')
    else:
        print('WhatsApp not found in Claude config (already clean).')
except Exception as e:
    print(f'Warning: Could not update Claude config: {e}', file=sys.stderr)
"
    fi

    echo ""
    ok "Uninstalled. Restart Claude desktop for changes to take effect."
    exit 0
fi

# --- Install ---

info "=== Whasapo Installer ==="
info "WhatsApp integration for Claude desktop"
echo ""

# Check macOS
if [ "$(uname -s)" != "Darwin" ]; then
    error "Whasapo currently only supports macOS."
    exit 1
fi

# Create install directory
mkdir -p "$INSTALL_DIR"

# Cleanup on failure
cleanup() {
    if [ "${INSTALL_COMPLETE:-}" != "1" ]; then
        error "Installation failed. Cleaning up..."
        rm -f "$BINARY"
    fi
}
trap cleanup EXIT

# --- Download or copy binary ---

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo "")"

# Check if binary exists locally (e.g. from a zip download)
LOCAL_BIN=""
for dir in "$SCRIPT_DIR" "$SCRIPT_DIR/bin" "$SCRIPT_DIR/dist"; do
    if [ -f "$dir/whasapo" ] 2>/dev/null; then
        LOCAL_BIN="$dir/whasapo"
        break
    fi
done

if [ -n "$LOCAL_BIN" ]; then
    info "Installing from local binary..."
    cp "$LOCAL_BIN" "$BINARY"
else
    info "Downloading latest release..."

    # Get latest release URL
    DOWNLOAD_URL=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"browser_download_url"' \
        | grep 'macos' \
        | head -1 \
        | cut -d '"' -f 4) || true

    if [ -z "$DOWNLOAD_URL" ]; then
        error "Could not find a release to download."
        error "Check https://github.com/$REPO/releases"
        exit 1
    fi

    TEMP_DIR=$(mktemp -d)

    echo "  Downloading from: $DOWNLOAD_URL"
    curl -sSL "$DOWNLOAD_URL" -o "$TEMP_DIR/whasapo.zip"

    echo "  Extracting..."
    unzip -qo "$TEMP_DIR/whasapo.zip" -d "$TEMP_DIR"

    if [ -f "$TEMP_DIR/whasapo" ]; then
        cp "$TEMP_DIR/whasapo" "$BINARY"
    else
        error "Downloaded archive doesn't contain whasapo binary."
        rm -rf "$TEMP_DIR"
        exit 1
    fi

    rm -rf "$TEMP_DIR"
fi

chmod +x "$BINARY"

# Remove macOS quarantine (prevents "app can't be opened" error)
xattr -d com.apple.quarantine "$BINARY" 2>/dev/null || true

ok "Binary installed to $BINARY"
echo ""

# --- Configure Claude desktop ---

info "Configuring Claude desktop app..."

CONFIG_DIR="$(dirname "$CONFIG_FILE")"
mkdir -p "$CONFIG_DIR"

if [ -f "$CONFIG_FILE" ]; then
    # Backup existing config
    cp "$CONFIG_FILE" "$CONFIG_FILE.backup"
    echo "  Backed up config to claude_desktop_config.json.backup"
fi

python3 -c "
import json, os, sys

config_file = '$CONFIG_FILE'
binary = '$BINARY'

# Read existing or create new
if os.path.exists(config_file):
    with open(config_file, 'r') as f:
        try:
            config = json.load(f)
        except json.JSONDecodeError:
            config = {}
else:
    config = {}

# Add/update mcpServers
if 'mcpServers' not in config:
    config['mcpServers'] = {}

config['mcpServers']['whatsapp'] = {
    'command': binary,
    'args': ['serve']
}

with open(config_file, 'w') as f:
    json.dump(config, f, indent=2)

print('  Added whatsapp MCP server to Claude config.')
" || {
    error "Failed to update Claude config. You can add it manually:"
    echo ""
    echo "  Edit: $CONFIG_FILE"
    echo '  Add to mcpServers: "whatsapp": {"command": "'$BINARY'", "args": ["serve"]}'
    echo ""
}

ok "Claude desktop configured"
echo ""

# --- Pair with WhatsApp ---

if [ -f "$INSTALL_DIR/session.db" ]; then
    info "WhatsApp session found (already paired)."
    echo ""
    echo "  To re-pair: whasapo pair"
    echo "  To check:   whasapo status"
else
    info "Let's link your WhatsApp account!"
    echo ""
    echo "  1. Open WhatsApp on your phone"
    echo "  2. Go to Settings > Linked Devices"
    echo "  3. Tap 'Link a Device'"
    echo "  4. Scan the QR code that appears below"
    echo ""
    read -p "Press Enter when ready... " < /dev/tty
    echo ""
    "$BINARY" pair
fi

INSTALL_COMPLETE=1
echo ""
echo "========================================="
ok "  Whasapo installed successfully!"
echo "========================================="
echo ""
echo "  Restart the Claude desktop app, then try asking:"
echo ""
echo "    \"Show me my recent WhatsApp messages\""
echo "    \"Send a WhatsApp message to Mom saying hi\""
echo ""
echo "  Commands:"
echo "    whasapo status      Check connection"
echo "    whasapo pair        Re-link WhatsApp"
echo "    whasapo --help      All commands"
echo ""
echo "  Uninstall:"
echo "    $INSTALL_DIR/whasapo uninstall"
echo "    # or: curl -sSL https://raw.githubusercontent.com/$REPO/main/install.sh | bash -s -- --uninstall"
echo ""
