package sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// Source is a named harness root.
type Source struct{ Name, Root string }

// Home is the user's home dir, empty when it cannot be found.
func Home() string { h, _ := os.UserHomeDir(); return h }

// HomeRoots returns Home() followed by any additional readable home
// directories listed in DEJA_HOME_ROOTS (separated by filepath.ListSeparator,
// e.g. "/home/alice:/home/bob" on shared multi-user servers). Relative paths
// and unreadable directories are skipped so a restrictive peer home never
// fails the caller's pass (#5759, #5773).
func HomeRoots() []string {
	var out []string
	if h := Home(); h != "" {
		out = append(out, filepath.Clean(h))
	}
	for _, h := range extraHomeRoots() {
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out
}

func rawExtraHomeRoots() []string {
	raw := os.Getenv("DEJA_HOME_ROOTS")
	if raw == "" {
		return nil
	}
	home := filepath.Clean(Home())
	var out []string
	for _, p := range filepath.SplitList(raw) {
		p = strings.TrimSpace(p)
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		if p == home || slices.Contains(out, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func extraHomeRoots() []string {
	var out []string
	for _, p := range rawExtraHomeRoots() {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			if _, rerr := os.ReadDir(p); rerr == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// ConfigHome is the XDG config base for deja's own files: the tombstones, the
// exclude list, the aider context, peers, policy, the wiring record and the
// install lock. Not for paths that mirror a harness's own lookup.
//
// The XDG spec says a relative value is invalid and must be ignored. Followed,
// it put the tombstone list and aider's context file in whatever directory the
// command ran from, where no later run looks (#1693, #5052). With no home
// directory the answer is relative too; main refuses to run in that case.
func ConfigHome() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(base) {
		return base
	}
	return filepath.Join(Home(), ".config")
}

// ConfigDir is deja's own directory under ConfigHome.
func ConfigDir() string { return filepath.Join(ConfigHome(), "deja") }

// EnvPath returns env var k, or def when it is unset or empty.
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
			// A date with no time at all. The whole stamp was lost to the zero
			// time, so the session sorted as "-", never appeared in `deja last`
			// and was outside every `--since` window — for the sake of a clock
			// reading nobody has.
			"2006-01-02",
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

// newerThanEpoch is the SQL for "this numeric stamp is after t", for a column
// deja reads through unixGuess and so accepts in either unit.
//
// A since clause that picks one unit is wrong in one direction or the other: a
// seconds row is below every millisecond watermark, so the store is read once
// and then never updated again with nothing printed (#2064); a millisecond row
// is above every seconds watermark, so the store comes back whole on every
// pass. Comparing against both unconditionally is the second of those. The
// split is unixGuess's own, applied to the row rather than to the watermark.
func newerThanEpoch(expr string, t time.Time) string {
	// Milliseconds and seconds are the two units a store writes and then
	// filters on; a microsecond or nanosecond row is a larger number than any
	// millisecond watermark, so it passes the first test and comes back. Over-
	// inclusion is the safe direction here — the reader decides the date.
	return fmt.Sprintf("(%[1]s > %[2]d or (%[1]s < %[4]d and %[1]s > %[3]d))",
		expr, t.UnixMilli(), t.Unix(), epochMilliCutoff)
}

// The magnitudes that tell one unit from another. A moment in the 2020s is
// about 1.7e9 seconds, 1.7e12 milliseconds, 1.7e15 microseconds and 1.7e18
// nanoseconds, so each band holds every real stamp of its unit with three
// decades of room either side.
//
// Reading anything above milliseconds AS milliseconds is what this replaces: a
// microsecond store came back dated to the year 58136 and a nanosecond one to
// the year 56 million, and a session dated in the future takes the top of every
// recency surface and keeps it (#2063, #2102).
const (
	epochMilliCutoff = int64(1e11)
	epochMicroCutoff = int64(1e14)
	epochNanoCutoff  = int64(1e17)
)

// unixGuess reads a numeric stamp in whichever unit it was written in. It is
// the front door for every store that writes a number rather than a date, so a
// unit it cannot place is a whole harness misdated.
func unixGuess(n int64) time.Time {
	switch {
	case n <= 0:
		return time.Time{}
	case n < epochMilliCutoff:
		return time.Unix(n, 0)
	case n < epochMicroCutoff:
		return time.UnixMilli(n)
	case n < epochNanoCutoff:
		return time.UnixMicro(n)
	default:
		return time.Unix(0, n)
	}
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
			if !IndexToolOutput() {
				continue
			}
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

// projectName is the project of a directory a client recorded, named the way
// Claude Code's is (cwdProjectName), so one directory is one project whichever
// agent ran there. The basename made /tmp/w/my-app "my-app" for codex and
// "w/my-app" for claude, and --project with either missed the other (#4457).
func projectName(path string) string {
	if name := cwdProjectName(path); name != "" {
		return name
	}
	return "-"
}

func scanJSONL(path string, fn func(map[string]any)) error {
	return scanJSONLFromOffset(path, 0, fn)
}

// scanJSONLWithHeaderFromOffsetFunc is scanJSONLFromOffset for a format whose
// first lines carry metadata rather than a turn: the session's id, its title,
// the directory it ran in. Resuming past that line left the parser with none
// of it, so an appended turn arrived under a key made from the filename and
// the session it belonged to never grew (#2870). The header is handed over
// again on every resume, which is safe because parsers read it into fields
// they set rather than append to. isHeader picks it out of the first
// lookahead lines: taking line 1 whatever it held replayed a flat
// transcript's first prompt on every resume, and left an omp session split
// in two, since omp writes a title slot before its session record (#4406). omp writes a title slot before its session record, and
// taking line 1 left an omp session split in two on every resume (#4406).
func scanJSONLWithHeaderFromOffsetFunc(path string, offset int64, lookahead int, isHeader func(map[string]any) bool, fn func(map[string]any)) error {
	if offset > 0 {
		if header := leadingJSONLHeader(path, offset, lookahead, isHeader); header != nil {
			fn(header)
		}
	}
	return scanJSONLFromOffset(path, offset, fn)
}

// headerLookahead is how many leading lines may come before a header.
const headerLookahead = 4

// leadingJSONLHeader decodes the first of a JSONL file's leading lines that
// isHeader accepts, looking no further than lookahead lines or the offset, or
// nil when none is there.
func leadingJSONLHeader(path string, offset int64, lookahead int, isHeader func(map[string]any) bool) map[string]any {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	r := jsonlReader()
	defer func() { r.Reset(nil); jsonlReaders.Put(r) }()
	r.Reset(io.LimitReader(f, offset))
	for range lookahead {
		l, err := readJSONLine(r)
		m, ok := l.m, l.ok
		if !l.long {
			m, ok, _ = decodeJSONObjectLine(l.raw)
		}
		if ok && isHeader(m) {
			return m
		}
		if err != nil {
			return nil
		}
	}
	return nil
}

// readEnds is how far a read of each path may go while an index pass holds
// it. The pass records a file's size from a stat taken before it parses, and
// the next pass resumes from there; a parser that read on to EOF also took the
// lines the client wrote in between, and the next pass read them again (#4442).
var (
	readEndsMu sync.Mutex
	readEnds   = map[string]readEnd{}
)

// readEnd counts its holders: a pass that falls back to a rebuild holds the
// same files again, and the inner release must not lift the outer bound.
type readEnd struct {
	size  int64
	holds int
}

// LimitReads holds every transcript read of these paths to the size given
// for each, until the returned func is called.
func LimitReads(ends map[string]int64) (release func()) {
	readEndsMu.Lock()
	for p, n := range ends {
		e := readEnds[p]
		readEnds[p] = readEnd{size: n, holds: e.holds + 1}
	}
	readEndsMu.Unlock()
	return func() {
		readEndsMu.Lock()
		defer readEndsMu.Unlock()
		for p := range ends {
			if e := readEnds[p]; e.holds > 1 {
				e.holds--
				readEnds[p] = e
			} else {
				delete(readEnds, p)
			}
		}
	}
}

// boundedFrom is f read from offset, stopping at the end LimitReads holds
// for path.
func boundedFrom(path string, f *os.File, offset int64) io.Reader {
	readEndsMu.Lock()
	e, ok := readEnds[path]
	readEndsMu.Unlock()
	if !ok {
		return watchedReader(path, f)
	}
	return watchedReader(path, io.LimitReader(f, max(e.size-offset, 0)))
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
	return scanJSONLReader(path, boundedFrom(path, f, offset), fn)
}

// scanJSONLReader is the line loop of scanJSONLFromOffset over any reader;
// path only names the file in diagnostics. A decompressed archive is read
// through it from memory, so its plaintext never lands in a temporary file.
//
// The buffer comes from jsonlReaders: allocated per file it was 11.7 GB of a
// 12k-session cold build, a megabyte a transcript (#5586).
func scanJSONLReader(path string, src io.Reader, fn func(map[string]any)) error {
	r := jsonlReader()
	defer func() { r.Reset(nil); jsonlReaders.Put(r) }()
	r.Reset(src)
	for {
		l, err := readJSONLine(r)
		m, ok, empty := l.m, l.ok, l.empty
		if !l.long {
			m, ok, empty = decodeJSONObjectLine(l.raw)
		}
		// A last line with no newline that does not decode is the client
		// still writing it; the next pass reads it whole (#4276).
		switch {
		case ok:
			fn(m)
		case !empty && l.ended:
			diagMalformedLine(path)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// decodeJSONObjectLine decodes a line holding one JSON object, numbers kept as
// json.Number. A line holding anything else is not one: a null, or an object
// with more after it, was handed on as a record, or as nothing, and went
// uncounted (#5391).
func decodeJSONObjectLine(line []byte) (m map[string]any, ok, empty bool) {
	// A UTF-8 BOM on the first line would fail the decode, so the opening
	// turn was dropped; trimJSONSpace strips it and the space around.
	if line = trimJSONSpace(line); len(line) == 0 {
		return nil, false, true
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.UseNumber()
	if d.Decode(&m) != nil || m == nil {
		return nil, false, false
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, false, false
	}
	return m, true, false
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
				diagWalkError(p, err)
			}
			return nil
		}
		// Only regular files. A FIFO or socket that matched the session glob
		// hung the whole index: the parser's Open blocks on a named pipe with
		// no writer and never returns, so one such file in a scanned store
		// froze indexing for good.
		if q := under(p); isRegularWalkEntry(d) && pred(q) {
			out = append(out, q)
		}
		return nil
	})
	return out
}

func isRegularWalkEntry(d os.DirEntry) bool {
	if d.Type()&os.ModeSymlink != 0 {
		// PRoot can report DT_LNK for regular files (#5921). Info
		// checks the entry itself, so a real symlink stays excluded.
		info, err := d.Info()
		return err == nil && info.Mode().IsRegular()
	}
	return d.Type().IsRegular()
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
	n := min(len(files), runtime.NumCPU())
	if n == 0 {
		return nil
	}
	for range n {
		wg.Go(func() {
			for j := range jobs {
				ss, err := ReadWatched(j.p, func() ([]model.Session, error) { return safeParse(j.p, parse) })
				diagFileError(j.p, err)
				fileParsed(j.p)
				outs <- struct {
					i  int
					ss []model.Session
				}{i: j.i, ss: ss}
			}
		})
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

// millisecondBackoff formats a watermark for a clause that compares through
// strftime's %f. That is milliseconds, and the readers parse nanoseconds, so a
// message inside the watermark's own millisecond compares as not after it — and
// the two sides disagree about which millisecond a stamp is in at all, because
// Go truncates here where strftime rounds. Backing the watermark off by a
// millisecond puts the error on the side that costs a message being offered
// twice rather than the side that skips it for good (#2155).
func millisecondBackoff(t time.Time) string {
	return t.UTC().Add(-time.Millisecond).Format("2006-01-02T15:04:05.000")
}

// OnFileParsed is called with each transcript path as it finishes parsing, so
// a caller drawing progress can move per file rather than per store. The index
// sets it for a cold rebuild: every file-based store parses through the pool
// above, and one store holding most of the corpus used to leave the bar at 0%
// until it finished — measured, ten seconds of a thirty-second rebuild (#3372).
//
// Set before the parse and cleared after; nil means nobody is watching. It is
// called from the pool's workers, so an implementation has to be safe to call
// from several goroutines at once.
var OnFileParsed func(path string)

// SetFileProgress installs the callback and returns a function that restores
// what was there. The mutex covers the swap, not the call: parsing runs long
// after this returns.
func SetFileProgress(fn func(path string)) func() {
	fileProgressMu.Lock()
	prev := OnFileParsed
	OnFileParsed = fn
	fileProgressMu.Unlock()
	return func() {
		fileProgressMu.Lock()
		OnFileParsed = prev
		fileProgressMu.Unlock()
	}
}

var fileProgressMu sync.Mutex

func fileParsed(path string) {
	fileProgressMu.Lock()
	fn := OnFileParsed
	fileProgressMu.Unlock()
	if fn != nil {
		fn(path)
	}
}

// readJSONFile reads a store kept as one JSON document, without the UTF-8 byte
// order mark some Windows tools write: json.Unmarshal refuses the mark, and the
// whole session went for three bytes. trimJSONSpace does the same for a line.
func readJSONFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var buf bytes.Buffer
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > 0 {
		buf.Grow(int(fi.Size()) + bytes.MinRead)
	}
	_, err = buf.ReadFrom(watchedReader(path, f))
	return bytes.TrimPrefix(buf.Bytes(), []byte("\xef\xbb\xbf")), err
}

func parseSQLiteTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
