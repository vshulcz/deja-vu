# JSON output contract

Several `deja` commands accept `--json` and print machine-readable output for
scripting, dashboards, and editor integrations. Object-shaped responses include
a `schema_version` field so consumers can detect breaking changes.

## Stability policy

- **Within a `schema_version`**, changes are additive only: new optional fields
  may appear, but existing field names, types, and meanings stay the same.
- **Bumping `schema_version`** signals a breaking change (field removal, rename,
  or type change). Consumers should branch on `schema_version` before parsing
  the rest of the envelope.
- The current version is **2** (constant in `internal/jsonout`).
- **`deja blame --json`** and **`deja log --json`** return a top-level JSON
  array rather than an envelope, so neither carries `schema_version`. Their
  element shapes are stable; only additive fields inside them are permitted.
- **`deja stats --impact --json`** returns one flat object of counters and
  carries no `schema_version` either, on the same terms.
- **`deja log --last --json`** carries no `schema_version` for two reasons of
  its own. The object it prints is the record deja stores in
  `.injections.jsonl`, marshalled from the same struct, so a version field on it
  would be written into every line of that file. And the surface answers `null`
  when no digest has been recorded: a shape that is sometimes absent cannot be
  relied on to carry a version.
- **`deja bench recall|context|prompt --json`** are object-shaped and carry no
  `schema_version` either. They report a benchmark run to whoever asked for it,
  not a contract anything downstream parses on a schedule.

### What changed in version 2

`deja search --json` used to return a bare array on the exact path and an object
envelope on every fallback path, so a consumer had to handle two shapes and
could not tell which it had without inspecting the value. It now always returns
the envelope, and the envelope answers the two questions a caller cannot answer
from a list of hits:

- `tier` — which tier answered: `exact`, `close`, `stemmed`, `semantic`,
  `error`, or `relevance`. **`relevance` means nothing matched** and these are
  the nearest sessions deja could find. Counting those as hits overstates
  recall, and this used to be readable only as a sentence on stderr. **`error`
  IS a match** — the query was a pasted error and these sessions hit that exact
  error (matched by signature, not by words); count them as hits.
- `total` and `capped` — how many sessions matched, and whether a cap hid some.
- `policy_withheld` — how many matching sessions this machine's trust policy
  kept out of the answer. Omitted when none were. Present on `search --json`,
  `last --json` and `stats --json`, so an empty result can be told apart from a
  rule.
  Counting the returned hits measures the cap: that figure moves when the
  window's membership changes, whether or not retrieval improved.
  **`capped: false` means the response holds everything that matched, on every
  tier**; when it is `true`, `total` is the figure to read and the hit count is
  not.

`capped` is omitted when false. When it is true, `total` is the count before
policy scoping and reranking ran, because there is no way to know how those
would have treated the sessions the cap removed.

### `hits` is not a fixed window across tiers

The number of hits a tier returns is that tier's own decision, so the same
invocation can return 15 on one tier and 50 on another. Read `total` and
`capped` for coverage; the length of `hits` answers a different question.

- `exact`, `close`, `stemmed` and `semantic` serve the ranked result cap:
  `--limit N` (1–100), 15 by default, and `--all` for no cap. With `--all`,
  `total` equals the hit count.
- `relevance` ranks the candidate pool and serves the top 50. That bound belongs
  to the ranking rather than to output: it is applied during retrieval, and
  `--limit` and `--all` act on the result set downstream of it, so neither moves
  it. A relevance response can therefore come back `capped: true` with `--all`
  passed, and `total` can exceed the 50 hits it returned.

Reporting `total` as the length of that window instead of the pool behind it was
[#497](https://github.com/vshulcz/deja-vu/issues/497): deeper queries all came
back `"total": 50, "capped": false`, which reads as "50 matched, none withheld"
in the one case where a consumer most needs to keep looking.

## `deja search --json`

Every search returns one envelope:

```json
{
  "schema_version": 2,
  "tier": "exact",
  "total": 391,
  "capped": true,
  "hits": [
  {
    "session": {
      "harness": "claude",
      "id": "abc123",
      "project": "myapp",
      "path": "/home/user/.claude/projects/.../session.jsonl",
      "started": "2026-01-02T03:04:05Z",
      "updated": "2026-01-02T03:10:00Z",
      "source": {"origin": "local", "instance": "workstation"},
      "messages": [
        {"role": "user", "text": "why does the parser fail on …", "time": "2026-01-02T03:04:05Z"}
      ]
    },
    "count": 2,
    "snippets": ["matched text …"],
    "score": 1.5,
    "tier": "exact",
    "tier_detail": "",
    "superseded": "2026-07-19"
  }
  ]
}
```

The envelope's `tier` and each hit's `tier` are the same idea at two scopes,
which is why they share a name. The fallback flags stay alongside for readers
written against version 1:

```json
{
  "schema_version": 2,
  "tier": "close",
  "total": 4,
  "hits": [ … ],
  "fuzzy": true
}
```

Stemmed search may also include `variants`; semantic search sets `semantic`.
`superseded` (optional) carries the date of a newer same-project session whose
matches overlap this hit — an earlier-attempt signal. `reused` (optional)
counts recent agent recalls that served this session.

`--limit N` bounds the ranked result set to 1–100 hits, on the tiers that serve
that cap (see [`hits` is not a fixed
window](#hits-is-not-a-fixed-window-across-tiers)). Every machine session has
`source.origin`, either `local` or `imported`. When
`DEJA_SOURCE_INSTANCE` is configured, local sessions also carry that stable
operator-chosen `source.instance`; imported sessions omit it because current
sync batches do not carry trustworthy peer identity.

### The session object

`search`, `last` and `show` all carry the same session object. The examples
above show the fields that are always there; these are the rest, each omitted
when empty:

| Field | Meaning |
|---|---|
| `path` | file the session was read from — the transcript, or the store for the database-backed harnesses; opencode is the exception and gives the project directory it ran in |
| `title` | first user turn, elided to terminal width |
| `agent_title` | `title` came from the assistant because the session has no user turn |
| `touched` | the few files this session worked on most |
| `gave_up` | the session's own text reports something being tried and backed out |
| `words` | length of the whole session in words, as the index counted it; ranking normalises by it |
| `orig_id` | id this session had on the machine it was imported from |
| `from` | machine the session was worked on, for sessions that arrived by sync; absent for local work and for batches written before deja stamped an origin. `deja last --from <machine>` filters by it, `--from local` for this machine's own |
| `lifecycle` | state of an imported promoted note: `accepted`, `rejected`, `superseded` or `stale` |
| `lifecycle_note` | the note left with that state |
| `lifecycle_at` | when that state was set |
| `kind` | the harness's own word for a session an agent spawned — `sidechain` from Claude, `subagent` or `subagent_fork` from Grok, and whatever a harness deja has not met yet calls it; absent for sessions a person started |
| `parent` | the session this one was spawned from, where the harness records the edge itself — deja never infers one |
| `agent` | name of the agent that ran a spawned session |

`messages` is likewise absent rather than empty on `last`, which never returns
turns.

Inside a message, `time` is the turn's own stamp and is absent when the
transcript did not carry one — the zero time reads as a date in the year one
rather than as the absence of a date, and every surface deja prints shows `-`
for it instead.

## `deja last --json`

Recent session metadata uses a versioned envelope and never includes messages:

```json
{
  "schema_version": 2,
  "sessions": [
    {
      "harness": "codex",
      "id": "abc123",
      "project": "myapp",
      "started": "2026-01-02T03:04:05Z",
      "updated": "2026-01-02T03:10:00Z",
      "source": {"origin": "local", "instance": "workstation"}
    }
  ]
}
```

The existing positional count remains the bound, for example
`deja last 20 --json --harness codex`.

## `deja show <exact-id> --harness <name> --json`

Machine reads require the composite harness plus exact native session ID. They
return redacted index content in a bounded message window; the default limit is
50 and the maximum is 200.

```json
{
  "schema_version": 2,
  "session": {
    "harness": "codex",
    "id": "abc123",
    "project": "myapp",
    "started": "2026-01-02T03:04:05Z",
    "updated": "2026-01-02T03:10:00Z",
    "source": {"origin": "local", "instance": "workstation"},
    "messages": [
      {"role": "user", "text": "bounded redacted text", "time": "2026-01-02T03:04:05Z"}
    ]
  },
  "window": {"offset": 0, "limit": 50, "total": 81, "returned": 50}
}
```

Use `--offset N --limit N` to page without parsing human output. An offset past
the end returns an empty `messages` array and a `returned` count of zero.

## `deja stats --json`

```json
{
  "schema_version": 2,
  "total_sessions": 42,
  "total_messages": 318,
  "repeat_questions": 3,
  "spans": 3836,
  "span_files": 862,
  "harnesses": [
    {"harness": "claude", "sessions": 30, "messages": 240}
  ],
  "top_projects": [
    {"project": "myapp", "sessions": 12}
  ],
  "monthly": [
    {"month": "2026-01", "messages": 45}
  ],
  "sparkline": "▁▂▅▇█",
  "date_range": {"start": "2026-01-02", "end": "2026-07-04"},
  "longest_session": {
    "id": "c3",
    "harness": "claude",
    "project": "myapp",
    "title": "Refactor parser",
    "messages": 48
  },
  "busiest_day": {"date": "2026-07-04", "messages": 22},
  "recall": {
    "recalls_served": 10,
    "dejavu_moments": 3,
    "injections": 4,
    "recall_sessions": 8,
    "injected_sessions": 3,
    "bytes": 40960,
    "injected_bytes": 12288,
    "raw_bytes": 524288,
    "empty_result_rate": 0.1,
    "since": "2026-06-20T09:00:00Z"
  },
  "week_recalls": 2,
  "week_bytes": 4096,
  "week_injected": 1,
  "handoffs_received": 0,
  "agent_credits": 1,
  "week_agent_credits": 0,
  "sidecar_size": 12345
}
```

`spans` and `span_files` count the replaced spans `deja restore` can hand back
and the files they belong to. Both are omitted when the index holds none.

Inside `recall`, `raw_bytes` is the size of the source transcripts the served
digests distilled and `since` is the oldest event still in the usage log; both
are omitted when zero, so a store with no recall history yet shows neither.


`week_recalls`, `week_bytes`, `week_injected` and `week_agent_credits` cover
seven calendar days back from now, at the same wall clock — not a fixed 168
hours. Across a clock change that means the week runs an hour longer in autumn
and an hour shorter in spring; and where a spring-forward removed the wall time
the week would have started at, it starts at the time the clock reached instead.
A day, where deja counts one, runs from local midnight. Both are the reader's own
timezone, not UTC.

`week_recalls` and `week_bytes` count what an agent asked for and got, so a
recall that matched nothing is left out. `week_injected` counts what deja pushed
unprompted, including a session start that carried only the environment block —
that injection has no project session in it, which is a different thing from
having served nothing.

deja had two rules for a week until #1921, and the two numbers on the status bar
disagreed for one week in each direction, so the definition is worth stating.

Optional fields are omitted when zero or empty — `sidecar_size`, for one,
appears only after `deja embed` has built a semantic sidecar. The heatmap grid used by
`--card` is intentionally excluded from `--json` output.

## `deja doctor --json`

```json
{
  "schema_version": 2,
  "stores": [
    {
      "name": "claude",
      "state": "ok",
      "paths": ["/home/user/.claude/projects"],
      "files": 12,
      "indexed_sessions": 240
    }
  ],
  "index": {
    "state": "ok",
    "path": "/home/user/.cache/deja/index.db",
    "stale_stores": 0,
    "sessions_stamped_ahead": 0
  },
  "mcp": [
    {
      "name": "claude-code",
      "state": "wired",
      "path": "/home/user/.claude.json"
    }
  ],
  "sqlite3": {"state": "ok"},
  "git": {"state": "ok"},
  "version": {
    "state": "ok",
    "current": "0.14.1",
    "latest": "0.14.1"
  },
  "embed": {
    "state": "reachable",
    "model": "text-embedding-3-small",
    "dim": 1536,
    "coverage": 87.5
  },
  "policy": {
    "state": "active",
    "path": "/home/user/.config/deja/policy.json",
    "indexed_sessions": 240,
    "activations": {
      "search": {"rule": "allow all", "withheld": 0},
      "mcp": {"rule": "allow all", "withheld": 0},
      "auto": {"rule": "deny imported", "withheld": 3}
    }
  },
  "ingest_health": {
    "claude": {"malformed_lines": 0, "failed_files": 0}
  },
  "ingest_files": {
    "/Users/you/.claude/projects/app/one.jsonl": {"malformed": 2}
  },
  "sync": {
    "state": "ok",
    "peers": [
      {
        "host": "laptop",
        "machine": "quicksilver",
        "last_push": "2026-08-22T10:00:00Z",
        "last_pull": "2026-08-22T10:00:00Z",
        "sessions_from_there": 12
      },
      {
        "host": "build-box",
        "last_push": "2026-08-20T10:00:00Z",
        "sessions_from_there": 0,
        "last_error": "ssh build-box: exit status 255"
      }
    ],
    "imported": [
      {"machine": "desktop", "sessions": 5}
    ]
  }
}
```

`embed`, `ingest_health` and `deep` (present only under `--deep`) are omitted
when unavailable; `policy` is always present. `embed.state` is the endpoint's,
`unavailable` or `reachable`. `sidecar` is the file's own state and appears only
when it is `unreadable` — the sidecar is on disk and deja cannot parse it —
with an `error` saying why. A sidecar fault is reported whether or not an
endpoint is configured, so `embed` is present in that case even with no
endpoint. `index.path` points at the index
directory; `index.db` is that directory's name, not a file. `index.state` is
`missing`, `ok`, `stale`, `stale-readonly` (stale where the index cannot be
written, so `deja index` cannot fix it) or `damaged` (records or postings are
gone; the next search rebuilds it). Store `state`
values are `ok`, `missing`, `unreadable`, `parsed-zero`, `denied` (which adds a
`denied` field naming the unreadable path), `needs-sqlite3` and `needs-zstd`
(both of which add a `skipped` field saying which CLI is missing); an existing
but empty store directory reports `missing`. A store also carries `indexed_sessions`
and, when it holds peer-synced work, `indexed_from_elsewhere`; a store whose
permission walk was cut short or blocked carries `partial` or `unchecked`.
`sqlite3` and `git` are the two tools deja shells out to, each `ok` or
`missing`: sqlite3 reads the opencode, Cursor, grok, hermes, goose and zed
stores, and git supplies changed-file notes, worktree names and the task
signal. Both degrade quietly, which is why the report names them.

Version `state` is `ok`, `update-available`, `ahead`, `dev`, `offline` (under
`--offline`), or `unknown`. `policy.state` is `default`, `active` or
`unreadable` (which adds an `error`); `activations` keys are `search`, `mcp` and
`auto`, each with the rule in force and how many sessions it withheld;
`ignored` and `inert` list policy lines that matched no harness or no import.
Per-harness `ingest_health` may also carry `clipped_messages` and `last_error`,
which quotes one of that store's failures — the first failing path in order,
so the same index reports the same error every run. `ingest_files` below has
every one of them.

`ingest_files` is where those counts came from, keyed by file path: `malformed`
lines, `clipped` messages, and `error` when nothing from that path is in the
index at all — it would not open, or it is one document that would not parse,
as a cline or roo task is. It
is sparse — a file with nothing to report is not in it — and absent when no file
has anything to report. The per-harness numbers above are the sum of the files
deja can attribute to a harness, so a path it cannot place is here and in no
harness's total. Line numbers are not recorded: the parsers count refused lines, not where they were.

`sync.state` is `ok` or `unreadable`; `unreadable` adds an `error` saying why the peers file could not be parsed, and its `peers` list is empty because deja could read nothing from it — a sync is never stopped by a malformed config, so the report is the only place that failure shows. `sync.peers` is every machine this one knows, and it is always present — an
empty list means no machines are configured, which a script can tell apart from
a deja too old to report. Each row carries `host`, `sessions_from_there` (how
much of this index arrived from it — matched by the name the machine calls
itself, which deja learns from the records a pull brings, since `host` is
whatever ssh alias was typed), and `last_push` / `last_pull` as RFC 3339
timestamps, each omitted when that direction has never happened: the two fail
apart, and a machine that takes what this one sends while sending nothing back
is a broken sync that reads as a working one. A row carrying neither is a
machine named once and never reached — the text report says "never exchanged"
for it — and it is still a row, which is what distinguishes it from a deja too
old to report peers at all: that one has no `sync` key. `machine` is what that host calls itself, learned from the records a pull
brings; it is the name `sessions_from_there` is counted by and the name every
listing prints for imported work. Absent until the machine has said, and absent
for a peer that has never been reached. `last_error` is why the most
recent exchange failed and is absent once one succeeds. `last_error` is written by
another machine and can be made arbitrarily long, so it is bounded before it is
reported. `host` is not: it is a name to act on — `deja sync ssh <host>` — and a
bounded name names no machine, so it is reported exactly as the config file
spells it, however long. Neither can carry a raw control byte into a terminal:
the JSON encoder escapes those in any string.
`sync.imported` names the machines whose work is in this index without a peer
row of their own — the state a first exchange leaves, when a batch was carried
by hand or by a shared folder and no `deja sync ssh` target has been named yet.
A machine counted in a `peers` row is not repeated here. The key is absent when
nothing has arrived that way, so a reader can tell that from a deja too old to
report it, and `machine` is the name the records carry, reported as they spell
it for the reason `host` is.

`stamped_ahead` appears when the newer of the two timestamps is more than a
minute later than this machine's clock: the age would be negative and the row
would otherwise read as a sync that just happened, so a consumer should treat
that peer's dates as unusable for "how long since" rather than as healthy. The
minute is what makes this different from the rule for a session, where anything
ahead counts: a peers file is written by deja itself, so a copy
from a machine a moment ahead — or an NTP step landing between the write and the
read — is not a clock worth reporting, while a session's stamp comes from a
transcript deja did not write.

## `deja friction --json`

What this machine keeps tripping over, machine-readable:

```json
{
  "schema_version": 2,
  "sessions_read": 506,
  "min_sessions": 3,
  "total": 8,
  "truncated": true,
  "rows": [
    {
      "error": "command not found: shellcheck",
      "sessions": 11,
      "harnesses": ["claude", "codex"],
      "last": "2026-08-30T09:14:02Z"
    }
  ]
}
```

`sessions_read` is the denominator the prose header states — sessions that
recorded tool output, after the trust policy and ignore rules have taken theirs.
`min_sessions` is the threshold a row had to clear: without it a consumer cannot
tell an empty result meaning "nothing recurs here" from one meaning "nothing
recurred *twice*", which are different facts about the same store.

`total` counts the recurring errors found before `--limit` was applied and
`truncated` says whether the cap hid any, on the same reasoning as the version 2
search envelope: reading `rows.length` answers neither question once a limit is
in play.

The empty result is the same shape with an empty `rows` array, never a different
one. The prose path answers "nothing recurring" five ways depending on which
rule emptied the store, and a script cannot branch on prose.

`last` is omitted rather than zero-valued when a row carries no recorded time.

## `deja fix <error> --json`

The commands sessions on this machine ran after that error:

```json
{
  "schema_version": 2,
  "fixes": [
    {
      "error": "command not found: shellcheck",
      "command": "brew install shellcheck",
      "candidate": false,
      "when": "2026-08-29T18:02:44Z"
    }
  ]
}
```

`candidate` is the half-evidence flag the prose renders as *"ran next,
unconfirmed"*: one session ran this after the error and nothing has confirmed it
worked. A caller acting on a fix automatically needs to know which half it is
holding, so it is a field rather than a wording difference.

As with `friction`, an empty result keeps the envelope and returns `fixes: []`.
The prose path distinguishes "held but unconfirmed", "nothing recorded for that
line" and "no session ran a command after that error" in three different
sentences; the JSON says only that there is nothing to act on.

## `deja how <what> --json`

The real command this machine runs for a given tool:

```json
{
  "schema_version": 2,
  "found": 13,
  "truncated": true,
  "withheld": 0,
  "ignored": 0,
  "commands": [
    {
      "command": "go test ./... -race",
      "runs": 41,
      "sessions": 12,
      "last": "2026-08-30T11:20:03Z",
      "failed_every_time": false,
      "outcomes": 3,
      "failures": 0
    }
  ]
}
```

`found` and `truncated` are here because the cap note goes to **stderr**: a caller
reading stdout alone could not otherwise tell eight ways to run the tests from
thirteen.

`failed_every_time` is the prose's failure note as a field. `how` offering a
command that never once worked, in the shape of one that always has, is the
sharpest miss this surface can make, so a machine reader has to be able to see
it too. `outcomes` and `failures` are beside it so a consumer can weigh that
flag rather than trust it blind — only about one run record in a hundred carries
an exit status at all, and `failed_every_time` is false whenever deja knows the
outcome of no run. `exit_code` is present only when every recorded failure
agreed on one.

`withheld` and `ignored` are what the trust policy and the ignore rule took out
before any of this was counted. Without them an empty `commands` means both
"nothing matched" and "everything that matched was hidden" — which is the reason
the count travels with the entries in the first place, and what the prose path
says in a sentence.

`last` is omitted rather than zero-valued when a command carries no recorded
time. An empty result keeps the envelope and returns `commands: []`.

## `deja stats --impact --json`

What deja has actually served on this machine, measured from the usage log:

```json
{
  "recalls": 30,
  "injections": 4,
  "served_bytes": 15000,
  "raw_bytes": 150000,
  "reused_twice": 2,
  "dejavu_moments": 1,
  "tool_lines": 12,
  "since": "2026-07-27T14:33:13Z",
  "credited_aloud": 3
}
```

No envelope and no `schema_version`, for the same reason as `blame` and `log`:
the shape is one flat object of counters, and a consumer reads the keys it knows.
Additive fields are permitted; a rename is a breaking change and would be called
one.

`recalls` counts agent-initiated recalls that returned matches, `injections`
session starts that began with project memory. `served_bytes` is what the
digests actually returned and `raw_bytes` the source transcripts they were
distilled from, so the ratio is how much reading deja saved. Both cover every
door that carried a digest — the session start, the per-prompt recall, the
tool-time line — while `injections` stays the count of session starts. What is
in neither is a session start that carried only the environment block, because
that block is a summary of what the machine keeps hitting rather than a digest
of transcripts. `deja stats` counts its bytes, which is the number for
everything deja handed over. `reused_twice` is
sessions agents recalled two or more times, `dejavu_moments` prompts matched to
prior work, `tool_lines` the PreToolUse injections — one line about the command
or file an agent was about to touch — and `credited_aloud` the recalls an agent
said out loud.

`since` is the oldest event still in the usage log, so no count above covers
more than the period from then to now. On a quiet machine that is every event
there has ever been; once the log grows past 1MB it is rewritten keeping the
last 14 days, and from then on the figures are a window whose start moves. Read
`since` rather than assuming either — it can also be older than 14 days, since a
rewrite that would leave nothing keeps the newest few hundred events instead of
emptying the file. It is absent only when the log holds nothing at all, which is
the one case where the counts are all zero anyway.

## `deja log --json`

What deja actually fed the agents, newest first:

```json
[
  { "t": "2026-08-24T12:00:00Z", "kind": "recall", "bytes": 0, "empty": true },
  { "t": "2026-08-24T11:00:00Z", "kind": "hook", "bytes": 400, "sessions": 1, "raw": 4000 },
  {
    "t": "2026-08-24T10:00:00Z",
    "kind": "recall",
    "bytes": 900,
    "sessions": 2,
    "raw": 9000,
    "ids": ["s1", "s2"]
  }
]
```

A top-level array, so no `schema_version`, on the same terms as `blame`. It is
`[]` and never `null` when there is nothing to report: this is the output a
script polls, and `null` raises where an empty list iterates zero times.

`t` is when it happened and `kind` is what deja did — `recall`, `recall_context`
and `blame` are answers to an agent that asked; `hook`, `dejavu` and `tool` are
memory offered unasked; `resource` is a read of `deja://session/…`; `remember`
writes rather than serves; `search` and `handoff` are the reader's own commands.
A kind this list does not name may still appear: another version of deja may
have written it, and the log keeps what it was given.

`bytes` is the size of the text that changed hands — what deja served, or, for
`remember`, the note the agent wrote — and `raw` the size of the transcripts
behind a served digest, which is omitted when there is none. `bytes` is always
there, zero included: a recall that served nothing is a fact, not a missing
field. `sessions` counts what the digest held, `ids` names them for a
recall, and `empty` marks an event no session went into — a recall that returned
no sessions is still a recall, and the count of those is what
`empty_result_rate` is made of. It does not mean nothing was served: a session
start on a checkout with no sessions of its own injects the environment block,
which is about the machine rather than the project, so that event is `empty`
and carries its `bytes`. `into` names the agent session an injection went to,
as the harness names it, and is absent when the writer did not know one — an
MCP recall answers a tool call, not a session start. `unreadable` is true when
a hook was sent a payload deja could not decode: the memory went out anyway, so
the row exists, and without this it is identical to one from a host that sent
nothing at all. It says nothing about `into` — a decode that fails on one field
keeps the ones it read, so a payload that named its session carries both.

A line needs both `t` and `kind` to appear here at all: a half-written line, or
one from something that is not deja, is skipped rather than shown with a missing
half. The same rule decides what the counters in `stats --json` read, so the two
never disagree about whether something happened.

`deja log --last --json` is a different shape: one object, the most recent
injected digest itself. It carries `t` and `kind` as above, the `digest` text,
`bytes`, and — each omitted when empty — `sessions`, the `policy` that allowed
the injection, the `terms` behind a déjà vu firing, `into`, the agent session
it went to, `unreadable` for an injection whose payload could not be decoded,
and `projects`, the projects the digest was built from. `projects`
is absent on records written before it existed and on injections whose writer
does not know them. It is `null` when no digest has been recorded: one object is the
shape, and that is how a missing one is spelled.

## `deja blame <path> --json`

Returns a JSON array of blame hits (same stability rules as exact search):

```json
[
  {
    "session": {
      "harness": "claude",
      "id": "abc123",
      "project": "myapp",
      "updated": "2026-01-02T03:10:00Z"
    },
    "title": "Fix parser edge case",
    "count": 3,
    "snippets": ["… parser.go …"],
    "score": 1.5,
    "tier": "exact"
  }
]
```

A hit for a session whose decision was promoted also carries `lifecycle`,
`lifecycle_note` and `lifecycle_at`. Every state appears there, `accepted`
included: the field says which decision this is, not that something is wrong
with it. A consumer reading its presence as "this was withdrawn" wants
`lifecycle != "accepted"`.

The MCP `blame` tool returns the same array shape.
