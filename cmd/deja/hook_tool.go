package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/prompt"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// `deja hook-tool` is recall at the moment of the action rather than at the
// moment of the sentence.
//
// Every other injection point deja has fires on text a person wrote. But an
// agent spends its time running commands and editing files, and that is where
// the machine's own history is both cheapest to match — an exact command, an
// exact path, no lexical guessing — and most likely to save work. On a real
// 1165-session store, 335 commands were run in three or more separate sessions
// and 487 files were touched in five or more.
//
// The cost side is the reason this stays lean. A prompt hook is paid once per
// message; this is paid once per action, and there are an order of magnitude
// more of those. So: at most one line carrying at most one prior decision, no
// digest, no snippets, and silence unless the history is a pattern rather than a
// coincidence. A measured A/B settled the payload: a line that only pointed at
// `deja blame` changed nothing an agent did, while the same line carrying the
// decision drove it to reuse the prior fix — so the file line names the decision
// and keeps the pointer only as a fallback.

const (
	// toolHookMaxBytes caps the whole payload. Wider than a bare pointer because
	// the file line now carries one prior decision, which the measurement showed
	// is what makes the difference; still one line, still per-action cheap.
	toolHookMaxBytes = 480
	// toolHookDecisionBudget bounds the extracted decision inside that cap.
	toolHookDecisionBudget = 200
	// toolHookDecisionScan caps how many of the newest sessions the file line
	// reads for a decision — this runs inside an action the agent waits on.
	toolHookDecisionScan = 3
	// toolHookMsgTail caps the messages scanned per session for the same reason:
	// a decision states itself near the end, not buried in a long transcript.
	toolHookMsgTail = 150
	// toolHookMinFileSessions is when a file's history stops being noise. A
	// file two sessions touched is ordinary work; five is a place with a past.
	toolHookMinFileSessions = 5
)

type toolHookInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

func runHookTool(dir string, stdin io.Reader, stdout io.Writer) error {
	var input toolHookInput
	_ = json.NewDecoder(bytes.NewReader(readHookPayload(stdin, hookStdinWait))).Decode(&input)
	adoptHookCWD(input.CWD)
	// Never build or repair from here. This runs inside an action the user is
	// waiting on, and a miss costs nothing while a rebuild costs seconds.
	if !planIndexReady(dir) {
		return nil
	}
	line := toolHookLine(dir, input)
	if line == "" {
		return nil
	}
	// A PreToolUse hook fires on every action, so the same fact must not be
	// re-injected turn after turn. Dedupe per agent session on the line itself,
	// the way hook-plan and hook-prompt dedupe what they inject.
	token := "tool:" + shortHash(line)
	if alreadyInjected(dir, input.SessionID)[token] {
		return nil
	}
	rememberInjectedIDs(dir, input.SessionID, token)
	line = truncateToolLine(line, toolHookMaxBytes)
	out := frameRecall(line)
	// Record the injection so deja's most frequent surface is not invisible to
	// stats and the receipt. Deduped above, so this counts a distinct fact
	// served, not every action.
	usage.RecordResult(dir, usage.KindTool, len(out), 1, false)
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "PreToolUse"
	resp.HookSpecificOutput.AdditionalContext = out
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(stdout, string(b))
	return nil
}

// toolHookLine is what the agent is told, or "" for the silence that is the
// default answer. It gates on the tool: a command line is worth saying only for
// the tool that runs a command, and a file's history only before the file is
// changed — Read, Glob and NotebookRead carry a file_path too, and a hook wired
// with a wide matcher would otherwise fire on every one of them.
func toolHookLine(dir string, input toolHookInput) string {
	switch input.ToolName {
	case "Bash":
		if cmd := strings.TrimSpace(input.ToolInput.Command); cmd != "" {
			return commandHookLine(dir, cmd)
		}
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		if path := strings.TrimSpace(input.ToolInput.FilePath); path != "" {
			return fileHookLine(dir, path)
		}
	case "apply_patch":
		// Codex and other OpenAI-style agents make every file edit through a
		// single apply_patch tool: the patch text is in `command` and the path
		// is in its header, not in file_path. Pull the targets out so the
		// file-history line fires here too — without this the hook is blind to
		// every edit those agents make.
		for _, path := range applyPatchFiles(input.ToolInput.Command) {
			if line := fileHookLine(dir, path); line != "" {
				return line
			}
		}
	}
	return ""
}

// applyPatchFiles pulls the target paths out of an apply_patch body. Each file
// section is headed by "*** Add File: p", "*** Update File: p" or "*** Delete
// File: p", and a rename adds "*** Move to: p".
func applyPatchFiles(patch string) []string {
	var out []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimSpace(line)
		for _, pfx := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "} {
			if strings.HasPrefix(line, pfx) {
				if p := strings.TrimSpace(line[len(pfx):]); p != "" {
					out = append(out, p)
				}
				break
			}
		}
	}
	return out
}

func commandHookLine(dir, cmd string) string {
	// "You have run this before" is worthless for an inspection command the
	// agent runs constantly — git status, git diff, ls, cat. On a real store
	// these are the top of the table (git status --short in 116 sessions), and
	// a line about them on every action is pure noise. The value is in a build,
	// a test, a deploy — a command that does something.
	if inspectionCommand(cmd) {
		return ""
	}
	use, ok := index.CommandHistory(dir, cmd)
	if !ok {
		return ""
	}
	// A hook that fires unasked is the auto activation. Count only the sessions
	// in projects the trust policy allows — and take the last-run date from
	// those projects too, so a command surfaced from an allowed project does
	// not print a withheld project's more-recent run. An index built before
	// ByProject existed has none; the version gate keeps the hook silent there
	// rather than falling back to a machine-wide count that ignores the policy.
	if len(use.ByProject) == 0 {
		return ""
	}
	pol := policy.Load()
	sessions := 0
	var last time.Time
	for proj, pu := range use.ByProject {
		if !pol.Allows(policy.ActivationAuto, proj) {
			continue
		}
		sessions += pu.Sessions
		if pu.Last.After(last) {
			last = pu.Last
		}
	}
	if sessions < 1 {
		return ""
	}
	when := ""
	if !last.IsZero() {
		when = ", last " + last.Local().Format("2006-01-02")
	}
	head := fmt.Sprintf("This machine has run that command in %s%s",
		toolSessionCount(sessions), when)
	// The file path carries the prior decision rather than a pointer to it,
	// for the measured reason recorded there — a line that only says history
	// exists changes nothing. A command deserves the same: before `npm run
	// build`, what matters is that it failed here last time and why, not that
	// it has been run twice.
	if d := commandDecisionLine(dir, cmd); d != "" {
		return head + " — last time: " + d
	}
	return head + "."
}

// commandDecisionLine returns what happened the last time this command ran, or
// "" when the history holds no conclusion about it.
//
// The command's sessions are found by searching for it: the manifest records
// the files a session touched but not the commands it ran, so there is no
// cheaper lookup, and this hook fires on a build or a deploy rather than on
// every message — the prompt hook already pays a search per keystroke.
func commandDecisionLine(dir, cmd string) string {
	terms := prompt.Terms(normalizedCommandText(cmd))
	if len(terms) == 0 {
		return ""
	}
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	ranked, matched, _, _, err := index.ProjectRelevant(dir, digest.ProjectNameCandidates(cwd), terms, toolHookDecisionScan)
	if err != nil {
		return ""
	}
	pol := policy.Load()
	states := sources.PromotedLifecycles()
	for i, s := range ranked {
		// Every term or nothing: a build command's words are common, and one
		// of them matching finds a session about something else entirely.
		if matched[i] < len(terms) {
			continue
		}
		if !pol.Allows(policy.ActivationAuto, s.Project) || !decisionUsable(s, states) {
			continue
		}
		if len(s.Messages) > toolHookMsgTail {
			s.Messages = s.Messages[len(s.Messages)-toolHookMsgTail:]
		}
		if cs := digest.Conclusions(s, toolHookDecisionBudget, 1); len(cs) > 0 {
			return trimTrailingFragment(search.SafeText(strings.TrimSpace(cs[0])))
		}
	}
	return ""
}

// normalizedCommandText is the command as words to search for: the invocation
// without its flags, which are punctuation to the tokeniser and noise to the
// ranking.
func normalizedCommandText(cmd string) string {
	var out []string
	for _, f := range strings.Fields(cmd) {
		if strings.HasPrefix(f, "-") {
			continue
		}
		out = append(out, f)
	}
	return strings.Join(out, " ")
}

func fileHookLine(dir, path string) string {
	// FileSessions matches on the file's basename, so without scoping "main.go"
	// or "README.md" collects every project's file of that name — the line then
	// claims a history this file does not have and points `deja blame` at a
	// pile of other repos. Count only sessions in the project being worked in,
	// unless the stored path is the exact one (which cannot collide).
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	projects := digest.ProjectNameCandidates(cwd)
	// A hook that fires unasked is the auto activation, so a session the trust
	// policy withholds must not even be counted here.
	pol := policy.Load()
	var sessions int
	var last time.Time
	var inScope []index.SessionMeta
	for _, meta := range index.FileSessions(dir, path) {
		if !pol.Allows(policy.ActivationAuto, meta.Project) {
			continue
		}
		if !fileMetaInScope(meta, path, projects) {
			continue
		}
		sessions++
		inScope = append(inScope, meta)
		if meta.Updated.After(last) {
			last = meta.Updated
		}
	}
	if sessions < toolHookMinFileSessions {
		return ""
	}
	when := ""
	if !last.IsZero() {
		when = ", last " + last.Local().Format("2006-01-02")
	}
	name := search.SafeText(baseName(path))
	head := fmt.Sprintf("%s has been worked on in %s%s", name, toolSessionCount(sessions), when)
	// The measured difference between a nudge that changes what an agent does
	// and one it ignores is whether it carries the decision or only points at
	// it: a line that said "deja blame X has the history" drove no reuse, while
	// the same moment carrying the prior decision did. So surface the decision
	// here, and fall back to the pointer only when none can be extracted.
	if d := fileDecisionLine(dir, inScope); d != "" {
		return head + " — prior decision: " + d
	}
	return fmt.Sprintf("%s — `deja blame %s` has the history.", head, name)
}

// fileDecisionLine returns the single most relevant prior decision recorded
// about this file, or "" if none can be extracted. It reads the newest in-scope
// sessions (bounded, newest first) and takes the first that yields a conclusion
// and was not later taken back — the same decision-carrying line the SessionStart
// digest surfaces, but delivered at the moment the file is about to change.
func fileDecisionLine(dir string, metas []index.SessionMeta) string {
	sort.Slice(metas, func(i, j int) bool { return metas[i].Updated.After(metas[j].Updated) })
	states := sources.PromotedLifecycles()
	for i, meta := range metas {
		if i >= toolHookDecisionScan {
			break
		}
		s, ok, err := index.FindByIdentity(dir, meta.Harness, meta.ID)
		if err != nil || !ok || !decisionUsable(s, states) {
			continue
		}
		// Bound the work inside a hook the agent is blocked on: a marathon
		// session's whole history is not worth scanning for one line, and a
		// decision states itself near the end anyway.
		if len(s.Messages) > toolHookMsgTail {
			s.Messages = s.Messages[len(s.Messages)-toolHookMsgTail:]
		}
		if cs := digest.Conclusions(s, toolHookDecisionBudget, 1); len(cs) > 0 {
			return trimTrailingFragment(search.SafeText(strings.TrimSpace(cs[0])))
		}
	}
	return ""
}

// decisionUsable rejects a session whose decision should not be reused: one that
// says in its own words it backed the approach out (GaveUp), and one a later
// state marked rejected, superseded or stale. Surfacing either at the point of an
// edit would push the agent to redo exactly what was undone.
func decisionUsable(s model.Session, states map[string]sources.Lifecycle) bool {
	if s.GaveUp {
		return false
	}
	state := s.Lifecycle // a note that arrived by sync carries its own state
	if lc, ok := states[s.Harness+":"+s.ID]; ok && lc.State != "" {
		state = lc.State
	}
	switch state {
	case "rejected", "superseded", "stale":
		return false
	}
	return true
}

// trimTrailingFragment drops a half-word left by a byte-budget cut, so a decision
// reads as "capped retries" rather than "capped retr". Only when the text was
// actually cut (near the budget and not ending a sentence).
func trimTrailingFragment(s string) string {
	if len(s) < toolHookDecisionBudget-4 {
		return s
	}
	if r := s[len(s)-1]; r == '.' || r == '!' || r == '?' {
		return s
	}
	if i := strings.LastIndexAny(s, " \t"); i > 0 {
		return strings.TrimRight(s[:i], " \t") + "…"
	}
	return s
}

// fileMetaInScope keeps a session only if it worked on this exact path (an
// absolute path cannot collide across projects) or if it belongs to the
// project being worked in now. It is what stops a same-named file in an
// unrelated repo from being counted.
func fileMetaInScope(meta index.SessionMeta, path string, projects []string) bool {
	for _, t := range meta.Touched {
		if t == path {
			return true
		}
	}
	proj := strings.TrimPrefix(meta.Project, "imported:")
	for _, cand := range projects {
		if cand == "" {
			continue
		}
		if proj == cand || strings.HasSuffix(proj, "/"+cand) || strings.HasSuffix(cand, "/"+proj) {
			return true
		}
	}
	return false
}

func baseName(p string) string { return index.CrossBase(p) }

// inspectionCommand reports whether the command only looks at state rather than
// changing it — the class whose reuse-count says nothing worth an injection.
func inspectionCommand(cmd string) bool {
	f := strings.Fields(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cmd), "$ ")))
	if len(f) == 0 {
		return true
	}
	switch f[0] {
	case "ls", "cat", "pwd", "echo", "which", "whoami", "date", "env", "printenv",
		"head", "tail", "less", "more", "stat", "file", "find", "tree", "df", "du",
		"ps", "top", "htop", "id", "uname", "hostname", "clear", "history":
		return true
	case "git":
		if len(f) > 1 {
			switch f[1] {
			case "status", "diff", "log", "show", "branch", "remote", "stash",
				"blame", "reflog", "describe", "rev-parse", "ls-files":
				return true
			}
		}
	}
	return false
}

// truncateToolLine caps the line at max bytes on a rune boundary, so a
// non-ASCII filename near the limit is not split mid-rune into a U+FFFD.
func truncateToolLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// shortHash fingerprints the injected line for per-session dedupe.
func shortHash(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 16)
}

// toolSessionCount words a count for a line read inside an action.
func toolSessionCount(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}
