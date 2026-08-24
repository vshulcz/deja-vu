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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// maxParsedMessage bounds one parsed message before it reaches the index. It
// sits well above the index's own maxIndexedText (64 KiB): redaction runs at
// ingest on the full message before that smaller cap, so keeping more text
// here lets a secret that straddles the 64 KiB indexed boundary be redacted
// whole instead of losing its closing marker to an earlier cut. The stored
// text is unchanged — ingest still caps it to 64 KiB after redacting. The cap
// only guards memory against a pathological single message.
const maxParsedMessage = 1 << 20

// capParsedMessage bounds s to maxParsedMessage on a rune boundary. A raw byte
// slice would split a multibyte rune and hand invalid UTF-8 to NFC folding and
// redaction downstream.
func capParsedMessage(s string) string {
	if len(s) <= maxParsedMessage {
		return s
	}
	cut := maxParsedMessage
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

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
		// The field is always a timestamp, so try the layouts a store might
		// have written it in before giving up. RFC3339 is the common one; the
		// rest drop a piece it treats as optional — a missing zone, missing
		// seconds, a space where the T should be — each of which used to lose
		// the whole date to the zero time.
		for _, layout := range []string{
			time.RFC3339Nano,
			"2006-01-02T15:04:05",
			"2006-01-02T15:04Z07:00",
			"2006-01-02T15:04",
			"2006-01-02 15:04:05Z07:00",
			"2006-01-02 15:04:05",
		} {
			if t, err := time.Parse(layout, x); err == nil {
				return t
			}
		}
		// Some stores stringify the epoch ("1777629600", fractional or not),
		// a bare number in a field that is always a timestamp.
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return unixGuess(int64(f))
		}
	case float64:
		return unixGuess(int64(x))
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return unixGuess(n)
		}
		// A fractional epoch — Python's time.time() writes one — is a valid
		// Number that Int64 rejects; without the Float64 fallback the whole
		// turn lost its date to the zero time, and the session sorted as "-".
		if f, err := x.Float64(); err == nil {
			return unixGuess(int64(f))
		}
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
//
// It joins items itself rather than delegating to textFromContent because a
// Claude item's content is not always a string: an MCP tool's result is an
// array of blocks with the text one level down, and the string-only read
// dropped those. Only the Claude path reads that shape; the other harnesses
// keep textFromContent as it was.
func textFromContentKind(v any) (string, bool) {
	c, ok := v.([]any)
	if !ok {
		return textFromContent(v), false
	}
	var sawTool, sawSpeech bool
	var b strings.Builder
	for _, it := range c {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		txt, _ := m["text"].(string)
		if txt == "" {
			txt = contentText(m["content"])
		}
		if typ == "tool_result" {
			sawTool = true
		} else if txt != "" {
			sawSpeech = true
		}
		if txt == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(txt)
	}
	return b.String(), sawTool && !sawSpeech
}

// contentText is claudeContentText for the reference parser: a content field
// holds a plain string, or an array of blocks whose text sits one level down.
func contentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, it := range c {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			txt, _ := m["text"].(string)
			if txt == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(txt)
		}
		return b.String()
	}
	return ""
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

// projectSegments joins the last two path segments into the name a project is
// known by. Always with "/", because that name is an identifier rather than a
// path on this disk: built with the OS separator it reads goprojects\deja-vu on
// windows and goprojects/deja-vu everywhere else, which is two names for one
// project. Scoping then misses on windows, and a project synced from a windows
// machine never matches itself on any other.
func projectSegments(parent, base string) string {
	return parent + "/" + base
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
		// A UTF-8 BOM on the first line would fail the JSON decode below, so the
		// opening turn was dropped; trimJSONSpace strips it (and surrounding
		// space) the way the claude decode path already does.
		if line = trimJSONSpace(line); len(line) > 0 {
			var m map[string]any
			d := json.NewDecoder(strings.NewReader(string(line)))
			d.UseNumber()
			if d.Decode(&m) == nil {
				fn(m)
			} else {
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

// keepRegular drops any path that is not a regular file. Discovery paths that
// glob or list names (rather than walking DirEntries) use it so a FIFO or
// socket matching the pattern cannot reach a parser's Open and block it.
func keepRegular(paths []string) []string {
	out := paths[:0]
	for _, p := range paths {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().IsRegular() {
			out = append(out, p)
		}
	}
	return out
}

// resolvedRoot follows a store root that is itself a symlink. People keep
// ~/.claude in a dotfiles repo, on an external disk, in a synced folder;
// WalkDir lstats its root, so the walk saw a symlink rather than a directory
// and descended nowhere — the store indexed nothing while every surface called
// it found and empty (#1744). Only the root: a link inside a store is still
// skipped, which is what keeps a FIFO from hanging the build and a link from
// walking out of the store.
func resolvedRoot(root string) string {
	fi, err := os.Lstat(root)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return root
	}
	target, err := filepath.EvalSymlinks(root)
	if err != nil {
		return root
	}
	if st, err := os.Stat(target); err != nil || !st.IsDir() {
		return root
	}
	return target
}

// walkRoot returns the directory to walk for a configured root and a function
// putting each walked path back under that root. Walking the target is what
// makes a symlinked store readable; reporting the configured spelling is what
// keeps it usable, since the kind predicates test strings.HasPrefix against the
// root and the manifest and the incremental pass key on the same string.
func walkRoot(root string) (string, func(string) string) {
	walked := resolvedRoot(root)
	if walked == root {
		return root, func(p string) string { return p }
	}
	return walked, func(p string) string {
		rel, err := filepath.Rel(walked, p)
		if err != nil {
			return p
		}
		return filepath.Join(root, rel)
	}
}

func walkFiles(root string, pred func(string) bool) []string {
	var out []string
	// Walk the target, report paths under the configured root: everything
	// downstream — the kind predicates that test strings.HasPrefix against the
	// root, the manifest, the incremental offsets — keys on the path the user
	// configured, and handing it two spellings of one file loses the file.
	walked, under := walkRoot(root)
	_ = filepath.WalkDir(walked, func(p string, d os.DirEntry, err error) error {
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
		// Only regular files. A FIFO or socket that matched the session glob
		// hung the whole index: the parser's Open blocks on a named pipe with
		// no writer and never returns, so one such file in a scanned store
		// froze indexing for good. IsRegular already excludes symlinks and
		// directories, so it subsumes the checks it replaces.
		if q := under(p); d.Type().IsRegular() && pred(q) {
			out = append(out, q)
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
	// oldKey names the argument holding the text an edit replaced. Copilot
	// calls it old_str where Claude calls it old_string, and reading the wrong
	// one loses the only record of what stopped existing. Empty means
	// old_string.
	oldKey string
	// commandKey names the argument holding the command. Empty means
	// "command"; cline's run_commands takes "commands", a list.
	commandKey string
	// pathListKey names an argument holding several files at once — cline's
	// read_files takes "files", whose elements each name a path under pathKey.
	// Empty means a call names at most one file.
	pathListKey string
}

// oldSpanKey is oldKey with its default applied.
func (d toolDialect) oldSpanKey() string {
	if d.oldKey == "" {
		return "old_string"
	}
	return d.oldKey
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
		for _, p := range toolPathStrings(in, d) {
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n")
}

// toolPathStrings pulls the paths out of one call. A call can name one file
// under the dialect's key, or carry a list of read requests — cline's
// read_files takes `files`, an array whose elements each name a path — and
// reading only the scalar key indexed none of those.
func toolPathStrings(in map[string]any, d toolDialect) []string {
	if p, _ := in[d.pathKey].(string); p != "" {
		return []string{p}
	}
	if d.pathListKey == "" {
		return nil
	}
	items, _ := in[d.pathListKey].([]any)
	var out []string
	for _, it := range items {
		switch e := it.(type) {
		case string:
			if e != "" {
				out = append(out, e)
			}
		case map[string]any:
			if p, _ := e[d.pathKey].(string); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
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
		old, _ := in[d.oldSpanKey()].(string)
		spans := []string{old}
		if edits, ok := in["edits"].([]any); ok {
			for _, e := range edits {
				em, ok := e.(map[string]any)
				if !ok {
					continue
				}
				o, _ := em[d.oldSpanKey()].(string)
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
		// One call can carry a list rather than a single command: cline's
		// run_commands takes `commands`, an array of complete shell strings.
		// Reading only the singular key indexed none of them.
		for _, cmd := range commandStrings(in, d) {
			if !worthIndexing(cmd) {
				continue
			}
			out = append(out, "$ "+cmd)
		}
	}
	return out
}

// commandStrings pulls the commands out of one call, singular or plural.
func commandStrings(in map[string]any, d toolDialect) []string {
	key := d.commandKey
	if key == "" {
		key = "command"
	}
	switch v := in[key].(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return []string{v}
	case []any:
		var out []string
		for _, it := range v {
			s, _ := it.(string)
			if strings.TrimSpace(s) == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	}
	return nil
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

// truncateRunes cuts a string to at most n bytes without splitting a character.
// Callers store or display the result, so a broken byte survives far past the
// cut that made it.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}
