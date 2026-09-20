# Building deja into your own tool

This is the page for somebody shipping an agent toolkit, a harness, a dotfiles
installer or a CI job that wants session memory in it, rather than for somebody
installing deja for themselves.

It exists because of what happened on 5 August 2026: another project —
`pacphi/agentic-kit` — added deja as an optional cross-host session memory, and
that single integration brought more people here in a day than any release has.
Nothing in this repository told them how; they worked it out. This is that
missing page.

## What you are integrating

One static binary. No daemon, no service to run, no runtime to install, and
nothing to configure before the first answer — it reads the session files the
agents on the machine have already written. The index lives in
`~/.cache/deja/index.db` (or `$DEJA_INDEX_DIR`) and building it is idempotent.

Three ways in, in order of how little work they are:

| way | what it gives | cost to you |
|---|---|---|
| `deja install --auto` | wires MCP and session-start recall into every agent found on the machine | one command |
| `deja mcp` | an MCP server on stdio: one tool, six modes | one config entry |
| `deja <command> --json` | search, blame, fix, how, files, wip as JSON | a subprocess |

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
`how`, `remember`. The older per-capability tool names still work, so a client
wired to them keeps working.

Whatever writes the config, **name the server `deja`**. Every manifest we ship
uses that name, so a machine where both your tool and `deja install` have run
collapses to one server rather than listing every tool twice.

## The JSON surfaces

`deja search`, `blame`, `fix`, `how`, `files`, `wip`, `last`, `show`, `stats`,
`log` and `doctor` all take `--json`. The envelope carries `schema_version`, and
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
bounded block on stdout — 1,536 bytes for a prompt, about 4 KB for a tool call.
There is one harness-shaped exception, `deja hook-antigravity`, because
Antigravity fires a single PreInvocation event and nothing else; if your hook
model does not fit the five above, that is the shape to copy.
`docs/compaction.md` covers the compaction half, which is the one with a payoff
nobody expects.

## What is a contract and what is not

**Stable:** the `--json` surfaces under their `schema_version`, the MCP tool
names and their arguments, the exit codes, and the environment variables named
in `deja doctor --json`.

**Not stable, do not read:** `index.db` and everything in it — `manifest.gob`,
`sessions.gob`, `records.bin`, the buckets, the sidecars. The format has moved
forty-odd times and will keep moving; that is why the version lives in the
manifest. If you find yourself wanting to read it, the surface you want is
probably missing and worth an issue.

## Isolating deja in your tests

Your CI must not read the history of whoever runs it, and a test that indexes
the developer's own store is slow and unrepeatable. Point the index and every
store root somewhere empty:

```sh
export DEJA_INDEX_DIR="$tmp/index.db"
export DEJA_CLAUDE_ROOT="$tmp/claude"      # the store you are exercising
# Every other store, pointed at nothing. The names live in the format
# registry, which is published: 45 of them today, and the list grows with
# every harness.
for v in $(curl -fsSL https://vshulcz.github.io/deja-vu/registry/registry.json |
             grep -o 'DEJA_[A-Z0-9_]*' | sort -u); do
  export "$v=$tmp/empty"
done
```

Two things that recipe does not cover, both found by running it:

- **`DEJA_NOTES_FILE`.** deja's own promoted notes are a store too, and it is
  not in the registry because it is not a harness — so a loop over the registry
  leaves it pointing at the developer's own notes. Set it as well.
- The store you point at a directory that holds no database will be reported as
  unreadable — `1 path could not be read` — which is noise rather than a
  failure. Point the database-backed ones at a path inside your temporary
  directory if you want silence.

`deja doctor --json` is the wrong source for the variable names, and it is the
mistake to avoid: its `stores[].paths` are resolved paths, not the variables
that set them. There is no single switch for "read nothing but what I name"
yet, which is #3802.

`fixtures/registry/<harness>/` in this repository holds a small, publishable
transcript for every format deja reads — take one rather than writing your own,
and your test then breaks when the format does.

## If you integrate it

Open an issue or a pull request adding a line to the list in the README. A
toolkit that wires deja for its users is the most useful thing that happens to
this project, and it should be visible from the front page rather than
discovered by reading commits.
