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

copy_clipboard() {
  if command -v pbcopy >/dev/null 2>&1; then
    pbcopy
  elif command -v wl-copy >/dev/null 2>&1; then
    wl-copy
  elif command -v xclip >/dev/null 2>&1; then
    xclip -selection clipboard
  elif command -v tmux >/dev/null 2>&1; then
    tmux load-buffer -w -
  else
    cat >/dev/null
  fi
}

BIN="$(resolve_deja || true)"
if [ -z "$BIN" ]; then
  printf 'deja binary not found in PATH.\nInstall: brew install deja-vu && deja install --auto\n'
  read -r -n 1 -s -p "Press any key to close..." || true
  exit 1
fi

format_rows() {
  if command -v python3 >/dev/null 2>&1; then
    python3 -c '
import json, sys
raw = sys.stdin.read().strip()
if not raw:
    sys.exit(0)
data = json.loads(raw)
items = data if isinstance(data, list) else (data.get("hits") or data.get("sessions") or [])
for entry in items:
    s = entry.get("session", entry) if isinstance(entry, dict) else {}
    sid = str(s.get("id") or "")
    if not sid:
        continue
    harness = str(s.get("harness") or "agent")
    proj = str(s.get("project") or "-")
    upd = str(s.get("updated") or "")[:16].replace("T", " ")
    title = " ".join(str(s.get("title") or s.get("snippet") or sid[:12]).split())[:90]
    print(f"{sid}\t{harness}\t[{harness}] {sid[:8]} · {proj} [{upd}] — {title}")
' 2>/dev/null || true
  elif command -v jq >/dev/null 2>&1; then
    jq -r '(.hits // .sessions // .)[] | (.session // .) | select(.id != null and .id != "") | "\(.id)\t\(.harness // "agent")\t[\(.harness // "agent")] \(.id[0:8]) · \(.project // "-") [\((.updated // "")[0:16] | sub("T"; " "))] — \((.title // .snippet // .id[0:12]) | gsub("[\\r\\n\\t]+"; " "))"' 2>/dev/null || true
  fi
}

case "${1:-}" in
  --list)
    q="${2:-}"
    if [ -z "$q" ]; then
      "$BIN" last 50 --json 2>/dev/null | format_rows
    else
      "$BIN" search --limit 50 --json -- "$q" 2>/dev/null | format_rows
    fi
    exit 0
    ;;
  --handoff)
    sid="${2:-}"
    [ -n "$sid" ] || exit 0
    "$BIN" handoff "$sid" 2>/dev/null | copy_clipboard
    if [ -n "${TMUX:-}" ]; then
      tmux display-message "deja: copied handoff digest for ${sid:0:8} to clipboard" || true
    fi
    exit 0
    ;;
  --yank)
    sid="${2:-}"
    [ -n "$sid" ] || exit 0
    printf '%s' "$sid" | copy_clipboard
    if [ -n "${TMUX:-}" ]; then
      tmux display-message "deja: copied session ID ${sid:0:8}" || true
    fi
    exit 0
    ;;
esac

if ! command -v fzf >/dev/null 2>&1; then
  "$BIN" last 30 | "${PAGER:-less -R}"
  exit 0
fi

SELF="$0"
OUT="$(
  "$SELF" --list "" | fzf --ansi --disabled \
    --delimiter=$'\t' --with-nth=3.. \
    --prompt="deja recall> " \
    --header="Type to search 43 agents · Enter: view · Ctrl-R: resume in split · Ctrl-H: copy handoff · Ctrl-Y: yank ID" \
    --bind "change:reload:$SELF --list {q} || true" \
    --bind "ctrl-h:execute-silent($SELF --handoff {1})" \
    --bind "ctrl-y:execute-silent($SELF --yank {1})" \
    --preview "'$BIN' show {1} --harness {2} --limit 60 2>/dev/null" \
    --preview-window="right:55%:wrap" \
    --expect="ctrl-r"
)" || exit 0

KEY="$(printf '%s\n' "$OUT" | sed -n '1p')"
ROW="$(printf '%s\n' "$OUT" | sed -n '2p')"
[ -n "$ROW" ] || exit 0

SID="$(printf '%s' "$ROW" | cut -f1)"
HARNESS="$(printf '%s' "$ROW" | cut -f2)"

if [ "$KEY" = "ctrl-r" ]; then
  RESUME_CMD="$("$BIN" resume "$SID" --harness "$HARNESS" 2>/dev/null | grep -v '^#' | sed '/^[[:space:]]*$/d' | head -n 1 || true)"
  if [ -n "$RESUME_CMD" ] && [ -n "${TMUX:-}" ]; then
    tmux split-window -v -c "#{pane_current_path}" "$RESUME_CMD"
  fi
else
  "$BIN" show "$SID" --harness "$HARNESS" | "${PAGER:-less -R}"
fi
