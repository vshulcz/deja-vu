# Windsurf / Devin Desktop

- **ID**: `windsurf`
- **Store**: `~/.codeium/windsurf/cascade/<cascade-id>.pb`, one file per Cascade conversation
- **Config root**: `~/.codeium/windsurf` on macOS and Linux, `%USERPROFILE%\.codeium\windsurf` on Windows
- **Read overrides**: `DEJA_WINDSURF_ROOT` replaces the config root
- **Format**: an AES-256-GCM sealed `CortexTrajectory` protobuf
- **Needs**: nothing
- **Resume**: none — `devin-desktop --sessions` opens the Sessions window, and no command takes a conversation id

Windsurf now ships as Devin Desktop. The bundle still carries the Windsurf
internals (`com.exafunction.windsurf`, `extensions/windsurf`, the language
server's `--codeium_dir .codeium/windsurf`), and the Cascade store is the same,
so deja reads both under one id. The `state.vscdb` databases hold no Cascade
transcripts on 3.5.17.

Each file is `nonce(12) || ciphertext || tag(16)`: AES-256-GCM, no additional
data, and the key the language server carries as a constant,
`safeCodeiumworldKeYsecretBalloon`. deja authenticates a file before decoding
it. One that fails is skipped whole and counted: `deja doctor` and the index
run say `windsurf 3 trajectories failed authentication` rather than show a
shorter history.

Inside is a `CortexTrajectory`. The fields deja reads, by number from the
generated descriptors in the bundled extension:

| Message | Fields |
| --- | --- |
| `CortexTrajectory` | 1 `trajectory_id`, 2 `steps`, 6 `cascade_id`, 7 `metadata`, 10 `renamed_title` |
| `CortexTrajectoryMetadata` | 1 `workspaces`, 2 `created_at` |
| `CortexWorkspaceMetadata` | 1 `workspace_folder_absolute_uri`, 2 `git_root_absolute_uri` |
| `CortexTrajectoryStep` | 5 `metadata`, 19 `user_input`, 20 `planner_response` |
| `CortexStepMetadata` | 1 `created_at`, 8 `completed_at` |
| `CortexStepUserInput` | 1 `query`, 2 `user_response` |
| `CortexStepPlannerResponse` | 1 `response`, 3 `thinking`, 8 `modified_response` |

The session id is `cascade_id`, which is also the file name. The project is
the first workspace's git root when it has one, else its folder; both are
`file://` URIs. The title is `renamed_title`, else the first prompt. A typed
prompt is `user_response`, with `query` read when that is empty; the reply is
`response`, with `modified_response` read when that is empty. Thinking, tool
calls, edits, terminal state and plans are left out. Timestamps come from each
step's metadata, then the trajectory's.

**Last verified:** 2026-07-28

## Known quirks and drift

- **Reply precedence is unverified.** The live trajectory examined on 3.5.17
  was refused by the service before the model answered, so which of
  `response` and `modified_response` a successful turn fills has not been
  seen. deja reads `response` first.
- **Error-only trajectories.** A refused turn still writes a trajectory: the
  prompt, then steps with no planner response. It is indexed with its prompts.
- **Not read.** `implicit/*.pb` (context trajectories), `brain/` (plans and
  other state) and the newer ACP session store are different sources from
  Cascade.
- **Unknown fields are skipped** by wire type, so a schema that grows keeps
  reading. A file that authenticates and does not decode is reported as an
  unreadable path and costs only itself.
- **Wiring.** `deja install windsurf` writes the server into
  `~/.codeium/windsurf/mcp_config.json` under `mcpServers`, the file Cascade
  reads. `devin-desktop --add-mcp` writes the Code OSS-level `User/mcp.json`
  instead, which Cascade does not read.
  The same install puts deja's guidance block in
  `~/.codeium/windsurf/memories/global_rules.md`, which Cascade keeps in
  context always. Cascade caps that file at 6,000 characters including the
  user's own rules, so a file that would pass the cap with the block is left
  as it is, and install and `deja doctor` say so. The skill goes to
  `skills/deja-history/SKILL.md` and `/deja` to
  `global_workflows/deja.md`. Workflows get no argument substitution, so the
  workflow names the query in words.
- **Hooks are not wired.** Cascade runs `~/.codeium/windsurf/hooks.json`, but
  none of a hook's output reaches the model: exit 2 only blocks, and
  `show_output` is shown in the UI and is not offered for `pre_user_prompt`
  ([docs](https://docs.devin.ai/desktop/cascade/hooks)). The rule, the skill and
  the workflow come from the docs and have not been run against the app.
- **Checked against** Devin Desktop 3.5.17 on macOS (Code OSS 1.110.1):
  runtime paths, the envelope against a live file, and descriptor field
  numbers (#308). The fixtures are synthetic, built by
  `internal/sources/windsurf_fixture_test.go`.
