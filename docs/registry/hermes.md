# Hermes

- **ID**: `hermes`
- **Store**: `~/.hermes/state.db` (0.17+) or `~/.hermes/profiles/<profile>/state.db` (older builds, one store per profile)
- **Home**: `HERMES_HOME`, Hermes's own variable, is followed — profiles, plugins and `config.yaml` are read and written under it. `DEJA_HERMES_HOME` overrides it.
- **Read overrides**: `DEJA_HERMES_PROFILES_ROOT` for the profiles directory, `DEJA_HERMES_DB` to pin a single store
- **Format**: SQLite relational store

A flat `messages` table, grouped by `session_id`: `role`, `content`, and `timestamp`
as REAL epoch seconds. Rows with `role` of `tool` carry no prose and are skipped, as
are rows with a null `content`. A `sessions` table beside it carries `id`, `cwd`,
`git_repo_root` and `title`; the `cwd` is where the work happened and is what names
the project, the same way a Cline or Roo workspace does. A store without that table,
or a session whose row has no `cwd`, falls back to the profile name. The title is left
to the index rather than taken from the first row.

- **MCP**: `mcp_servers` in `~/.hermes/config.yaml`; `deja install hermes-auto` also drops a plugin whose `pre_llm_call` hook injects recall and registers `/deja`, and a memory provider (`deja-memory`) that `hermes memory setup` lists next to mem0 and supermemory — the same recall in the `memory.provider` slot, with `deja_recall`, `deja_fix` and `deja_blame` as its tools. The provider is written against the interface Hermes has: `RecallStatus` arrived after `MemoryProvider`, so it is imported behind a guard and the status line is skipped where the class is missing — checked against Hermes 0.17.0, which has none. The hook stands aside for the provider only after loading it, so a provider that cannot import leaves the hook doing the recall rather than nobody.
- **Resume**: Hermes has its own session commands; nothing documented that starts a session from a prompt.
- **Handoff**: paste.

Requested in [#355](https://github.com/vshulcz/deja-vu/issues/355).

**Last verified:** 2026-07-28
