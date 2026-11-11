#!/usr/bin/env bash
set -euo pipefail

resolve_deja() {
  if [ -n "${DEJA_BIN:-}" ] && [ -x "$DEJA_BIN" ]; then
    printf '%s' "$DEJA_BIN"
    return 0
  fi
  if command -v deja >/dev/null 2>&1; then
    command -v deja
    return 0
  fi
  local p
  for p in "$HOME/.config/deja/bin/deja-hook" "$HOME/.local/bin/deja" "/opt/homebrew/bin/deja" "/usr/local/bin/deja" "$HOME/go/bin/deja"; do
    if [ -x "$p" ]; then
      printf '%s' "$p"
      return 0
    fi
  done
  return 1
}

BIN="$(resolve_deja || true)"
[ -n "$BIN" ] || exit 0

RAW="$("$BIN" statusline 2>/dev/null | head -n 1 | sed $'s/\x1b\\[[0-9;]*m//g' || true)"
[ -n "$RAW" ] || exit 0

printf '%s' "$RAW" | awk -F' · ' '{
  if (NF >= 3) print $1 " · " $2 " · " $3;
  else print $0;
}'
