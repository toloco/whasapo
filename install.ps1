# Whasapo installer for Windows
# Usage:
#   irm https://raw.githubusercontent.com/toloco/whasapo/main/install.ps1 | iex
#   .\install.ps1
#   .\install.ps1 -Uninstall

param(
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"
$Repo = "toloco/whasapo"
$InstallDir = "$env:LOCALAPPDATA\whasapo"
$Binary = "$InstallDir\whasapo.exe"
$ConfigFile = "$env:APPDATA\Claude\claude_desktop_config.json"

# --- Uninstall ---

if ($Uninstall) {
    Write-Host "=== Uninstalling Whasapo ===" -ForegroundColor Cyan
    Write-Host ""

    if (Test-Path $InstallDir) {
        if (Test-Path "$InstallDir\session.db") {
            Write-Host "Backing up session..." -ForegroundColor Yellow
            Copy-Item "$InstallDir\session.db" "$env:USERPROFILE\.whasapo.session.db.bak"
        }
        Remove-Item -Recurse -Force $InstallDir
        Write-Host "Removed $InstallDir" -ForegroundColor Green
    }

    # Remove from PATH
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($UserPath -like "*$InstallDir*") {
        $NewPath = ($UserPath.Split(';') | Where-Object { $_ -ne $InstallDir }) -join ';'
        [Environment]::SetEnvironmentVariable("Path", $NewPath, "User")
        Write-Host "Removed from PATH"
    }

    # Remove from Claude config
    if (Test-Path $ConfigFile) {
        try {
            $config = Get-Content $ConfigFile -Raw | ConvertFrom-Json
            if ($config.mcpServers.PSObject.Properties.Name -contains "whatsapp") {
                $config.mcpServers.PSObject.Properties.Remove("whatsapp")
                $config | ConvertTo-Json -Depth 10 | Set-Content $ConfigFile
                Write-Host "Removed WhatsApp from Claude config."
            }
        } catch {
            Write-Warning "Could not update Claude config: $_"
        }
    }

    Write-Host ""
    Write-Host "Uninstalled. Restart Claude desktop for changes to take effect." -ForegroundColor Green
    exit 0
}

# --- Install ---

Write-Host "=== Whasapo Installer ===" -ForegroundColor Cyan
Write-Host "WhatsApp integration for Claude"
Write-Host "  Platform: Windows (amd64)"
Write-Host ""

# Create install directory
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# Download latest release
Write-Host "Downloading latest release..." -ForegroundColor Cyan

try {
    $release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
    $asset = $release.assets | Where-Object { $_.name -like "*windows-amd64*" } | Select-Object -First 1

    if (-not $asset) {
        Write-Error "Could not find Windows release. Check https://github.com/$Repo/releases"
        exit 1
    }

    $tempFile = "$env:TEMP\whasapo-download.zip"
    Write-Host "  Downloading from: $($asset.browser_download_url)"
    Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $tempFile

    Write-Host "  Extracting..."
    Expand-Archive -Path $tempFile -DestinationPath "$env:TEMP\whasapo-extract" -Force

    if (Test-Path "$env:TEMP\whasapo-extract\whasapo.exe") {
        Copy-Item "$env:TEMP\whasapo-extract\whasapo.exe" $Binary -Force
    } else {
        Write-Error "Archive doesn't contain whasapo.exe"
        exit 1
    }

    Remove-Item $tempFile -Force -ErrorAction SilentlyContinue
    Remove-Item "$env:TEMP\whasapo-extract" -Recurse -Force -ErrorAction SilentlyContinue
} catch {
    Write-Error "Download failed: $_"
    exit 1
}

Write-Host "Binary installed to $Binary" -ForegroundColor Green

# Add to PATH
$UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$InstallDir;$UserPath", "User")
    $env:Path = "$InstallDir;$env:Path"
    Write-Host "  Added to PATH"
}
Write-Host ""

# --- Configure Claude desktop ---

Write-Host "Configuring Claude desktop app..." -ForegroundColor Cyan

$ConfigDir = Split-Path $ConfigFile
New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null

if (Test-Path $ConfigFile) {
    Copy-Item $ConfigFile "$ConfigFile.backup"
    Write-Host "  Backed up config"
}

try {
    $config = if (Test-Path $ConfigFile) {
        Get-Content $ConfigFile -Raw | ConvertFrom-Json
    } else {
        [PSCustomObject]@{}
    }

    if (-not $config.PSObject.Properties.Name -contains "mcpServers") {
        $config | Add-Member -NotePropertyName "mcpServers" -NotePropertyValue ([PSCustomObject]@{})
    }

    $whatsappConfig = [PSCustomObject]@{
        command = $Binary
        args = @("serve")
    }

    if ($config.mcpServers.PSObject.Properties.Name -contains "whatsapp") {
        $config.mcpServers.whatsapp = $whatsappConfig
    } else {
        $config.mcpServers | Add-Member -NotePropertyName "whatsapp" -NotePropertyValue $whatsappConfig
    }

    $config | ConvertTo-Json -Depth 10 | Set-Content $ConfigFile
    Write-Host "  Added whatsapp MCP server to Claude config."
} catch {
    Write-Warning "Failed to update Claude config: $_"
    Write-Host ""
    Write-Host "  Add manually to: $ConfigFile"
    Write-Host '  "whatsapp": {"command": "' + $Binary + '", "args": ["serve"]}'
}

Write-Host "Claude desktop configured" -ForegroundColor Green
Write-Host ""

# --- Pair with WhatsApp ---

if (Test-Path "$InstallDir\session.db") {
    Write-Host "WhatsApp session found (already paired)." -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  To re-pair: whasapo pair"
    Write-Host "  To check:   whasapo status"
} else {
    Write-Host "Linking your WhatsApp account..." -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  Open WhatsApp on your phone > Settings > Linked Devices > Link a Device"
    Write-Host "  Then scan the QR code below:"
    Write-Host ""
    & $Binary pair
}

Write-Host ""
Write-Host "=========================================" -ForegroundColor Green
Write-Host "  Whasapo installed successfully!" -ForegroundColor Green
Write-Host "=========================================" -ForegroundColor Green
Write-Host ""
Write-Host '  Restart the Claude desktop app, then try asking:'
Write-Host ""
Write-Host '    "Show me my recent WhatsApp messages"'
Write-Host '    "Send a WhatsApp message to Mom saying hi"'
Write-Host ""
Write-Host "  Commands:"
Write-Host "    whasapo status      Check connection"
Write-Host "    whasapo pair        Re-link WhatsApp"
Write-Host "    whasapo --help      All commands"
Write-Host ""
