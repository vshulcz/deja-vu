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
| `path` | file the session was read from |
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
| `kind` | the harness's own word for a session an agent spawned (`subagent`, `subagent_fork`); absent for sessions a person started |
| `parent` | the session this one was spawned from, where the harness records the edge itself — deja never infers one |
| `agent` | name of the agent that ran a spawned session |

`messages` is likewise absent rather than empty on `last`, which never returns
turns.

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
    "stale_stores": 0
  },
  "mcp": [
    {
      "name": "claude-code",
      "state": "wired",
      "path": "/home/user/.claude.json"
    }
  ],
  "sqlite3": {"state": "ok"},
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
  "sync": {
    "state": "ok",
    "peers": [
      {
        "host": "laptop",
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
    ]
  }
}
```

`embed`, `ingest_health` and `deep` (present only under `--deep`) are omitted
when unavailable; `policy` is always present. `index.path` points at the index
directory; `index.db` is that directory's name, not a file. Store `state`
values are `ok`, `missing`, `unreadable`, `parsed-zero`, `denied` (which adds a
`denied` field naming the unreadable path), `needs-sqlite3` and `needs-zstd`
(both of which add a `skipped` field saying which CLI is missing); an existing
but empty store directory reports `missing`. A store also carries `indexed_sessions`
and, when it holds peer-synced work, `indexed_from_elsewhere`; a store whose
permission walk was cut short or blocked carries `partial` or `unchecked`.
Version `state` is `ok`, `update-available`, `ahead`, `dev`, `offline` (under
`--offline`), or `unknown`. `policy.state` is `default`, `active` or
`unreadable` (which adds an `error`); `activations` keys are `search`, `mcp` and
`auto`, each with the rule in force and how many sessions it withheld;
`ignored` and `inert` list policy lines that matched no harness or no import.
Per-harness `ingest_health` may also carry `clipped_messages` and `last_error`.

`sync.state` is `ok` or `unreadable`; `unreadable` adds an `error` saying why the peers file could not be parsed, and its `peers` list is empty because deja could read nothing from it — a sync is never stopped by a malformed config, so the report is the only place that failure shows. `sync.peers` is every machine this one knows, and it is always present — an
empty list means no machines are configured, which a script can tell apart from
a deja too old to report. Each row carries `host`, `sessions_from_there` (how
much of this index arrived from it), and `last_push` / `last_pull` as RFC 3339
timestamps, each omitted when that direction has never happened: the two fail
apart, and a machine that takes what this one sends while sending nothing back
is a broken sync that reads as a working one. A row carrying neither is a
machine named once and never reached — the text report says "never exchanged"
for it — and it is still a row, which is what distinguishes it from a deja too
old to report peers at all: that one has no `sync` key. `last_error` is why the most
recent exchange failed and is absent once one succeeds. Both `host` and
`last_error` are written elsewhere — a config file, another machine — so they
are bounded and stripped of control characters before they are reported.
`stamped_ahead` appears when the newer of the two timestamps is more than a
minute later than this machine's clock: the age would be negative and the row
would otherwise read as a sync that just happened, so a consumer should treat
that peer's dates as unusable for "how long since" rather than as healthy. The
minute is what makes this different from the rule for a session, where anything
ahead counts: a peers file is written by deja itself, so a copy
from a machine a moment ahead — or an NTP step landing between the write and the
read — is not a clock worth reporting, while a session's stamp comes from a
transcript deja did not write.

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

The MCP `blame` tool returns the same array shape.
