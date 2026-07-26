#!/bin/sh
# Bridges Claude Code's hooks to the deja binary.
#
# The plugin can be installed before deja itself is, so a missing binary must
# not look like a broken hook: we say once, through the hook's own JSON, how
# to get it, and otherwise stay silent. Exit status is always 0 — a hook that
# fails is a hook that interrupts the user.
set -u

find_deja() {
	if command -v deja >/dev/null 2>&1; then
		command -v deja
		return
	fi
	for candidate in \
		"$HOME/.local/bin/deja" \
		"/opt/homebrew/bin/deja" \
		"/usr/local/bin/deja" \
		"$HOME/go/bin/deja"; do
		if [ -x "$candidate" ]; then
			printf '%s' "$candidate"
			return
		fi
	done
}

# `deja install claude-auto` writes the same hooks into settings.json. If the
# user has both, the plugin must stand down or every session gets the digest
# twice.
SETTINGS="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json"
if [ -f "$SETTINGS" ] && grep -q "deja hook-" "$SETTINGS" 2>/dev/null; then
	cat >/dev/null 2>&1 || true
	exit 0
fi

DEJA=$(find_deja)

if [ -z "${DEJA:-}" ]; then
	# Drain stdin so the caller never blocks on the pipe.
	cat >/dev/null 2>&1 || true
	if [ "${1:-}" = "hook-context" ]; then
		printf '{"systemMessage":"deja-vu plugin is installed but the deja binary is not on PATH — install it with: brew install vshulcz/tap/deja  (or: go install github.com/vshulcz/deja-vu/cmd/deja@latest)"}\n'
	fi
	exit 0
fi

exec "$DEJA" "$@"
