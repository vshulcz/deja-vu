package sources

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Claude Code deletes transcripts after cleanupPeriodDays, and a setting that
// fails to load falls back to 30. ~/.claude/history.jsonl is left alone: one
// line per typed prompt with the session id, the directory and the pasted
// text. For a session whose transcript is gone that is the user's half of the
// conversation, and deja read nothing of it (#5046).

// ClaudeHistoryFile is Claude Code's prompt log. DEJA_CLAUDE_HISTORY_FILE
// moves it; with DEJA_CLAUDE_ROOT set and no file named there is none, so a
// stand pinned to its own store does not read the machine's prompts.
func ClaudeHistoryFile() string {
	if p := os.Getenv("DEJA_CLAUDE_HISTORY_FILE"); p != "" {
		return p
	}
	if os.Getenv("DEJA_CLAUDE_ROOT") != "" {
		return ""
	}
	return filepath.Join(ClaudeConfigDir(), "history.jsonl")
}

// IsClaudeHistory reports whether a path is Claude Code's prompt log.
func IsClaudeHistory(p string) bool {
	h := ClaudeHistoryFile()
	return h != "" && filepath.Clean(p) == filepath.Clean(h)
}

// IsPromptLog reports whether a session was read from a prompt log rather than
// a transcript: Claude Code's, Codex's or TRAE's history.jsonl. Such a session
// holds what was typed and nothing the agent said or did. Claude Code's can be
// moved under another name with DEJA_CLAUDE_HISTORY_FILE.
func IsPromptLog(p string) bool {
	return strings.EqualFold(filepath.Base(p), "history.jsonl") || IsClaudeHistory(p)
}

// PromptLogSessionIDs is the set of sessions the lines of a prompt log name
// between two offsets: session_id in Codex's and TRAE's, sessionId in Claude
// Code's. A pass that holds the log up to from reads it whole for these alone,
// so a new prompt replaces its own session and not every one the log holds
// (#5403). Lines that do not decode name nothing.
func PromptLogSessionIDs(path string, from, to int64) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	r := bufio.NewReader(io.LimitReader(f, max(to-from, 0)))
	for {
		line, err := r.ReadBytes('\n')
		if line = trimJSONSpace(line); len(line) > 0 {
			var l struct {
				Codex  string `json:"session_id"`
				Claude string `json:"sessionId"`
			}
			if json.Unmarshal(line, &l) == nil {
				for _, id := range []string{l.Codex, l.Claude} {
					if id != "" {
						ids[id] = true
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return ids, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func ParseClaudeHistory(path string) ([]model.Session, error) {
	return ParseClaudeHistoryFromOffset(path, 0)
}

// ParseClaudeHistoryFromOffset reads the prompts of sessions with no
// transcript on disk. A session that still has one is skipped: the transcript
// holds the same prompt at its own instant, so the pair never collapses and
// the question would be asked twice. A transcript deja kept after Claude Code
// deleted it is not on disk, and the index drops the prompts for it there.
func ParseClaudeHistoryFromOffset(path string, offset int64) ([]model.Session, error) {
	var out []model.Session
	at := map[string]int{}
	live := map[string]bool{}
	err := scanJSONLFromOffset(path, offset, func(m map[string]any) {
		id, _ := m["sessionId"].(string)
		cwd, _ := m["project"].(string)
		txt := claudeHistoryText(m)
		if id == "" || strings.TrimSpace(txt) == "" {
			return
		}
		has, seen := live[id]
		if !seen {
			has = claudeTranscriptOnDisk(id, cwd)
			live[id] = has
		}
		if has {
			return
		}
		t := parseTimeAny(m["timestamp"])
		i, ok := at[id]
		if !ok {
			i = len(out)
			at[id] = i
			out = append(out, model.Session{Harness: "claude", ID: id, Project: cwdProjectName(cwd), Path: path})
		}
		out[i].Touch(t)
		out[i].Messages = append(out[i].Messages, model.Message{Role: "user", Text: txt, Time: t})
	})
	return out, err
}

// pastedRef is how the prompt line names a paste: "[Pasted text #2 +40 lines]".
var pastedRef = regexp.MustCompile(`\[Pasted text #(\d+)[^\]]*\]`)

// claudeHistoryText is the prompt as typed, with each paste put back where the
// line names it. A paste the line does not name goes after it. A large paste
// is stored by hash with no content and stays the reference it was.
func claudeHistoryText(m map[string]any) string {
	display, _ := m["display"].(string)
	pasted, _ := m["pastedContents"].(map[string]any)
	if len(pasted) == 0 {
		return display
	}
	content := map[string]string{}
	var keys []string
	for k, v := range pasted {
		p, _ := v.(map[string]any)
		if typ, _ := p["type"].(string); typ != "" && typ != "text" {
			continue
		}
		c, _ := p["content"].(string)
		if c == "" {
			continue
		}
		id := k
		if n, ok := p["id"].(float64); ok {
			id = strconv.Itoa(int(n))
		}
		content[id] = c
		keys = append(keys, id)
	}
	used := map[string]bool{}
	text := pastedRef.ReplaceAllStringFunc(display, func(ref string) string {
		id := pastedRef.FindStringSubmatch(ref)[1]
		if c, ok := content[id]; ok {
			used[id] = true
			return c
		}
		return ref
	})
	sort.Slice(keys, func(i, j int) bool {
		a, _ := strconv.Atoi(keys[i])
		b, _ := strconv.Atoi(keys[j])
		return a < b
	})
	for _, k := range keys {
		if !used[k] {
			text += "\n\n" + content[k]
		}
	}
	return text
}

// claudeTranscriptOnDisk reports whether a session still has its transcript
// under any Claude root, in the folder Claude Code names after the directory
// it ran in. A name longer than 200 characters is cut there and given a hash
// suffix, so that one is matched by its cut.
func claudeTranscriptOnDisk(id, cwd string) bool {
	file := id + ".jsonl"
	enc := claudeEncodedDir(cwd)
	for _, root := range ClaudeRoots() {
		if fileExists(filepath.Join(root, file)) {
			return true
		}
		if enc == "" {
			continue
		}
		if fileExists(filepath.Join(root, enc, file)) {
			return true
		}
		if len(enc) > claudeDirNameMax {
			entries, _ := os.ReadDir(root)
			for _, e := range entries {
				if e.IsDir() && strings.HasPrefix(e.Name(), enc[:claudeDirNameMax]+"-") && fileExists(filepath.Join(root, e.Name(), file)) {
					return true
				}
			}
		}
	}
	return false
}

const claudeDirNameMax = 200

// claudeEncodedDir is the project folder name Claude Code gives a directory:
// every UTF-16 unit outside [A-Za-z0-9] becomes a dash.
func claudeEncodedDir(cwd string) string {
	if cwd == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range cwd {
		switch {
		case r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'):
			b.WriteRune(r)
		case utf16.RuneLen(r) == 2:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
