# Building deja into your own tool

This is the page for somebody shipping an agent toolkit, a harness, a dotfiles
installer or a CI job that wants session memory in it, rather than for somebody
installing deja for themselves.

It exists because `pacphi/agentic-kit` added deja as an optional cross-host
session memory on 5 August 2026 and had to work out how on their own — nothing
in this repository told them. This is that missing page.

## What you are integrating

One static binary. No daemon, no service to run, no runtime to install, and
nothing to configure before the first answer — it reads the session files the
agents on the machine have already written. The index lives in
`~/.cache/deja/index.db` (or `$DEJA_INDEX_DIR`) and building it is idempotent.

Three ways in, in order of how little work they are:

| way | what it gives | cost to you |
|---|---|---|
| `deja install --auto` | wires MCP and session-start recall into every agent found on the machine | one command |
| `deja mcp` | an MCP server on stdio: one tool, seven modes | one config entry |
| `deja <command> --json` | search, blame, fix, how, files, wip, friction, secrets, tests, recap as JSON | a subprocess |

## Detecting it, and installing it if missing

```sh
if ! command -v deja >/dev/null 2>&1; then
  curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
fi
deja version            # prints the version this machine has
```

For a mirror-friendly path — China, or an air-gapped npm proxy:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
```

`deja install --auto` wires everything it finds. If your tool owns the agent's
configuration and would rather write one entry at a time, `deja install
<target>` takes named targets — `deja install --help` prints the list, which is
fifty-odd of them — and `--all` is `--auto` without the session-start hook.

## The MCP server

```sh
deja mcp        # stdio
```

One tool, `deja`, with a `mode` argument: `recall`, `context`, `blame`, `fix`,
`how`, `orient`, `remember`. The older per-capability tool names still work, so a client
wired to them keeps working.

Whatever writes the config, **name the server `deja`**. Every manifest we ship
uses that name, so a machine where both your tool and `deja install` have run
collapses to one server rather than listing every tool twice.

## The JSON surfaces

`deja search`, `blame`, `fix`, `how`, `files`, `wip`, `last`, `show`, `stats`,
`log`, `doctor`, `friction`, `secrets`, `tests` and `recap` all take `--json`. The envelope carries `schema_version`, and
`docs/json-output.md` is the contract: within a version, changes are additive.

The smallest useful integration is two lines — what was decided about this file
before your agent edits it:

```sh
deja blame "$path:$line" --json |
  jq -r '.[0].snippets[0] // empty'
```

And the one that earns its place in a wrapper: what this machine ran after the
error your tool just caught.

```sh
deja fix "$stderr_line" --json | jq -r '.[0].command // empty'
```

## The hook contracts

If your harness has hooks, deja is designed to be called from them and to
answer on stdout, which is the channel harnesses feed back to the model.
The commands are `deja hook-context` (session start, once per session),
`deja hook-prompt` (per prompt), `deja hook-tool` (before a Bash or Edit call),
`deja hook-tool-after` (after one failed) and `deja hook-plan` (before a plan is
accepted). Each reads its harness's JSON event on stdin and writes at most a
bounded block on stdout — 1,536 bytes for a prompt, 480 before a tool call and
420 after a failed one; the one-time compaction packet is 4 KB.
There is one harness-shaped exception, `deja hook-antigravity`, because
Antigravity fires a single PreInvocation event and nothing else; if your hook
model does not fit the five above, that is the shape to copy.
`docs/compaction.md` covers the compaction half, which is the one with a payoff
nobody expects.

## What is a contract and what is not

**Stable:** the `--json` surfaces under their `schema_version`, the MCP tool
names and their arguments, the exit codes, and the `DEJA_*` variables documented
in the format registry and on this page.

**Not stable, do not read:** `index.db` and everything in it — `manifest.gob`,
`sessions.gob`, `records.bin`, the buckets, the sidecars. The format has moved
fifty-odd times and will keep moving; that is why the version lives in the
manifest. If you find yourself wanting to read it, the surface you want is
probably missing and worth an issue.

## Isolating deja in your tests

Your CI must not read the history of whoever runs it, and a test that indexes
the developer's own store is slow and unrepeatable. Name the stores you want
and nothing else is read:

```sh
export DEJA_INDEX_DIR="$tmp/index.db"
export DEJA_STORES=claude                  # the store you are exercising
export DEJA_CLAUDE_ROOT="$tmp/claude"      # your fixtures
```

`DEJA_STORES` takes a comma-separated list of store names — the names `deja
sources` prints, with `notes` for deja's own promoted notes. Every other store
resolves to nothing: no path, no `1 path could not be read`, and no row in
`deja doctor`. A name that is not a store is refused before the command runs,
because a typo would otherwise silence everything and read as a machine with no
history.

It is an environment variable rather than a flag because hooks and the MCP
server are started by somebody else's process, and there is no command line of
yours to put a flag on.

The recipe before this was a loop over the 45 `DEJA_*_ROOT`/`_DB` names
in the published registry, and it leaked twice: the notes store is not in that
registry, because it is not a harness, so the loop left it pointing at the
developer's own notes — and `deja doctor --json` is no help either, since its
`stores[].paths` are resolved paths rather than the variables behind them.

`fixtures/registry/<harness>/` in this repository holds a small, publishable
transcript for every format deja reads — take one rather than writing your own,
and your test then breaks when the format does.

## If you integrate it

Open an issue or a pull request adding a line to the list in the README. A
toolkit that wires deja for its users is the most useful thing that happens to
this project, and it should be visible from the front page rather than
discovered by reading commits.
