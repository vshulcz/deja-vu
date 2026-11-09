# dsh-deja

English | [中文](https://github.com/vshulcz/deja-vu/blob/main/extensions/dsh/docs/zh.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

Part of **[deja-vu](https://github.com/vshulcz/deja-vu)** (**97.2% R@5** on LongMemEval-S · **13.6 s** to index 19,195 sessions · **half the tokens** of agentmemory) · [Website & live search](https://vshulcz.github.io/deja-vu/) · [Browser demo](https://vshulcz.github.io/deja-vu/view/) · **[★ Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu)**

DeepSeek Harness can already search its own sessions — that is what the built-in
`session-query` subsystem does. This plugin answers the other question: what you
did in the *other* agents on this machine.

deja indexes the session files that forty-two other coding agents — Claude Code,
Codex, Cursor, opencode, Antigravity, Grok Build, Kimi, Cline and Zed among
them — already write to disk. Nothing has to have been recorded ahead of time: the history is already
there, including the months before deja was installed. If you moved to dsh last
week, everything you did before last week is still reachable from inside it.

## Install

```sh
dsh plugin --profile web add dsh-deja
```

From the repository instead of npm — the root manifest re-exports this
directory, so either form works:

```sh
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

The plugin runs the `deja` binary. It is pulled in as a dependency, so npm
installs it with the plugin; a `deja` already on `PATH` is used as-is, and
`DEJA_BIN` overrides both.

If you have the CLI, `deja install --auto` wires dsh too — it adds deja's MCP
server and a `/deja` command to your profile — and that is the shorter path.
Having both is fine: this plugin looks for what the installer wrote in
`$DSH_HOME/plugins/deja/` and contributes only what is missing, so the tools and
the command are never registered twice.

## What it adds

Six tools the model can call:

| Tool | Answers |
|---|---|
| `deja_recall` | Sessions matching an error string, function name, file path or flag. |
| `deja_session` | The full digest of one past session, when the reasoning behind it matters. |
| `deja_blame` | The sessions that discussed a file, before you edit or delete it. |
| `deja_fix` | What this machine ran after that same error before. |
| `deja_how` | The real invocation for a build, test or deploy, from what ran here. |
| `deja_remember` | Stores one settled decision for later recall. |

One command: `/deja <what to look for>`.

And, unless you turn it off, automatic recall: once per session the plugin adds
what past sessions settled in this project, and before each step it asks deja
whether this machine's history answers the prompt and adds the answer to the
runtime context. Silence is the common case — it speaks only when there is
something to say.

The same recall also wires what `deja install dsh-auto` wires: after a read,
edit or write the model gets what past sessions said about that file, after a
command that failed it gets what this machine ran after the same error before,
a compaction keeps what the session was in the middle of, and a session that
ends drops its live stamp.

In `dsh web` you also see the lines deja prints for the person in Claude Code:
the receipt naming the agent a prompt's answer came from, and
at session start what was recalled, the first-build note and the weekly note.
They sit in a small panel above the input until the next prompt, or until you
close it. The model gets exactly what it got before; these lines never reach
it. tui, headless and sdk profiles have no page, so nothing changes there:
the notes still come with the reply to `/deja`. A line that waits more than
ten minutes for the page is dropped. `deja install dsh-auto` writes the same
panel from these files; with both installed you get one panel.

```yaml
- insert:
    - id: deja
      name: dsh-deja
      config:
        autoRecall: false   # keep tools, /deja and manual search
```

## Search controls

Open **Settings → Deja** for three switches, all enabled by default and grouped
in two sections.

**In-conversation search**

- **Mod+F in conversations** writes a real `/deja` command into the message
  input — `/deja --session <bare session UUID> -- <your draft>` — and focuses
  it. Sending it runs deja against that one conversation, so the search
  actually happens instead of arriving as an ordinary message; reference
  chips in the draft are kept and move with the prefix. The shortcut does not
  send the draft; you send it when you choose. A draft that already starts
  with another command is left untouched. The default is **Mod+F** — in this
  document `Mod` means `Ctrl` on Windows/Linux and `Cmd` on macOS, and the
  defaults below are written in the registry's own `primary` form, which the
  framework expands per platform. In a browser the default is **Mod+Alt+F**
  (on macOS, **Cmd+Option+F**), because DSH reserves the browser's ordinary
  Find shortcut.

**Deja full-text search** (two switches)

- **Mod+Shift+K opens Deja full-text search** is the direct cross-session
  shortcut. It opens DSH's sidebar search and starts it in Deja full-text mode
  instead of the title search. The magnifying glass returns the native search
  for that opening. DSH's own **Mod+K** is left untouched: the shortcut
  registry rejects a second binding for the same key, so Deja uses
  **Mod+Shift+K** (**Cmd+Shift+K** on macOS), the same default on desktop and in
  a browser. Turn the switch off to restore the official behaviour completely.
- **Deja search in the sidebar** controls the magnifying-glass path. It adds a
  full-text mode to sidebar search: open sidebar search, then click its
  magnifying glass again to switch to Deja. Enter a query to search the local
  index of **DSH conversations only**, see up to 20 hits with matching
  snippets, and click a hit to open its session. Click the magnifying glass
  again to return to regular sidebar results. If more matches exist, narrow the
  query. Missing Deja, failed searches and sessions that cannot be opened are
  shown as errors.

**Linux browsers** get no default binding for either shortcut: the browser
service admits only `Mod+/`, `Mod+Shift+,` and `Mod+Shift+.`. `Mod+/` already
belongs to DSH's own `shortcuts.open` (the keyboard settings), so apart from the
other two (`Mod+Shift+,` and `Mod+Shift+.`) every combination is rejected.
Record one of those two in the settings below.

These preferences are saved separately in this browser/profile's local storage;
changing one does not change the other, and they do not sync between browsers
or devices. Manual search remains available with `autoRecall: false`.

### Customizing the shortcuts

Open **Settings → Deja → Customize shortcuts** to change the actual key
combinations. Three rows show the current binding for conversation search,
Deja cross-session search, and DSH's sidebar opener (`session.search`). Click
**Change shortcut**, press and release the desired combination, and it saves
immediately. **Restore default** restores that command's default; **Remove**
unbinds it. Escape or moving focus away cancels recording. Conflicts and save
failures are displayed without replacing the accepted binding. If a combination
is occupied, first change or remove the other command's binding.

The controls use DSH's native keybinding service, with the same platform-specific
persistence, conflict validation and live catalog as **Settings → General →
Shortcuts**. Edits survive plugin updates. Browser/system reserved combinations
remain subject to DSH's validation. Older DSH versions without the editing
service show an upgrade notice. The switches above enable the features; the
keybinding rows determine which keys invoke them.

Sidebar full-text search needs the **dsh-deja package host plugin**, which
registers its endpoint. The CLI's `deja install dsh-auto` copies the browser
panel but not that endpoint, so the panel probes it once at startup:

- **Settings → Deja → Search endpoint** always says what it found — the
  installed package and its version, or that `/api/deja-search` is missing.
  Missing means the sidebar mode, its switch, the cross-session shortcut and
  its keybinding rows stand down, DSH's sidebar stays exactly as the host drew
  it, and the page offers the install command (`dsh plugin --profile web add
  dsh-deja`, with the profile swapped for yours) to copy instead of leaving a
  dead switch.
- The probe is the route's own `?probe=1` ping: only a JSON payload with
  `ok: true` and `plugin: "dsh-deja"` counts as a working endpoint. A 500, a
  proxy error or an HTML fallback is treated as missing. It names the plugin's
  version, starts no `deja search`, and a declared state (used by tests) wins
  over a late answer.

Install the package as above to enable the sidebar search; the existing CLI
tools and command can coexist with the package.

## How it is wired

Recall arrives through `ctx.systemPrompt.context`, evaluated on every assembly.
The obvious alternative — a middleware on `agent/pre-step` that splices a
message into the step — loads fine, completes the turn, and never reaches the
model: a later listener in that waterfall rebuilds its answer from the payload
and the added message is dropped with nothing reported. This was settled by
reading the requests dsh 0.1.1-rc.2 actually sent.

The person's lines take one deja call with the recall, not two: where dsh
serves the page, the plugin asks for the hook's JSON reply, hands `additionalContext` to the model and keeps
`systemMessage` on the host until the page asks for it at `/api/deja-notes`,
behind dsh's own login. Anywhere else it keeps asking for `--plain`, because
deja marks a note shown when it prints it. The browser half is `client.js`,
plain JS in dsh's module wrapper, so the package has no build step.

Manual sidebar search uses `GET /api/deja-search` on the same authenticated DSH
API mount. It runs an asynchronous, shell-free
`deja search --harness deepseek --json --limit 20 -- <query>` call, bounded to
20 seconds and 2 MiB of output. Empty queries launch no process; queries longer
than 500 characters are rejected. The route returns compact session metadata
and snippets rather than complete transcripts.

DSH currently exposes no sidebar-search provider API. The browser adapter stays
inside the `data-slot="sidebar.workspaces"` anchor and finds the semantic
`aria-expanded` search button and the `_listArea` CSS class suffix. This markup
was inspected in DSH **0.2.1-rc.5**. If those elements are absent in an older or
changed layout, the adapter leaves the regular sidebar alone. These internal
markup dependencies may need updating for future DSH versions.

## Testing the search controls

Run `node --test extensions/dsh/test/*.test.js` from the repository root for the
synthetic host, preferences, shortcut and composition tests. For the DOM/React
adapter check, serve the repository with a static HTTP server and open
`extensions/dsh/test/search-browser.html`. That optional fixture loads React
18 from a CDN and uses synthetic history; it does not connect to DSH or read a
local index. A passing report covers mode switching, session opening, host
errors, empty queries, disabling the feature and cleanup.

## What it does not do

No LLM, no embeddings. The index is a local BM25 store over files
that already exist, so a query answers in about a millisecond, with no network
path unless you ask for one. Secrets are redacted at index time.

Part of [deja-vu](https://github.com/vshulcz/deja-vu). MIT. Found it useful? [Star deja-vu on GitHub](https://github.com/vshulcz/deja-vu).
