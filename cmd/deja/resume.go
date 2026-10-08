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
	"unicode/utf8"

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
	doExec, writeBack := false, false
	prefix := ""
	for _, a := range args {
		if a == "--exec" {
			doExec = true
			continue
		}
		if a == "--write-back" {
			writeBack = true
			continue
		}
		// The last argument used to win, so a flag resume does not take, or a
		// stray word, silently replaced the id and the refusal named it as the
		// session that was missing (#2251).
		if strings.HasPrefix(a, "-") && a != "-" {
			return fmt.Errorf("resume: unknown flag %q — it takes an id-prefix, --write-back and --exec", a)
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
	indexDir := dir
	dir, cmdline, err := resumeCommand(s)
	// Some harnesses' resume reads the directory from the transcript itself
	// (CodeBuddy, the pi family), which is what is gone: the command is worked
	// out again once the file is back.
	if writeBack && (err == nil || (transcriptGone(s) && !strings.HasPrefix(s.Project, "imported:"))) {
		if werr := writeBackSession(indexDir, s, os.Stderr); werr != nil {
			return werr
		}
		dir, cmdline, err = resumeCommand(s)
	}
	if err != nil {
		if gone := resumeGoneError(s); gone != nil && !writeBack && !strings.HasPrefix(s.Project, "imported:") {
			return gone
		}
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
		line, ok := resumeLine(runtime.GOOS, dir, cmdline)
		if !ok {
			fmt.Fprintf(os.Stderr, "deja: run it from %q — the directory's name has characters the printed line cannot carry, so it leaves out the cd\n", dir)
		}
		fmt.Fprintln(stdout, line)
		return nil
	}
	parts, err := resumeArgv(cmdline)
	if err != nil {
		return err
	}
	c := exec.Command(parts[0], parts[1:]...)
	if dir != "" {
		c.Dir = dir
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// resumeWord puts a path on a resume command as one word. Anything outside
// the set no shell acts on is single-quoted, which bash, zsh and PowerShell
// (the -Command a Windows cd wraps the line in) all read literally, so a
// Windows path with its backslashes, an 8.3 ~ or a space goes on too (#4455).
// A quote, $, backtick or control character has no form all of them read
// alike, and ok is false. So do the curly quotes PowerShell closes a string
// on, a trailing \ (fish reads \' as a quote, which turned the next quoted
// word inside out), a leading - (an option to roo, not its value) and a byte
// that is not UTF-8 (--exec would read it back as U+FFFD). The bare set has
// no , or @: PowerShell reads a,b as two arguments and @x as a splat. A word
// cmd.exe would expand is refused too (cmdExpands).
func resumeWord(s string) (word string, ok bool) {
	if s == "" || !utf8.ValidString(s) || strings.ContainsAny(s, "'\"$`‘’‚‛“”„") ||
		strings.HasPrefix(s, "-") || strings.HasSuffix(s, `\`) || cmdExpands(s) {
		return "", false
	}
	bare := true
	for _, r := range s {
		if actsOnATerminal(r) {
			return "", false
		}
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_+:./-", r) {
			bare = false
		}
	}
	if bare {
		return s, true
	}
	return "'" + s + "'", true
}

// resumeArgv splits a resume command for --exec. Words are separated by
// spaces, and a word resumeWord quoted is read back whole without its quotes,
// where strings.Fields cut it at its space.
func resumeArgv(cmdline string) ([]string, error) {
	var out []string
	var cur strings.Builder
	quoted, inWord := false, false
	for _, r := range cmdline {
		switch {
		case r == '\'':
			quoted, inWord = !quoted, true
		case r == ' ' && !quoted:
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quoted {
		return nil, fmt.Errorf("resume: unbalanced quote in %q", cmdline)
	}
	if inWord {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("resume: empty command")
	}
	return out, nil
}

// resumeCaveats names what a printed command does that "resume" does not
// promise. Continue's is the first: its flag forks the session rather than
// continuing it, so the work comes back under a new id.
var resumeCaveats = map[string]string{
	"continue": "continue forks rather than continues: the history comes back under a new session id",
}

// resumeLine is the printed command: a cd into dir, then cmdline. ok is false
// when dir holds a character no quoting carries into the shell the line is
// pasted into; the line is then cmdline alone and the caller says where to
// run it (#4591).
//
// Windows wraps the line in powershell.exe so it runs from cmd and PowerShell
// alike. The PowerShell it is pasted into expands $ and backtick escapes in
// the outer double-quoted -Command and ends it on " and its curly forms, and
// neither shell's escaping works in the other, so those leave the cd out. The
// inner path is single-quoted, and PowerShell ends that on any of ' and
// U+2018 to U+201B, each read literally when doubled.
//
// fish reads \' and \\ inside single quotes as escapes where sh, bash and zsh
// keep both bytes, so a backslash in a POSIX path has no form all of them
// read alike: `a\'\';echo hi;#` ran echo in fish.
func resumeLine(goos, dir, cmdline string) (string, bool) {
	if dir == "" {
		return cmdline, true
	}
	for _, r := range dir {
		if actsOnATerminal(r) {
			return cmdline, false
		}
	}
	if goos == "windows" {
		if strings.ContainsAny(dir, "\"$`\u201c\u201d\u201e") || cmdExpands(dir) {
			return cmdline, false
		}
		dir = "'" + psSingleQuoted.Replace(dir) + "'"
		return fmt.Sprintf(`powershell.exe -NoProfile -Command "Set-Location -LiteralPath %s -ErrorAction Stop; %s"`, dir, cmdline), true
	}
	if strings.Contains(dir, `\`) {
		return cmdline, false
	}
	return fmt.Sprintf("cd %s && %s", shellQuote(dir), cmdline), true
}

// cmdExpands reports whether cmd.exe could read part of s as a variable: it
// expands %name% even inside double quotes, and !name! under delayed
// expansion, before PowerShell sees the line. The value is the user's, not
// the path's, and one holding a quote (a user named O'Brien) ended the
// single-quoted path early, so the rest of the name ran as PowerShell. A
// lone % or ! expands nothing.
func cmdExpands(s string) bool {
	return strings.Count(s, "%") > 1 || strings.Count(s, "!") > 1
}

var psSingleQuoted = strings.NewReplacer("'", "''", "\u2018", "\u2018\u2018", "\u2019", "\u2019\u2019", "\u201a", "\u201a\u201a", "\u201b", "\u201b\u201b")

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
// Junie names its sessions session-<yymmdd>-<hhmmss>-<suffix>.
var junieSessionID = regexp.MustCompile(`^session-[0-9]{6}-[0-9]{6}-[0-9a-zA-Z]+$`)

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
	// A Kimi, Qwen or CodeBuddy sub-agent log is a session in deja under
	// DEJA_INCLUDE_SUBAGENTS=1, but none of these clients opens one on its own; the
	// id deja gives it is one they have never seen (#4483).
	// Muse's child logs are the same: `muse resume <child>` answers "has no
	// saved log" (#4710).
	if s.Kind == "subagent" && (s.Harness == "kimi" || s.Harness == "qwen" || s.Harness == "muse" || s.Harness == "codebuddy") && s.Parent != "" {
		return "", "", fmt.Errorf("session %s is a sub-agent run, which %s does not reopen on its own — `deja resume %s` reopens the session that spawned it", digest.Short(s.ID), s.Harness, s.Parent)
	}
	// Nor a Kimi /btw side question, which runs in a fork of the session it
	// was asked in (#4484).
	if s.Kind == "fork" && s.Harness == "kimi" && s.Parent != "" {
		return "", "", fmt.Errorf("session %s is a /btw side question, which kimi does not reopen on its own — `deja resume %s` reopens the session it was asked in", digest.Short(s.ID), s.Parent)
	}
	switch s.Harness {
	case "claude":
		return claudeProjectDirFor(s), "claude --resume " + s.ID, nil
	case "codex":
		if s.Project == "history" {
			return "", "", fmt.Errorf("session %s is a one-off codex exec entry, nothing to resume", digest.Short(s.ID))
		}
		return "", "codex resume " + s.ID, nil
	case "trae":
		// TRAE CLI keeps codex's `resume <uuid>` subcommand, and traex is the
		// shortest of its three names (agentsview's resume table, botmux's
		// adapter).
		if s.Project == "history" {
			return "", "", fmt.Errorf("session %s is a history.jsonl entry with no rollout, nothing to resume", digest.Short(s.ID))
		}
		return "", "traex resume " + s.ID, nil
	case "muse":
		// `muse resume <uuid>` finds the session from any directory, then
		// takes the directory it was run from as the workspace, so the cd is
		// what puts its tools back in the right tree. With the workspace gone
		// the conversation still reopens, so the bare command is printed
		// (#4710).
		return existingDir(sources.MuseWorkspace(s.Path)), "muse resume " + s.ID, nil
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
	case "zcode":
		// The terminal client for the ZCode runtime (zcode-app-cli 0.16.9,
		// runtime 3.14.4) takes `--resume <sessionId>` with the sess_ id its
		// database stores, and reopens the session from any directory; the cd
		// keeps the agent in the project, when it is still there (#4430). The
		// JSONL transcripts are not in that database.
		// A snapshot an older ZCode left is in no store the client opens until
		// its restore-legacy-sessions command has copied it in (#4432).
		if sources.ZCodeLegacyUnderRoot(s.Path) {
			return "", "", fmt.Errorf("zcode session %s is a snapshot from an older ZCode; run /restore-legacy-sessions in zcode first, then `zcode --resume %s`", digest.Short(s.ID), s.ID)
		}
		if strings.HasSuffix(s.Path, ".jsonl") {
			return "", "", fmt.Errorf("zcode session %s is a transcript under ~/.zcode/projects; `zcode --resume` opens only sessions from ZCode's CLI database", digest.Short(s.ID))
		}
		return existingDir(s.Path), "zcode --resume " + s.ID, nil
	case "continue":
		// `cn --fork <sessionId>` loads the session by id straight out of the
		// store deja reads — `historyManager.load` opens
		// `<sessions>/<id>.json` (core/util/history.ts) — and starts a new
		// session from its history. So the history comes back and the id is
		// not the one that continues; the caveat below says so. It runs its
		// tools in the current directory, so the fork runs in the session's
		// workspace (#4375), or says it will not when that is gone (#4460).
		return existingDir(resumeRecordedDir(s)), "cn --fork " + s.ID, nil
	case "commandcode":
		// `cmd --resume <id>` (1.73.4 --help) finds the id only under the
		// project folder of the current directory, so it runs where the
		// session did (#4372). `--session <id>` searches every project and
		// continues the same transcript from wherever it runs, so that is the
		// command when the directory is gone or unknown (#4460). On Windows
		// the bin is cmdc, as the client prints on exit.
		bin := "cmd"
		if runtime.GOOS == "windows" {
			bin = "cmdc"
		}
		if dir := existingDir(resumeRecordedDir(s)); dir != "" {
			return dir, bin + " --resume " + s.ID, nil
		}
		return "", bin + " --session " + s.ID, nil
	case "kiro":
		// `kiro-cli chat --resume-id <sessionId>`, which Kiro's own docs give
		// and two orchestrators drive — one of them noting it needs Kiro CLI
		// 2.2.0 or newer. kiro-cli finds the session from anywhere but runs it
		// in the current directory and rewrites the session's cwd to it, so
		// the command runs where the session did (#4305).
		dir := existingDir(resumeRecordedDir(s))
		// A `sess_` id is the <workspace>/sess_<uuid> layout, which the IDE
		// and `kiro-cli --v3` both write. V3 lists its own in session-index
		// and takes the id back; the IDE's reopen from the app (#4307).
		if strings.HasPrefix(s.ID, "sess_") {
			if sources.KiroV3Session(s.Path) {
				return dir, "kiro-cli --v3 chat --resume-id " + s.ID, nil
			}
			return "", "", fmt.Errorf("session %s belongs to the Kiro IDE, which reopens it from its own history; kiro-cli --v3 lists only its own sessions", digest.Short(s.ID))
		}
		return dir, "kiro-cli chat --resume-id " + s.ID, nil
	case "senpi":
		// `--session <path|id>` takes a partial uuid, from senpi's own help, and
		// `--fork` is beside it for the copy-instead-of-continue case. Measured
		// on @code-yeongyu/senpi (#3670). It finds the session from anywhere,
		// but outside its project asks to fork it, as Kimchi does, so the
		// command runs in the directory the header records (#4426).
		return existingDir(resumeRecordedDir(s)), "senpi --session " + s.ID, nil
	case "kimchi":
		// Kimchi's own argument parser rewrites `--resume <selector>` to
		// `--session <id>` (src/cli-args.ts), so the id deja indexes is the
		// selector it takes. It finds the session from anywhere, but outside
		// its project asks to fork it, so the command runs in the directory
		// the header records (#4400).
		return existingDir(resumeRecordedDir(s)), "kimchi --session " + s.ID, nil
	case "gjc":
		// gjc's session-operations doc: `--resume <id|path>` at startup opens
		// an existing session. From another project 0.18 refuses it or forks
		// it into the current one, so the command runs in the directory the
		// header records — no guess, the same cwd gjc's scope file names
		// (#4395).
		return existingDir(resumeRecordedDir(s)), "gjc --resume " + s.ID, nil
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
		// aider appends to the history and loads none of it back unless told
		// to, and what it loads then is the whole file, every launch in it —
		// "run aider there and it continues" opened an empty chat (#4329).
		dir := filepath.Dir(s.Path)
		cmd := "aider --restore-chat-history"
		if filepath.Base(s.Path) != ".aider.chat.history.md" {
			cmd += " --chat-history-file " + shellQuoteForPaste(s.Path)
		}
		return "", "", fmt.Errorf("aider has no session resume — `%s` in %s loads the whole history file, every session in it, not only this one; `deja show %s` has this one alone", cmd, dir, s.ID)
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
		// working directory, so this runs in the original project. It reopens
		// one from any directory too, so a deleted project is left out rather
		// than printed as a cd that fails (#4459). The grok-dev store is a
		// different product sharing ~/.grok: its rows come out of grok.db and
		// there is no CLI to hand them to.
		if !strings.HasSuffix(s.Path, "updates.jsonl") {
			return "", "", fmt.Errorf("session %s comes from the grok-dev store, which has no terminal resume", digest.Short(s.ID))
		}
		return existingDir(resumeRecordedDir(s)), "grok --resume " + s.ID, nil
	case "cline":
		if strings.HasPrefix(s.ID, "cline-task-") {
			return "", "", fmt.Errorf("legacy Cline VS Code tasks reopen from the extension's history UI, not the terminal")
		}
		// cline reopens the transcript from anywhere but runs its tools in
		// the current directory, so the command runs where the session did
		// (#4318).
		return existingDir(sources.ClineSessionDir(s.Path)), "cline --id " + s.ID, nil
	case "roo":
		// The Roo CLI runs the extension against a VS Code shim and keeps its
		// tasks in a store of its own, which is the half that reopens from a
		// terminal: `roo --session-id`, scoped to the workspace the task was
		// in. Editor tasks live under the host's globalStorage, the CLI never
		// lists them, and there is still no command for those.
		//
		// The CLI looks a task up under the workspace it is handed, and with
		// no -w that is the real path of its cwd. A task created with -w
		// through a symlink (/tmp on macOS) recorded the link, so a plain cd
		// answered "Session not found" (#4422). The path goes on the command
		// as one word, quoted when it needs it, so a Windows workspace under
		// an 8.3 or spaced name keeps its -w (#4455); only one no quoting
		// carries keeps the cd alone. A workspace that is gone has no task to
		// find (#4459).
		if id, ws := sources.RooCLITask(s.Path); id != "" {
			dir, err := recordedResumeDir(s, ws, "roo --session-id")
			if err != nil {
				return "", "", err
			}
			if w, ok := resumeWord(dir); ok {
				return dir, "roo -w " + w + " --session-id " + id, nil
			}
			return dir, "roo --session-id " + id, nil
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
		// `codewhale exec` (verified against 0.9.13's own --help). The
		// directory is the absolute workspace from the session file; the
		// project label is a relative path that only resolved from the
		// workspace's parent (#4362).
		return existingDir(resumeRecordedDir(s)), "codewhale --resume " + s.ID, nil
	case "junie":
		// `--resume` with `--session-id` reopens a saved session (3110.7); it
		// goes with the project the session was started in.
		if !junieSessionID.MatchString(s.ID) {
			return "", "", fmt.Errorf("session id %q is not one junie --session-id takes", s.ID)
		}
		return existingDir(resumeRecordedDir(s)), "junie --session-id " + s.ID + " --resume", nil
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
			dir, err := recordedResumeDir(s, ws, "reasonix --resume")
			if err != nil {
				return "", "", err
			}
			return dir, "reasonix --resume " + s.ID, nil
		}
		// The JSONL store sits under Reasonix's state, not the workspace, and
		// --resume reads a file path from any directory, so a session whose
		// workspace is gone is reached by its path, like one that had none
		// (#4459).
		if ws := existingDir(sources.ReasonixWorkspace(s.Path)); ws != "" {
			return ws, "reasonix --resume " + s.ID, nil
		}
		if !reasonixPathPattern.MatchString(s.Path) {
			return "", "", fmt.Errorf("session %s has no workspace to run in and its path holds characters deja will not place in a command — run reasonix --resume with the file %s", digest.Short(s.ID), s.Path)
		}
		return "", "reasonix --resume " + s.Path, nil
	case "codebuddy":
		// `codebuddy -r <id>` looks the id up under the folder of the
		// directory it runs in and answers "No conversation found" from any
		// other, like qwen (#4707).
		short := digest.Short(s.ID)
		if sources.IsWorkBuddyTranscript(s.Path) {
			return "", "", fmt.Errorf("session %s is WorkBuddy's, and deja knows no command that reopens one — `deja show %s` has the conversation", short, short)
		}
		dir := sources.CodeBuddySessionDir(s.Path)
		if dir == "" {
			return "", "", fmt.Errorf("codebuddy session %s records no directory, and `codebuddy -r` finds a session only from the one it ran in — `deja show %s` has the conversation", short, short)
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return "", "", fmt.Errorf("codebuddy session %s ran in %s, which is gone, and `codebuddy -r` finds a session only from there — `deja show %s` has the conversation", short, dir, short)
		}
		// -c too: CodeBuddy gives SessionStart context to the model on a
		// resume only with `continue` set, and the -r id still picks the
		// session. Without it the recall was logged and never arrived (#4718).
		return dir, "codebuddy -c -r " + s.ID, nil
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
		// directory, so the same id resolves to nothing from elsewhere. That
		// store is inside the project, so a project that is gone took the
		// session with it (#4459).
		if !crushSessionID.MatchString(s.ID) {
			return "", "", fmt.Errorf("session id %q is not the uuid crush --session takes", s.ID)
		}
		dir, err := recordedResumeDir(s, sources.CrushProjectDir(s.Path), "crush --session")
		if err != nil {
			return "", "", err
		}
		return dir, "crush --session " + s.ID, nil
	case "pi":
		// In the directory the header records, not one decoded from the
		// folder name, where my-app and my/app fold to the same name. From
		// another project pi 0.73 finds the session globally and asks to fork
		// it (#4456).
		return existingDir(resumeRecordedDir(s)), "pi --session " + s.ID, nil
	case "omp":
		return "", "omp --resume " + s.ID, nil
	case "amp":
		// Amp takes the thread id as a positional argument; there is no flag.
		return "", "amp threads continue " + s.ID, nil
	case "prime":
		// In the session's own directory: prime-agent refuses a session from
		// another project (#4408).
		return existingDir(resumeRecordedDir(s)), "prime-agent --resume " + s.ID, nil
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

// resumeRecordedDir is the directory a session recorded running in, for the
// harnesses whose resume command goes there when it still exists and reopens
// it from elsewhere when it does not: opencode, Kilo and ZCode's CLI keep it
// as the session's path, the pi family and prime-agent in the transcript
// header, the others in a sidecar or the session file (#4459, #4460).
func resumeRecordedDir(s model.Session) string {
	switch s.Harness {
	case "opencode", "kilocode":
		return s.Path
	case "zcode":
		if !strings.HasSuffix(s.Path, ".jsonl") && !sources.ZCodeLegacyUnderRoot(s.Path) {
			return s.Path
		}
	case "pi", "gjc", "kimchi", "senpi":
		return sources.PiHeaderCwd(s.Path)
	case "prime":
		return sources.PrimeSessionDir(s.Path)
	case "grok":
		if strings.HasSuffix(s.Path, "updates.jsonl") {
			return sources.GrokCWDForSession(s.Path)
		}
	case "kiro":
		if s.Path == sources.KiroDB() {
			return sources.KiroDBSessionDir(s.Path, s.ID)
		}
		return sources.KiroSessionDir(s.Path)
	case "continue":
		return sources.ContinueSessionDir(s.Path)
	case "codewhale":
		return sources.CodeWhaleWorkspace(s.Path)
	case "junie":
		return sources.JunieSessionProject(s.Path)
	case "commandcode":
		return sources.CommandCodeSessionDir(s.Path)
	case "reasonix":
		if sources.ReasonixStore(s.Path) == "jsonl" {
			return sources.ReasonixWorkspace(s.Path)
		}
	}
	return ""
}

// resumeDirGoneNote says where a session whose directory is gone will run:
// opencode, Kilo and ZCode reopen it from anywhere, and their tools then work
// in the directory the command is run from, and so do Grok, Kiro, Continue,
// CodeWhale, Command Code and a Reasonix JSONL session (#4459, #4460). Cline
// does the same, and its directory is the manifest's rather than the store
// path's (#4318). pi, gjc, Kimchi and Senpi
// offer to fork it there instead, and prime-agent refuses it unless told to
// fork (#4408, #4456).
func resumeDirGoneNote(s model.Session, dir string) string {
	if dir == "" && s.Harness == "cline" && s.Path != "" {
		if d := sources.ClineSessionDir(s.Path); d != "" {
			return fmt.Sprintf("the directory this session ran in is gone (%s); it reopens in the one you run the command from", d)
		}
		return ""
	}
	recorded := resumeRecordedDir(s)
	if dir != "" || recorded == "" {
		return ""
	}
	if _, err := os.Stat(recorded); !os.IsNotExist(err) {
		return ""
	}
	if s.Harness == "prime" {
		return fmt.Sprintf("the directory this session ran in is gone (%s); from any other directory prime-agent refuses it unless you add --fork %s", recorded, s.ID)
	}
	if s.Harness == "pi" || s.Harness == "gjc" || s.Harness == "kimchi" || s.Harness == "senpi" {
		return fmt.Sprintf("the directory this session ran in is gone (%s); from any other directory %s offers to fork it rather than reopen it", recorded, s.Harness)
	}
	return fmt.Sprintf("the directory this session ran in is gone (%s); it reopens in the one you run the command from", recorded)
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
