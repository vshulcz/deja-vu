package sources

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// pi and the agents built on its session format (omp, gjc, Kimchi, Senpi)
// keep a session as <ts>_<id>.jsonl under a folder named after the project
// directory: a `session` header with the id and cwd, then message entries
// chained by parentId. The header's cwd is gone with the file, so it is
// decoded from the folder name when that directory still exists, and left
// out otherwise. gjc names the folder with a hash and keeps the directory in
// a scope file beside the sessions, and refuses a header without one.
func init() {
	for harness, roots := range map[string]func() []string{
		"pi":     func() []string { return []string{PiRoot()} },
		"omp":    OmpSessionRoots,
		"gjc":    func() []string { return []string{GjcRoot()} },
		"kimchi": func() []string { return []string{KimchiRoot()} },
		"senpi":  func() []string { return []string{SenpiRoot()} },
	} {
		registerWriteBack(harness, roots, renderPiShaped)
	}
}

func renderPiShaped(s model.Session, turns []model.Message) (string, []byte, error) {
	if s.Kind == "subagent" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "session is a sub-agent run, which " + s.Harness + " does not reopen on its own"}
	}
	if !strings.HasSuffix(s.Path, ".jsonl") {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "the index has no .jsonl path for this session, which is where " + s.Harness + " keeps one"}
	}
	start := turns[0].Time
	if !s.Started.IsZero() && s.Started.Before(start) {
		start = s.Started
	}
	header := map[string]any{
		"type":      "session",
		"version":   3,
		"id":        s.ID,
		"timestamp": writeBackStamp(start),
	}
	if s.Harness == "gjc" {
		// gjc migrates an older header in place, rewriting the whole file,
		// and the index would then read it again from the top.
		header["version"] = 5
		header["starredPatchVersion"] = 1
	}
	cwd := piFamilyCWD(s)
	if cwd == "" && s.Harness == "gjc" {
		return "", nil, &WriteBackRefusal{Harness: s.Harness, Reason: "gjc opens a session only with the directory it ran in, and the scope file beside the session that names it (.gjc-managed-session-scope.v2.json) is gone"}
	}
	if cwd != "" {
		header["cwd"] = cwd
	}
	lines := []any{header}
	parent := any(nil)
	for i, m := range turns {
		id := strings.ReplaceAll(writeBackUUID(s.ID, i), "-", "")[:8]
		msg := map[string]any{
			"role":      m.Role,
			"content":   []any{map[string]any{"type": "text", "text": m.Text}},
			"timestamp": m.Time.UnixMilli(),
		}
		if m.Role == "assistant" {
			msg["usage"] = map[string]any{
				"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "totalTokens": 0,
				"cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0},
			}
			msg["stopReason"] = "stop"
		}
		lines = append(lines, map[string]any{
			"type":      "message",
			"id":        id,
			"parentId":  parent,
			"timestamp": writeBackStamp(m.Time),
			"message":   msg,
		})
		parent = id
	}
	data, err := jsonLines(lines)
	return s.Path, data, err
}

// piFamilyCWD is the directory a deleted pi-family session ran in, as far as
// what is left on disk says.
func piFamilyCWD(s model.Session) string {
	dir := filepath.Dir(s.Path)
	if s.Harness == "gjc" {
		scopes, _ := filepath.Glob(filepath.Join(dir, ".gjc-managed-session-scope*.json"))
		for _, p := range scopes {
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var scope struct {
				CanonicalPath string `json:"canonicalPath"`
			}
			if json.Unmarshal(raw, &scope) == nil && scope.CanonicalPath != "" {
				return scope.CanonicalPath
			}
		}
		return ""
	}
	return ResolveEncodedPath(filepath.Base(dir))
}
