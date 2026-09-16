# Senpi

- **ID**: `senpi`
- **Store**: `${SENPI_CODING_AGENT_DIR:-~/.senpi/agent}/sessions/<encoded-cwd>/<session>.jsonl`
- **Read override**: `DEJA_SENPI_ROOT` replaces the session root
- **Format**: pi's session JSONL
- **Needs**: nothing

Senpi (OmO Native) descends from pi and kept its transcript envelope — a
`session` header line, then one `message` line per turn — so the parsing is
pi's. The encoded directory names the project, the way pi's does.

**Last verified:** 2026-09-16

## Known quirks and drift

- `SENPI_CODING_AGENT_DIR` moves the whole agent directory, sessions included,
  so deja reads it: a machine that has already said where its sessions are
  should not have to say it twice in a second variable.
- Read support only. Nothing is wired into Senpi yet, which the registry records
  as gaps rather than leaving blank.
