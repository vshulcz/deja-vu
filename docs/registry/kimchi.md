# Kimchi Coding

- **ID**: `kimchi`
- **Store**: `~/.config/kimchi/harness/sessions/--<encoded-cwd>--/<session>.jsonl`
- **Read override**: `DEJA_KIMCHI_ROOT` replaces the session root
- **Format**: pi's session JSONL
- **Needs**: nothing

Kimchi Coding is another pi descendant and writes the same envelope, so the
parsing is pi's — including the directory per project. Its own binary builds
that path in `getDefaultSessionDirPath`: `<agent>/sessions/--<encoded cwd>--`,
where the encoding is the working directory with the separators replaced by
dashes and a `--` on each end. A session file directly under the root is read
too. Either way the header's `cwd` is what names the project, by its last two
segments, rather than encoded and decoded back, which could not tell `my-app`
from `my/app` (#4427, #4457).

**Last verified:** 2026-10-07

## Known quirks and drift

- Resume: `kimchi --session <id>`. Its own argument parser rewrites
  `--resume <selector>` to `--session <id>` (`src/cli-args.ts`), so the id
  deja indexes is the selector Kimchi takes — `deja resume` prints that
  command rather than handing over a paste. It runs in the directory the
  header's `cwd` names: Kimchi 1.5 finds a session from anywhere, but outside
  its project asks to fork it instead of reopening it (#4400).
- **Sub-agent runs** of the `Agent` tool are transcripts of their own beside
  the parent, marked by `parentSession` on the header and a
  `kimchi:subagent-session` entry after it. They are skipped, as gjc's sub-agents
  are; `DEJA_INCLUDE_SUBAGENTS=1` takes them. A
  fork carries `parentSession` alone and stays a session (#4401).
- A session file directly under the root has no encoded project directory to
  read a name from, so the header line's `cwd` names the project there — the
  same choice omp and prime-agent make (#3678).
- The harness directory is always `~/.config/kimchi/harness`. Kimchi 0.1.99
  sets `KIMCHI_CODING_AGENT_DIR` itself at startup (`src/entry.ts:46-51`),
  over whatever the shell had, and never reads `XDG_CONFIG_HOME`: a stand with
  both set still wrote its sessions there. deja used to follow both and read or
  wrote an empty directory on a machine that set either (#4802).
- Wiring: `deja install kimchi` writes the server into the harness directory's
  `mcp.json` (`join(getAgentDir(), "mcp.json")` in Kimchi's
  `src/extensions/mcp-adapter/config.ts`) and the skill into its `skills/`.
  Kimchi does not list `~/.agents/skills`. The skill is offered when the
  config's `skillPaths` includes `.config/kimchi/harness/skills`, which the
  first-run setup writes by default without a terminal and offers in its
  wizard with one.
- `deja install kimchi-auto` adds pi's extension to `extensions/deja.ts`.
  Kimchi loads it without a trust prompt. On a 0.1.99 stand against a stub
  endpoint, what its `before_agent_start`, `context` and `tool_result`
  handlers returned was in the request, and `session_before_compact`,
  `session_compact` and `session_shutdown` fired (#4802). The extension also
  registers `/deja`. With it in place, leave Kimchi's
  `extensions.claude-code-hook-adapter` off: it would run deja's Claude hooks
  as well and recall everything twice.

## Measured on a live install

`@getkimchi/kimchi` 0.1.99, in a hermetic HOME:

- `deja install kimchi` writes `<config>/kimchi/harness/mcp.json` and kimchi's
  own first-run panel lists it: `MCP servers: deja`.
- That panel also reports what it found from deja's Claude Code install —
  `Claude Code skills: 1`, `Claude Code commands: 1`, `Agents skills: 2` — and
  offers `Migrate MCP servers to Kimchi?` with migrate / skip / never.
- Skill directories are chosen in the wizard ("Select skill paths to enable",
  default `none`), which is a second switch beside the compatibility extension.
- `kimchi resources enable extensions.claude-code-hook-adapter` adopts **all
  five** of deja's Claude hooks, and `kimchi resources status` then names each
  one: `hooks.claude-code.user.session-start.0`,
  `…user-prompt-submit.0`, `…pre-tool-use.0`, `…post-tool-use.0`,
  `…pre-compact.0`.
- A turn needs no browser login: an `apiKey` and `llmEndpoint` in
  `~/.config/kimchi/config.json` point it at any OpenAI-compatible endpoint,
  which is how the extension above was measured.
