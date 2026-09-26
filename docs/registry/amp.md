# Amp

Amp (Sourcegraph) stores one JSON object per thread under its local data
 directory:

- `${XDG_DATA_HOME:-~/.local/share}/amp/threads/`, on every OS: macOS and
  Windows use the same `~/.local/share` default, and a set `XDG_DATA_HOME` is
  followed there too.
- `DEJA_AMP_ROOT` replaces the thread directory directly. When set, it is the
  only root deja reads.

Each `*.json` file is one thread. The parser uses `id` as the stable session ID,
`title` as the session title, and the first `env.initial.trees[0].uri` when it
is a `file://` URI to derive the project name from its final path component.
If that working-directory URI is absent or not a file URI, the project falls
back to the title.

A thread's `messages` are retained only for `user` and `assistant` roles. Within
each message, blocks with `type: "text"` are joined in order; other block types
are ignored. Amp does not write per-message timestamps, so every retained
message uses the thread's `created` Unix-millisecond timestamp. This is an
intentional approximation for ordering and recency, not an inferred message
time.

A malformed or truncated JSON file is reported as a per-file ingestion error
and skipped by discovery/load and index rebuilds; it does not prevent other
thread files from being indexed. Amp thread files are not append-only, so deja
uses the normal full-file parse path when a file changes.

## Synthetic fixture

[`fixtures/registry/amp/thread.json`](../../fixtures/registry/amp/thread.json)
is a minimal conformance sample with deterministic IDs, paths, and timestamps.
It contains no personal data or credentials.

**Last verified:** 2026-09-02
