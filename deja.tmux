#!/usr/bin/env bash
set -euo pipefail

CURRENT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

tmux_option() {
  local opt="$1"
  local default="$2"
  local val
  val="$(tmux show-option -gqv "$opt" 2>/dev/null || true)"
  if [ -n "$val" ]; then
    printf '%s' "$val"
  else
    printf '%s' "$default"
  fi
}

KEY="$(tmux_option "@deja-key" "D")"
WIDTH="$(tmux_option "@deja-width" "85%")"
HEIGHT="$(tmux_option "@deja-height" "80%")"

if [ "$KEY" != "none" ] && [ "$KEY" != "off" ]; then
  tmux bind-key "$KEY" display-popup -E -w "$WIDTH" -h "$HEIGHT" -d "#{pane_current_path}" "$CURRENT_DIR/scripts/tmux-popup.sh"
fi

INTERPOLATION='#{deja_status}'
COMMAND="#($CURRENT_DIR/scripts/tmux-status.sh)"

update_status() {
  local opt="$1"
  local cur
  cur="$(tmux show-option -gqv "$opt" 2>/dev/null || true)"
  if [[ "$cur" == *"$INTERPOLATION"* ]]; then
    tmux set-option -gq "$opt" "${cur//$INTERPOLATION/$COMMAND}"
  fi
}

update_status "status-right"
update_status "status-left"
