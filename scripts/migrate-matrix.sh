#!/bin/sh
# What happens to a store built by an older deja when this
# build reads it. Downloads the published binaries, builds a small store with
# each, deletes one transcript the way a client's cleanup does, then upgrades.
#
# Run nightly with --check, and by hand without it. It exists because the suite
# could not answer "does an upgraded machine keep what it had": every test
# builds its store with the binary under test, so a format change is only ever
# read by the build that wrote it (#3505). Not on pull requests: it downloads
# release archives and takes minutes.
#
# Requires: gh, tar, python3, and a built deja (or pass one as $1).
# Usage: sh scripts/migrate-matrix.sh [--check] [path-to-deja]
set -eu

# --check turns the report into a gate: the newest release has to keep the
# session whose transcript is gone, and no row may lose one silently. Nightly
# runs it that way; by hand it prints the table and says nothing.
check=0
if [ "${1:-}" = "--check" ]; then
	check=1
	shift
fi

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
	# One directory per release and the file called deja, because an install
	# writes the path it was run from into the config: named deja-v0.19.4, the
	# check that reads a hook for a binary that is gone skips it, and the
	# dead-hook column read healthy for every release that writes a path.
	bin="$cache/$v/deja"
	if [ -x "$bin" ]; then
		echo "$bin"
		return
	fi
	mkdir -p "$cache/$v"
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

rows=$(mktemp)
# wiring_state is what this build says about the claude-code auto-recall entry:
# wired, stale, missing, or dead when the entry names a binary that is gone. The
# suite has a test for the wiring deja writes today and none for the one a user
# already has, which is how every hook being dead after an upgrade reported as
# healthy (#3502, #3505).
wiring_state() { # binary
	"$1" doctor --json --offline 2>/dev/null | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print("unreadable"); raise SystemExit
for row in d.get("auto_recall", []):
    if row.get("name") != "claude-code":
        continue
    print("dead" if row.get("binary_missing") else row.get("state", "?"))
    raise SystemExit
print("absent")
'
}

printf '%-9s %-12s %-13s %-26s %-19s %-11s %s\n' version built-with after-upgrade upgrade-said deleted-transcript old-wiring dead-hook
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
	# What the old build itself kept of it. A release from before the keep-back
	# (#2970) drops the session on its own pass, so there is nothing for the
	# upgrade to carry and nothing for it to report.
	held=$(exact "$old" alpha2)
	# The config shape this release wrote, read by the build under test. The
	# -auto target is the one that writes hooks; the plain one writes the MCP
	# entry, which is a different file and a different row.
	"$old" install claude-auto --no-index >/dev/null 2>&1
	said=$("$new" index 2>&1 | grep -oE "re-reading your sources|cannot read those records|index is up to date" | paste -sd+ -)
	after=$(exact "$new" alpha3)
	kept=$(exact "$new" alpha2)
	wiring=$(wiring_state "$new")
	# And the same config once what its entries name is gone — a versioned
	# install directory an upgrade replaced. Silence here is the bug: every
	# hook exits 127 and nothing but this row says so (#3502).
	#
	# Both files, because which one the entries name depends on the release:
	# before #3422 an install wrote the binary's own path, after it the
	# launcher. PATH is emptied for the read so a deja installed on the machine
	# running this cannot answer for the one that was taken away.
	launcher=$XDG_CONFIG_HOME/deja/bin/deja-hook
	mv "$old" "$old.gone"
	if [ -e "$launcher" ]; then
		mv "$launcher" "$launcher.gone"
	fi
	dead=$(PATH=/usr/bin:/bin DEJA_BIN='' wiring_state "$new")
	mv "$old.gone" "$old"
	if [ -e "$launcher.gone" ]; then
		mv "$launcher.gone" "$launcher"
	fi
	printf '%-9s %-12s %-13s %-26s %-19s %-11s %s\n' "$v" "$built" "$after" "${said:-(nothing)}" "$kept" "$wiring" "$dead"
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$v" "$after" "${said:-(nothing)}" "$kept" "$held" "$wiring" "$dead" >>"$rows"
	rm -rf "$home"
done

cat <<'NOTE'

built-with and after-upgrade are the same session read before and after: a store
this build cannot read at all shows "none" in the second column. The last column
is the session whose transcript was deleted before the upgrade — what a machine
keeps of work the client has already thrown away.
NOTE

[ "$check" -eq 1 ] || exit 0

# The gate. Two claims, both of them about what an upgrade costs a machine that
# has been running deja for a while:
#
#   every row     — the store still answers after the upgrade, and a session
#                   whose transcript is gone is either carried or named as lost
#   the last row  — the newest release, whose records this build can decode,
#                   carries it (#3529)
#
# Rows above that are allowed to lose it: a store older than the record-layout
# bump holds bytes this build cannot decode, and there is nowhere else for a
# deleted transcript to come from. What is not allowed is losing it quietly.
python3 - "$rows" <<'GATE'
import sys

rows = [line.rstrip("\n").split("\t") for line in open(sys.argv[1]) if line.strip()]
if not rows:
    print("migrate-matrix: no releases were read, so nothing was checked", file=sys.stderr)
    raise SystemExit(1)

bad = []
for version, after, said, kept, held, wiring, dead in rows:
    # An older config shape has to read as wiring, not as nothing: a shape this
    # build cannot parse reports the same way an uninstalled machine does, and
    # the reader is then told to install what they already have.
    if wiring in ("missing", "absent", "unreadable", "?"):
        bad.append(f"{version}: the wiring this release wrote reads as {wiring!r} to this build")
    # And once the binary that entry names is gone, it has to say so. Every
    # hook exits 127 in that state and nothing else reports it (#3502).
    if dead != "dead":
        bad.append(f"{version}: the entry named a binary that is gone and this build called it {dead!r}")
    if after in ("none", "-", ""):
        bad.append(f"{version}: the store stopped answering after the upgrade")
    lost = kept in ("none", "-", "")
    hadIt = held not in ("none", "-", "")
    if lost and hadIt and "cannot read those records" not in said:
        bad.append(f"{version}: the old build still held the session whose transcript was deleted, the upgrade lost it, and the run said nothing ({said})")

newest = rows[-1]
if newest[4] not in ("none", "-", "") and newest[3] in ("none", "-", ""):
    bad.append(f"{newest[0]} is the newest release and its deleted-transcript session did not survive the upgrade")

for line in bad:
    print("migrate-matrix:", line, file=sys.stderr)
raise SystemExit(1 if bad else 0)
GATE
