package sources

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// aider writes one markdown file per launch directory, appending forever:
// sessions start with "# aider chat started at <ts>", user input lines are
// prefixed "#### ", tool/system output "> ", and assistant output is raw
// markdown in between. Fenced code blocks may contain any of those prefixes,
// so the parser tracks fences.

const aiderHistoryName = ".aider.chat.history.md"

// AiderFiles returns history files to index: $HOME plus any directories
// listed in DEJA_AIDER_ROOTS (colon-separated, scanned two levels deep —
// scanning all of $HOME would make warmup unusable).
func AiderFiles() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if seen[p] {
			return
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(filepath.Join(Home(), aiderHistoryName))
	// aider maps every flag to an env var; --chat-history-file is the
	// documented way to move the history off the default name.
	if p := os.Getenv("AIDER_CHAT_HISTORY_FILE"); p != "" {
		add(p)
	}
	for _, root := range filepath.SplitList(os.Getenv("DEJA_AIDER_ROOTS")) {
		if root == "" {
			continue
		}
		add(filepath.Join(root, aiderHistoryName))
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			add(filepath.Join(root, e.Name(), aiderHistoryName))
			sub, err := os.ReadDir(filepath.Join(root, e.Name()))
			if err != nil {
				continue
			}
			for _, s := range sub {
				if s.IsDir() {
					add(filepath.Join(root, e.Name(), s.Name(), aiderHistoryName))
				}
			}
		}
	}
	return out
}

func LoadAider() []model.Session {
	return parseFiles(AiderFiles(), ParseAiderFile)
}

const aiderSessionMark = "# aider chat started at "

func ParseAiderFile(path string) ([]model.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	project := projectName(filepath.Dir(path))
	var out []model.Session
	var cur *model.Session
	var role string
	var buf []string
	inFence := false
	idx := 0

	flush := func() {
		if cur == nil || len(buf) == 0 {
			role, buf = "", nil
			return
		}
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" && role != "" {
			cur.Messages = append(cur.Messages, model.Message{Role: role, Text: text, Time: cur.Started})
		}
		role, buf = "", nil
	}
	endSession := func() {
		flush()
		if cur != nil && len(cur.Messages) > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}

	// Unbounded line reader: a single pasted blob can exceed any fixed
	// scanner cap, which would silently drop every session after it. Read
	// line by line with no cap, matching the other parsers.
	r := bufio.NewReader(f)
	for {
		raw, readErr := r.ReadString('\n')
		if raw == "" && readErr != nil {
			break
		}
		line := strings.TrimRight(raw, " \t\r\n")
		if !inFence && strings.HasPrefix(line, aiderSessionMark) {
			endSession()
			idx++
			ts, _ := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(strings.TrimPrefix(line, aiderSessionMark)), time.Local)
			id := aiderSessionID(path, idx)
			cur = &model.Session{Harness: "aider", ID: id, Project: project, Path: path, Started: ts, Updated: ts}
			inFence = false
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			if role == "" {
				role = "assistant"
			}
			buf = append(buf, line)
			continue
		}
		if inFence {
			buf = append(buf, line)
			continue
		}
		switch {
		case strings.HasPrefix(line, "#### "):
			if role != "user" {
				flush()
				role = "user"
			}
			t := strings.TrimPrefix(line, "#### ")
			if t == "<blank>" {
				t = ""
			}
			buf = append(buf, t)
		case strings.HasPrefix(line, "> "), line == ">":
			// tool/system output: ends any assistant block, not indexed as a message
			flush()
		case strings.TrimSpace(line) == "":
			buf = append(buf, "")
		default:
			if role != "assistant" {
				flush()
				role = "assistant"
			}
			buf = append(buf, line)
		}
	}
	endSession()
	return out, nil
}

// aider has no session ids; derive a stable one from file path + ordinal.
func aiderSessionID(path string, idx int) string {
	h := sha1.Sum([]byte(path))
	return "aider-" + hex.EncodeToString(h[:6]) + "-" + itoa(idx)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
