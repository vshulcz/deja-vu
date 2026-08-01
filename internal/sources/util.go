package sources

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

type Source struct{ Name, Root string }

func Home() string { h, _ := os.UserHomeDir(); return h }
func EnvPath(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseTimeAny(v any) time.Time {
	switch x := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t
		}
	case float64:
		return unixGuess(int64(x))
	case json.Number:
		n, _ := x.Int64()
		return unixGuess(n)
	}
	return time.Time{}
}

func unixGuess(n int64) time.Time {
	if n > 1e12 {
		return time.UnixMilli(n)
	}
	if n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

// textFromContentKind is textFromContent plus the attribution question: did
// everything it joined come from tool results? Claude files those inside `user`
// messages, and labelling them as speech was wrong for 89% of that role (#559).
func textFromContentKind(v any) (string, bool) {
	c, ok := v.([]any)
	if !ok {
		return textFromContent(v), false
	}
	var sawTool, sawSpeech bool
	for _, it := range c {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		txt, _ := m["text"].(string)
		str, _ := m["content"].(string)
		if typ == "tool_result" {
			sawTool = true
		} else if txt != "" || str != "" {
			sawSpeech = true
		}
	}
	return textFromContent(v), sawTool && !sawSpeech
}

func textFromContent(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, it := range c {
			if m, ok := it.(map[string]any); ok {
				if txt, _ := m["text"].(string); txt != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(txt)
				} else if s, _ := m["content"].(string); s != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return ""
}

func projectName(path string) string {
	if path == "" {
		return "-"
	}
	return filepath.Base(path)
}

func scanJSONL(path string, fn func(map[string]any)) error {
	return scanJSONLFromOffset(path, 0, fn)
}

func scanJSONLFromOffset(path string, offset int64, fn func(map[string]any)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var m map[string]any
			d := json.NewDecoder(strings.NewReader(string(line)))
			d.UseNumber()
			if d.Decode(&m) == nil {
				fn(m)
			} else if len(strings.TrimSpace(string(line))) > 0 {
				diagMalformedLine(path)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func walkFiles(root string, pred func(string) bool) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// A directory the process cannot read takes its sessions out of
			// recall. Dropping the error here left the index run with nothing
			// to say: the only sign was a file count nobody has memorised
			// (#816, and the ingest half of it #818).
			if os.IsPermission(err) {
				diagFileError(p, err)
			}
			return nil
		}
		if d.Type()&os.ModeSymlink == 0 && !d.IsDir() && pred(p) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// safeParse shields the index from a panicking parser: one malformed session
// file must cost one file, not the whole build.
func safeParse(path string, parse func(string) ([]model.Session, error)) (ss []model.Session, err error) {
	defer func() {
		if r := recover(); r != nil {
			ss, err = nil, fmt.Errorf("parser panic on %s: %v", path, r)
		}
	}()
	return parse(path)
}

func parseFiles(files []string, parse func(string) ([]model.Session, error)) []model.Session {
	files = append([]string(nil), files...)
	sort.Strings(files)
	type job struct {
		i int
		p string
	}
	jobs := make(chan job)
	outs := make(chan struct {
		i  int
		ss []model.Session
	})
	var wg sync.WaitGroup
	n := runtime.NumCPU()
	if len(files) < n {
		n = len(files)
	}
	if n == 0 {
		return nil
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				ss, err := safeParse(j.p, parse)
				diagFileError(j.p, err)
				outs <- struct {
					i  int
					ss []model.Session
				}{i: j.i, ss: ss}
			}
		}()
	}
	go func() {
		for i, f := range files {
			jobs <- job{i: i, p: f}
		}
		close(jobs)
		wg.Wait()
		close(outs)
	}()
	byFile := make([][]model.Session, len(files))
	for out := range outs {
		byFile[out.i] = out.ss
	}
	var all []model.Session
	for _, ss := range byFile {
		all = append(all, ss...)
	}
	return all
}

// toolDialect names one harness's tool-call vocabulary. The wire shape is the
// same everywhere — a `tool_use` part with a name and an input object — but the
// vocabulary is not, and it is not guessable: Claude names the file key
// `file_path` and its shell tool `Bash`, Cursor names them `path` and `Shell`.
// Both were read off transcripts the vendor's own CLI had just written.
type toolDialect struct {
	pathKey   string
	pathTools map[string]bool
	shellTool string
	// editTools bounds which calls carry a replaced span. Empty means any call
	// with a path and an old_string, which is how the Claude decoder has always
	// read MultiEdit's sub-edits.
	editTools map[string]bool
}

var claudeDialect = toolDialect{
	pathKey:   "file_path",
	pathTools: pathTools,
	shellTool: "Bash",
}

func toolPart(it any, d toolDialect) (name string, in map[string]any, ok bool) {
	m, ok := it.(map[string]any)
	if !ok {
		return "", nil, false
	}
	if t, _ := m["type"].(string); t != "tool_use" {
		return "", nil, false
	}
	name, _ = m["name"].(string)
	in, _ = m["input"].(map[string]any)
	return name, in, in != nil
}

// toolPathsFromContent is claudeToolPaths for the reference parser, which walks
// decoded maps rather than raw JSON. The two must agree or the differential
// test in #502 stops meaning anything.
func toolPathsFromContent(v any) string { return toolPathsIn(v, claudeDialect) }

func toolPathsIn(v any, d toolDialect) string {
	items, ok := v.([]any)
	if !ok {
		return ""
	}
	var out []string
	seen := map[string]bool{}
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok || !d.pathTools[name] {
			continue
		}
		p, _ := in[d.pathKey].(string)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, "\n")
}

// editSpansFromContent is claudeEditSpans for the reference parser.
func editSpansFromContent(v any) []string { return editSpansIn(v, claudeDialect) }

func editSpansIn(v any, d toolDialect) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok {
			continue
		}
		if len(d.editTools) > 0 && !d.editTools[name] {
			continue
		}
		path, _ := in[d.pathKey].(string)
		if path == "" {
			continue
		}
		old, _ := in["old_string"].(string)
		spans := []string{old}
		if edits, ok := in["edits"].([]any); ok {
			for _, e := range edits {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				o, _ := em["old_string"].(string)
				spans = append(spans, o)
			}
		}
		for _, span := range spans {
			if span == "" {
				continue
			}
			if len(span) > editSpanMax {
				span = span[:editSpanMax]
			}
			out = append(out, path+"\n"+span)
		}
	}
	return out
}

// commandsFromContent is claudeCommands for the reference parser.
func commandsFromContent(v any) []string { return commandsIn(v, claudeDialect) }

func commandsIn(v any, d toolDialect) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, it := range items {
		name, in, ok := toolPart(it, d)
		if !ok || name != d.shellTool {
			continue
		}
		cmd, _ := in["command"].(string)
		if cmd == "" || !worthIndexing(cmd) {
			continue
		}
		out = append(out, "$ "+cmd)
	}
	return out
}

// HarnessAuthored reports whether a role marks text the harness wrote to the
// model rather than anything a person or the assistant said.
//
// #551 removed the Claude-side markers and asked for one place that recognises
// harness-injected wrappers across parsers instead of a list per parser. This
// is that place, and Codex is why it now exists: its preamble is not a string
// to add to a list, it is a whole role. Measured on one store — 28 of 28
// sessions took their title from it — and on a contributor's, 81 of 82 (#636).
//
// It is dropped rather than stored under an ignored role: the text is
// identical in every session, so it is not memory, and it is lexically broad
// enough to outrank real turns on aggregate match count.
func HarnessAuthored(role string) bool {
	return role == "developer" || role == "system"
}
