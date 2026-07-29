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
- **`deja blame --json`** returns a top-level JSON array. Its element shape is
  stable; only additive fields inside `session` or hit objects are permitted.

### What changed in version 2

`deja search --json` used to return a bare array on the exact path and an object
envelope on every fallback path, so a consumer had to handle two shapes and
could not tell which it had without inspecting the value. It now always returns
the envelope, and the envelope answers the two questions a caller cannot answer
from a list of hits:

- `match` — which tier answered: `exact`, `close`, `stemmed`, `semantic`, or
  `relevance`. **`relevance` means nothing matched** and these are the nearest
  sessions deja could find. Counting those as hits overstates recall, and this
  used to be readable only as a sentence on stderr.
- `total` and `capped` — how many sessions matched, and whether the result cap
  hid some. Counting the returned hits measures the cap: that figure moves when
  the window's membership changes, whether or not retrieval improved. Pass
  `--all` for an uncapped set, in which case `total` equals the hit count.

`capped` is omitted when false. When it is true, `total` is the count before
policy scoping and reranking ran, because there is no way to know how those
would have treated the sessions the cap removed.

## `deja search --json`

Every search returns one envelope:

```json
{
  "schema_version": 2,
  "match": "exact",
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

`tier` on each hit is the per-hit equivalent of `match`. The fallback flags stay
alongside it for readers written against version 1:

```json
{
  "schema_version": 2,
  "match": "close",
  "total": 4,
  "hits": [ … ],
  "fuzzy": true
}
```

Stemmed search may also include `variants`; semantic search sets `semantic`.
`superseded` (optional) carries the date of a newer same-project session whose
matches overlap this hit — an earlier-attempt signal. `reused` (optional)
counts recent agent recalls that served this session.

`--limit N` bounds the ranked result set to 1–100 hits. Every machine session
has `source.origin`, either `local` or `imported`. When
`DEJA_SOURCE_INSTANCE` is configured, local sessions also carry that stable
operator-chosen `source.instance`; imported sessions omit it because current
sync batches do not carry trustworthy peer identity.

## `deja last --json`

Recent session metadata uses a versioned envelope and never includes messages:

```json
{
  "schema_version": 1,
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
  "schema_version": 1,
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
  "schema_version": 1,
  "total_sessions": 42,
  "total_messages": 318,
  "repeat_questions": 3,
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
    "empty_result_rate": 0.1
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

Optional fields are omitted when zero or empty. The heatmap grid used by
`--card` is intentionally excluded from `--json` output.

## `deja doctor --json`

```json
{
  "schema_version": 1,
  "stores": [
    {
      "name": "claude",
      "state": "ok",
      "paths": ["/home/user/.claude/projects"],
      "files": 12
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
  "ingest_health": {
    "claude": {"malformed_lines": 0, "failed_files": 0}
  }
}
```

`embed` and `ingest_health` are omitted when unavailable. Store `state` values
include `ok`, `missing`, `empty`, `unreadable`, and `parsed-zero`.

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
