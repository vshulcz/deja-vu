package sources

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

var (
	antigravityRequestTagRE = regexp.MustCompile(`[ \t]*\n?\s*</?USER_REQUEST>\s*`)
	// "Comments on artifact URI: file:///…/implementation_plan.md" and the
	// sentence under it, which is the IDE recording what the person clicked.
	antigravityArtifactCommentRE = regexp.MustCompile(`(?m)^Comments on artifact URI:.*$|^The user has (?:approved|rejected) this document\.$`)
)

var antigravityUserBlockREs = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`),
	regexp.MustCompile(`(?s)<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>`),
}

func AntigravityRoots() []string {
	if v := os.Getenv("DEJA_ANTIGRAVITY_ROOT"); v != "" {
		return []string{v}
	}
	roots, err := filepath.Glob(filepath.Join(Home(), ".gemini", "antigravity*"))
	if err != nil {
		return nil
	}
	var out []string
	for _, root := range roots {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			out = append(out, root)
		}
	}
	return out
}

// AntigravitySidecarFiles lists what an Antigravity store keeps beside the
// transcript deja reads: the full log, the chunk files it is written in, the
// message records and the metadata beside a plan. Everything lives under
// `.system_generated`, so the row could say nothing about any of it until
// #3377.
func AntigravitySidecarFiles() []string {
	var out []string
	for _, root := range AntigravityRoots() {
		out = append(out, walkFiles(root, func(p string) bool {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return false
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			// Outside a conversation: the settings, the caches, the update
			// marker, the MCP and plugin files deja itself installs. Named one
			// by one — exempting everything outside brain/ swallowed a
			// transcript restored beside it, which is what this row exists to
			// find (review of #3377).
			if parts[0] != "brain" {
				switch parts[0] {
				case "cache", "mcp", "plugins", "updater", "tmp":
					return true
				}
				switch filepath.Base(p) {
				case "settings.json", "history.jsonl", "import_manifest.json":
					return true
				}
				return false
			}
			base := filepath.Base(p)
			dir := filepath.ToSlash(filepath.Dir(p))
			switch {
			case base == "read.json", base == "remember.json":
				return true
			case strings.HasSuffix(base, ".metadata.json"):
				return true
			case base == "transcript_full.jsonl":
				// Only when it is the second copy of a log deja does read: a
				// session whose only artefact is the full transcript has its
				// content nowhere else, and the row must say so.
				return fileExists(filepath.Join(filepath.Dir(p), "transcript.jsonl"))
			}
			return strings.Contains(dir, "/.system_generated/messages") ||
				strings.Contains(dir, "/logs/chunks/")
		})...)
	}
	return out
}

func AntigravityTranscripts() []string {
	var out []string
	for _, root := range AntigravityRoots() {
		matches, err := filepath.Glob(filepath.Join(root, "brain", "*", ".system_generated", "logs", "transcript.jsonl"))
		if err == nil {
			out = append(out, keepRegular(matches)...)
		}
	}
	return out
}

func LoadAntigravity() []model.Session {
	return parseFiles(AntigravityTranscripts(), ParseAntigravityFile)
}

func ParseAntigravityFile(path string) ([]model.Session, error) {
	id := antigravitySessionID(path)
	if id == "" || id == "." || id == string(filepath.Separator) {
		return nil, nil
	}
	s := model.Session{Harness: "antigravity", ID: id, Project: antigravityProject(id), Path: path}
	// Records already taken from a planner's tool_calls, so the step that
	// runs the call and names it again in its header is not a second run.
	fromCalls := map[model.Message]int{}
	// What a write_to_file call is about to write, by file, until the step
	// that ran it says it did (#4528).
	pendingWrites := map[string]string{}
	cwd := ""
	// The commands the latest planner row asked for, where they sit, waiting
	// for the step that says how each ended. A step carries no call id, so
	// it answers the call it follows (#4530).
	var running []int
	asked := 0
	err := scanJSONLFromOffset(path, 0, func(m map[string]any) {
		role := ""
		source, _ := m["source"].(string)
		switch source {
		case "USER_EXPLICIT":
			role = "user"
		case "MODEL":
			role = "assistant"
		default:
			return
		}
		t, _ := time.Parse(time.RFC3339Nano, str(m["created_at"]))
		if t.IsZero() {
			t = s.Started
		}
		var calls []model.Message
		if role == "assistant" {
			var c string
			calls, c = antigravityToolCalls(m["tool_calls"], t)
			if cwd == "" {
				cwd = c
			}
			if calls, _ := m["tool_calls"].([]any); len(calls) > 0 {
				// A planner row's steps follow it before the next row: a
				// write still waiting here is one whose step failed.
				clear(pendingWrites)
				antigravityNoteWrites(m["tool_calls"], pendingWrites)
			}
		}
		text, _ := m["content"].(string)
		if role == "user" {
			text = cleanAntigravityUserContent(text)
		}
		if strings.TrimSpace(text) == "" && len(calls) == 0 {
			return
		}
		s.Touch(t)
		if str(m["type"]) == "PLANNER_RESPONSE" {
			running = nil
			asked = antigravityRunCalls(m["tool_calls"])
			for i, c := range calls {
				if c.Role == RoleCommand {
					running = append(running, len(s.Messages)+i)
				}
			}
			// The speech, when there is any, goes in ahead of the calls.
			if strings.TrimSpace(text) != "" {
				for i := range running {
					running[i]++
				}
			}
		}
		if strings.TrimSpace(text) == "" {
			s.Messages = append(s.Messages, antigravityTakeCalls(calls, fromCalls)...)
			return
		}
		text = capParsedMessage(text)
		// A step's kind decides what it is, not its source. Antigravity puts
		// prose and tool transcripts in the same MODEL stream, and reading
		// only the source made shell dumps into assistant speech: 333 of 369
		// MODEL rows on this machine, 90%, ranked as things the agent said.
		if role == "assistant" {
			step := antigravityStep(str(m["type"]), text, t)
			own := -1
			for _, rec := range step {
				key := model.Message{Role: rec.Role, Text: rec.Text}
				if (rec.Role == RoleCommand || rec.Role == RoleFiles) && fromCalls[key] > 0 {
					fromCalls[key]--
					continue
				}
				if rec.Role == RoleCommand {
					own = len(s.Messages)
				}
				s.Messages = append(s.Messages, rec)
			}
			if code, ok := antigravityExitCode(str(m["type"]), text); ok {
				running = antigravityStampExit(s.Messages, running, asked, own, antigravityField(text, "Task Description:"), code)
			}
			if str(m["type"]) == "CODE_ACTION" && str(m["status"]) == "DONE" {
				s.Messages = append(s.Messages, antigravityTakeWrite(text, step, pendingWrites, t)...)
			}
			s.Messages = append(s.Messages, antigravityTakeCalls(calls, fromCalls)...)
			return
		}
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	if s.Project == "-" || s.Project == "" {
		if p := antigravityProjectFromFiles(s.Messages); p != "" {
			s.Project = p
		} else if cwd != "" {
			s.Project = projectName(cwd)
		}
	}
	return []model.Session{s}, err
}

func antigravitySessionID(path string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
}

func cleanAntigravityUserContent(text string) string {
	// The IDE's own blocks can sit inside the request tag as well as beside it.
	for _, re := range antigravityUserBlockREs {
		text = re.ReplaceAllString(text, "")
	}
	// The comment the IDE writes when a document is approved or rejected: it
	// stands above an empty <USER_REQUEST>, and taking the tag off only when it
	// opened the content indexed that comment as the person's words (#3326).
	text = antigravityArtifactCommentRE.ReplaceAllString(text, "")
	// The tags come off wherever they stand and whatever is around them stays:
	// a person can write above the tag, and a turn can carry two of them.
	text = antigravityRequestTagRE.ReplaceAllString(text, "\n")
	return strings.TrimSpace(text)
}

// antigravityStep turns one MODEL step into the records it actually is.
//
// Only PLANNER_RESPONSE is the agent talking — 36 of 369 rows here. The rest
// are tool transcripts, and they carry the work in a readable header: a
// RUN_COMMAND names its command on a "Task Description:" line, VIEW_FILE and
// CODE_ACTION name their file. So the same change that stops shell output
// being ranked as speech also gives antigravity the file and command records
// the other harnesses with work records have.
func antigravityStep(kind, text string, t time.Time) []model.Message {
	if kind == "PLANNER_RESPONSE" || kind == "" {
		return []model.Message{{Role: "assistant", Text: text, Time: t}}
	}
	var out []model.Message
	switch kind {
	case "RUN_COMMAND", "GENERIC":
		if cmd := antigravityField(text, "Task Description:"); cmd != "" && IndexCommands() && worthIndexing(cmd) {
			out = append(out, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: t})
		}
	case "VIEW_FILE", "CODE_ACTION", "LIST_DIRECTORY":
		p := antigravityPath(text)
		if p != "" && IndexToolPaths() {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
		// An edit tool writes the change as a diff block; the removed lines
		// are the span the other harnesses record as an edit (#3279).
		if p != "" && IndexEdits() {
			if span := antigravityRemovedLines(text); span != "" {
				out = append(out, model.Message{Role: RoleEdit, Text: p + "\n" + span, Time: t})
			}
		}
		// The added lines of the same block are the written side, which is what
		// attribution reads: a step that only adds lines has no removed side at
		// all (#3773).
		if p != "" && IndexWrites() {
			if rec := WroteRecord(p, antigravityAddedLines(text)); rec != "" {
				out = append(out, model.Message{Role: RoleWrote, Text: rec, Time: t})
			}
		}
	}
	if IndexToolOutput() {
		if body := antigravityBody(text); body != "" {
			out = append(out, model.Message{Role: RoleToolOutput, Text: body, Time: t})
		}
	}
	return out
}

// antigravityToolCalls reads the structured calls a planner row carries —
// the args the client hands its own PreToolUse hooks. The RUN_COMMAND step
// after a call does not always name the command, and without this the command
// was lost whenever it did not (#4358). The edit tools' diff stays with their
// CODE_ACTION step; the call gives only the file. Returns the first Cwd too.
func antigravityToolCalls(v any, t time.Time) ([]model.Message, string) {
	calls, _ := v.([]any)
	var out []model.Message
	cwd := ""
	for _, c := range calls {
		call, _ := c.(map[string]any)
		args, _ := call["args"].(map[string]any)
		if args == nil {
			continue
		}
		switch str(call["name"]) {
		case "run_command":
			cmd := strings.TrimSpace(antigravityArg(args, "CommandLine"))
			if cwd == "" {
				cwd = antigravityArg(args, "Cwd")
			}
			if cmd != "" && IndexCommands() && worthIndexing(cmd) {
				out = append(out, model.Message{Role: RoleCommand, Text: "$ " + cmd, Time: t})
			}
		case "view_file", "replace_file_content", "multi_replace_file_content", "write_to_file":
			p := antigravityArg(args, "AbsolutePath")
			if p == "" {
				p = antigravityArg(args, "TargetFile")
			}
			p = decodeURIPath(strings.TrimPrefix(p, "file://"))
			if p != "" && IndexToolPaths() {
				out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
			}
		}
	}
	return out, cwd
}

// antigravityExitCode reads how a command ended off the step that ran it: a
// header line "The command exited with code N.", above the step's Output:.
func antigravityExitCode(kind, text string) (int, bool) {
	if kind != "GENERIC" && kind != "RUN_COMMAND" {
		return 0, false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// The header ends where the output starts, "Output:" or the
		// Stdout:/Stderr: pair; a line in the output is not the status.
		if line == "Output:" || strings.HasPrefix(line, "Stdout:") || strings.HasPrefix(line, "Stderr:") {
			break
		}
		if code, ok := statusCode(line, "The command exited with code ", "."); ok {
			return code, true
		}
	}
	return 0, false
}

// antigravityStampExit marks the command a step's exit belongs to: the one
// the step itself recorded, else the planner's call it names, else the
// planner's only call. With two calls and no name the code is left off
// rather than guessed, counting a call too trivial to record: its step would
// otherwise stamp the one that was. Returns the calls still waiting.
func antigravityStampExit(msgs []model.Message, running []int, asked, own int, named string, code int) []int {
	at := -1
	switch {
	case own >= 0:
		at = own
	case named != "":
		for k, i := range running {
			if msgs[i].Text == "$ "+named {
				at = i
				running = append(running[:k:k], running[k+1:]...)
				break
			}
		}
	case len(running) == 1 && asked == 1:
		at, running = running[0], nil
	}
	if at >= 0 && at < len(msgs) && !strings.Contains(msgs[at].Text, "  → exit ") {
		msgs[at].Text += fmt.Sprintf("  → exit %d", code)
	}
	return running
}

// antigravityRunCalls counts a planner row's run_command calls, recorded or
// not.
func antigravityRunCalls(v any) int {
	calls, _ := v.([]any)
	n := 0
	for _, c := range calls {
		if call, _ := c.(map[string]any); str(call["name"]) == "run_command" {
			n++
		}
	}
	return n
}

// antigravityNoteWrites keeps the content of the latest write_to_file call
// for each file it names. The CODE_ACTION step that runs the call says
// "Created file" and carries no diff block — 1 of 50 did on the store this was read off — so
// CodeContent is the only record of what the file was given (#4528).
func antigravityNoteWrites(v any, pending map[string]string) {
	calls, _ := v.([]any)
	for _, c := range calls {
		call, _ := c.(map[string]any)
		args, _ := call["args"].(map[string]any)
		if args == nil || str(call["name"]) != "write_to_file" {
			continue
		}
		p := decodeURIPath(strings.TrimPrefix(antigravityArg(args, "TargetFile"), "file://"))
		if p != "" {
			pending[p] = antigravityArg(args, "CodeContent")
		}
	}
}

// antigravityTakeWrite is the wrote record of a write_to_file call, once a
// finished CODE_ACTION step names its file. A step that failed names none, so
// a write that never happened is not recorded; one whose step had a diff
// block already gave its written side. The step answers the latest call for
// its file: an earlier one still waiting is a call whose step failed, and
// its content never reached the file.
func antigravityTakeWrite(text string, step []model.Message, pending map[string]string, t time.Time) []model.Message {
	// Only the step that created the file: a replace step on the same path
	// with no diff block did not run the write.
	if antigravityField(text, "Created file") == "" {
		return nil
	}
	p := antigravityPath(text)
	content, ok := pending[p]
	if !ok {
		return nil
	}
	delete(pending, p)
	for _, rec := range step {
		if rec.Role == RoleWrote {
			return nil
		}
	}
	if rec := WroteRecord(p, content); rec != "" && IndexWrites() {
		return []model.Message{{Role: RoleWrote, Text: rec, Time: t}}
	}
	return nil
}

// antigravityArg reads one call argument. On disk each value is JSON in its
// own right — CommandLine is `"go test ./..."` with the quotes — so a value
// that decodes as a JSON string is that string; a bare one is taken as it is.
func antigravityArg(args map[string]any, key string) string {
	v := str(args[key])
	var decoded string
	if strings.HasPrefix(v, `"`) && json.Unmarshal([]byte(v), &decoded) == nil {
		return decoded
	}
	return v
}

// antigravityTakeCalls keeps the records from a row's calls and notes each,
// so the step that later names the same call adds nothing.
func antigravityTakeCalls(calls []model.Message, seen map[model.Message]int) []model.Message {
	for _, c := range calls {
		seen[model.Message{Role: c.Role, Text: c.Text}]++
	}
	return calls
}

// antigravityField reads a labelled line out of a step's header.
func antigravityField(text, label string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, label); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// antigravityEditSentence is how the edit tools name their file: "The
// following changes were made by the replace_file_content tool to: <path>."
// — a sentence, not a labelled line, so the label list never saw an edit
// (#3279).
var antigravityEditSentence = regexp.MustCompile(`changes were made by the \S+ tool to: (\S+?)\.?(?:\s|$)`)

// antigravityAddedLines is the written side of the same block: its "+" lines,
// minus the "+++ b/x" header, which is not a line of the file.
func antigravityAddedLines(text string) string {
	var lines []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "[diff_block_start]"):
			in = true
		case strings.HasPrefix(line, "[diff_block_end]"):
			in = false
		case in && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			lines = append(lines, line[1:])
		}
	}
	return strings.Join(lines, "\n")
}

// antigravityRemovedLines is the span an edit took out: the "-" lines of the
// step's diff block, without the hunk headers, bounded like every edit span.
func antigravityRemovedLines(text string) string {
	var lines []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "[diff_block_start]"):
			in = true
		case strings.HasPrefix(line, "[diff_block_end]"):
			in = false
		case in && strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			lines = append(lines, line[1:])
		}
	}
	span := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(span) > editSpanMax {
		span = span[:editSpanMax]
	}
	return span
}

// antigravityPath pulls the file a step names, as a plain path: the transcript
// writes them as file:// URIs, sometimes in backticks.
func antigravityPath(text string) string {
	if m := antigravityEditSentence.FindStringSubmatch(text); m != nil {
		return decodeURIPath(strings.TrimPrefix(strings.Trim(m[1], "`"), "file://"))
	}
	for _, label := range []string{"File Path:", "Created file", "Edited file", "Modified file"} {
		v := antigravityField(text, label)
		if v == "" {
			continue
		}
		v = strings.Trim(v, "`")
		if i := strings.Index(v, " "); i > 0 && strings.HasPrefix(v, "file://") {
			v = v[:i]
		}
		if p, ok := strings.CutPrefix(v, "file://"); ok {
			return decodeURIPath(strings.Trim(p, "`"))
		}
	}
	return ""
}

// decodeURIPath undoes the percent-encoding a file:// URI carries. The scheme
// is stripped textually above, which leaves `%20` where a space was — the same
// shape that put `/c%3A/Users/me/my%20app/main.go` into a Copilot Chat files
// record until #3498 parsed the URI instead of slicing it.
//
// Nothing on this machine exercises it: of 329 file:// URIs in its Antigravity
// transcripts, none is encoded, because no path here has a space in it. That is
// the blind spot #3505 is about, so the decode goes in on the writer's shape
// rather than on what one laptop happens to hold.
//
// A path that is not valid escaping — a literal `%` in a filename — is left as
// it is, because there the escape was never an escape.
func decodeURIPath(p string) string {
	if !strings.Contains(p, "%") {
		return p
	}
	decoded, err := url.PathUnescape(p)
	if err != nil {
		return p
	}
	// A decoded newline would split one path into two lines of a files record,
	// which is the guard copilotChatRefPath already has.
	if strings.ContainsAny(decoded, "\n\r") {
		return p
	}
	return decoded
}

// antigravityBody drops the timestamps every step opens with. Keeping them
// leaves records whose whole content is "Created At: … Completed At: …".
func antigravityBody(text string) string {
	lines := strings.Split(text, "\n")
	cut := 0
	for cut < len(lines) {
		l := strings.TrimSpace(lines[cut])
		if l == "" || strings.HasPrefix(l, "Created At:") || strings.HasPrefix(l, "Completed At:") {
			cut++
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines[cut:], "\n"))
}

// antigravityProject resolves a conversation to the workspace it belongs to.
//
// The parser hardcoded "-" for every session, so antigravity was unreachable
// from any `--project` query — and the workspace was sitting in plain JSON in
// a directory deja already walks, no protobuf involved.
func antigravityProject(id string) string {
	for _, root := range AntigravityRoots() {
		p := filepath.Join(root, "cache", "conversation_metadata.json")
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var doc struct {
			Conversations map[string]struct {
				Summary struct {
					WorkspaceURIs []string `json:"WorkspaceURIs"`
				} `json:"summary"`
			} `json:"conversations"`
		}
		if json.Unmarshal(b, &doc) != nil {
			continue
		}
		for key, c := range doc.Conversations {
			if !strings.HasPrefix(key, id) && !strings.HasPrefix(id, key) {
				continue
			}
			for _, uri := range c.Summary.WorkspaceURIs {
				if w, ok := fileURIPath(uri); ok {
					return projectName(w)
				}
			}
		}
	}
	// conversation_metadata.json is written by the IDE. The CLI never appears
	// in it, so every `agy` session landed with no project — and a session
	// with no project is invisible to recall, which ranks within the project
	// the user is in. The CLI records the mapping in its own cache instead.
	for _, root := range AntigravityRoots() {
		b, err := os.ReadFile(filepath.Join(root, "cache", "last_conversations.json"))
		if err != nil {
			continue
		}
		var byWorkspace map[string]string
		if json.Unmarshal(b, &byWorkspace) != nil {
			continue
		}
		for workspace, conv := range byWorkspace {
			if workspace == "" {
				continue
			}
			if strings.HasPrefix(conv, id) || strings.HasPrefix(id, conv) {
				return projectName(workspace)
			}
		}
	}
	return "-"
}

// isAbsolutePath accepts both conventions, not the host's. A synced store
// holds whatever the machine that wrote it used. A leading `\` is rooted too:
// Windows reads \tmp\x against the current drive, never against a cwd.
func isAbsolutePath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	// C:\src or C:/src
	return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
}

// resolveToolPath puts a tool call's path relative to the session's cwd onto
// that cwd, and leaves a rooted one as written. filepath.IsAbs is the host's
// rule, and on Windows it is false for /tmp/proj/retry.go, which then became
// \tmp\proj\tmp\proj\retry.go (#4438). A slash-rooted cwd is joined with
// slashes so the record reads the same whichever host indexed it.
func resolveToolPath(p, cwd string) string {
	if p == "" || cwd == "" || isAbsolutePath(p) {
		return p
	}
	if strings.HasPrefix(cwd, "/") {
		return path.Join(cwd, p)
	}
	return filepath.Join(cwd, p)
}

// slashed puts a path in one convention so the segment arithmetic below reads
// the same on either host. Go's own path handling accepts forward slashes on
// Windows, so nothing has to be converted back.
func slashed(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// antigravityProjectFromFiles reads the project off the work itself.
//
// The CLI's cache holds only the newest conversation per workspace, so
// yesterday's sessions — the ones recall exists to surface — have no entry by
// the time they matter. The files a session opened do not expire: the deepest
// directory shared by all of them is the checkout it ran in.
func antigravityProjectFromFiles(messages []model.Message) string {
	var common []string
	for _, m := range messages {
		// Absolute either way round: a store synced from Windows holds
		// C:\src\main.go, and requiring a leading slash dropped every one of
		// those — on Windows itself, no CLI session would ever find its
		// project. Split on both separators for the same reason CrossBase
		// exists.
		if m.Role != RoleFiles || !isAbsolutePath(m.Text) {
			continue
		}
		parts := strings.Split(path.Dir(slashed(m.Text)), "/")
		if common == nil {
			common = parts
			continue
		}
		n := 0
		for n < len(common) && n < len(parts) && common[n] == parts[n] {
			n++
		}
		common = common[:n]
	}
	// Three segments past the root is "/Users/<name>" — a home directory,
	// which names no project.
	if len(common) < 4 {
		return ""
	}
	dir := strings.Join(common, "/")
	// A session that touched one file deep in a tree shares only that file's
	// own directory, which is a package and not the project. The checkout
	// above it is the answer whenever it is still on disk.
	for probe, depth := dir, len(common); depth >= 4; depth-- {
		if fi, err := os.Stat(filepath.Join(probe, ".git")); err == nil && (fi.IsDir() || fi.Mode().IsRegular()) {
			return projectName(probe)
		}
		probe = path.Dir(probe)
	}
	return projectName(dir)
}
