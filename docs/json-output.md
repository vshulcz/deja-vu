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
- The current version is **1** (constant in `internal/jsonout`).
- **Exact `deja search --json` and `deja blame --json`** return a top-level JSON
  array (not an object envelope). Their element shapes are stable; only additive
  fields inside `session` or hit objects are permitted.

## `deja search --json`

Default exact search returns a JSON array of hits:

```json
[
  {
    "session": {
      "harness": "claude",
      "id": "abc123",
      "project": "myapp",
      "path": "/home/user/.claude/projects/.../session.jsonl",
      "started": "2026-01-02T03:04:05Z",
      "updated": "2026-01-02T03:10:00Z",
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
```

When fuzzy, stemmed, or semantic reranking is active, the output is an object
envelope with `schema_version`:

```json
{
  "schema_version": 1,
  "hits": [ … ],
  "fuzzy": true
}
```

Stemmed search may also include `variants`; semantic search sets `semantic`.
`superseded` (added in 0.15, optional) carries the date of a newer same-project
session whose matches overlap this hit — an earlier-attempt signal.

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
