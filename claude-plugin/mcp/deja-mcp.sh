#!/bin/sh
# Starts the deja MCP server for harnesses that take MCP servers from a plugin.
#
# Unlike the hook bridge this does not stand down when deja is also installed
# locally: clients key MCP servers by name, so the plugin entry and a local one
# collapse into a single "deja" rather than running twice.
set -u

for candidate in \
	"$HOME/.local/bin/deja" \
	"/opt/homebrew/bin/deja" \
	"/usr/local/bin/deja" \
	"$HOME/go/bin/deja"; do
	if [ -x "$candidate" ]; then
		exec "$candidate" mcp
	fi
done

if command -v deja >/dev/null 2>&1; then
	exec deja mcp
fi

# No binary: fail loudly here rather than silently registering a server that
# answers nothing. The client reports it as one server that would not start.
echo "deja binary not found — install it with: brew install vshulcz/tap/deja" >&2
exit 1
