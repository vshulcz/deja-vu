# Kimchi Coding

- **ID**: `kimchi`
- **Store**: `${KIMCHI_CODING_AGENT_DIR:-${XDG_CONFIG_HOME:-~/.config}/kimchi/harness}/sessions/<session>.jsonl`
- **Read override**: `DEJA_KIMCHI_ROOT` replaces the session root
- **Format**: pi's session JSONL
- **Needs**: nothing

Kimchi Coding is another pi descendant and writes the same envelope, so the
parsing is pi's.

**Last verified:** 2026-09-16

## Known quirks and drift

- The root is flat — one directory, every session in it — so there is no encoded
  project directory to read a name from. The header line's `cwd` is what names
  the project, the same choice omp and prime-agent make for the same reason;
  drop it and every Kimchi session lands under no project at all.
- The default root sits under the config home rather than a dot directory of its
  own, so `XDG_CONFIG_HOME` moves it. `KIMCHI_CODING_AGENT_DIR` moves it
  outright.
- Read support only.
