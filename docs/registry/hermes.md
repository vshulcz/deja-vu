# Hermes

- **ID**: `hermes`
- **Store**: `~/.hermes/state.db` (0.17+) or `~/.hermes/profiles/<profile>/state.db` (older builds, one store per profile)
- **Read overrides**: `DEJA_HERMES_PROFILES_ROOT` for the profiles directory, `DEJA_HERMES_DB` to pin a single store
- **Format**: SQLite relational store

A flat `messages` table, grouped by `session_id`: `role`, `content`, and `timestamp`
as REAL epoch seconds. Rows with `role` of `tool` carry no prose and are skipped, as
are rows with a null `content`. There is no working directory in the schema, so the
profile name stands in for the project.

- **MCP**: `mcp_servers` in `~/.hermes/config.yaml`; `deja install hermes-auto` also drops a plugin whose `pre_llm_call` hook injects recall and registers `/deja`.
- **Resume**: Hermes has its own session commands; nothing documented that starts a session from a prompt.
- **Handoff**: paste.

Requested in [#355](https://github.com/vshulcz/deja-vu/issues/355).
