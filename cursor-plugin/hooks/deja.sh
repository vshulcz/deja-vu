#!/bin/sh
# Bridges Cursor's hooks to the deja binary.
#
# The plugin can be installed before deja itself is, so a missing binary must
# not look like a broken hook: the bridge prints nothing and exits 0. The
# README says how to install deja.
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

# `deja install cursor-auto` writes the same hooks into Cursor's hooks.json,
# and Cursor runs both, so the plugin stands down there. The installer writes
# the launcher (`.../deja-hook hook-context`), so match the subcommand rather
# than a bare `deja hook-`.
HOOKS="${CURSOR_CONFIG_DIR:-$HOME/.cursor}/hooks.json"
if [ -f "$HOOKS" ] && grep -qE 'deja[^"]*hook-(context|prompt|precompact|tool)' "$HOOKS" 2>/dev/null; then
	cat >/dev/null 2>&1 || true
	exit 0
fi

DEJA=$(find_deja)

if [ -z "${DEJA:-}" ]; then
	# Drain stdin so Cursor never blocks on the pipe.
	cat >/dev/null 2>&1 || true
	exit 0
fi

exec "$DEJA" "$@"
