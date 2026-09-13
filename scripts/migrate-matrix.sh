#!/bin/sh
# Manual, on-demand: what happens to a store built by an older deja when this
# build reads it. Downloads the published binaries, builds a small store with
# each, deletes one transcript the way a client's cleanup does, then upgrades.
#
# NOT wired into CI — it downloads release assets and takes minutes. It exists
# because the suite could not answer "does an upgraded machine keep what it
# had": every test builds its store with the binary under test, so a format
# change is only ever read by the build that wrote it (#3505).
#
# Requires: gh, tar, python3, and a built deja (or pass one as $1).
# Usage: sh scripts/migrate-matrix.sh [path-to-deja]
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
new=${1:-}
if [ -z "$new" ]; then
	new=$(mktemp -d)/deja
	(cd "$root" && go build -o "$new" ./cmd/deja)
fi
cache=${DEJA_OLDBIN_CACHE:-$HOME/.cache/deja-oldbins}
mkdir -p "$cache"

# The releases to read from. Older ones are worth keeping in the list even once
# they predate a format change: "this build cannot read that store" is an answer
# too, and the one this script exists to make visible.
versions=${DEJA_MIGRATE_VERSIONS:-"v0.19.0 v0.19.2 v0.19.4 v0.19.5 v0.20.0"}

case $(uname -s) in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) echo "unsupported OS for release assets: $(uname -s)" >&2; exit 2 ;;
esac
case $(uname -m) in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) echo "unsupported arch for release assets: $(uname -m)" >&2; exit 2 ;;
esac

fetch() { # version -> prints a path, or nothing
	v=$1
	bin="$cache/deja-$v"
	if [ -x "$bin" ]; then
		echo "$bin"
		return
	fi
	tmp=$(mktemp -d)
	if (cd "$tmp" && gh release download "$v" --repo vshulcz/deja-vu \
		--pattern "deja-vu_*_${os}_${arch}.tar.gz" >/dev/null 2>&1) &&
		(cd "$tmp" && tar xzf deja-vu_*_"${os}"_"${arch}".tar.gz >/dev/null 2>&1) &&
		[ -f "$tmp/deja" ]; then
		cp "$tmp/deja" "$bin"
		chmod +x "$bin"
		echo "$bin"
	fi
	rm -rf "$tmp"
}

# exact() answers with the session ids that matched on the exact tier. Anything
# else is a close-spelling rescue answering for a neighbour, which reads exactly
# like a session that survived.
exact() { # binary query
	"$1" search "$2" --limit 5 --json 2>/dev/null | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("-")
    raise SystemExit
if (d.get("tier") or "") != "exact":
    print("none")
    raise SystemExit
ids = sorted({(h.get("session") or {}).get("id", "?") for h in (d.get("hits") or [])})
print(",".join(ids) or "none")
'
}

printf '%-9s %-12s %-13s %-26s %s\n' version built-with after-upgrade upgrade-said deleted-transcript
# The list is a space-separated string on purpose, so it can be overridden from
# the environment; splitting it is the point.
# shellcheck disable=SC2086
for v in $versions; do
	old=$(fetch "$v")
	if [ -z "$old" ]; then
		printf '%-9s %s\n' "$v" "(no ${os}_${arch} asset)"
		continue
	fi
	home=$(mktemp -d)
	HOME=$home
	USERPROFILE=$home
	XDG_CONFIG_HOME=$home/config
	NO_COLOR=1
	DEJA_INDEX_DIR=$home/index.db
	DEJA_CLAUDE_ROOT=$home/claude
	DEJA_CODEX_ROOT=$home/absent
	DEJA_OPENCODE_DB=$home/absent.db
	export HOME USERPROFILE XDG_CONFIG_HOME NO_COLOR DEJA_INDEX_DIR DEJA_CLAUDE_ROOT DEJA_CODEX_ROOT DEJA_OPENCODE_DB
	mkdir -p "$home/claude/-work-app"
	i=1
	while [ "$i" -le 5 ]; do
		cat >"$home/claude/-work-app/s$i.jsonl" <<JSON
{"type":"user","sessionId":"s$i","timestamp":"2026-08-0${i}T10:00:00Z","cwd":"/work/app","message":{"role":"user","content":"alpha$i the billing exporter drops every third retry"}}
{"type":"assistant","sessionId":"s$i","timestamp":"2026-08-0${i}T10:05:00Z","cwd":"/work/app","message":{"role":"assistant","content":"alpha$i capped the retries at three, because the backoff counted from zero"}}
JSON
		i=$((i + 1))
	done
	"$old" index >/dev/null 2>&1
	built=$(exact "$old" alpha3)
	# The client's own cleanup: one transcript gone, its store still there.
	rm -f "$home/claude/-work-app/s2.jsonl"
	"$old" index >/dev/null 2>&1
	said=$("$new" index 2>&1 | grep -oE "re-reading your sources|cannot read those records|index is up to date" | paste -sd+ -)
	after=$(exact "$new" alpha3)
	kept=$(exact "$new" alpha2)
	printf '%-9s %-12s %-13s %-26s %s\n' "$v" "$built" "$after" "${said:-(nothing)}" "$kept"
	rm -rf "$home"
done

cat <<'NOTE'

built-with and after-upgrade are the same session read before and after: a store
this build cannot read at all shows "none" in the second column. The last column
is the session whose transcript was deleted before the upgrade — what a machine
keeps of work the client has already thrown away.
NOTE
