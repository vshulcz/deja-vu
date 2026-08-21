# Zed session format

## Store and files

Zed's built-in agent keeps every thread in one SQLite database under Zed's **data** directory, following its own `paths::data_dir()`:

| Platform | Store |
| --- | --- |
| macOS | `~/Library/Application Support/Zed/threads/threads.db` |
| Linux, FreeBSD | `${XDG_DATA_HOME:-~/.local/share}/zed/threads/threads.db` |
| Windows | `%LOCALAPPDATA%\Zed\threads\threads.db` |

`~/.config/zed` is the *config* directory. It exists on every platform and holds `settings.json`, keymaps and prompts — it holds no threads, so a reader that looks there finds nothing on a machine with years of history.

A Flatpak install overrides the Linux root with `FLATPAK_XDG_DATA_HOME`. `DEJA_ZED_ROOT` relocates the root and `DEJA_ZED_DB` points at a store directly; the second is how the fixture is read without a Zed install.

Reading this store needs the `sqlite3` CLI, like opencode's and Cursor's, **and** the `zstd` CLI, which no other harness needs. Thread bodies are compressed; sqlite3 alone opens the store and reads nothing out of it. With either tool missing, `deja sources` and `deja doctor` say which one rather than reporting an empty history.

## Records

One row is one thread, and one thread is one session. The table Zed ends up with is:

```sql
CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    summary TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    data_type TEXT NOT NULL,
    data BLOB NOT NULL,
    parent_id TEXT,
    folder_paths TEXT,
    folder_paths_order TEXT,
    created_at TEXT
);
```

Only the first five columns are in the `CREATE TABLE`. The rest arrive as `ALTER TABLE` statements Zed runs at startup, so a store an older Zed wrote and a newer one has never opened has only the original five.

`data_type` is `zstd` for everything Zed writes today and `json` for rows written before compression landed. Either way the payload is one thread document, and the document is flattened — Zed serialises `SerializedThread { #[serde(flatten)] thread, version }`, so `version` sits beside the thread's own fields rather than wrapping them.

Current documents are version `0.3.0` and hold Rust's externally tagged `Message` enum:

```json
{"version":"0.3.0","title":"read the zed thread store","updated_at":"2026-07-19T09:00:02Z",
 "messages":[{"User":{"id":"u1","content":[{"Text":"where does zed keep its agent threads?"}]}},
             {"Agent":{"content":[{"Text":"in threads.db under the data dir"}],"tool_results":{}}},
             "Resume"]}
```

A document written by the previous agent generation names the title `summary` and carries internally tagged segments instead:

```json
{"version":"0.2.0","summary":"legacy zed thread","updated_at":"2026-07-19T08:00:02Z",
 "messages":[{"id":1,"role":"user","segments":[{"type":"text","text":"does the old shape still load?"}]}]}
```

Both matter. Zed rewrites a thread in the current shape only when that thread is next saved, so a store mixes generations indefinitely.

`User` maps to `user` and `Agent` to `assistant`; a legacy document's `role` is already lowercase (`user`, `assistant`, `system`), and `system` is dropped as harness-authored. `Text` blocks are indexed and joined with newlines, and a `Mention`'s inlined `content` is indexed because that is the text Zed put in front of the model. Thinking, redacted thinking, tool calls, tool results, images and the `Resume` control marker are skipped.

`folder_paths` is a serialized `PathList`: the workspace roots newline-joined in lexicographic order, with `folder_paths_order` holding comma-joined display indices. The first path names the project.

## Known quirks and drift

- Thread documents carry no per-message timestamp in either generation. Every message inherits the thread's start, as aider's do; message order comes from the array.
- `updated_at` and `created_at` are Rust `to_rfc3339` output, so they end in `+00:00` rather than `Z`. They are not lexicographically comparable with a Go `RFC3339Nano` watermark — `.` sorts below `Z`, so comparing them as text drops a row that is newer by a fraction of a second. The incremental filter normalises both sides with `strftime` first, and keeps a row whose timestamp SQLite cannot parse rather than assuming it was already seen.
- `created_at` is set from `updated_at` when a thread is first written, so a fresh thread's window is a point, not a range. On a store predating the column the window collapses onto `updated_at`.
- A thread body that will not decode — a truncated frame, an unknown `data_type`, a document that is not a thread — costs that thread only. One unreadable row must not cost a user every other thread they have.
- `parent_id` marks a subagent thread. Those rows are indexed like any other today; nothing filters them.
- The compressed fixture row is a real zstd frame, so its bytes are opaque in review. The SQL states the plaintext it decompresses to and a test pins the two together.

## Skill and command

Zed 1.4.2 replaced its rules library with Agent Skills and loads them globally
from `~/.agents/skills/<name>/SKILL.md` — the shared file deja already writes,
so `deja install zed` needs no Zed-specific one. Typing `/` in the agent panel
lists those skills by name, which is what a `/deja` command would have been:
the skill is the command, as it is for Codex, Qwen, Kimi and Copilot.

Auto-recall stays out of reach for a different reason than a missing file: the
editor runs nothing before a prompt is sent, so there is no point to inject at.

Checked against Zed 1.16.1.

**Last verified:** 2026-08-21
