#!/bin/bash
set -euo pipefail

# Whasapo installer — macOS and Linux
# Usage:
#   curl -sSL https://raw.githubusercontent.com/toloco/whasapo/main/install.sh | bash
#   ./install.sh                  (local install)
#   ./install.sh --uninstall      (remove whasapo)

REPO="toloco/whasapo"
INSTALL_DIR="$HOME/.whasapo"
BINARY="$INSTALL_DIR/whasapo"

# Detect OS and architecture
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Darwin) PLATFORM="macos" ;;
    Linux)  PLATFORM="linux" ;;
    *)      echo "Error: Unsupported OS: $OS. Use install.ps1 for Windows." >&2; exit 1 ;;
esac

case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *)             echo "Error: Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Claude Desktop config path
case "$OS" in
    Darwin) CONFIG_FILE="$HOME/Library/Application Support/Claude/claude_desktop_config.json" ;;
    Linux)
        CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}"
        CONFIG_FILE="$CONFIG_DIR/Claude/claude_desktop_config.json"
        ;;
esac

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

    # Remove symlinks
    for dir in /usr/local/bin "$HOME/.local/bin"; do
        if [ -L "$dir/whasapo" ]; then
            rm -f "$dir/whasapo"
            echo "Removed $dir/whasapo"
        fi
    done

    if [ -d "$INSTALL_DIR" ]; then
        if [ -f "$INSTALL_DIR/session.db" ]; then
            warn "Backing up session to $HOME/.whasapo.session.db.bak"
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
info "WhatsApp integration for Claude"
echo "  Platform: $PLATFORM ($ARCH)"
echo ""

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

# When piped from curl, always download from GitHub.
IS_PIPED=0
if [ ! -t 0 ] && [ -z "${BASH_SOURCE[0]:-}" ]; then
    IS_PIPED=1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo "")"

LOCAL_BIN=""
if [ "$IS_PIPED" = "0" ]; then
    for dir in "$SCRIPT_DIR" "$SCRIPT_DIR/bin" "$SCRIPT_DIR/dist"; do
        if [ -f "$dir/whasapo" ] 2>/dev/null; then
            LOCAL_BIN="$dir/whasapo"
            break
        fi
    done
fi

if [ -n "$LOCAL_BIN" ]; then
    info "Installing from local binary..."
    cat "$LOCAL_BIN" > "$BINARY"
else
    info "Downloading latest release..."

    # Determine asset pattern based on platform
    if [ "$PLATFORM" = "macos" ]; then
        ASSET_PATTERN="macos"
    else
        ASSET_PATTERN="linux-${ARCH}"
    fi

    DOWNLOAD_URL=$(curl -sSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"browser_download_url"' \
        | grep "$ASSET_PATTERN" \
        | head -1 \
        | cut -d '"' -f 4) || true

    if [ -z "$DOWNLOAD_URL" ]; then
        error "Could not find a $PLATFORM-$ARCH release."
        error "Check https://github.com/$REPO/releases"
        exit 1
    fi

    TEMP_DIR=$(mktemp -d)

    echo "  Downloading from: $DOWNLOAD_URL"
    curl -sSL "$DOWNLOAD_URL" -o "$TEMP_DIR/whasapo-archive"

    echo "  Extracting..."
    if echo "$DOWNLOAD_URL" | grep -q '\.zip$'; then
        unzip -qo "$TEMP_DIR/whasapo-archive" -d "$TEMP_DIR"
    else
        tar xzf "$TEMP_DIR/whasapo-archive" -C "$TEMP_DIR"
    fi

    if [ -f "$TEMP_DIR/whasapo" ]; then
        cat "$TEMP_DIR/whasapo" > "$BINARY"
    else
        error "Archive doesn't contain whasapo binary."
        rm -rf "$TEMP_DIR"
        exit 1
    fi

    rm -rf "$TEMP_DIR"
fi

chmod +x "$BINARY"

# Remove macOS quarantine/provenance
if [ "$OS" = "Darwin" ]; then
    xattr -d com.apple.quarantine "$BINARY" 2>/dev/null || true
    xattr -d com.apple.provenance "$BINARY" 2>/dev/null || true
fi

ok "Binary installed to $BINARY"

# Add to PATH via symlink
LINK_DIR="/usr/local/bin"
if [ -d "$LINK_DIR" ] && [ -w "$LINK_DIR" ]; then
    ln -sf "$BINARY" "$LINK_DIR/whasapo"
    echo "  Linked to $LINK_DIR/whasapo"
else
    LINK_DIR="$HOME/.local/bin"
    mkdir -p "$LINK_DIR"
    ln -sf "$BINARY" "$LINK_DIR/whasapo"
    echo "  Linked to $LINK_DIR/whasapo"
    if ! echo "$PATH" | tr ':' '\n' | grep -q "$LINK_DIR"; then
        warn "  Add this to your shell profile: export PATH=\"$LINK_DIR:\$PATH\""
    fi
fi
echo ""

# --- Configure Claude desktop ---

info "Configuring Claude desktop app..."

CONFIG_DIR_PATH="$(dirname "$CONFIG_FILE")"
mkdir -p "$CONFIG_DIR_PATH"

if [ -f "$CONFIG_FILE" ]; then
    cp "$CONFIG_FILE" "$CONFIG_FILE.backup"
    echo "  Backed up config to claude_desktop_config.json.backup"
fi

python3 -c "
import json, os, sys

config_file = '$CONFIG_FILE'
binary = '$BINARY'

if os.path.exists(config_file):
    with open(config_file, 'r') as f:
        try:
            config = json.load(f)
        except json.JSONDecodeError:
            config = {}
else:
    config = {}

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
    info "Linking your WhatsApp account..."
    echo ""
    echo "  Open WhatsApp on your phone > Settings > Linked Devices > Link a Device"
    echo "  Then scan the QR code below:"
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
echo "    whasapo uninstall"
echo "    # or: curl -sSL https://raw.githubusercontent.com/$REPO/main/install.sh | bash -s -- --uninstall"
echo ""
