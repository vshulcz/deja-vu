#!/bin/sh
# Draw the site's stats card from the synthetic history rather than by hand.
#
# The committed one was hand-made and had drifted a whole redesign behind the
# card the tool actually draws — the same fault the SVG assets had before
# scripts/genlogo. scripts/demo already builds a believable fake home for
# recording the demo, so the card can come out of the real code path with none
# of anyone's real history in it.
#
#   go build -o /tmp/deja ./cmd/deja
#   go run ./scripts/demo -out /tmp/demo-home
#   sh scripts/gencarddemo.sh /tmp/deja /tmp/demo-home docs/assets/stats-card-demo.svg
set -e

BIN=${1:?usage: gencarddemo.sh <deja binary> <synthetic home> <output.svg>}
HOME_DIR=${2:?usage: gencarddemo.sh <deja binary> <synthetic home> <output.svg>}
OUT=${3:?usage: gencarddemo.sh <deja binary> <synthetic home> <output.svg>}

# XDG_CACHE_HOME as well as HOME: the index would otherwise land in the real
# cache and this would report on whoever ran it.
HOME="$HOME_DIR" XDG_CACHE_HOME="$HOME_DIR/.cache" "$BIN" index --rebuild >/dev/null 2>&1
HOME="$HOME_DIR" XDG_CACHE_HOME="$HOME_DIR/.cache" "$BIN" stats --card "$OUT" >/dev/null

echo "wrote $OUT"
