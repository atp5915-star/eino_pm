#!/usr/bin/env bash
set -euo pipefail

LABEL="com.eino.health-assistant"
PLIST="$HOME/Library/LaunchAgents/${LABEL}.plist"

launchctl bootout "gui/$(id -u)" "$PLIST" >/dev/null 2>&1 || true
rm -f "$PLIST"

echo "已停止并移除：$LABEL"
