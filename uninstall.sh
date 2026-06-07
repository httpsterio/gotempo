#!/usr/bin/env bash
# Remove a user-level gotempo install.
set -e

BIN_DIR="$HOME/.local/bin"
ICON_DIR="$HOME/.local/share/icons/hicolor/512x512/apps"
APP_DIR="$HOME/.local/share/applications"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/gotempo"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/gotempo"

rm -f "$BIN_DIR/gotempo"
rm -f "$ICON_DIR/gotempo.png"
rm -f "$APP_DIR/gotempo.desktop"
rm -rf "$CONFIG_DIR"
rm -rf "$DATA_DIR"

update-desktop-database "$APP_DIR" >/dev/null 2>&1 || true
gtk-update-icon-cache -f -t "$HOME/.local/share/icons/hicolor" >/dev/null 2>&1 || true

echo "gotempo uninstalled"
echo "Removed config ($CONFIG_DIR) and logs ($DATA_DIR)."
