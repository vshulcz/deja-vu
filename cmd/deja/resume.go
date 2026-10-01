package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// runResume turns a found session into the command that reopens it in its
// native harness. Prints the command by default; --exec runs it with the
// terminal attached.
func runResume(dir string, args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return idPrefixNeeded(dir, "resume needs an id-prefix", "resume needs id-prefix (see `deja last`)")
	}
	doExec := false
	prefix := ""
	for _, a := range args {
		if a == "--exec" {
			doExec = true
			continue
		}
		// The last argument used to win, so a flag resume does not take, or a
		// stray word, silently replaced the id and the refusal named it as the
		// session that was missing (#2251).
		if strings.HasPrefix(a, "-") && a != "-" {
			return fmt.Errorf("resume: unknown flag %q — it takes an id-prefix and --exec", a)
		}
		if prefix != "" {
			return fmt.Errorf("resume takes one id-prefix — got %q and %q", prefix, a)
		}
		prefix = a
	}
	if prefix == "" {
		return idPrefixNeeded(dir, "resume needs an id-prefix", "resume needs id-prefix (see `deja last`)")
	}
	s, ok, err := findByPrefix(dir, prefix)
	noteAmbiguousPrefix(dir, prefix, "resuming")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no session matches %q", prefix)
	}
	// Naming an exact id is still browsing under the search activation, so a
	// session a trust rule withholds must not be reopenable here any more than
	// through show, share, promote or handoff (#1026).
	if err := denyPolicyHidden(prefix, s, os.Stderr); err != nil {
		return err
	}
	dir, cmdline, err := resumeCommand(s)
	if err != nil {
		return err
	}
	if err := resumeGoneError(s); err != nil {
		return err
	}
	if note := resumeDirGoneNote(s, dir); note != "" {
		fmt.Fprintf(os.Stderr, "deja: %s\n", note)
	}
	if !doExec {
		if note := resumeCaveats[s.Harness]; note != "" {
			// stderr, so `$(deja resume …)` still composes: the command is the
			// answer, the caveat is for the person reading.
			fmt.Fprintf(os.Stderr, "deja: %s\n", note)
		}
		fmt.Fprintln(stdout, formatResumeCommand(dir, cmdline))
		return nil
	}
	parts := strings.Fields(cmdline)
	c := exec.Command(parts[0], parts[1:]...)
	if dir != "" {
		c.Dir = dir
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// resumeCaveats names what a printed command does that "resume" does not
// promise. Continue's is the first: its flag forks the session rather than
// continuing it, so the work comes back under a new id.
var resumeCaveats = map[string]string{
	"continue": "continue forks rather than continues: the history comes back under a new session id",
}

func formatResumeCommand(dir, cmdline string) string {
	if dir == "" {
		return cmdline
	}
	if runtime.GOOS == "windows" {
		dir = "'" + strings.ReplaceAll(dir, "'", "''") + "'"
		return fmt.Sprintf(`powershell.exe -NoProfile -Command "Set-Location -LiteralPath %s -ErrorAction Stop; %s"`, dir, cmdline)
	}
	return fmt.Sprintf("cd %s && %s", shellQuote(dir), cmdline)
}

// resumeIDPattern matches every supported harness's session identifiers
// (UUIDs, ses_... ids, hex prefixes). Anything else — whitespace, shell
// metacharacters, quotes, leading dashes — is refused so a crafted id read
// from a session store cannot alter the command deja builds or prints.
var resumeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// hermesProfilePattern and hermesReservedProfiles are Hermes' own
// _PROFILE_ID_RE and _RESERVED_NAMES (hermes_cli/profiles.py): a directory
// outside them is not a profile `hermes -p` will open, and `-p default` opens
// the root rather than profiles/default.
var (
	hermesProfilePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	hermesReservedProfiles = map[string]bool{"hermes": true, "default": true, "test": true, "tmp": true, "root": true, "sudo": true}
)

// openclawKeyPattern matches a session key (agent:<id>:<name>). Colons are
// what separates a key's parts, so the id pattern is too strict here — and
// anything looser than this would let a key read off disk carry shell
// metacharacters into a printed command.
var openclawKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

// reasonixPathPattern is a transcript path that can go on a command line as
// one unquoted argument: no whitespace, quotes or shell metacharacters.
var reasonixPathPattern = regexp.MustCompile(`^[A-Za-z0-9/\\:._~+-]+$`)

// Crush names its sessions with a uuid. Nothing else goes on a command line.
var crushSessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resumeCommand maps a session to (workdir, command). workdir is empty when
// the harness resumes globally or the original directory is unknown.
func resumeCommand(s model.Session) (string, string, error) {
	if strings.HasPrefix(s.Project, "imported:") {
		return "", "", fmt.Errorf("session %s was synced from another machine — resume it there", digest.Short(s.ID))
	}
	if !resumeIDPattern.MatchString(s.ID) {
		return "", "", fmt.Errorf("session id %q contains characters deja will not place in a command", digest.Short(s.ID))
	}
	switch s.Harness {
	case "claude":
		return claudeProjectDirFor(s), "claude --resume " + s.ID, nil
	case "codex":
		if s.Project == "history" {
			return "", "", fmt.Errorf("session %s is a one-off codex exec entry, nothing to resume", digest.Short(s.ID))
		}
		return "", "codex resume " + s.ID, nil
	case "opencode":
		// opencode sessions carry their project directory. opencode reopens a
		// session from anywhere, so a deleted one is left out rather than
		// printed as a cd that fails (#4201).
		return existingDir(s.Path), "opencode -s " + s.ID, nil
	case "antigravity":
		return "", "agy --conversation " + s.ID, nil
	case "kilocode":
		// `kilo -s <id>` — "session id to continue" in Kilo's own CLI options
		// (packages/opencode/src/cli/cmd/tui.ts). That is the CLI half of the
		// store; the extension's tasks live under the editor's globalStorage
		// and reopen from its own history view, the split Roo has too, so
		// those are refused with the reason rather than given a command that
		// would not find them.
		// Which half a session came from is the path, but not the path this
		// used to compare: a session read out of the CLI database carries the
		// directory it ran in, never the database file, so `!= KiloDB()` was
		// true for every CLI session and the command was refused for the only
		// store it was written for. An extension task is the one with a path
		// under the host's `tasks/` (#3677).
		if sources.KiloTaskPath(s.Path) {
			return "", "", fmt.Errorf("session %s is a Kilo Code editor task — reopen it from Kilo's history view; the CLI lists only its own sessions", digest.Short(s.ID))
		}
		// And in the directory it ran in, the way the opencode case does with
		// the same field: Kilo is OpenCode vendored and a CLI session carries
		// its own working directory — when it is still there (#4201).
		return existingDir(s.Path), "kilo -s " + s.ID, nil
	case "continue":
		// `cn --fork <sessionId>` loads the session by id straight out of the
		// store deja reads — `historyManager.load` opens
		// `<sessions>/<id>.json` (core/util/history.ts) — and starts a new
		// session from its history. So the history comes back and the id is
		// not the one that continues; the caveat below says so.
		return "", "cn --fork " + s.ID, nil
	case "kiro":
		// `kiro-cli chat --resume-id <sessionId>`, which Kiro's own docs give
		// and two orchestrators drive — one of them noting it needs Kiro CLI
		// 2.2.0 or newer. The IDE's sessions reopen from the app instead, and
		// those carry a `sess_` id, so only the CLI's get a command.
		if strings.HasPrefix(s.ID, "sess_") {
			return "", "", fmt.Errorf("session %s belongs to the Kiro IDE, which reopens it from its own history", digest.Short(s.ID))
		}
		return "", "kiro-cli chat --resume-id " + s.ID, nil
	case "senpi":
		// `--session <path|id>` takes a partial uuid, from senpi's own help, and
		// `--fork` is beside it for the copy-instead-of-continue case. Measured
		// on @code-yeongyu/senpi (#3670).
		return "", "senpi --session " + s.ID, nil
	case "kimchi":
		// Kimchi's own argument parser rewrites `--resume <selector>` to
		// `--session <id>` (src/cli-args.ts), so the id deja indexes is the
		// selector it takes.
		return "", "kimchi --session " + s.ID, nil
	case "gjc":
		// gjc's session-operations doc: `--resume <id|path>` at startup opens
		// an existing session. A session from another project forks into the
		// current one there, so no working directory is printed rather than
		// one deja would be guessing at.
		return "", "gjc --resume " + s.ID, nil
	case "hermes":
		// Hermes takes the same session ID deja indexes, so this resumes the
		// exact conversation rather than the most recent one — from the
		// profile whose store holds it (#4248).
		if p, dir := sources.HermesResumeProfile(s.Path); p != "" {
			if dir && (!hermesProfilePattern.MatchString(p) || hermesReservedProfiles[p]) {
				return "", "", fmt.Errorf("session %s is in profiles/%s, a name Hermes does not take as a profile (`-p default` is the root), so `hermes -p` cannot reopen it — `deja show %s` has the conversation", digest.Short(s.ID), p, digest.Short(s.ID))
			}
			return "", "hermes -p " + p + " --resume " + s.ID, nil
		}
		return "", "hermes --resume " + s.ID, nil
	case "aider":
		dir := filepath.Dir(s.Path)
		return "", "", fmt.Errorf("aider has no session resume — run aider in %s and it continues the same history", dir)
	case "gemini":
		// gemini finds a session only from the directory it ran in: anywhere
		// else it says "No previous sessions found for this project" (#4211).
		// Current stores record that directory in projects.json and
		// .project_root; an older one keyed by a hash of it has nothing to
		// invert, and gets no cd. A recorded directory that is gone is
		// refused, as qwen's is (#4259).
		dir, err := recordedResumeDir(s, sources.GeminiProjectDir(s.Path), "gemini --resume")
		if err != nil {
			return "", "", err
		}
		return dir, "gemini --resume " + s.ID, nil
	case "cursor":
		if strings.HasSuffix(s.Path, ".jsonl") {
			// A CLI transcript is named after the chat id `--resume` takes.
			// `cursor-agent --resume <id>` looks for the chat only under the
			// md5 of the directory it is started in, so the command has to
			// run in the one the chat ran in and the chat has to be there;
			// otherwise it opens an empty chat (#4193).
			short := digest.Short(s.ID)
			dir := cursorProjectDirFor(s)
			if dir == "" {
				return "", "", fmt.Errorf("cursor chat %s: the directory it ran in is not recorded, and `cursor-agent --resume <id>` finds a chat only from there — `cursor-agent --resume` with no id lists chats from every directory, and `deja show %s` has the conversation", short, short)
			}
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				return "", "", fmt.Errorf("cursor chat %s ran in %s, which is gone — `deja show %s` has the conversation", short, dir, short)
			}
			if _, err := os.Stat(filepath.Join(sources.CursorCLIHome(), "chats", sources.CursorChatBucket(dir), s.ID, "store.db")); err != nil {
				return "", "", fmt.Errorf("cursor chat %s is no longer in cursor-agent's store — `deja show %s` has the conversation", short, short)
			}
			return dir, "cursor-agent --resume " + s.ID, nil
		}
		return "", "", fmt.Errorf("cursor IDE chats reopen from the Cursor UI, not the terminal")
	case "grok":
		// Grok Build resumes by session id and scopes its session list by the
		// working directory, so this has to run in the original project. The
		// grok-dev store is a different product sharing ~/.grok: its rows come
		// out of grok.db and there is no CLI to hand them to.
		if !strings.HasSuffix(s.Path, "updates.jsonl") {
			return "", "", fmt.Errorf("session %s comes from the grok-dev store, which has no terminal resume", digest.Short(s.ID))
		}
		return sources.GrokCWDForSession(s.Path), "grok --resume " + s.ID, nil
	case "cline":
		if strings.HasPrefix(s.ID, "cline-task-") {
			return "", "", fmt.Errorf("legacy Cline VS Code tasks reopen from the extension's history UI, not the terminal")
		}
		return "", "cline --id " + s.ID, nil
	case "roo":
		// The Roo CLI runs the extension against a VS Code shim and keeps its
		// tasks in a store of its own, which is the half that reopens from a
		// terminal: `roo --session-id`, scoped to the workspace the task was
		// in. Editor tasks live under the host's globalStorage, the CLI never
		// lists them, and there is still no command for those.
		if id, ws := sources.RooCLITask(s.Path); id != "" {
			return ws, "roo --session-id " + id, nil
		}
		return "", "", fmt.Errorf("roo tasks from the VS Code extension reopen from its history UI; only the ones the roo CLI created take --session-id")
	case "zed":
		// These two used to fall through to "don't know how to resume", which
		// reads like deja is missing something. Both are settled answers, and
		// the registry has carried the reason all along.
		return "", "", fmt.Errorf("zed threads reopen from the editor's own history — no zed flag takes a thread id")
	case "deepseek":
		return "", "", fmt.Errorf("neither of DeepSeek Harness's two apps takes a session id, so there is nothing to reopen by")
	case "codewhale":
		// Its sessions are per-workspace — `--continue` refuses in a directory
		// that has none — so the command goes with the workspace it was worked
		// in. `--session-id` is the alias of `--resume` on both the TUI and
		// `codewhale exec` (verified against 0.9.13's own --help).
		return s.Project, "codewhale --resume " + s.ID, nil
	case "reasonix":
		// `--resume` looks an id up in the store of the workspace it runs in
		// (the git root of the working directory), so it goes with that
		// workspace. It also takes a file path, which is the only way back to
		// a session saved with no workspace (internal/frontend/cli/cli_flags.go).
		//
		// 1.x keeps a session as a directory. Its --resume matches the id in
		// the sessions-v4 store of the working directory and takes no
		// directory path (internal/cli/cli_flags.go:131-200), so a session in
		// the global or the desktop store has no command to reopen it by.
		switch sources.ReasonixStore(s.Path) {
		case "desktop":
			return "", "", fmt.Errorf("session %s was made in the Reasonix desktop app and reopens from its sidebar; the CLI does not read that store", digest.Short(s.ID))
		case "global":
			return "", "", fmt.Errorf("session %s is in Reasonix's store for sessions with no workspace, which `reasonix --resume` does not search", digest.Short(s.ID))
		case "project":
			ws := sources.ReasonixWorkspace(s.Path)
			if ws == "" {
				return "", "", fmt.Errorf("session %s: deja cannot tell which directory it was started in; run `reasonix --resume %s` there", digest.Short(s.ID), s.ID)
			}
			if !reasonixPathPattern.MatchString(s.ID) {
				return "", "", fmt.Errorf("session id %q contains characters deja will not place in a command", s.ID)
			}
			return ws, "reasonix --resume " + s.ID, nil
		}
		if ws := sources.ReasonixWorkspace(s.Path); ws != "" {
			return ws, "reasonix --resume " + s.ID, nil
		}
		if !reasonixPathPattern.MatchString(s.Path) {
			return "", "", fmt.Errorf("session %s has no workspace and its path holds characters deja will not place in a command — run reasonix --resume with the file %s", digest.Short(s.ID), s.Path)
		}
		return "", "reasonix --resume " + s.Path, nil
	case "qwen":
		// qwen keys its sessions by the directory they ran in: run anywhere
		// else, `qwen -r <id>` answers "No saved session found". Unlike
		// opencode (#4201) there is no running it from elsewhere, so with the
		// directory gone this refuses, as it does for a Cursor CLI chat (#4259).
		short := digest.Short(s.ID)
		dir, recorded := sources.QwenSessionDir(s.Path)
		if recorded {
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				return "", "", fmt.Errorf("qwen session %s ran in %s, which is gone, and `qwen -r` finds a session only from there — `deja show %s` has the conversation", short, dir, short)
			}
		}
		if dir == "" {
			return "", "", fmt.Errorf("qwen session %s: deja cannot tell which directory it ran in, and `qwen -r` finds a session only from there — `deja show %s` has the conversation", short, short)
		}
		return dir, "qwen -r " + s.ID, nil
	case "openclaw":
		key, err := sources.OpenClawSessionKey(s.Path, s.ID)
		if err != nil {
			return "", "", err
		}
		if key == "" {
			// Either the store never mapped this id, or a reset moved its key
			// on to a newer window: the old one stays searchable but
			// `--session <key>` would open the new one.
			return "", "", fmt.Errorf("session %s is not the current session of any openclaw key, so there is no key that reopens it", digest.Short(s.ID))
		}
		if !openclawKeyPattern.MatchString(key) {
			return "", "", fmt.Errorf("session key %q contains characters deja will not place in a command", key)
		}
		return "", "openclaw chat --session " + key, nil
	case "kimi":
		// In the directory the session was created in: Kimi Code refuses a
		// session from anywhere else (#4274), so a workDir that is gone is
		// refused rather than printed without the cd.
		dir, err := recordedResumeDir(s, sources.KimiSessionDir(s.Path), "kimi --session")
		if err != nil {
			return "", "", err
		}
		return dir, "kimi --session " + s.ID, nil
	case "goose":
		return "", "goose session --resume --session-id " + s.ID, nil
	case "crush":
		// In the project directory, not anywhere: Crush keeps one store per
		// project and looks for the session in the one under the current
		// directory, so the same id resolves to nothing from elsewhere.
		if !crushSessionID.MatchString(s.ID) {
			return "", "", fmt.Errorf("session id %q is not the uuid crush --session takes", s.ID)
		}
		return sources.CrushProjectDir(s.Path), "crush --session " + s.ID, nil
	case "pi":
		return piProjectDirFor(s), "pi --session " + s.ID, nil
	case "omp":
		return "", "omp --resume " + s.ID, nil
	case "amp":
		// Amp takes the thread id as a positional argument; there is no flag.
		return "", "amp threads continue " + s.ID, nil
	case "prime":
		return "", "prime-agent --resume " + s.ID, nil
	case "copilot":
		return "", "copilot --resume=" + s.ID, nil
	case "copilot-chat":
		// VS Code lists only the open workspace's chats, so the folder comes
		// first (#4223).
		if dir := sources.CopilotChatWorkspaceDir(s.Path); dir != "" {
			return "", "", fmt.Errorf("copilot-chat sessions reopen in VS Code: open the workspace (code %s), then Chat: Show Chats", shellQuoteIfNeeded(dir))
		}
		return "", "", fmt.Errorf("copilot-chat sessions reopen from Chat: Show Chats, not the terminal")
	default:
		return "", "", fmt.Errorf("don't know how to resume %q sessions", s.Harness)
	}
}

// recordedResumeDir is the directory a session recorded, for an agent that
// opens a session only from there: dir when it exists, an error naming
// `deja show` when it is gone, and "" when nothing was recorded.
func recordedResumeDir(s model.Session, dir, command string) (string, error) {
	if dir == "" {
		return "", nil
	}
	if existingDir(dir) == "" {
		short := digest.Short(s.ID)
		return "", fmt.Errorf("%s session %s ran in %s, which is gone, and `%s` finds a session only from there — `deja show %s` has the conversation", s.Harness, short, dir, command, short)
	}
	return dir, nil
}

// existingDir is p when it is a directory on this machine, else "": a cd into
// one that is gone stops the command before the harness starts.
func existingDir(p string) string {
	if p == "" {
		return ""
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p
	}
	return ""
}

// resumeDirGoneNote says where a session whose directory is gone will run:
// opencode and Kilo reopen it from anywhere, and their tools then work in the
// directory the command is run from.
func resumeDirGoneNote(s model.Session, dir string) string {
	if dir != "" || s.Path == "" || (s.Harness != "opencode" && s.Harness != "kilocode") {
		return ""
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		return ""
	}
	return fmt.Sprintf("the directory this session ran in is gone (%s); it reopens in the one you run the command from", s.Path)
}

// claudeProjectDirFor recovers the original working directory from the
// transcript location when the encoded project dir still exists on disk.
func claudeProjectDirFor(s model.Session) string {
	if s.Path == "" {
		return ""
	}
	return sources.ClaudeSessionDir(s.Path)
}

// cursorProjectDirFor recovers the working directory a CLI transcript belongs
// to, when the encoded name still resolves on this machine.
func cursorProjectDirFor(s model.Session) string {
	if s.Path == "" {
		return ""
	}
	// Only a directory the chat is filed under: one read from a meta.json
	// whose folder is not its md5 sends cursor-agent to an empty chat.
	if cwd, verified := sources.CursorChatCWD(s.Path); verified {
		return cwd
	}
	base := sources.CursorTranscriptProjectDirBase(s.Path)
	if base == "" {
		return ""
	}
	return sources.ResolveEncodedPath(base)
}

// piProjectDirFor recovers the original working directory from the
// transcript location when the encoded project dir still exists on disk.
func piProjectDirFor(s model.Session) string {
	if s.Path == "" {
		return ""
	}
	base := sources.PiProjectDirBase(s.Path)
	if base == "" {
		return ""
	}
	return sources.ResolveEncodedPath(base)
}
