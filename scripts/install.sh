#!/usr/bin/env bash
# Install gotempo for the current user (no sudo). Safe to run from anywhere the
# release tarball was extracted — paths are resolved relative to this script.
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

BIN_DIR="$HOME/.local/bin"
ICON_DIR="$HOME/.local/share/icons/hicolor/512x512/apps"
APP_DIR="$HOME/.local/share/applications"

install -Dm755 "$SCRIPT_DIR/gotempo" "$BIN_DIR/gotempo"
install -Dm644 "$SCRIPT_DIR/assets/logo.png" "$ICON_DIR/gotempo.png"

mkdir -p "$APP_DIR"
cat > "$APP_DIR/gotempo.desktop" <<'EOF'
[Desktop Entry]
Type=Application
Name=gotempo
Comment=Heart-rate monitor tray app
Exec=gotempo
Icon=gotempo
Terminal=false
Categories=Utility;
EOF

update-desktop-database "$APP_DIR" >/dev/null 2>&1 || true
gtk-update-icon-cache -f -t "$HOME/.local/share/icons/hicolor" >/dev/null 2>&1 || true

echo "gotempo installed to $BIN_DIR/gotempo"
echo "Ensure $BIN_DIR is on your PATH, then launch 'gotempo' or find it in your app menu."
