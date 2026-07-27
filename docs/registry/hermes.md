# Hermes

- **ID**: `hermes`
- **Store**: `~/.hermes/profiles/<profile>/state.db` (SQLite, one store per profile)
- **Read overrides**: `DEJA_HERMES_PROFILES_ROOT` for the profiles directory, `DEJA_HERMES_DB` to pin a single store
- **Format**: SQLite relational store

A flat `messages` table, grouped by `session_id`: `role`, `content`, and `timestamp`
as REAL epoch seconds. Rows with `role` of `tool` carry no prose and are skipped, as
are rows with a null `content`. There is no working directory in the schema, so the
profile name stands in for the project.

- **MCP**: no client — `deja install` has no target here.
- **Resume**: Hermes has its own session commands; nothing documented that starts a session from a prompt.
- **Handoff**: paste.

Requested in [#355](https://github.com/vshulcz/deja-vu/issues/355).
