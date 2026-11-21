#!/usr/bin/env bash
# Build and install XGIF. Safe to re-run for updates: config, database and cookies are preserved.
set -euo pipefail

CONF_DIR=/etc/xgif
BIN=/usr/local/bin/xgif
UNIT=/etc/systemd/system/xgif.service
SVC_USER=xgif
SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log()  { printf '==> %s\n' "$*"; }
warn() { printf 'WARNING: %s\n' "$*" >&2; }
fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || fail "run as root: sudo bash setup.sh"
export PATH="$PATH:/usr/local/go/bin"
command -v go >/dev/null || fail "Go is not installed (see https://go.dev/dl/)"
command -v systemctl >/dev/null || fail "systemd is required"
YTDLP="$(command -v yt-dlp)" || fail "yt-dlp is not installed or not in PATH"
case "$YTDLP" in
  /usr/*|/bin/*|/opt/*) ;;
  *) warn "yt-dlp is at $YTDLP; the service may not find it. Install it under /usr/local/bin." ;;
esac

if [ -x "$BIN" ]; then MODE=update; else MODE=install; fi
log "Mode: $MODE"

# Build into a temp dir first so a failed build never replaces a working binary.
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT
log "Building"
(
  cd "$SRC_DIR"
  [ -f go.sum ] || go mod tidy
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$BUILD_DIR/xgif" .
) || fail "build failed; the installed binary was left untouched"

id "$SVC_USER" >/dev/null 2>&1 || {
  log "Creating system user $SVC_USER"
  useradd --system --user-group --no-create-home --shell /usr/sbin/nologin "$SVC_USER"
}

install -d -o "$SVC_USER" -g "$SVC_USER" -m 0750 "$CONF_DIR"

NEW_CONFIG=0
if [ ! -e "$CONF_DIR/config.yaml" ]; then
  log "Creating default config"
  install -m 0640 -o root -g "$SVC_USER" "$SRC_DIR/config.example.yaml" "$CONF_DIR/config.yaml"
  NEW_CONFIG=1
fi

# An empty file is a valid SQLite database; the bot creates its tables on start.
[ -e "$CONF_DIR/db" ] || : > "$CONF_DIR/db"
chown "$SVC_USER:$SVC_USER" "$CONF_DIR/db"
chmod 0600 "$CONF_DIR/db"

if [ -e "$CONF_DIR/cookies.txt" ]; then
  chown "$SVC_USER:$SVC_USER" "$CONF_DIR/cookies.txt"
  chmod 0600 "$CONF_DIR/cookies.txt"
fi

WAS_RUNNING=0
if systemctl is-active --quiet xgif; then WAS_RUNNING=1; fi

log "Installing binary to $BIN"
install -m 0755 "$BUILD_DIR/xgif" "$BIN.new"
mv -f "$BIN.new" "$BIN"

log "Installing systemd unit"
install -m 0644 "$SRC_DIR/xgif.service" "$UNIT"
systemctl daemon-reload
systemctl enable xgif >/dev/null 2>&1

if [ "$WAS_RUNNING" -eq 1 ]; then
  log "Restarting service"
  systemctl restart xgif
  systemctl --no-pager --lines=0 status xgif || true
elif [ "$NEW_CONFIG" -eq 1 ] || grep -q 'TELEGRAM_BOT_TOKEN' "$CONF_DIR/config.yaml"; then
  warn "Edit $CONF_DIR/config.yaml, then run: systemctl start xgif"
else
  log "Done. Start the service with: systemctl start xgif"
  exit 0
fi
log "Done. Logs: journalctl -u xgif -f"
