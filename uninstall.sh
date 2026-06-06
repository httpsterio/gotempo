#!/usr/bin/env bash
# Remove a user-level gotempo install.
set -e

BIN_DIR="$HOME/.local/bin"
ICON_DIR="$HOME/.local/share/icons/hicolor/512x512/apps"
APP_DIR="$HOME/.local/share/applications"

rm -f "$BIN_DIR/gotempo"
rm -f "$ICON_DIR/gotempo.png"
rm -f "$APP_DIR/gotempo.desktop"

update-desktop-database "$APP_DIR" >/dev/null 2>&1 || true
gtk-update-icon-cache -f -t "$HOME/.local/share/icons/hicolor" >/dev/null 2>&1 || true

echo "gotempo uninstalled"
