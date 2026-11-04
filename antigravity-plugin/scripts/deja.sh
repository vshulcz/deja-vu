#!/bin/sh
# Bridges Antigravity's hooks to the deja binary.
#
# The plugin can be installed before deja itself is, so a missing binary must
# not look like a broken hook: we stay silent and exit 0 so the hook never
# interrupts the user.
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

# `deja install antigravity-auto` writes the same hooks into
# ~/.gemini/config/plugins/deja/hooks.json. If the user has both, the plugin
# must stand down or every invocation gets the digest twice.
HOOKS="$HOME/.gemini/config/plugins/deja/hooks.json"
if [ -f "$HOOKS" ] && grep -qE 'deja.*hook-(antigravity|session-end)' "$HOOKS" 2>/dev/null; then
	cat >/dev/null 2>&1 || true
	exit 0
fi

DEJA=$(find_deja)

if [ -z "${DEJA:-}" ]; then
	# Drain stdin so the caller never blocks on the pipe.
	cat >/dev/null 2>&1 || true
	printf '{}\n'
	exit 0
fi

exec "$DEJA" "$@"
