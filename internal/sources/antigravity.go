package sources

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

var (
	antigravityRequestOpenRE  = regexp.MustCompile(`^\s*<USER_REQUEST>\s*`)
	antigravityRequestCloseRE = regexp.MustCompile(`\s*</USER_REQUEST>\s*$`)
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
		text, _ := m["content"].(string)
		if strings.TrimSpace(text) == "" {
			return
		}
		if role == "user" {
			text = cleanAntigravityUserContent(text)
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		text = capParsedMessage(text)
		t, _ := time.Parse(time.RFC3339Nano, str(m["created_at"]))
		if t.IsZero() {
			t = s.Started
		}
		s.Touch(t)
		// A step's kind decides what it is, not its source. Antigravity puts
		// prose and tool transcripts in the same MODEL stream, and reading
		// only the source made shell dumps into assistant speech: 333 of 369
		// MODEL rows on this machine, 90%, ranked as things the agent said.
		if role == "assistant" {
			s.Messages = append(s.Messages, antigravityStep(str(m["type"]), text, t)...)
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
		}
	}
	return []model.Session{s}, err
}

func antigravitySessionID(path string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
}

func cleanAntigravityUserContent(text string) string {
	for _, re := range antigravityUserBlockREs {
		text = re.ReplaceAllString(text, "")
	}
	text = antigravityRequestOpenRE.ReplaceAllString(text, "")
	text = antigravityRequestCloseRE.ReplaceAllString(text, "")
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
		if p := antigravityPath(text); p != "" && IndexToolPaths() {
			out = append(out, model.Message{Role: RoleFiles, Text: p, Time: t})
		}
	}
	if IndexToolOutput() {
		if body := antigravityBody(text); body != "" {
			out = append(out, model.Message{Role: RoleToolOutput, Text: body, Time: t})
		}
	}
	return out
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

// antigravityPath pulls the file a step names, as a plain path: the transcript
// writes them as file:// URIs, sometimes in backticks.
func antigravityPath(text string) string {
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
			return strings.Trim(p, "`")
		}
	}
	return ""
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
				if w, ok := strings.CutPrefix(uri, "file://"); ok && w != "" {
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
// holds whatever the machine that wrote it used.
func isAbsolutePath(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	// C:\src or C:/src
	return len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
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
