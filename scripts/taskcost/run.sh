#!/bin/bash
# One run of one arm of the task-cost stand: what finishing a piece of work
# costs an agent on a machine whose history already holds the answer.
#
#   python3 scripts/taskcost/fixture.py /tmp/taskcost
#   scripts/taskcost/run.sh /tmp/taskcost none 1
#   scripts/taskcost/run.sh /tmp/taskcost deja 1
#
# The arm is the memory the agent is given: `none` is the same machine and the
# same history with nothing wired, `deja` is deja wired the way `deja install
# opencode-auto` wires it. Another tool is an arm too — put its opencode config
# in <fixture>/home-<arm> and pass <arm>.
#
# Every run gets its own HOME, its own copy of the repository and its own index,
# so one run never reads another's store — and deja is pinned to a history-only
# home so it cannot index the session it is serving.
set -u
F=$1; arm=$2; n=$3
MODEL=${MODEL:-openai/gpt-5.6-luna}
DEJA=${DEJA:-deja}
PROMPT=${PROMPT:-'The golden test in internal/store never runs here. Find the exact command that runs it and makes it pass, without modifying any file, and show the proof that it passed.'}
R=$F/runs/$arm-$n
rm -rf "$R"; mkdir -p "$R"
cp -R "$F/project" "$R/project"
[ -d "$F/home-$arm" ] && cp -R "$F/home-$arm" "$R/home" || mkdir -p "$R/home"
export DEJA_INDEX_DIR="$R/idx"
export DEJA_CLAUDE_ROOT="$R/hist/.claude/projects"
# deja must not index the session it is serving: the run's own opencode store
# sits under the agent's HOME, and without this the first recall answers with
# the agent's own prompt.
export DEJA_EXCLUDE_HARNESSES=opencode
if [ "$arm" = deja ]; then
  cp -R "$F/history" "$R/hist"
  # The prior sessions worked in this repository, which in a run is the copy.
  for f in "$R"/hist/.claude/projects/*/*.jsonl; do
    sed -i '' "s|$F/project|$R/project|g" "$f" 2>/dev/null || sed -i "s|$F/project|$R/project|g" "$f"
  done
  # And the directory name, which is where the project a session belongs to
  # comes from: left alone, the history reads as another project's and every
  # surface scoped to this one skips it.
  slug=$(printf '%s' "$R/project" | sed 's|/|-|g')
  for d in "$R"/hist/.claude/projects/*/; do
    [ "$(basename "$d")" = "$slug" ] || mv "$d" "$R/hist/.claude/projects/$slug"
  done
  # HOME is the run's own home here too: without it the build reads the real
  # machine's stores and the arm measures a history it was never given.
  ( cd "$R/project" && HOME="$R/home" "$DEJA" index --rebuild >/dev/null 2>&1 )
  # And wait until it answers from that index rather than from a build in
  # progress: a session that starts while the index is still coming up gets the
  # "building" notice in place of the map, which is a different arm.
  for _ in 1 2 3 4 5; do
    w=$( cd "$R/project" && echo '{}' | HOME="$R/home" "$DEJA" hook-context 2>/dev/null )
    case "$w" in *"comes online"*|*"building"*) sleep 2;; *) break;; esac
  done
  # opencode does not pass its environment to an MCP child, so the server is
  # given the same three variables in the config it is started from.
  python3 - "$R/home/.config/opencode/opencode.json" "$R" <<'PYEOF'
import json, os, sys
path, run = sys.argv[1], sys.argv[2]
cfg = json.load(open(path)) if os.path.exists(path) else {}
for name, server in cfg.get("mcp", {}).items():
    if "deja" in name:
        server.setdefault("environment", {}).update({
            "DEJA_INDEX_DIR": f"{run}/idx",
            "DEJA_CLAUDE_ROOT": f"{run}/hist/.claude/projects",
            "DEJA_EXCLUDE_HARNESSES": "opencode",
        })
json.dump(cfg, open(path, "w"), indent=2)
PYEOF
fi
BEFORE=$( cd "$R/project" && find . -type f | sort | xargs shasum 2>/dev/null | shasum | cut -c1-12 )
cd "$R/project" || exit 1
start=$(date +%s)
HOME="$R/home" opencode run -m "$MODEL" "$PROMPT" > "$R/stdout.txt" 2> "$R/stderr.txt" &
pid=$!
( sleep "${TIMEOUT:-420}"; kill -9 $pid 2>/dev/null ) & watchdog=$!
wait $pid; kill $watchdog 2>/dev/null
end=$(date +%s)
D="$R/home/.local/share/opencode/opencode.db"
read -r input output reasoning cread total <<<"$(sqlite3 -separator ' ' "$D" "
 select coalesce(sum(json_extract(data,'\$.tokens.input')),0),
  coalesce(sum(json_extract(data,'\$.tokens.output')),0),
  coalesce(sum(json_extract(data,'\$.tokens.reasoning')),0),
  coalesce(sum(json_extract(data,'\$.tokens.cache.read')),0),
  coalesce(sum(json_extract(data,'\$.tokens.total')),0)
 from message where json_extract(data,'\$.role')='assistant'" 2>/dev/null)"
tools=$(sqlite3 "$D" "select count(*) from part where json_extract(data,'\$.type')='tool'" 2>/dev/null)
AFTER=$( cd "$R/project" && find . -type f | sort | xargs shasum 2>/dev/null | shasum | cut -c1-12 )
dirty=1; [ "$AFTER" = "$BEFORE" ] && dirty=0
ok=0
grep -qE "ok[[:space:]]+example.com/svc/internal/store" "$R/stderr.txt" "$R/stdout.txt" 2>/dev/null && ok=1
echo "$arm,$n,$((end-start)),$tools,$input,$output,$reasoning,$cread,$total,$dirty,$ok"
