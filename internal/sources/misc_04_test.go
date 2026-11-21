package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/model"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func jetBrainsFixture(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "jetbrains"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_JETBRAINS_ROOT", root)
	return filepath.Join(root, "IntelliJIdea2026.2", "workspace", "2xQ7mN4bV8cK1pR6tY3wZ9aL0dF.xml")
}

func TestJetBrainsReadsChatsAndAgentTasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ws := jetBrainsFixture(t)
	if files := JetBrainsSessionFiles(); len(files) != 1 || !isJetBrainsWorkspace(files[0]) {
		t.Fatalf("workspace files = %v", files)
	}
	ss, err := ParseJetBrainsFile(ws)
	if err != nil {
		t.Fatal(err)
	}
	// The codex-acp chat is left to Codex's own store.
	if len(ss) != 2 {
		t.Fatalf("%d sessions, want the chat and the local agent's task", len(ss))
	}
	project := projectName(filepath.Join(home, "src", "notes-api"))
	byID := map[string][]string{}
	for _, s := range ss {
		if s.Harness != "jetbrains" || s.Project != project || s.Started.IsZero() {
			t.Fatalf("session = %q %q %q %v", s.ID, s.Harness, s.Project, s.Started)
		}
		for _, m := range s.Messages {
			byID[s.ID] = append(byID[s.ID], m.Role+": "+strings.SplitN(m.Text, "\n", 2)[0])
		}
	}
	chat := strings.Join(byID["7c1e9a52-3b4d-4f60-8a71-2d9e0b4c6f13"], "\n")
	if chat != "user: how do I add cursor pagination to GET /notes\nassistant: Return a next_cursor with the last note id and filter on id > cursor in the query." {
		t.Fatalf("chat:\n%s", chat)
	}
	task := strings.Join(byID["b84f2c10-6e9a-4d37-9c25-1a7e3f8d0b46"], "\n")
	for _, want := range []string{
		"user: the notes list test fails one run in five, find out why",
		"assistant: Running the test a few times to catch the failure.",
		"command: $ go test -count=5 ./notes  → exit 1",
		"files: " + filepath.Join(home, "src", "notes-api", "notes", "list.go"),
		"edit: " + filepath.Join(home, "src", "notes-api", "notes", "list.go"),
		"command: $ go test -count=5 ./notes",
		"assistant: List returned map order",
	} {
		if !strings.Contains(task, want) {
			t.Fatalf("task records lack %q:\n%s", want, task)
		}
	}
	if _, ok := byID["e2a7d913-58c4-4b0f-a6e1-9f3c2b7d4a85"]; ok {
		t.Fatal("read a chat whose agent keeps its own store")
	}
}

func TestJetBrainsSkipsAWorkspaceWithNoChat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "workspace", "abc.xml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`<project version="4"><component name="ChangeListManager"/></project>`), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseJetBrainsFile(p)
	if err != nil || len(ss) != 0 {
		t.Fatalf("got %d sessions, %v", len(ss), err)
	}
}

// streamedBytes is what readLineBytes hands on for a line past the buffer: a
// reader too small for the line forces the streamed path.
func streamedBytes(t *testing.T, line string) []byte {
	t.Helper()
	r := bufio.NewReaderSize(strings.NewReader(line+"\n"), 16)
	got, partial, err := readLineBytes(r)
	if err != nil || partial {
		t.Fatalf("%q: %v, partial %v", line, err, partial)
	}
	if rest, _ := r.ReadByte(); rest != 0 {
		t.Fatalf("%q: the line was not read to its end", line)
	}
	return bytes.Clone(got)
}

// A line past the buffer reaches a scanJSONLBytes caller as bytes that decode
// to what the line read whole decodes to, and a line that does not decode is
// handed on as bytes that do not either, so its caller still counts it.
// Strings stay under the cap here; the cut is TestScanJSONLBytesHoldsALongLineByItsCut.
func TestStreamedBytesDecodeAsTheLineReadWhole(t *testing.T) {
	cases := []string{ //nolint:prealloc // the fuzz loop below grows the seeds
		`{}`, `{"a":1}`, `{"a" : [1, 2.5e3, -0, true, false, null, {"b":"c"}]}`,
		`{"s":"tab\tquote\" slash\/ back\\ \b\f\n\r <tag> & amp"}`,
		`{"u":"\u00e9\u20ac\ud83d\ude00\u2028"}`, `{"u":"\ud83d"}`, `{"u":"\ude00"}`,
		"{\"raw\":\"\xff\xfe ok \xe2\x82\"}", "{\"ctl\":\"a\x01b\"}", `{"bad":"\x"}`,
		`{"n":01}`, `{"n":1e+5}`, `{"n":-1.5E-3}`, `{"n":12345678901234567890123}`,
		`null`, `[1,2]`, `"str"`, `{"a":1}}`, `{"a":1} trailing`, `{"a":1}{"b":2}`, `{"a":1,}`,
		`{"dup":1,"dup":2}`, `{"Type":"user","type":"assistant"}`, `{"a":[{}]}`, `{,}`,
		// The 16-byte head of this one is a record on its own.
		`{"a":1}` + strings.Repeat(" ", 40) + `{"b":2}`,
	}
	rng := rand.New(rand.NewSource(2)) //nolint:gosec // seeded fuzz input
	words := []string{"a", "é", "\\", "\"", "\x00", "\xff", "{", "}", "[", "]", ":", ",", "1", "-", "e", " ", "\\u00", "\\ud83d"}
	for range 3000 {
		b := mustJSON(t, map[string]any{"k": randValue(rng, 3)})
		cases = append(cases, string(b))
		mut := []byte(string(b))
		for j := rng.Intn(3); j >= 0 && len(mut) > 0; j-- {
			p := rng.Intn(len(mut))
			mut = append(mut[:p], append([]byte(words[rng.Intn(len(words))]), mut[p+1:]...)...)
		}
		cases = append(cases, strings.ReplaceAll(string(mut), "\n", ""))
	}
	for _, c := range cases {
		// A line of 16 bytes or less fits the buffer and is not streamed.
		line := "  " + c
		if len(line) <= 16 {
			continue
		}
		want, wok, _ := decodeJSONObjectLine([]byte(line))
		got := streamedBytes(t, line)
		if !wok {
			if len(trimJSONSpace([]byte(line))) > 0 && (len(trimJSONSpace(got)) == 0 || json.Valid(got)) {
				t.Fatalf("%q does not decode, handed on as %q", c, got)
			}
			continue
		}
		m, ok, _ := decodeJSONObjectLine(got)
		if !ok || !reflect.DeepEqual(m, want) {
			t.Fatalf("%q:\n  whole    %#v\n  streamed %s", c, want, got)
		}
		// The typed decode the Claude reader does reads the same record.
		var a, b claudeLine
		if (json.Unmarshal([]byte(line), &a) == nil) != (json.Unmarshal(got, &b) == nil) || a.Type != b.Type || a.SessionID != b.SessionID {
			t.Fatalf("%q: typed decode differs", c)
		}
	}
}

// writeLongClaudeLine writes a Claude transcript whose tool result is one
// line of size bytes, between a question and an answer.
func writeLongClaudeLine(t *testing.T, size int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s1.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, `{"type":"user","sessionId":"s1","timestamp":"2026-10-01T10:00:00Z","cwd":"/tmp/proj","message":{"role":"user","content":"why is the build red"}}`)
	fmt.Fprintln(w, `{"type":"assistant","sessionId":"s1","timestamp":"2026-10-01T10:00:01Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`)
	fmt.Fprint(w, `{"type":"user","sessionId":"s1","timestamp":"2026-10-01T10:00:02Z","cwd":"/tmp/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1\n`)
	chunk := strings.Repeat("--- FAIL: TestRetry\\n", 1<<12)
	for n := 0; n < size; n += len(chunk) {
		fmt.Fprint(w, chunk)
	}
	fmt.Fprintln(w, `"}]}}`)
	fmt.Fprintln(w, `{"type":"assistant","sessionId":"s1","timestamp":"2026-10-01T10:00:03Z","cwd":"/tmp/proj","message":{"role":"assistant","content":[{"type":"text","text":"the retry test times out"}]}}`)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return p
}

// A Claude tool output line far past the buffer is read in memory bounded by
// the cap, not the line, and the turns around it read as before (#5414). A
// line four times as long costs about the same: read whole, each extra
// megabyte of line was a few more allocated.
func TestScanJSONLBytesHoldsALongLineByItsCut(t *testing.T) {
	if testing.Short() {
		t.Skip("writes an 8 MB line")
	}
	// What the parse allocates besides the line is buffers it takes from
	// pools, and whether a pool still holds one is chance: -race drops a
	// quarter of all puts, and encoding/json keeps a large buffer by how the
	// process used it before, so on CI a 4 MB and a 16 MB line read 40 and
	// 47 MB (#5461, #5473). With the cut at 16 KB those buffers are too small
	// to matter: every json buffer lost costs kilobytes, a line held whole
	// still costs megabytes. The read buffers the pool makes again are a
	// megabyte each and not counted, and no collection empties a pool while
	// it is measured.
	defer func(n int) { longStringCap = n }(longStringCap)
	longStringCap = 16 << 10
	var refills atomic.Uint64
	newReader := jsonlReaders.New
	jsonlReaders.New = func() any { refills.Add(1); return newReader() }
	defer func() { jsonlReaders.New = newReader }()
	parse := func(size int) uint64 {
		p := writeLongClaudeLine(t, size)
		var before, after runtime.MemStats
		runtime.GC()
		refills.Store(0)
		gc, procs := debug.SetGCPercent(-1), runtime.GOMAXPROCS(1)
		runtime.ReadMemStats(&before)
		ss, err := ParseClaudeFile(p)
		runtime.ReadMemStats(&after)
		debug.SetGCPercent(gc)
		runtime.GOMAXPROCS(procs)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse: %v, %d sessions", err, len(ss))
		}
		roles := make([]string, 0, len(ss[0].Messages))
		var out string
		for _, m := range ss[0].Messages {
			roles = append(roles, m.Role)
			if m.Role == RoleToolOutput {
				out = m.Text
			}
		}
		if want := "user,command,tool-output,assistant"; strings.Join(roles, ",") != want {
			t.Errorf("roles = %s, want %s", strings.Join(roles, ","), want)
		}
		if len(out) != longStringCap || !strings.HasPrefix(out, "Exit code 1\n--- FAIL: TestRetry\n") {
			t.Errorf("tool output read as %d bytes: %.40q", len(out), out)
		}
		// The file takes three buffers at most, so more would be the parse
		// itself making them, which this must not take off.
		if n := refills.Load(); n > 3 {
			t.Errorf("a %d MB line made %d read buffers", size>>20, n)
		}
		return after.TotalAlloc - before.TotalAlloc - refills.Load()*jsonlBuffer
	}
	small, large := parse(2<<20), parse(8<<20)
	if large > small+1<<20 {
		t.Errorf("a 2 MB line allocated %d KB and an 8 MB one %d KB", small>>10, large>>10)
	}
}

// A long last line with no newline that does not decode yet is left for the
// next pass, not counted, as a short one is (#4276); one that does decode is
// read.
func TestScanJSONLBytesLeavesALongPartialLastLine(t *testing.T) {
	big := strings.Repeat("z", jsonlBuffer+10)
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"partial":  {`{"a":1}` + "\n" + `{"x":"` + big, 1},
		"complete": {`{"a":1}` + "\n" + `{"x":"` + big + `"}`, 2},
		"broken":   {`{"a":1}` + "\n" + `{"x":"` + big + `"` + "\n" + `{"b":2}` + "\n", 3},
	} {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		n := 0
		if err := scanJSONLBytes(p, 0, func([]byte) { n++ }); err != nil {
			t.Fatal(err)
		}
		if n != tc.want {
			t.Errorf("%s: handed on %d lines, want %d", name, n, tc.want)
		}
	}
}

// lineMatch finds what bytes.Contains finds in the whole line, however the
// line is cut into pieces.
func TestLineMatchFindsAWordAcrossPieces(t *testing.T) {
	rng := rand.New(rand.NewSource(3)) //nolint:gosec // seeded fuzz input
	pat := []byte(`"response_item"`)
	for range 5000 {
		line := make([]byte, rng.Intn(80))
		for j := range line {
			line[j] = `"response_itm`[rng.Intn(13)]
		}
		if rng.Intn(3) == 0 {
			at := rng.Intn(len(line) + 1)
			line = append(line[:at], append(append([]byte(nil), pat...), line[at:]...)...)
		}
		m := lineMatch{pat: pat}
		for rest := line; len(rest) > 0; {
			n := 1 + rng.Intn(len(rest))
			m.feed(rest[:n])
			rest = rest[n:]
		}
		if want := bytes.Contains(line, pat); m.seen != want {
			t.Fatalf("%q: seen %v, want %v", line, m.seen, want)
		}
	}
}

// codexRoledBefore finds a roled response_item on a line far past its
// buffer, a word cut at the buffer's edge included, and not two words on two
// lines.
func TestCodexRoledBeforeReadsALongLineInPieces(t *testing.T) {
	// `{"pad":"` and `","type":` put the word 17 bytes past the padding; the
	// middle two cut it at the 64 KB buffer's edge.
	for _, pad := range []int{0, 64*1024 - 17 - 5, 64*1024 - 17 - 14, 3 << 20} {
		line := `{"pad":"` + strings.Repeat("x", pad) + `","type":"response_item","payload":{"role":"user"}}` + "\n"
		p := filepath.Join(t.TempDir(), "rollout.jsonl")
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		if !codexRoledBefore(p, int64(len(line))) {
			t.Errorf("pad %d: roled response_item not found", pad)
		}
		apart := `{"type":"response_item","payload":{"output":"` + strings.Repeat("x", pad) + `"}}` + "\n" + `{"type":"event_msg","payload":{"role":"user"}}` + "\n"
		if err := os.WriteFile(p, []byte(apart), 0o644); err != nil {
			t.Fatal(err)
		}
		if codexRoledBefore(p, int64(len(apart))) {
			t.Errorf("pad %d: two words on two lines matched", pad)
		}
	}
}

// The session a file was filed under is read back past a long line without
// holding it: the line is streamed from its start once that is found, so a
// line four times as long costs about the same (#5414).
func TestLastClaudeKeyBeforeStreamsALongLine(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 16 MB line")
	}
	// readBack reads back from the end of the long line, and from its
	// middle, where the line is not whole yet and names nothing.
	readBack := func(size int) (alloc uint64) {
		p := writeLongClaudeLine(t, size)
		last := func() int64 {
			body, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			return int64(bytes.LastIndexByte(body[:len(body)-1], '\n') + 1)
		}()
		for _, end := range []int64{last, last - int64(size/2)} {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			key, found := lastClaudeKeyBefore(p, end)
			runtime.ReadMemStats(&after)
			if key != "s1" || !found {
				t.Errorf("%d MB line, end %d: key %q found %v", size>>20, end, key, found)
			}
			alloc = max(alloc, after.TotalAlloc-before.TotalAlloc)
		}
		return alloc
	}
	small, large := readBack(4<<20), readBack(16<<20)
	if large > small+6<<20 {
		t.Errorf("reading back past a 4 MB line allocated %d MB and past a 16 MB one %d MB", small>>20, large>>20)
	}
	// A long line in the middle of a chunk boundary run: every short line
	// is still read, and the result matches a read of the joined bytes.
	short := filepath.Join(t.TempDir(), "s2.jsonl")
	lines := []string{
		`{"type":"user","sessionId":"a","message":{"role":"user","content":"q"}}`,
		`{"type":"user","sessionId":"b","message":{"role":"user","content":"` + strings.Repeat("y", jsonlBuffer+70<<10) + `"}}`,
		`{"type":"summary","leafUuid":"x"}`,
	}
	if err := os.WriteFile(short, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	full := int64(len(strings.Join(lines, "\n")) + 1)
	if key, found := lastClaudeKeyBefore(short, full); key != "b" || !found {
		t.Errorf("past a summary: key %q found %v, want b", key, found)
	}
	first := int64(len(lines[0]) + 1)
	if key, found := lastClaudeKeyBefore(short, first); key != "a" || !found {
		t.Errorf("before the long line: key %q found %v, want a", key, found)
	}
}

// writeLongGrokFile writes a Grok session transcript where a tool_call event is
// one line of size bytes.
func writeLongGrokFile(t *testing.T, size int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sessions", "work", "s1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"s1","cwd":"/work"},"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:00:05Z"}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "updates.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, `{"timestamp":1782900001,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello"}}}}`)
	fmt.Fprint(w, `{"timestamp":1782900002,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","content":{"type":"text","text":"`)
	chunk := strings.Repeat("output text chunk\\n", 1<<12)
	for n := 0; n < size; n += len(chunk) {
		fmt.Fprint(w, chunk)
	}
	fmt.Fprintln(w, `"}}}`)
	fmt.Fprintln(w, `{"timestamp":1782900003,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}}}}`)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return p
}

// A Grok tool output line far past the buffer is read in memory bounded by the
// cap, not the line (#5424).
func TestParseGrokHoldsALongLineByItsCut(t *testing.T) {
	if testing.Short() {
		t.Skip("writes an 8 MB line")
	}
	defer func(n int) { longStringCap = n }(longStringCap)
	longStringCap = 16 << 10
	var refills atomic.Uint64
	newReader := jsonlReaders.New
	jsonlReaders.New = func() any { refills.Add(1); return newReader() }
	defer func() { jsonlReaders.New = newReader }()
	parse := func(size int) uint64 {
		p := writeLongGrokFile(t, size)
		var before, after runtime.MemStats
		runtime.GC()
		refills.Store(0)
		gc, procs := debug.SetGCPercent(-1), runtime.GOMAXPROCS(1)
		runtime.ReadMemStats(&before)
		ss, err := ParseGrokFile(p)
		runtime.ReadMemStats(&after)
		debug.SetGCPercent(gc)
		runtime.GOMAXPROCS(procs)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse: %v, %d sessions", err, len(ss))
		}
		if n := refills.Load(); n > 3 {
			t.Errorf("a %d MB line made %d read buffers", size>>20, n)
		}
		return after.TotalAlloc - before.TotalAlloc - refills.Load()*jsonlBuffer
	}
	small, large := parse(2<<20), parse(8<<20)
	if large > small+1<<20 {
		t.Errorf("a 2 MB line allocated %d KB and an 8 MB one %d KB", small>>10, large>>10)
	}
}

// CommandCodeSessionDir streams a long line before cwd without holding it (#5424).
func TestCommandCodeSessionDirStreamsALongLine(t *testing.T) {
	if testing.Short() {
		t.Skip("writes an 8 MB line")
	}
	defer func(n int) { longStringCap = n }(longStringCap)
	longStringCap = 16 << 10
	var refills atomic.Uint64
	newReader := jsonlReaders.New
	jsonlReaders.New = func() any { refills.Add(1); return newReader() }
	defer func() { jsonlReaders.New = newReader }()
	scan := func(size int) uint64 {
		dir := t.TempDir()
		p := filepath.Join(dir, "cmd.jsonl")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		w := bufio.NewWriter(f)
		fmt.Fprint(w, `{"type":"tool_result","content":"`)
		chunk := strings.Repeat("long output chunk\\n", 1<<12)
		for n := 0; n < size; n += len(chunk) {
			fmt.Fprint(w, chunk)
		}
		fmt.Fprintln(w, `"}`)
		wantCWD := filepath.Join(dir, "work")
		fmt.Fprintf(w, `{"type":"session","cwd":%q}`+"\n", wantCWD)
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()

		var before, after runtime.MemStats
		runtime.GC()
		refills.Store(0)
		gc, procs := debug.SetGCPercent(-1), runtime.GOMAXPROCS(1)
		runtime.ReadMemStats(&before)
		cwd := CommandCodeSessionDir(p)
		runtime.ReadMemStats(&after)
		debug.SetGCPercent(gc)
		runtime.GOMAXPROCS(procs)
		if cwd != wantCWD {
			t.Fatalf("got cwd %q, want %q", cwd, wantCWD)
		}
		return after.TotalAlloc - before.TotalAlloc - refills.Load()*jsonlBuffer
	}
	small, large := scan(2<<20), scan(8<<20)
	if large > small+1<<20 {
		t.Errorf("a 2 MB line allocated %d KB and an 8 MB one %d KB", small>>10, large>>10)
	}
}

// eachLineFrom streams a long line past the buffer without holding it (#5424).
func TestEachLineFromHoldsALongLineByItsCut(t *testing.T) {
	if testing.Short() {
		t.Skip("writes an 8 MB line")
	}
	defer func(n int) { longStringCap = n }(longStringCap)
	longStringCap = 16 << 10
	var refills atomic.Uint64
	newReader := jsonlReaders.New
	jsonlReaders.New = func() any { refills.Add(1); return newReader() }
	defer func() { jsonlReaders.New = newReader }()
	scan := func(size int) uint64 {
		p := filepath.Join(t.TempDir(), "stream.jsonl")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		w := bufio.NewWriter(f)
		fmt.Fprint(w, `{"type":"tool_result","text":"`)
		chunk := strings.Repeat("long line chunk\\n", 1<<12)
		for n := 0; n < size; n += len(chunk) {
			fmt.Fprint(w, chunk)
		}
		fmt.Fprintln(w, `"}`)
		fmt.Fprintln(w, `{"type":"assistant","text":"done"}`)
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()

		var before, after runtime.MemStats
		runtime.GC()
		refills.Store(0)
		gc, procs := debug.SetGCPercent(-1), runtime.GOMAXPROCS(1)
		runtime.ReadMemStats(&before)
		count := 0
		eachLineFrom(p, 0, func(_ []byte) bool {
			count++
			return true
		})
		runtime.ReadMemStats(&after)
		debug.SetGCPercent(gc)
		runtime.GOMAXPROCS(procs)
		if count != 2 {
			t.Fatalf("counted %d lines, want 2", count)
		}
		return after.TotalAlloc - before.TotalAlloc - refills.Load()*jsonlBuffer
	}
	small, large := scan(2<<20), scan(8<<20)
	if large > small+1<<20 {
		t.Errorf("a 2 MB line allocated %d KB and an 8 MB one %d KB", small>>10, large>>10)
	}
}

// Kimi and Kiro resume checks stream past long lines (#5424).
func TestKimiAndKiroResumesWithLongLine(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 4 MB line")
	}
	t.Run("kimi", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "wire.jsonl")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		w := bufio.NewWriter(f)
		line1 := `{"type":"context.append_loop_event","event":{"type":"tool.call"}}` + "\n"
		fmt.Fprint(w, line1)
		off := int64(len(line1))

		fmt.Fprint(w, `{"type":"context.append_loop_event","event":{"type":"progress","data":"`)
		chunk := strings.Repeat("long output\\n", 1<<12)
		for n := 0; n < 4<<20; n += len(chunk) {
			fmt.Fprint(w, chunk)
		}
		fmt.Fprintln(w, `"}}`)
		fmt.Fprintln(w, `{"type":"context.append_message","message":{"role":"user"}}`)
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()

		if KimiResumes(p, off) {
			t.Error("KimiResumes should return false when a user turn follows the long line")
		}
	})

	t.Run("kiro", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "s.jsonl")
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		w := bufio.NewWriter(f)
		head := `{"version":"v1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"fix the retry loop"}]}}` + "\n" +
			`{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m2","content":[{"kind":"text","data":"Checking"}]}}` + "\n"
		fmt.Fprint(w, head)
		off := int64(len(head))

		fmt.Fprint(w, `{"version":"v1","kind":"ToolResults","data":{"message_id":"m3","content":[{"kind":"output","data":"`)
		chunk := strings.Repeat("long output\\n", 1<<12)
		for n := 0; n < 4<<20; n += len(chunk) {
			fmt.Fprint(w, chunk)
		}
		fmt.Fprintln(w, `"}]}}`)
		fmt.Fprintln(w, `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m2","content":[{"kind":"text","data":" the loop."}]}}`)
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()

		if KiroCLIResumes(p, off) {
			t.Error("KiroCLIResumes should return false when tail continues the reply past the long line")
		}
	})
}

// strictObject is what a line decodes to by the rule scanJSONLReader keeps:
// one JSON object and nothing after it, numbers as json.Number.
func strictObject(line []byte) (map[string]any, bool) {
	m, ok, _ := decodeJSONObjectLine(line)
	return m, ok
}

func streamed(line []byte) (map[string]any, bool) {
	src := &lineSrc{r: bufio.NewReader(bytes.NewReader(nil)), head: append(append([]byte(nil), line...), '\n')}
	m, ok, _ := decodeStreamedLine(src)
	return m, ok
}

// With no string past the cap, a streamed line decodes exactly as the line
// read whole does, valid or not: escapes, surrogates, bad UTF-8, numbers,
// nesting, and lines that hold something other than one object.
func TestStreamedLineDecodesAsALineReadWhole(t *testing.T) {
	cases := []string{ //nolint:prealloc // the fuzz loop below grows the seeds
		`{}`, `{"a":1}`, `  {"a" : [1, 2.5e3, -0, true, false, null, {"b":"c"}]}  `,
		`{"s":"tab\tquote\" slash\/ back\\ \b\f\n\r"}`,
		`{"u":"\u00e9\u20ac\ud83d\ude00"}`, `{"u":"\ud83d"}`, `{"u":"\ud83dx"}`, `{"u":"\ud83d\u0041"}`,
		`{"u":"\ud83d\ud83d\ude00"}`, `{"u":"\ude00"}`, `{"u":"\ud83d\n"}`, `{"u":"\ud83d\"}`,
		"{\"raw\":\"\xff\xfe ok \xe2\x82\"}", "{\"ctl\":\"a\x01b\"}", `{"bad":"\x"}`, `{"bad":"\u12g4"}`,
		`{"n":01}`, `{"n":1.}`, `{"n":-}`, `{"n":1e}`, `{"n":1e+5}`, `{"n":-1.5E-3}`,
		`null`, `[1,2]`, `"str"`, `{"a":1}}`, `{"a":1} trailing`, `{"a":1}{"b":2}`, `{"a":1,}`, `{"a"}`,
		`{"a":1`, `{"dup":1,"dup":2}`, `{"a":[]}`, `{"a":{}}`, `{"a":[{}]}`, `{"t":tru}`, `{,}`, ``, `   `,
	}
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // seeded fuzz input
	words := []string{"a", "é", "\U0001F600", "\\", "\"", "\n", "\x00", "\xff", "{", "}", "[", "]", ":", ",", "1", "-", ".", "e", " ", "\\u00", "\\ud83d", "\\ude00"}
	for range 3000 {
		v := map[string]any{"k": randValue(rng, 3)}
		b := mustJSON(t, v)
		cases = append(cases, string(b))
		// The same with a few bytes changed, most of them broken.
		mut := []byte(string(b))
		for j := rng.Intn(3); j >= 0 && len(mut) > 0; j-- {
			p := rng.Intn(len(mut))
			w := words[rng.Intn(len(words))]
			mut = append(mut[:p], append([]byte(w), mut[p+1:]...)...)
		}
		cases = append(cases, strings.ReplaceAll(string(mut), "\n", ""))
	}
	for _, c := range cases {
		wm, wok := strictObject([]byte(c))
		gm, gok := streamed([]byte(c))
		if wok != gok || !reflect.DeepEqual(wm, gm) {
			t.Fatalf("%q:\n  whole    %v %#v\n  streamed %v %#v", c, wok, wm, gok, gm)
		}
	}
}

func randValue(rng *rand.Rand, depth int) any {
	switch n := rng.Intn(7); {
	case depth > 0 && n == 0:
		m := map[string]any{}
		for i := rng.Intn(4); i > 0; i-- {
			m[randString(rng)] = randValue(rng, depth-1)
		}
		return m
	case depth > 0 && n == 1:
		var a []any
		for i := rng.Intn(4); i > 0; i-- {
			a = append(a, randValue(rng, depth-1))
		}
		return a
	case n == 2:
		return rng.NormFloat64() * 1e6
	case n == 3:
		return rng.Intn(2) == 0
	case n == 4:
		return nil
	}
	return randString(rng)
}

func randString(rng *rand.Rand) string {
	runes := []rune{'a', 'z', ' ', '"', '\\', '/', '\n', '\t', 0x01, 'é', '€', 0x1F600, '<', '&', 0x2028}
	var b strings.Builder
	for i := rng.Intn(12); i > 0; i-- {
		b.WriteRune(runes[rng.Intn(len(runes))])
	}
	return b.String()
}

// Past the cap a string keeps its head on a rune boundary and the rest of the
// line still decodes. A string that holds JSON keeps its own text with its
// inner strings cut, so the metadata after an old Codex output survives, but
// not past twice the cap: JSON of many small values is cut as text is.
func TestStreamedLineCutsLongStrings(t *testing.T) {
	defer func(n int) { longStringCap = n }(longStringCap)
	longStringCap = 64
	nested := "{\n  \"output\": \"" + strings.Repeat("x", 200) + "\",\n  \"metadata\": {\"exit_code\": 2}\n}"
	many := "[" + strings.Repeat(`{"id":1,"ok":true},`, 40) + `{"id":2}]`
	line := mustJSON(t, map[string]any{
		"text":   strings.Repeat("a", 63) + "€€€ and more",
		"short":  "ok",
		"nested": nested,
		"many":   many,
		"broken": `{"output":"` + strings.Repeat("y", 200),
		"after":  map[string]any{"call_id": "c1"},
	})
	m, ok := streamed(line)
	if !ok {
		t.Fatalf("%s did not decode", line)
	}
	if m["text"] != strings.Repeat("a", 63) || m["short"] != "ok" || m["broken"] != `{"output":"`+strings.Repeat("y", 53) || m["many"] != many[:64] {
		t.Errorf("cut strings: %q %q %q %q", m["text"], m["short"], m["broken"], m["many"])
	}
	if after, _ := m["after"].(map[string]any); after["call_id"] != "c1" {
		t.Errorf("the object after the long strings: %#v", m["after"])
	}
	want := "{\n  \"output\": \"" + strings.Repeat("x", 64) + "\",\n  \"metadata\": {\"exit_code\": 2}\n}"
	if m["nested"] != want {
		t.Errorf("nested JSON:\n  got  %q\n  want %q", m["nested"], want)
	}
}

// Nesting is refused where encoding/json refuses it.
func TestStreamedLineNestingLimit(t *testing.T) {
	for _, n := range []int{9999, 10000, 10001, 10002} {
		line := []byte(strings.Repeat(`{"a":`, n-1) + "{}" + strings.Repeat("}", n-1))
		_, wok := strictObject(line)
		_, gok := streamed(line)
		if wok != gok {
			t.Errorf("depth %d: whole %v, streamed %v", n, wok, gok)
		}
	}
}

// A line far past the buffer is read in memory bounded by the cap, not the
// line: 64 MB of output allocated four times its size read whole (#5391).
func TestScanJSONLReaderHoldsALongLineByItsCut(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 64 MB line")
	}
	p := filepath.Join(t.TempDir(), "rollout.jsonl")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 << 20
	w := bufio.NewWriter(f)
	fmt.Fprint(w, `{"type":"response_item","payload":{"type":"function_call_output","output":"Process exited with code 2\nOutput:\n`)
	chunk := strings.Repeat("abcdefghi\\n", 1<<16)
	for n := 0; n < size; n += len(chunk) {
		fmt.Fprint(w, chunk)
	}
	fmt.Fprint(w, `","call_id":"big"}}`+"\n"+`{"type":"response_item","payload":{"type":"message","role":"assistant"}}`+"\n")
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	var got []map[string]any
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	if err := scanJSONLFromOffset(p, 0, func(m map[string]any) { got = append(got, m) }); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if len(got) != 2 {
		t.Fatalf("read %d records, want 2", len(got))
	}
	payload, _ := got[0]["payload"].(map[string]any)
	out, _ := payload["output"].(string)
	if len(out) != longStringCap || !strings.HasPrefix(out, "Process exited with code 2\nOutput:\nabcdefghi\n") || payload["call_id"] != "big" {
		t.Errorf("long line read as %d bytes of output, call_id %v", len(out), payload["call_id"])
	}
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 16<<20 {
		t.Errorf("reading a %d MB line allocated %d MB", size>>20, alloc>>20)
	}
}

// Each line that is not one JSON object counts as skipped: a null, and an
// object with more after it, were handed on or dropped without a count, so of
// three bad lines the summary said two (#5391). A last line with no newline
// that does not decode is still left for the next pass.
func TestEveryMalformedLineIsCounted(t *testing.T) {
	big := `{"x":"` + strings.Repeat("z", jsonlBuffer+10) + `"}`
	for name, tc := range map[string]struct {
		body string
		want int
		read int
	}{
		"short": {"not json\n{\"a\":1}\nnull\n{\"b\":2}}\n{\"c\":3} trailing\n[1]\n\n{\"d\":4}\n{\"torn\":", 5, 2},
		"long":  {big + "\n" + big[:len(big)-1] + "\n" + "null" + strings.Repeat(" ", jsonlBuffer) + "\n" + big + "x\n" + big, 3, 2},
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "a.jsonl")
			if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			DiagSnapshot()
			read := 0
			if err := scanJSONLFromOffset(p, 0, func(map[string]any) { read++ }); err != nil {
				t.Fatal(err)
			}
			malformed, _ := DiagSnapshot()
			if malformed[p] != tc.want || read != tc.read {
				t.Errorf("counted %d skipped and read %d, want %d and %d", malformed[p], read, tc.want, tc.read)
			}
		})
	}
}

// A reader stops at an error that is not the end of the file.
func TestReadJSONLineCarriesTheReadError(t *testing.T) {
	boom := errors.New("boom")
	r := bufio.NewReaderSize(io.MultiReader(strings.NewReader(`{"a":"`+strings.Repeat("b", jsonlBuffer+5)), iotest(boom)), jsonlBuffer)
	if _, err := readJSONLine(r); !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func iotest(err error) io.Reader { return errReader{err} }

// A session_meta past the buffer still names the session when a resumed
// read looks for it, and the line is streamed rather than held.
func TestCodexRolloutHeadReadsALongSessionMeta(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rollout-2026-06-01T10-00-00-019a0000-0000-7000-8000-000000000001.jsonl")
	meta := mustJSON(t, map[string]any{"timestamp": "2026-06-01T10:00:00.000Z", "type": "session_meta", "payload": map[string]any{
		"instructions": strings.Repeat("be brief. ", 300_000), "id": "019a0000-0000-7000-8000-000000000001", "cwd": "/w/app"}})
	if err := os.WriteFile(p, append(meta, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	id, cwd, payload, at := codexRolloutHead(p)
	if at.IsZero() {
		t.Error("the long meta line lost its time")
	}
	instructions, _ := payload["instructions"].(string)
	if id != "019a0000-0000-7000-8000-000000000001" || cwd != "/w/app" || len(instructions) != longStringCap {
		t.Errorf("head = %q %q, %d bytes of instructions", id, cwd, len(instructions))
	}
}

// A resume check hands on a long line only when its hint is in it, wherever
// in the line it sits.
func TestScanJSONLHintedLongLines(t *testing.T) {
	pad := strings.Repeat("p", jsonlBuffer+10)
	body := `{"out":"` + pad + `","type":"function_call_output"}` + "\n" +
		`{"out":"` + pad + `","type":"message"}` + "\n" +
		`{"type":"function_call","out":"` + pad + `"}` + "\n"
	p := filepath.Join(t.TempDir(), "r.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := scanJSONLHinted(p, 0, []byte(`"function_call`), func(m map[string]any) { got = append(got, fmt.Sprint(m["type"])) }); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[function_call_output function_call]" {
		t.Errorf("handed on %v", got)
	}
}

func junieFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "junie", "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_JUNIE_ROOT", root)
	return root
}

func TestJunieReadsTheEventLog(t *testing.T) {
	junieFixtureRoot(t)
	files := JunieSessionFiles()
	if len(files) != 1 || !isJunieSession(files[0]) {
		t.Fatalf("session files = %v", files)
	}
	ss, err := ParseJunieFile(files[0])
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	s := ss[0]
	if s.ID != "session-260901-101500-k3m9" || s.Title != "Fix the retry loop off-by-one" || s.Project != projectName("/work/retry-demo") {
		t.Fatalf("session = %q %q %q", s.ID, s.Title, s.Project)
	}
	var got []string
	for _, m := range s.Messages {
		got = append(got, m.Role+": "+strings.SplitN(m.Text, "\n", 2)[0])
	}
	want := []string{
		"user: why does the retry loop drop the last attempt",
		"command: $ go test ./...  → exit 1",
		"tool-output: --- FAIL: TestRetryRunsEveryAttempt (0.00s)",
		"files: /work/retry-demo/retry.go",
		"wrote: /work/retry-demo/retry.go",
		"edit: /work/retry-demo/retry.go",
		"command: $ go test ./...  → exit 0",
		"tool-output: ok  \tretry-demo\t0.003s",
		"assistant: ### Summary",
		"user: does the backoff still double between attempts",
		"assistant: Yes: the delay doubles after each failed attempt and stops at the cap.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !s.Messages[0].Time.Before(s.Messages[len(s.Messages)-1].Time) {
		t.Fatalf("times not in order: %v .. %v", s.Messages[0].Time, s.Messages[len(s.Messages)-1].Time)
	}
	if got := JunieProjectDir(s.ID); got != "/work/retry-demo" {
		t.Fatalf("project dir = %q", got)
	}
	if JunieSessionEvents("../"+s.ID) != "" || JunieSessionEvents("..") != "" {
		t.Fatal("a session id with a separator reached outside the root")
	}
}

func TestJunieTurnAndCompaction(t *testing.T) {
	junieFixtureRoot(t)
	st := JunieTurn("session-260901-101500-k3m9")
	// The failed run was followed by one that passed; the replay of the
	// failure at the task's end is not a new command.
	if st.FailureID != "" {
		t.Fatalf("newest command passed, failure = %q", st.FailureID)
	}
	if st.CompactionID != "5f0c1d2e-0006-4000-8000-000000000006" {
		t.Fatalf("compaction = %q", st.CompactionID)
	}
	tr, found, err := ReadJunieCompaction("session-260901-101500-k3m9")
	if err != nil || !found {
		t.Fatalf("compaction: %v %v", found, err)
	}
	if tr.Workspace != "/work/retry-demo" || tr.Harness != "junie" || tr.Fingerprint == "" {
		t.Fatalf("transcript = %+v", tr)
	}
	// The prompt that started the task is logged before the compaction it
	// set off; the answer after it is not part of the capture.
	for _, m := range tr.Session.Messages {
		if strings.Contains(m.Text, "delay doubles") {
			t.Fatalf("capture kept a turn from after the compaction: %q", m.Text)
		}
	}
}

func TestJunieTurnSeesALiveFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_JUNIE_ROOT", root)
	dir := filepath.Join(root, "session-261001-090000-ab12")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"kind":"UserPromptEvent","prompt":"run the tests","timestampMs":1790000000000}
{"kind":"SessionA2uxEvent","event":{"state":"IN_PROGRESS","agentEvent":{"kind":"TerminalBlockUpdatedEvent","stepId":"s1","status":"IN_PROGRESS","command":"make test"}},"timestampMs":1790000001000}
{"kind":"SessionA2uxEvent","event":{"state":"IN_PROGRESS","agentEvent":{"kind":"TerminalBlockUpdatedEvent","stepId":"s1","status":"FAILED","command":"make test","output":"undefined: retryBudget","exitCode":2}},"timestampMs":1790000002000}
`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	st := JunieTurn("session-261001-090000-ab12")
	if st.FailureID != "s1" || st.FailureCommand != "make test" || st.Failure != "undefined: retryBudget" {
		t.Fatalf("turn = %+v", st)
	}
	if _, found, _ := ReadJunieCompaction("session-261001-090000-ab12"); found {
		t.Fatal("found a compaction in a log with none")
	}
}

func TestJunieHeadlessTaskComesFromState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_JUNIE_ROOT", root)
	dir := filepath.Join(root, "session-261001-100000-cd34")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"kind":"SessionA2uxEvent","event":{"state":"IN_PROGRESS","agentEvent":{"kind":"MarkdownBlockUpdatedEvent","stepId":"m1","text":"The cache key ignores the locale."}},"timestampMs":1790000005000}
`
	state := `{"event":{"agentEvent":{"blob":"{\"lastAgentState\":{\"issue\":{\"description\":\"<additional_context>\\nrecalled\\n</additional_context>\\nwhy is the cache stale\"}}}"}}}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseJunieFile(filepath.Join(dir, "events.jsonl"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	if m := ss[0].Messages[0]; m.Role != "user" || m.Text != "why is the cache stale" {
		t.Fatalf("first record = %+v", m)
	}
}

// Kilo Code is a Roo fork that vendors OpenCode, so it has two stores and deja
// needs no new parser for either: the extension writes Roo's task shape and
// the CLI writes OpenCode's message schema (#3643). What is new is the paths
// and the name on the session.
func TestKiloReadsTheExtensionTasks(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "globalStorage", KiloExtensionID)
	task := filepath.Join(root, "tasks", "kilo-task-1")
	if err := os.MkdirAll(task, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KILO_ROOTS", root)
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "absent.db"))

	transcript := `[{"role":"user","content":[{"type":"text","text":"<task>why does the migration hang</task>"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"the advisory lock was never released"}]}]`
	if err := os.WriteFile(filepath.Join(task, "api_conversation_history.json"), []byte(transcript), 0o644); err != nil {
		t.Fatal(err)
	}
	item := `{"id":"kilo-task-1","ts":1784278800000,"task":"why does the migration hang","workspace":"/work/api"}`
	if err := os.WriteFile(filepath.Join(task, "history_item.json"), []byte(item), 0o644); err != nil {
		t.Fatal(err)
	}

	files := KiloTaskFiles()
	if len(files) != 1 {
		t.Fatalf("task files = %v", files)
	}
	ss := LoadKilo()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "kilocode" {
		t.Errorf("harness = %q, want kilocode — a Kilo session must not report as Roo", s.Harness)
	}
	if s.ID != "kilocode-task-kilo-task-1" {
		t.Errorf("id = %q", s.ID)
	}
	if s.Project == "roo" || s.Project == "" {
		t.Errorf("project = %q, want the workspace from history_item.json", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the two turns", len(s.Messages))
	}
	if !strings.Contains(s.Messages[0].Text, "why does the migration hang") {
		t.Errorf("the task envelope was not unwrapped: %q", s.Messages[0].Text)
	}
	if !strings.Contains(s.Messages[1].Text, "advisory lock") {
		t.Errorf("the reply is missing: %q", s.Messages[1].Text)
	}

	// The registry has to claim the file, or incremental ingest treats a Kilo
	// task as unknown content and never reads it again.
	kind := ""
	for _, h := range Registry() {
		if h.Name != "kilocode" {
			continue
		}
		for _, k := range h.Kinds {
			if k.Match(files[0]) {
				kind = k.Name
			}
		}
	}
	if kind != "kilocode-task" {
		t.Errorf("registry kind = %q, want kilocode-task", kind)
	}
}

// A Roo task must not be read as Kilo's, nor the other way round: the two
// extensions live in sibling directories under the same globalStorage.
func TestKiloAndRooDoNotClaimEachOther(t *testing.T) {
	home := t.TempDir()
	kiloRoot := filepath.Join(home, "globalStorage", KiloExtensionID)
	rooRoot := filepath.Join(home, "globalStorage", "rooveterinaryinc.roo-cline")
	for _, root := range []string{kiloRoot, rooRoot} {
		dir := filepath.Join(root, "tasks", "t1")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `[{"role":"user","content":[{"type":"text","text":"hello"}]}]`
		if err := os.WriteFile(filepath.Join(dir, "api_conversation_history.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_KILO_ROOTS", kiloRoot)
	t.Setenv("DEJA_ROO_ROOTS", rooRoot)
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "absent.db"))

	kiloFiles, rooFiles := KiloTaskFiles(), RooTaskFiles()
	if len(kiloFiles) != 1 || !strings.Contains(kiloFiles[0], KiloExtensionID) {
		t.Fatalf("kilo files = %v", kiloFiles)
	}
	if len(rooFiles) != 1 || strings.Contains(rooFiles[0], KiloExtensionID) {
		t.Fatalf("roo files = %v", rooFiles)
	}
	for _, h := range Registry() {
		if h.Name != "kilocode" {
			continue
		}
		for _, k := range h.Kinds {
			if k.Name == "kilocode-task" && k.Match(rooFiles[0]) {
				t.Error("the kilocode kind claims a Roo task")
			}
		}
	}
}

func TestKiloDatabasePathFollowsXDGAndTheOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_KILO_DB", "")
	if got, want := KiloDB(), filepath.Join(home, ".local", "share", "kilo", "kilo.db"); got != want {
		t.Errorf("KiloDB = %q, want %q", got, want)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	if got, want := KiloDB(), filepath.Join(home, "data", "kilo", "kilo.db"); got != want {
		t.Errorf("with XDG_DATA_HOME, KiloDB = %q, want %q", got, want)
	}
	t.Setenv("DEJA_KILO_DB", filepath.Join(home, "elsewhere.db"))
	if got, want := KiloDB(), filepath.Join(home, "elsewhere.db"); got != want {
		t.Errorf("with the override, KiloDB = %q, want %q", got, want)
	}
}

// The CLI store is OpenCode's schema, so the same rows read as Kilo sessions.
// Skipped without sqlite3, like every other database store here.
func TestKiloReadsTheCLIDatabase(t *testing.T) {
	if !SQLite3Available() {
		t.Skip("sqlite3 CLI not installed")
	}
	home := t.TempDir()
	db := filepath.Join(home, "kilo.db")
	t.Setenv("DEJA_KILO_DB", db)
	t.Setenv("DEJA_KILO_ROOTS", filepath.Join(home, "absent"))

	// OpenCode's own schema, the shape fixtures/registry/opencode/opencode.sql
	// carries, with a Kilo session in it.
	schema := `create table session (id text primary key, directory text, time_created any, time_updated any);
create table message (id text primary key, session_id text, time_created any, data text);
create table part (id text primary key, message_id text, data text);
insert into session values ('kilo-1', '/work/api', '2026-07-17T09:00:00Z', '2026-07-17T09:00:02Z');
insert into message values ('m-user', 'kilo-1', 1784278801000, '{"role":"user","time":{"created":"2026-07-17T09:00:01Z"}}');
insert into message values ('m-assistant', 'kilo-1', 1784278802000, '{"role":"assistant","time":{"created":"2026-07-17T09:00:02Z"}}');
insert into part values ('p-user', 'm-user', '{"type":"text","text":"why does the migration hang","time":{"start":"2026-07-17T09:00:01Z"}}');
insert into part values ('p-assistant', 'm-assistant', '{"type":"text","text":"the advisory lock was never released","time":{"start":"2026-07-17T09:00:02Z"}}');
`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, schema).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}
	ss := LoadKilo()
	if len(ss) == 0 {
		t.Fatal("the CLI database was not read")
	}
	for _, s := range ss {
		if s.Harness != "kilocode" {
			t.Fatalf("harness = %q, want kilocode", s.Harness)
		}
	}
	found := false
	for _, s := range ss {
		for _, m := range s.Messages {
			if strings.Contains(m.Text, "advisory lock") {
				found = true
			}
		}
	}
	if !found {
		t.Error("the reply from the database is missing")
	}
}

// `deja sources` names these defaults on a machine with no editor yet (#5638),
// so each has to be the directory its reader picks up once the editor writes
// it, not a path deja would never look in.
func TestVSCodeDefaultRootsAreWhereTheReadersLook(t *testing.T) {
	cases := []struct {
		name  string
		env   string
		def   func() string
		roots func() []string
		under []string // below VS Code's User directory
	}{
		{"copilot-chat", "DEJA_COPILOT_CHAT_ROOTS", CopilotChatDefaultRoot, CopilotChatRoots, nil},
		{"roo", "DEJA_ROO_ROOTS", RooDefaultRoot, RooRoots, []string{"globalStorage", "rooveterinaryinc.roo-cline"}},
		{"kilocode", "DEJA_KILO_ROOTS", KiloDefaultRoot, KiloRoots, []string{"globalStorage", "kilocode.kilo-code"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("APPDATA", "")
			t.Setenv(tc.env, "")

			// The stable "Code" host, where each platform keeps it.
			user := filepath.Join(home, ".config", "Code", "User")
			switch runtime.GOOS {
			case "darwin":
				user = filepath.Join(home, "Library", "Application Support", "Code", "User")
			case "windows":
				user = filepath.Join(home, "AppData", "Roaming", "Code", "User")
			}
			want := filepath.Join(append([]string{user}, tc.under...)...)
			def := tc.def()
			if def != want {
				t.Fatalf("default root = %q, want %q", def, want)
			}
			if !strings.HasPrefix(def, home) {
				t.Fatalf("default root %q is not under the home %q", def, home)
			}
			if slices.Contains(tc.roots(), def) {
				t.Fatalf("roots already list %q before it exists", def)
			}
			if err := os.MkdirAll(def, 0o755); err != nil {
				t.Fatal(err)
			}
			if got := tc.roots(); !slices.Contains(got, def) {
				t.Fatalf("roots = %v, want them to include the default %q once it exists", got, def)
			}
		})
	}
}

// Kimchi writes each `Agent` sub-agent run as a transcript of its own beside
// its parent, and deja indexed it as a session, where the sub-agent's
// restatement of the task competed with the parent for recall. The run is
// marked by a `kimchi:subagent-session` entry after the header; a fork carries
// `parentSession` too and is a session of its own (#4401).
func TestKimchiSubagentRunsAreSkipped(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMCHI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	dir := filepath.Join(root, "--private-tmp-proj--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	msg := `{"type":"message","id":"m1","timestamp":"2026-10-01T21:57:05.000Z","message":{"role":"user","content":[{"type":"text","text":"summarise retry.cfg in one line"}]}}` + "\n"
	write := func(name, head string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(head+msg), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	parent := write("2026-10-01T21-57-03-925Z_01a0f978-9334.jsonl",
		`{"type":"session","version":3,"id":"01a0f978-9334","timestamp":"2026-10-01T21:57:03.925Z","cwd":"/private/tmp/proj"}`+"\n")
	sub := write("2026-10-01T21-57-04-562Z_01a0f978-95b2.jsonl",
		`{"type":"session","version":3,"id":"01a0f978-95b2","timestamp":"2026-10-01T21:57:04.562Z","cwd":"/private/tmp/proj","parentSession":"`+parent+`"}`+"\n"+
			`{"type":"custom","id":"c1","parentId":null,"timestamp":"2026-10-01T21:57:04.562Z","customType":"kimchi:subagent-session","data":{"type":"General-Purpose"}}`+"\n")
	fork := write("2026-10-01T22-00-00-000Z_01a0f990-0000.jsonl",
		`{"type":"session","version":3,"id":"01a0f990-0000","timestamp":"2026-10-01T22:00:00.000Z","cwd":"/private/tmp/proj","parentSession":"`+parent+`"}`+"\n")

	files := KimchiSessionFiles()
	if !slices.Contains(files, parent) || !slices.Contains(files, fork) {
		t.Errorf("files = %v, want the parent and the fork", files)
	}
	if slices.Contains(files, sub) {
		t.Errorf("the sub-agent run is listed as a session: %v", files)
	}
	if !KimchiSubagentFile(sub) || KimchiSubagentFile(fork) || KimchiSubagentFile(parent) {
		t.Errorf("KimchiSubagentFile: sub %v, fork %v, parent %v", KimchiSubagentFile(sub), KimchiSubagentFile(fork), KimchiSubagentFile(parent))
	}
	if kindFor := KindForPath(sub); kindFor == "kimchi" {
		t.Errorf("the incremental pass still claims the sub-agent run")
	}

	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if !slices.Contains(KimchiSessionFiles(), sub) {
		t.Error("DEJA_INCLUDE_SUBAGENTS=1 does not take the sub-agent run")
	}
}

// Kimi Code runs a /btw side question in a fork of the main agent: state.json
// registers agent-N with forkedFrom "main", and its wire.jsonl opens with a
// copy of main's context, then the btw reminder, the question and the answer.
// The question is the person's own words, and deja left the whole file out as
// a sub-agent (#4484). Only what follows the reminder is read, as a fork of
// the session it was asked in.
//
// 0.28 marks the reminder {kind: system_trigger, name: btw}; 0.43 and 2.x
// write it through the reminder service as {kind: injection, variant: btw},
// and from 2.x an Agent call with fork: true is a forkedFrom agent as well.
func TestKimiBtwForkKeepsTheQuestionNotTheCopiedContext(t *testing.T) {
	for name, origin := range map[string]string{
		"0.28": `{"kind":"system_trigger","name":"btw"}`,
		"2.x":  `{"kind":"injection","variant":"btw","ownerPromptId":"p7"}`,
	} {
		t.Run(name, func(t *testing.T) { testKimiBtwFork(t, origin) })
	}
}

func testKimiBtwFork(t *testing.T, btwOrigin string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", filepath.Join(root, "kimi"))
	put := func(p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	session := filepath.Join(root, "kimi", "sessions", "wd_proj_ab", "session_t01")
	put(filepath.Join(session, "state.json"), `{"createdAt":"2026-07-01T10:00:00.000Z","title":"parent task","workDir":"/tmp/proj",
"agents":{"main":{"homedir":"agents/main","type":"main"},
"agent-1":{"homedir":"agents/agent-1","type":"sub","parentAgentId":"main","forkedFrom":"main"},
"agent-2":{"homedir":"agents/agent-2","type":"sub","parentAgentId":"main"},
"agent-3":{"homedir":"agents/agent-3","type":"sub","parentAgentId":"main","forkedFrom":"main"}}}`)
	put(filepath.Join(session, "agents", "main", "wire.jsonl"), kimiWireHead)
	fork := put(filepath.Join(session, "agents", "agent-1", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.4","created_at":1782295300000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"first question"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1782295300001}
{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"answer one, joined"}],"toolCalls":[]},"time":1782295300002}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nThis is a side-channel conversation with the user.\n</system-reminder>"}],"toolCalls":[],"origin":`+btwOrigin+`},"time":1782295300003}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"what does the btwjitter knob do"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1782295305000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"b1"},"time":1782295306000}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"it spreads the retries"}},"time":1782295306100}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"b1"},"time":1782295306200}
`)
	// A sub-agent the main agent spawned is not a fork: read by default as its
	// task, answer and changes (#5229), left out under =0.
	sub := put(filepath.Join(session, "agents", "agent-2", "wire.jsonl"),
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"search the repo"}]},"time":1782295400000}
`)

	// An Agent call with fork: true copies main's context too, and is a
	// sub-agent run, not the person's question.
	agentFork := put(filepath.Join(session, "agents", "agent-3", "wire.jsonl"),
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"first question"}],"toolCalls":[],"origin":{"kind":"user"}},"time":1782295300001}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"audit the retry path"}],"toolCalls":[],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1782295500000}
`)

	for _, sw := range []string{"", "1", "0"} {
		t.Setenv("DEJA_INCLUDE_SUBAGENTS", sw)
		files := strings.Join(KimiSessionFiles(), "\n")
		if !strings.Contains(files, fork) {
			t.Errorf("DEJA_INCLUDE_SUBAGENTS=%q: the /btw fork is not read: %s", sw, files)
		}
		if strings.Contains(files, sub) != (sw != "0") {
			t.Errorf("DEJA_INCLUDE_SUBAGENTS=%q: spawned sub-agent read=%v", sw, strings.Contains(files, sub))
		}
		if strings.Contains(files, agentFork) != (sw == "1") {
			t.Errorf("DEJA_INCLUDE_SUBAGENTS=%q: Agent fork: true sub-agent read=%v", sw, strings.Contains(files, agentFork))
		}
		ss, err := ParseKimiFile(fork)
		if err != nil || len(ss) != 1 {
			t.Fatalf("DEJA_INCLUDE_SUBAGENTS=%q: parsed %d sessions: %v", sw, len(ss), err)
		}
		s := ss[0]
		if s.Kind != "fork" || s.Parent != "session_t01" || s.ID == "session_t01" {
			t.Errorf("fork read as id=%q kind=%q parent=%q", s.ID, s.Kind, s.Parent)
		}
		got := make([]string, 0, len(s.Messages))
		for _, m := range s.Messages {
			got = append(got, m.Role+": "+m.Text)
		}
		want := []string{"user: what does the btwjitter knob do", "assistant: it spreads the retries"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("fork messages = %q, want %q", got, want)
		}
		if start := time.UnixMilli(1782295305000); !s.Started.Equal(start) {
			t.Errorf("fork starts %v, want the question's time %v", s.Started, start)
		}
	}
	// The reminder sits before any later offset, so a tail read would lose
	// where the fork's own turns start: the file is read whole.
	if fi, err := os.Stat(fork); err != nil || kimiTailResumes(fork, fi.Size()) {
		t.Errorf("a /btw fork resumes from an offset: %v", err)
	}
	// The Agent fork is no /btw fork, and resumes from an offset as before.
	if fi, err := os.Stat(agentFork); err != nil || !kimiTailResumes(agentFork, fi.Size()) {
		t.Errorf("an Agent fork no longer resumes from an offset: %v", err)
	}
	// Control: main resumes as before.
	main := filepath.Join(session, "agents", "main", "wire.jsonl")
	if fi, err := os.Stat(main); err != nil || !kimiTailResumes(main, fi.Size()) {
		t.Errorf("main no longer resumes from an offset: %v", err)
	}
}

// Shapes below are what kimi-code 2.2.0 wrote in a live run: `kimi fork`
// copies each agent's wire.jsonl and closes the copy with a forked record, and
// a coder sub-agent opens on the parent's prompt under origin
// system_trigger/subagent.

func kimiLineagePut(t *testing.T, p, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const kimiSourceWire = `{"type":"metadata","protocol_version":"1.5","created_at":1792304049712}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"ticket srcneedle: create parent2.txt"}],"origin":{"kind":"user"}},"time":1792304049800}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"tool.call","toolCallId":"c1","name":"Write","args":{"path":"/tmp/proj/kq-app/parent2.txt","content":"the parent agent wrote this particular line KQP-11\n"}},"time":1792304049900}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"step.begin","uuid":"s1"},"time":1792304050000}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"content.part","part":{"type":"text","text":"created parent2.txt"}},"time":1792304050100}
{"type":"context.append_loop_event","agentId":"main","event":{"type":"step.end","uuid":"s1"},"time":1792304050200}
`

func TestKimiForkKeepsOnlyItsOwnTurnsUnderItsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	ws := filepath.Join(root, "sessions", "wd_kq-app_ab")
	kimiLineagePut(t, filepath.Join(ws, "session_src", "state.json"), `{"id":"session_src","version":2,"cwd":"/tmp/proj/kq-app","createdAt":1792304049671,"updatedAt":1792304091777}`)
	src := kimiLineagePut(t, filepath.Join(ws, "session_src", "agents", "main", "wire.jsonl"), kimiSourceWire)
	kimiLineagePut(t, filepath.Join(ws, "session_fork", "state.json"), `{"id":"session_fork","version":2,"cwd":"/tmp/proj/kq-app","title":"Fork: session_src","forkedFrom":"session_src","createdAt":1792304312258,"updatedAt":1792304091777,"agents":{"main":{"type":"main"},"agent-0":{"type":"sub","parentAgentId":"main"}}}`)
	fork := kimiLineagePut(t, filepath.Join(ws, "session_fork", "agents", "main", "wire.jsonl"), kimiSourceWire+
		`{"type":"forked","agentId":"main","time":1792304312241}
{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"forkneedle: only the fork asked this"}],"origin":{"kind":"user"}},"time":1792304313000}
`)
	// A sub-agent run of the source, copied into the fork and never resumed
	// there: nothing in it is the fork's.
	copiedChild := kimiLineagePut(t, filepath.Join(ws, "session_fork", "agents", "agent-0", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.5","created_at":1792304049712}
{"type":"context.append_message","agentId":"agent-0","message":{"role":"user","content":[{"type":"text","text":"child task copied"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304049850}
{"type":"forked","agentId":"agent-0","time":1792304312241}
`)

	ss, err := ParseKimiFile(fork)
	if err != nil || len(ss) != 1 {
		t.Fatalf("fork parsed %d sessions: %v", len(ss), err)
	}
	f := ss[0]
	if f.Kind != "fork" || f.Parent != "session_src" {
		t.Errorf("fork read as kind=%q parent=%q, want a fork of session_src", f.Kind, f.Parent)
	}
	for _, m := range f.Messages {
		if strings.Contains(m.Text, "srcneedle") || m.Role == RoleWrote {
			t.Errorf("fork holds the source's copied record %s %q", m.Role, m.Text)
		}
	}
	if len(f.Messages) != 1 || !strings.Contains(f.Messages[0].Text, "forkneedle") {
		t.Errorf("fork messages = %#v, want only its own turn", f.Messages)
	}
	if got := f.Started.UnixMilli(); got != 1792304313000 {
		t.Errorf("fork starts at %d, want its own first turn 1792304313000", got)
	}
	if copied, _ := ParseKimiFile(copiedChild); len(copied) != 0 {
		t.Errorf("a copied, unresumed sub-agent log reads as %d sessions of the fork: %#v", len(copied), copied)
	}

	// The source is untouched by the rule: it carries no forkedFrom.
	ss, err = ParseKimiFile(src)
	if err != nil || len(ss) != 1 || ss[0].Kind != "" || ss[0].Parent != "" {
		t.Fatalf("source parsed as %#v: %v", ss, err)
	}
	if !strings.Contains(ss[0].Messages[0].Text, "srcneedle") {
		t.Errorf("source lost its own turn: %#v", ss[0].Messages)
	}
}

func TestKimiSubagentKeepsItsTaskAndRelativePathsSitUnderTheCwd(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	sess := filepath.Join(root, "sessions", "wd_kq-app_ab", "session_p")
	kimiLineagePut(t, filepath.Join(sess, "state.json"), `{"id":"session_p","version":2,"cwd":"/tmp/proj/kq-app","createdAt":1792304049671,"updatedAt":1792304091777}`)
	kimiLineagePut(t, filepath.Join(sess, "agents", "main", "wire.jsonl"), kimiSourceWire)
	child := kimiLineagePut(t, filepath.Join(sess, "agents", "agent-1", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.5","created_at":1792304060000}
{"type":"context.append_message","agentId":"agent-1","message":{"role":"user","content":[{"type":"text","text":"Ticket KQC-13 child task: edit notes2.md"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304060100}
{"type":"context.append_message","agentId":"agent-1","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nAuto permission mode is active.\n</system-reminder>"}],"origin":{"kind":"injection","variant":"permission_mode"}},"time":1792304060110}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e1","name":"Edit","args":{"path":"notes2.md","old_string":"the original retry budget line that the child will replace soon","new_string":"the retry budget line rewritten by the coder sub agent KQC-13"}},"time":1792304060200}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e2","name":"Write","args":{"path":"~/notes/home.txt","content":"a line written under the home directory by the sub agent\n"}},"time":1792304060300}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e4","name":"Write","args":{"path":"~tilde.txt","content":"a file whose name starts with a tilde, under the cwd\n"}},"time":1792304060350}
{"type":"context.append_loop_event","agentId":"agent-1","event":{"type":"tool.call","toolCallId":"e3","name":"Write","args":{"path":"/tmp/elsewhere/abs.txt","content":"an absolute line written by the coder sub agent here\n"}},"time":1792304060400}
`)
	ss, err := ParseKimiFile(child)
	if err != nil || len(ss) != 1 {
		t.Fatalf("child parsed %d sessions: %v", len(ss), err)
	}
	s := ss[0]
	if s.Kind != "subagent" || s.Parent != "session_p" {
		t.Errorf("child read as kind=%q parent=%q", s.Kind, s.Parent)
	}
	if len(s.Messages) == 0 || s.Messages[0].Role != "user" || !strings.Contains(s.Messages[0].Text, "KQC-13 child task") {
		t.Errorf("child's first message = %#v, want its task as a user turn", s.Messages)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "Auto permission mode") {
			t.Errorf("the host's reminder came through as %s: %q", m.Role, m.Text)
		}
	}
	paths := map[string][]string{}
	for _, m := range s.Messages {
		switch m.Role {
		case RoleFiles, RoleEdit, RoleWrote:
			p, _, _ := strings.Cut(m.Text, "\n")
			paths[m.Role] = append(paths[m.Role], p)
		}
	}
	want := map[string][]string{
		RoleFiles: {"/tmp/proj/kq-app/notes2.md", "~/notes/home.txt", "/tmp/proj/kq-app/~tilde.txt", "/tmp/elsewhere/abs.txt"},
		RoleEdit:  {"/tmp/proj/kq-app/notes2.md"},
		RoleWrote: {"/tmp/proj/kq-app/notes2.md", "~/notes/home.txt", "/tmp/proj/kq-app/~tilde.txt", "/tmp/elsewhere/abs.txt"},
	}
	for role, w := range want {
		if strings.Join(paths[role], "|") != strings.Join(w, "|") {
			t.Errorf("%s paths = %v, want %v", role, paths[role], w)
		}
	}
}

// The explore profile puts Kimi's <git-context> block ahead of the parent's
// prompt, under the same origin; kept, it titled the run "<git-context>
// Working directory: …".
func TestKimiExploreTaskLeavesKimisGitContextOut(t *testing.T) {
	for name, block := range map[string]string{
		"repo":       `<git-context>\nWorking directory: /tmp/proj/kq-app\nBranch: master\nDirty files (1):\n ?? main.go\n</git-context>\n\n`,
		"not a repo": `<git-context status=\"unavailable\" reason=\"not-a-repo\"/>\n\n`,
	} {
		root := t.TempDir()
		t.Setenv("DEJA_KIMI_ROOT", root)
		t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
		sess := filepath.Join(root, "sessions", "wd_kq-app_ab", "session_p")
		kimiLineagePut(t, filepath.Join(sess, "state.json"), `{"id":"session_p","version":2,"cwd":"/tmp/proj/kq-app"}`)
		child := kimiLineagePut(t, filepath.Join(sess, "agents", "agent-0", "wire.jsonl"),
			`{"type":"metadata","protocol_version":"1.5","created_at":1792304060000}
{"type":"context.append_message","agentId":"agent-0","message":{"role":"user","content":[{"type":"text","text":"`+block+`find the retry notes"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304060100}
`)
		ss, err := ParseKimiFile(child)
		if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
			t.Fatalf("%s: parsed %#v: %v", name, ss, err)
		}
		if got := ss[0].Messages[0].Text; got != "find the retry notes" {
			t.Errorf("%s: task = %q, want the parent's prompt alone", name, got)
		}
	}
}

// kimi-code writes a fork's wire copies before its state.json; a pass in that
// gap stored the copy as the fork's own, and a tail holding only the forked
// record could not take it back. That tail is read whole.
func TestKimiTailWithTheForkedRecordIsReadWhole(t *testing.T) {
	p := kimiLineagePut(t, filepath.Join(t.TempDir(), "session_fork", "agents", "main", "wire.jsonl"), kimiSourceWire)
	offset := int64(len(kimiSourceWire))
	add := func(line string) {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
	add(`{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[{"type":"text","text":"next"}],"origin":{"kind":"user"}},"time":1792304060000}` + "\n")
	if !kimiTailResumes(p, offset) {
		t.Fatal("control: an ordinary tail is not appended")
	}
	add(`{"type":"forked","agentId":"main","time":1792304312241}` + "\n")
	if kimiTailResumes(p, offset) {
		t.Error("a tail holding the forked record is appended to the stored copy")
	}
}

// A line a Kimi sub-agent wrote had no author unless DEJA_INCLUDE_SUBAGENTS=1:
// the logs were skipped whole (#5229). They come in by default the way
// Claude's and Qwen's do, as the task, the last turns and every change.
func TestKimiSubagentIsReadByDefaultAsTaskAnswerAndChanges(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_KIMI_ROOT", root)
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "")
	sess := filepath.Join(root, "sessions", "wd_kq-app_ab", "session_p")
	kimiLineagePut(t, filepath.Join(sess, "state.json"), `{"id":"session_p","version":2,"cwd":"/tmp/proj/kq-app"}`)
	kimiLineagePut(t, filepath.Join(sess, "agents", "main", "wire.jsonl"), kimiSourceWire)
	var b strings.Builder
	b.WriteString(`{"type":"context.append_message","agentId":"agent-0","message":{"role":"user","content":[{"type":"text","text":"Ticket KQC-12 child task: create child2.txt"}],"origin":{"kind":"system_trigger","name":"subagent"}},"time":1792304060100}` + "\n")
	b.WriteString(`{"type":"context.append_loop_event","agentId":"agent-0","event":{"type":"tool.call","toolCallId":"w1","name":"Write","args":{"path":"child2.txt","content":"the coder sub agent wrote this particular line KQC-12\n"}},"time":1792304060200}` + "\n")
	for range 8 {
		b.WriteString(`{"type":"context.append_loop_event","agentId":"agent-0","event":{"type":"tool.result","toolCallId":"r","result":{"output":"middleneedle reading"}},"time":1792304060300}` + "\n")
	}
	b.WriteString(`{"type":"context.append_message","agentId":"agent-0","message":{"role":"assistant","content":[{"type":"text","text":"created child2.txt"}]},"time":1792304060400}` + "\n")
	child := kimiLineagePut(t, filepath.Join(sess, "agents", "agent-0", "wire.jsonl"), b.String())

	found := false
	for _, f := range KimiSessionFiles() {
		found = found || f == child
	}
	if !found {
		t.Fatal("the sub-agent log is not read by default")
	}
	ss, err := ParseKimiFile(child)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parsed %d sessions: %v", len(ss), err)
	}
	roles := make([]string, 0, len(ss[0].Messages))
	wrote := false
	for _, m := range ss[0].Messages {
		roles = append(roles, m.Role)
		wrote = wrote || m.Role == RoleWrote && strings.HasPrefix(m.Text, "/tmp/proj/kq-app/child2.txt\n")
	}
	if ss[0].Messages[0].Role != "user" || !wrote || ss[0].Messages[len(ss[0].Messages)-1].Text != "created child2.txt" {
		t.Errorf("default read kept %v, want the task, the write and the answer", roles)
	}
	if n := len(ss[0].Messages); n > SubagentTailKept+3 {
		t.Errorf("default read kept %d messages, want the middle cut: %v", n, roles)
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	if ss, _ := ParseKimiFile(child); len(ss) != 1 || len(ss[0].Messages) < 12 {
		t.Errorf("DEJA_INCLUDE_SUBAGENTS=1 does not keep the whole run: %d", len(ss[0].Messages))
	}
}

// Kimi writes every message it appends under role user and says who wrote it
// in origin.kind: the person, an injected reminder, a hook's wrapped stdout, a
// background task. Only the person's are user turns (#3199).
func TestParseKimiKeepsOnlyThePersonsLinesAsUser(t *testing.T) {
	_, wire := kimiFixture(t)
	body := `{"type":"metadata","protocol_version":"1.4","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"why does glimwrax drop the trailing newline"}],"origin":{"kind":"user"}},"time":1782295201000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<hook_result hook_event=\"UserPromptSubmit\">\ndeja-vu — you have been here: zorbflax retry fix\n</hook_result>"}],"origin":{"kind":"hook_result","event":"UserPromptSubmit"}},"time":1782295201200}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nAuto permission mode is active.\n</system-reminder>"}],"origin":{"kind":"injection","variant":"permission_mode"}},"time":1782295201300}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"quuxnotify: background task 7 finished"}],"origin":{"kind":"background_task"}},"time":1782295201400}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"older protocol, no origin"}]},"time":1782295201500}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"/review the tokenizer"}],"origin":{"kind":"skill_activation","trigger":"user-slash"}},"time":1782295201600}
{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"the tokenizer trims it"}]},"time":1782295202000}
`
	if err := os.WriteFile(wire, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 3 || users[0] != "why does glimwrax drop the trailing newline" || users[1] != "older protocol, no origin" || users[2] != "/review the tokenizer" {
		t.Fatalf("user lines = %q", users)
	}
}

// DEJA_INCLUDE_SUBAGENTS=1 took Claude, Cursor, gjc, prime, senpi and Kimchi
// sub-agent runs, and did nothing for Kimi Code or Qwen Code: their file lists
// kept only the main transcript (#4483). Under the switch the run is read as a
// sub-agent of the session it sits under; without it, it stays out.
func TestKimiAndQwenSubagentsComeInUnderTheSwitch(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", filepath.Join(root, "kimi"))
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	put := func(p, body string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	kimiSession := filepath.Join(root, "kimi", "sessions", "wd_proj_ab", "session_t01")
	put(filepath.Join(kimiSession, "state.json"), `{"createdAt":"2026-07-01T10:00:00.000Z","updatedAt":"2026-07-01T10:00:05.000Z","title":"parent task","workDir":"/tmp/proj"}`)
	put(filepath.Join(kimiSession, "agents", "main", "wire.jsonl"), kimiWireHead)
	kimiChild := put(filepath.Join(kimiSession, "agents", "agent-1", "wire.jsonl"),
		`{"type":"metadata","protocol_version":"1.4","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"trace the kimichild backoff jitter"}]},"time":1782295203000}
`)
	qwenProject := filepath.Join(root, "qwen", "projects", "-tmp-proj")
	put(filepath.Join(qwenProject, "chats", "q-1.jsonl"),
		`{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:04:05Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"parent question"}]}}`+"\n")
	qwenChild := put(filepath.Join(qwenProject, "subagents", "q-1", "agent-a1.jsonl"),
		`{"type":"user","sessionId":"q-1","timestamp":"2026-01-02T03:05:05Z","cwd":"/tmp/proj","message":{"role":"user","parts":[{"text":"trace the qwenchild backoff jitter"}]}}`+"\n")

	has := slices.Contains[[]string]
	// Both are read by default now (#5223, #5229); =0 keeps them out.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	if has(KimiSessionFiles(), kimiChild) {
		t.Fatal("a Kimi sub-agent log is read under DEJA_INCLUDE_SUBAGENTS=0")
	}
	if has(QwenSessionFiles(), qwenChild) {
		t.Fatal("a Qwen sub-agent log is read under DEJA_INCLUDE_SUBAGENTS=0")
	}
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "1")
	for _, tc := range []struct {
		name, child, parent, word string
		files                     []string
		parse                     func(string) ([]model.Session, error)
		load                      func() []model.Session
	}{
		{"kimi", kimiChild, "session_t01", "kimichild", KimiSessionFiles(), ParseKimiFile, LoadKimi},
		{"qwen", qwenChild, "q-1", "qwenchild", QwenSessionFiles(), ParseQwenFile, LoadQwen},
	} {
		if !has(tc.files, tc.child) {
			t.Errorf("%s: DEJA_INCLUDE_SUBAGENTS=1 leaves the sub-agent log out: %v", tc.name, tc.files)
			continue
		}
		ss, err := tc.parse(tc.child)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s: parsed %d sessions: %v", tc.name, len(ss), err)
		}
		s := ss[0]
		if s.Kind != "subagent" || s.Parent != tc.parent || s.ID == tc.parent {
			t.Errorf("%s: child read as id=%q kind=%q parent=%q, want a sub-agent of %s under its own id", tc.name, s.ID, s.Kind, s.Parent, tc.parent)
		}
		if len(s.Messages) == 0 || !strings.Contains(s.Messages[0].Text, tc.word) {
			t.Errorf("%s: the child's turn did not come through: %#v", tc.name, s.Messages)
		}
		ids := map[string]bool{}
		for _, s := range tc.load() {
			ids[s.ID] = true
		}
		if len(ids) != 2 {
			t.Errorf("%s: parent and child load as %d distinct sessions, want 2: %v", tc.name, len(ids), ids)
		}
	}
}

func kimiFixture(t *testing.T) (root, wire string) {
	t.Helper()
	root = t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("KIMI_CODE_HOME", "")
	t.Setenv("DEJA_KIMI_ROOT", filepath.Join(root, "kimi"))
	dir := filepath.Join(root, "kimi", "sessions", "wd_demo_ab", "session_t01", "agents", "main")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := `{"createdAt":"2026-07-01T10:00:00.000Z","updatedAt":"2026-07-01T10:00:05.000Z","title":"t","workDir":"/home/u/work/proj"}`
	if err := os.WriteFile(filepath.Join(root, "kimi", "sessions", "wd_demo_ab", "session_t01", "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(dir, "wire.jsonl")
}

const kimiWireHead = `{"type":"metadata","protocol_version":"1.4","created_at":1782295200000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"first question"}]},"time":1782295201000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s1"},"time":1782295201100}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"think","text":"hidden"}},"time":1782295201150}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"answer one, "}},"time":1782295201200}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"joined"}},"time":1782295201300}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1","finishReason":"end_turn"},"time":1782295201400}
`

func TestParseKimiReconstructsStreamedAssistant(t *testing.T) {
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireHead), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	s := ss[0]
	if s.Harness != "kimi" || s.ID != "session_t01" || s.Project != "work/proj" || s.Title != "t" {
		t.Fatalf("meta: %+v", s)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != "first question" {
		t.Fatalf("user msg: %+v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != "answer one, joined" {
		t.Fatalf("assistant msg: %+v", s.Messages[1])
	}
	for _, m := range s.Messages {
		if m.Text == "hidden" {
			t.Fatal("think part leaked into the index")
		}
	}
}

func TestParseKimiMidStreamAndIncrementalAppend(t *testing.T) {
	_, wire := kimiFixture(t)
	// File ends mid-step: content.part written, no step.end yet.
	head := kimiWireHead + `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"second question"}]},"time":1782295202000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s2"},"time":1782295202100}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"partial answer"}},"time":1782295202200}
`
	if err := os.WriteFile(wire, []byte(head), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	last := ss[0].Messages[len(ss[0].Messages)-1]
	if last.Role != "assistant" || last.Text != "partial answer" {
		t.Fatalf("mid-stream flush lost the response: %+v", ss[0].Messages)
	}
	// Append the rest and reparse from the previous offset.
	offset := int64(len(head))
	rest := `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":" continues"}},"time":1782295202300}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","finishReason":"end_turn"},"time":1782295202400}
`
	if err = os.WriteFile(wire, []byte(head+rest), 0o644); err != nil {
		t.Fatal(err)
	}
	inc, err := ParseKimiFileFromOffset(wire, offset)
	if err != nil || len(inc) != 1 {
		t.Fatalf("incremental parse: %v %d", err, len(inc))
	}
	tail := inc[0].Messages[len(inc[0].Messages)-1]
	if tail.Role != "assistant" || tail.Text != "continues" {
		t.Fatalf("appended remainder: %+v", inc[0].Messages)
	}
}

func TestParseKimiToleratesTornTail(t *testing.T) {
	// A torn tail is what a crash mid-write leaves behind. The message tear is
	// the original case; tool.call and tool.result lines are the longer ones —
	// an args blob is where a write is most likely to be caught mid-flush — so
	// each shape is torn in turn. The guarantee is the same for all three: the
	// torn line vanishes, everything before it comes out untouched.
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireTools), 0o644); err != nil {
		t.Fatal(err)
	}
	whole, err := ParseKimiFile(wire)
	if err != nil || len(whole) != 1 {
		t.Fatalf("baseline: %v %d", err, len(whole))
	}
	tears := []struct{ name, tail string }{
		{"message", `{"type":"context.append_message","message":{"role":"user","co`},
		{"tool call", `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t9","name":"Edit","args":{"path":"/w/proj/main.go","old_string":"the old bytes, torn off mid-`},
		{"tool result", `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t9","result":{"output":"go: downloading github.com/`},
	}
	for _, tear := range tears {
		if err := os.WriteFile(wire, []byte(kimiWireTools+tear.tail), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := ParseKimiFile(wire)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s tear: err=%v sessions=%d", tear.name, err, len(ss))
		}
		if got, want := len(ss[0].Messages), len(whole[0].Messages); got != want {
			t.Errorf("%s tear: messages = %d, want the %d the untorn file yields", tear.name, got, want)
		}
	}
}

// kimiWireTools is one step that works: it speaks, runs a command, reads the
// file, makes an edit that fails, writes a note, reads an image, touches its
// todo list — a call that yields no record, though its result still carries
// output — and closes with more text. Shapes are the ones the Kimi CLI writes:
// args key `path` (not `file_path`), `command` for Bash, and results under
// `result.output` — plain, isError, or empty.
const kimiWireTools = kimiWireHead + `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"please fix the test"}]},"time":1782295203000}
{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s2"},"time":1782295203100}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"looking at the failing test"}},"time":1782295203200}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t1","name":"Bash","args":{"command":"go test ./...","timeout":60}},"time":1782295203300}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t1","result":{"output":"FAIL: TestX (0.01s)"}},"time":1782295203400}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t2","name":"Read","args":{"path":"/w/proj/main.go"}},"time":1782295203500}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t2","result":{"output":"package main"}},"time":1782295203600}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t3","name":"Edit","args":{"path":"/w/proj/main.go","old_string":"println(\"hello\")","new_string":"println(\"goodbye\")"}},"time":1782295203700}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t3","result":{"isError":true,"output":"old_string not found"}},"time":1782295203800}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t4","name":"Write","args":{"path":"/w/proj/notes.txt","content":"alpha"}},"time":1782295203900}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t4","result":{"output":""}},"time":1782295204000}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t5","name":"ReadMediaFile","args":{"path":"/w/proj/cover.png"}},"time":1782295204050}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"t6","name":"TodoList","args":{"items":[]}},"time":1782295204100}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"t6","result":{"output":"2 todos, both open"}},"time":1782295204150}
{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":" — fixed"}},"time":1782295204200}
{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s2","finishReason":"end_turn"},"time":1782295204300}
`

func TestParseKimiEmitsWorkRecords(t *testing.T) {
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireTools), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	byRole := map[string][]string{}
	for _, m := range ss[0].Messages {
		byRole[m.Role] = append(byRole[m.Role], m.Text)
	}
	if got := byRole[RoleCommand]; len(got) != 1 || got[0] != "$ go test ./..." {
		t.Errorf("commands = %q, want the test run alone — TodoList and friends carry no command", got)
	}
	wantFiles := []string{"/w/proj/main.go", "/w/proj/main.go", "/w/proj/notes.txt", "/w/proj/cover.png"}
	if got := byRole[RoleFiles]; strings.Join(got, ",") != strings.Join(wantFiles, ",") {
		t.Errorf("files = %q, want the read, the edit, the write and the media read in order", got)
	}
	if got := byRole[RoleEdit]; len(got) != 1 || got[0] != "/w/proj/main.go\nprintln(\"hello\")" ||
		strings.Contains(strings.Join(got, ""), "goodbye") {
		t.Errorf("edit = %q, want only the bytes that stopped existing", got)
	}
	// The todo note lands although its call yields no record: tool.result is
	// indexed on its own switch, independent of what the call produced.
	wantOut := []string{"FAIL: TestX (0.01s)", "package main", "old_string not found", "2 todos, both open"}
	if got := byRole[RoleToolOutput]; strings.Join(got, "|") != strings.Join(wantOut, "|") {
		t.Errorf("tool output = %q, want the failure, the file, the error and the todo note — never the empty result", got)
	}
	// The stream flushes when a call yields a record, so the words that led
	// to the first command land before it, not at step.end.
	var order []string
	for _, m := range ss[0].Messages {
		if m.Role == "assistant" || m.Role == RoleCommand {
			order = append(order, m.Text)
		}
	}
	want := []string{"answer one, joined", "looking at the failing test", "$ go test ./...", "— fixed"}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Errorf("order = %q, want the speech ahead of the command it led to", order)
	}
}

func TestParseKimiToolRecordSwitchesOff(t *testing.T) {
	_, wire := kimiFixture(t)
	t.Setenv("DEJA_INDEX_COMMANDS", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	t.Setenv("DEJA_INDEX_EDITS", "0")
	t.Setenv("DEJA_INDEX_TOOL_OUTPUT", "0")
	if err := os.WriteFile(wire, []byte(kimiWireTools), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v", err)
	}
	// With every switch off, no record-driven flush fires either: the step's
	// text comes out as the single message it always was.
	last := ss[0].Messages[len(ss[0].Messages)-1]
	if last.Role != "assistant" || last.Text != "looking at the failing test — fixed" {
		t.Fatalf("switched off, want the untouched stream back: %+v", ss[0].Messages)
	}
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleCommand, RoleFiles, RoleEdit, RoleToolOutput:
			t.Fatalf("record %q survived its switch", m.Role)
		}
	}
}

func TestKimiSessionFilesSkipsSubAgentsUnderZero(t *testing.T) {
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	root, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireHead), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "kimi", "sessions", "wd_demo_ab", "session_t01", "agents", "agent-x1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "wire.jsonl"), []byte(kimiWireHead), 0o644); err != nil {
		t.Fatal(err)
	}
	files := KimiSessionFiles()
	if len(files) != 1 || files[0] != wire {
		t.Fatalf("sub-agent wire indexed: %v", files)
	}
}

// Records as kimi-code 0.28.1 wrote them: a failed Bash call's result is
// flagged isError and ends with the tool's own footer (#4262).
const kimiWireExit = `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]},"time":1790870004000}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_02ee5295c2c3","name":"Bash","args":{"command":"go vet ./nonexistent"}},"time":1790870004786}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_e0ddee76d756","name":"Bash","args":{"command":"go build ./..."}},"time":1790870004790}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_5","name":"Bash","args":{"command":"go test ./retry"}},"time":1790870004795}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"call_6","name":"Bash","args":{"command":"make release"}},"time":1790870004796}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_02ee5295c2c3","result":{"output":"go: cannot find main module, but found .git/config in /private/tmp/proj-kimi\n\tto create a module there, run:\n\tgo mod init\nCommand failed with exit code: 1.","isError":true}},"time":1790870004853}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_e0ddee76d756","result":{"output":"printing the footer it would write\nCommand failed with exit code: 2.\n"}},"time":1790870004900}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_5","result":{"output":"--- FAIL: TestRetry [...truncated]\nCommand failed with exit code: 3. Output is truncated to fit in the message.\n\n[Full output saved]\ntask_id: b1\noutput_path: /tmp/b1.log\noutput_size_bytes: 90000\nnext_step: Use Read with output_path to page through the full log.","isError":true}},"time":1790870004950}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"call_6","result":{"output":"building\nCommand killed by timeout (2m)","isError":true}},"time":1790870004960}
`

func TestKimiFailedCommandCarriesItsExitCode(t *testing.T) {
	_, wire := kimiFixture(t)
	if err := os.WriteFile(wire, []byte(kimiWireExit), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	var cmds []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleCommand {
			cmds = append(cmds, m.Text)
		}
	}
	// The clean run ends on the footer itself but is not flagged isError,
	// and a timeout carries no exit code: both stay bare.
	want := []string{"$ go vet ./nonexistent  → exit 1", "$ go build ./...", "$ go test ./retry  → exit 3", "$ make release"}
	if strings.Join(cmds, "|") != strings.Join(want, "|") {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
}

// The Kimi Resumes hook is two rules, and either one sends the file back for
// a whole read: a failed result for a call stored already (#4443), and a reply
// streamed across the offset (#4445). Each case here is one only the other
// rule catches, so dropping either from kimiTailResumes fails it.
func TestKimiTailResumesKeepsBothRules(t *testing.T) {
	head := `{"type":"metadata","protocol_version":"1.4","created_at":1790870000000}` + "\n" +
		`{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"fix it"}]},"time":1790870001000}` + "\n" +
		`{"type":"context.append_loop_event","event":{"type":"step.begin","uuid":"s1"},"time":1790870001100}` + "\n"
	for _, c := range []struct {
		name, before, after string
		want                bool
	}{
		{
			// The step ended before the offset, so the stream rule lets it
			// through; the failed result answers a call already stored.
			name:   "failed result after a closed step",
			before: `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c1","name":"Bash","args":{"command":"go vet"}},"time":1790870002000}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1"},"time":1790870002100}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"c1","result":{"output":"Command failed with exit code: 2.","isError":true}},"time":1790870002200}` + "\n",
		},
		{
			// No tool at all, so the answering rule lets it through; the
			// reply's text goes on past the offset.
			name:   "reply streamed across the offset",
			before: `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"half "}},"time":1790870002000}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"done"}},"time":1790870002100}` + "\n",
		},
		{
			name:   "next turn after a finished step",
			before: `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"done"}},"time":1790870002000}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1"},"time":1790870002100}` + "\n",
			after:  `{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"thanks"}]},"time":1790870003000}` + "\n",
			want:   true,
		},
		{
			// A call and its failure in the same tail: nothing stored
			// misses the exit.
			name:   "call and failure both in the tail",
			before: `{"type":"context.append_loop_event","event":{"type":"step.end","uuid":"s1"},"time":1790870002000}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c2","name":"Bash","args":{"command":"go vet"}},"time":1790870002100}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"c2","result":{"output":"Command failed with exit code: 2.","isError":true}},"time":1790870002200}` + "\n",
			want:   true,
		},
		{
			// A tool call inside an open step counts as the stream going on,
			// and the reply that closes it in the tail is its end, not more
			// of it; a message line without a message changes nothing.
			name:   "open step closed by the reply",
			before: `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c3","name":"Read","args":{}},"time":1790870002000}` + "\n" + `{"type":"context.append_message"}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"think","think":"hm"}},"time":1790870002100}` + "\n" + `{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"time":1790870002200}` + "\n" + `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"late"}},"time":1790870002300}` + "\n",
			want:   true,
		},
		{
			name:   "tool result streamed across the offset",
			before: `{"type":"context.append_loop_event","event":{"type":"content.part","part":{"type":"text","text":"reading "}},"time":1790870002000}` + "\n",
			after:  `{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"c4","name":"Read","args":{}},"time":1790870002100}` + "\n",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wire.jsonl")
			if err := os.WriteFile(path, []byte(head+c.before+c.after), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := kimiTailResumes(path, int64(len(head+c.before))); got != c.want {
				t.Fatalf("kimiTailResumes = %v, want %v", got, c.want)
			}
			if !kimiTailResumes(path, 0) {
				t.Fatal("a read from the start must resume")
			}
		})
	}
}

// Kimi Code 2.x names the directory cwd and stamps state.json in epoch
// milliseconds; the string-typed struct failed on the number and every 2.x
// session indexed with no project and resumed with no cd (#5143).
func TestKimi2StateGivesProjectAndResumeDir(t *testing.T) {
	root, wire := kimiFixture(t)
	state := `{"id":"session_t01","version":2,"cwd":"/home/u/work/proj","createdAt":1782295200000,"updatedAt":1782295205000,"agents":{"main":{"type":"main"}},"custom":{},"archived":false,"isCustomTitle":false}`
	if err := os.WriteFile(filepath.Join(root, "kimi", "sessions", "wd_demo_ab", "session_t01", "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wire, []byte(kimiWireHead), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v %d", err, len(ss))
	}
	if ss[0].Project != "work/proj" {
		t.Fatalf("project = %q, want work/proj", ss[0].Project)
	}
	if got := KimiSessionDir(wire); got != "/home/u/work/proj" {
		t.Fatalf("resume dir = %q", got)
	}
	if ss[0].Started.UnixMilli() != 1782295200000 {
		t.Fatalf("started = %v", ss[0].Started)
	}
}

// Kimi Code 2.x hands the person's message to the model with each
// UserPromptSubmit hook's output as a <hook_result> part ahead of the typed
// words, all under origin user. The parts are the host's; the session was
// titled by their empty wrappers (#5144).
func TestKimi2HookResultPartsAreNotTheUsersWords(t *testing.T) {
	_, wire := kimiFixture(t)
	line := `{"type":"context.append_message","agentId":"main","message":{"role":"user","content":[` +
		`{"type":"text","text":"<hook_result hook_event=\"UserPromptSubmit\">\n<deja-recall>\nrecalled\n</deja-recall>\n</hook_result>"},` +
		`{"type":"text","text":"<hook_result hook_event=\"UserPromptSubmit\">\nplain hook stdout\n</hook_result>"},` +
		`{"type":"text","text":"raise the retry cap to 6"}],"origin":{"kind":"user"}},"time":1782295201000}` + "\n"
	if err := os.WriteFile(wire, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKimiFile(wire)
	if err != nil || len(ss) != 1 || len(ss[0].Messages) != 1 {
		t.Fatalf("parse: %v %+v", err, ss)
	}
	if got := ss[0].Messages[0].Text; got != "raise the retry cap to 6" {
		t.Fatalf("user text = %q", got)
	}
}

// kiroTestDB seeds a data.sqlite3 with the conversations_v2 table as kiro-cli
// 2.22.0 creates it.
func kiroTestDB(t *testing.T, rows ...string) string {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "data.sqlite3")
	schema := `CREATE TABLE conversations_v2 (key TEXT NOT NULL, conversation_id TEXT NOT NULL, value TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, PRIMARY KEY (key, conversation_id));
` + strings.Join(rows, "\n")
	cmd := exec.CommandContext(t.Context(), "sqlite3", db)
	cmd.Stdin = strings.NewReader(schema)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	t.Setenv("DEJA_KIRO_DB", db)
	return db
}

const kiroDBConversation = `{"conversation_id":"bf73e1f5-0000-4000-8000-000000000002","history":[
 {"user":{"env_context":{"env_state":{"current_working_directory":"/tmp/proj"}},"content":{"Prompt":{"prompt":"fix the retry loop"}},"timestamp":"2026-10-01T10:00:00+00:00"},
  "assistant":{"ToolUse":{"message_id":"m2","content":"Checking the loop.","tool_uses":[{"id":"t1","name":"execute_bash","args":{"command":"go test ./retry"}},{"id":"t2","name":"fs_write","args":{"command":"create","path":"/tmp/proj/retry.go","file_text":"package retry\n\nconst maxAttemptsBeforeGivingUp = 3\n"}}]}}},
 {"user":{"content":{"ToolUseResults":{"tool_use_results":[{"tool_use_id":"t1","content":[{"Json":{"exit_status":"0","stdout":"ok  retry 0.01s","stderr":""}}],"status":"Success"},{"tool_use_id":"t2","content":[{"Text":""}],"status":"Success"}]}},"timestamp":null},
  "assistant":{"Response":{"message_id":"m3","content":"The loop now stops after three attempts."}}}]}`

func kiroDBRow(key, id, value string, updated int64) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	return "INSERT INTO conversations_v2 VALUES (" + q(key) + "," + q(id) + "," + q(value) + "," +
		strconv.FormatInt(updated, 10) + "," + strconv.FormatInt(updated, 10) + ");"
}

// `kiro-cli chat --no-interactive` writes nothing under ~/.kiro/sessions: the
// conversation is a row of data.sqlite3, and every headless run was missing
// from the index (#4300).
func TestKiroDBReadsNoInteractiveConversations(t *testing.T) {
	db := kiroTestDB(t, kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000002", kiroDBConversation, 1790848800000))
	ss, err := ParseKiroDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want the one conversation", len(ss))
	}
	s := ss[0]
	if s.Harness != "kiro" || s.ID != "bf73e1f5-0000-4000-8000-000000000002" {
		t.Errorf("harness/id = %s/%s, want kiro and the conversation_id", s.Harness, s.ID)
	}
	if !strings.Contains(s.Project, "proj") {
		t.Errorf("project = %q, want it from the row's directory", s.Project)
	}
	got := kiroRoles(s)
	if !kiroHas(got["user"], "fix the retry loop") {
		t.Errorf("the prompt is missing: %q", got["user"])
	}
	if !kiroHas(got["assistant"], "Checking the loop.") || !kiroHas(got["assistant"], "three attempts") {
		t.Errorf("the replies are missing: %q", got["assistant"])
	}
	if !kiroHas(got[RoleCommand], "$ go test ./retry") {
		t.Errorf("the execute_bash call is not a command: %q", got[RoleCommand])
	}
	if !kiroHas(got[RoleFiles], "/tmp/proj/retry.go") || len(got[RoleWrote]) == 0 {
		t.Errorf("the fs_write call is not a file and a write: %+v", s.Messages)
	}
	if !kiroHas(got[RoleToolOutput], "ok  retry") {
		t.Errorf("the tool output is missing: %q", got[RoleToolOutput])
	}
	if s.Started.IsZero() {
		t.Error("the session has no time")
	}
}

// The incremental read returns only conversations written after the
// watermark, whole, so what comes back replaces what the index held.
func TestKiroDBSinceFilters(t *testing.T) {
	old := strings.Replace(kiroDBConversation, "bf73e1f5-0000-4000-8000-000000000002", "aa000000-0000-4000-8000-000000000001", 1)
	db := kiroTestDB(t,
		kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000002", kiroDBConversation, 1790848800000),
		kiroDBRow("/tmp/old", "aa000000-0000-4000-8000-000000000001", old, 1790000000000),
	)
	ss, err := ParseKiroDBSince(db, time.UnixMilli(1790500000000))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].ID != "bf73e1f5-0000-4000-8000-000000000002" {
		t.Fatalf("since read = %+v, want only the newer conversation", ss)
	}
}

// The registry has to list and claim the store, or incremental ingest never
// opens it.
func TestKiroClaimsItsDB(t *testing.T) {
	t.Setenv("DEJA_KIRO_ROOT", t.TempDir())
	db := kiroTestDB(t, kiroDBRow("/tmp/proj", "bf73e1f5-0000-4000-8000-000000000002", kiroDBConversation, 1790848800000))
	files := KiroSessionFiles()
	if len(files) != 1 || files[0] != db {
		t.Fatalf("files = %v, want the store", files)
	}
	claimed := false
	for _, h := range allHarnesses() {
		for _, k := range h.Kinds {
			if k.Match(db) {
				claimed = h.Name == "kiro"
			}
		}
	}
	if !claimed {
		t.Error("no kiro kind claims the store")
	}
	if ss := LoadKiro(); len(ss) != 1 {
		t.Errorf("LoadKiro = %d sessions, want the store's one", len(ss))
	}
}

// kiro-cli creates data.sqlite3 on first launch and conversations_v2 only once
// a headless chat is saved, so a fresh install has a store without the table.
// That is an empty store, not a schema change to report.
func TestKiroDBWithoutTheTableIsEmpty(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	db := filepath.Join(t.TempDir(), "data.sqlite3")
	cmd := exec.CommandContext(t.Context(), "sqlite3", db, "CREATE TABLE state (key TEXT PRIMARY KEY, value BLOB);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed: %v: %s", err, out)
	}
	ss, err := ParseKiroDB(db)
	if err != nil || len(ss) != 0 {
		t.Fatalf("ParseKiroDB = %d sessions, %v; want none and no error", len(ss), err)
	}
}

// Without the sqlite3 CLI the store is there and unread, which is the missing
// tool to name, not a format to report.
func TestKiroDBNamesTheMissingSQLite3(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "data.sqlite3")
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KIRO_DB", db)
	t.Setenv("PATH", dir)
	if SQLite3Available() {
		t.Skip("sqlite3 still resolvable")
	}
	if got := SkipReason("kiro"); got != SQLite3NotFound {
		t.Errorf("SkipReason(kiro) = %q, want %q", got, SQLite3NotFound)
	}
}

func jsonUnmarshalString(s string, v any) error { return json.Unmarshal([]byte(s), v) }

// A streamed reply is several records under one message id, each carrying the
// next piece. Read one per record and recall quotes a fragment, which is the
// thing worth pinning: the run has to come back as one answer (#3103).
func TestKiroCLIJoinsAStreamedReply(t *testing.T) {
	root := kiroFixtureRoot(t)
	ss, err := ParseKiroCLIFile(filepath.Join(root, "cli", "registry-kiro.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.ID != "registry-kiro" {
		t.Errorf("id = %q, want the header's session_id", s.ID)
	}
	if !strings.Contains(s.Project, "api") {
		t.Errorf("project = %q, want it from the header's cwd", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the prompt and one joined answer: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || !strings.Contains(s.Messages[0].Text, "retry loop") {
		t.Errorf("first message = %+v, want the prompt", s.Messages[0])
	}
	answer := s.Messages[1]
	if answer.Role != "assistant" {
		t.Errorf("second message role = %q, want assistant", answer.Role)
	}
	if !strings.Contains(answer.Text, "needs <=") || !strings.Contains(answer.Text, "compares with <") {
		t.Errorf("the reply was not joined, so half the answer is missing: %q", answer.Text)
	}
	if answer.Time.IsZero() {
		t.Error("the answer has no time, so recency cannot rank it")
	}
}

// The IDE writes a different file in a different place, and its metadata half
// names the workspace.
func TestKiroIDEReadsTheWorkspaceSession(t *testing.T) {
	root := kiroFixtureRoot(t)
	path := filepath.Join(root, "w-api", "sess_00000000-0000-4000-8000-000000000000", "messages.jsonl")
	ss, err := ParseKiroIDEFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.ID != "sess_00000000-0000-4000-8000-000000000000" {
		t.Errorf("id = %q, want the session.json id", s.ID)
	}
	if !strings.Contains(s.Project, "api") {
		t.Errorf("project = %q, want it from workspacePaths", s.Project)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("messages = %d, want the prompt and the answer: %+v", len(s.Messages), s.Messages)
	}
	if !strings.Contains(s.Messages[1].Text, "backfill") {
		t.Errorf("the answer is missing: %+v", s.Messages)
	}
	// The bookkeeping lines are not turns: a session_metadata or turn_end
	// record read as a message is an empty message in the middle of a recall.
	for _, m := range s.Messages {
		if m.Text == "" {
			t.Errorf("a bookkeeping record became a message: %+v", s.Messages)
		}
	}
}

// A kiro-cli --v3 TUI turn that ran a shell command, taken from a logged-in
// 2.22.0 stand: the prompt, the command, its output and the answer are read.
// The Reasoning records hold "..." in place of the encrypted thinking, and
// session_start holds the system prompt; neither is a turn.
func TestKiroV3ReadsAssistantAndToolRecords(t *testing.T) {
	path := filepath.Join("..", "..", "fixtures", "registry", "kiro-v3", "w-v3", "sess_00000000-0000-4000-8000-000000000003", "messages.jsonl")
	ss, err := ParseKiroIDEFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, %v", len(ss), err)
	}
	got := make([]string, 0, len(ss[0].Messages))
	for _, m := range ss[0].Messages {
		got = append(got, m.Role+": "+strings.TrimSpace(m.Text))
	}
	// echo is below what deja keeps as a command (worthIndexing); the call
	// itself is read, checked below with a command that is kept.
	want := []string{
		"user: Run the shell command: echo deja-v3-stand  -- then reply with one short sentence saying what it printed.",
		RoleToolOutput + ": Output:\ndeja-v3-stand\n\n\nExit Code: 0",
		"assistant: It printed `deja-v3-stand`.",
	}
	if strings.Join(got, "\n|") != strings.Join(want, "\n|") {
		t.Errorf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n|"), strings.Join(want, "\n|"))
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if !strings.Contains(line, `"type":"tool_call"`) {
			continue
		}
		line = strings.Replace(line, "echo deja-v3-stand", "go test ./pool/...", 1)
		var m map[string]any
		if err := jsonUnmarshalString(line, &m); err != nil {
			t.Fatal(err)
		}
		call, isCall, failed := kiroIDECall(m)
		work := kiroWorkRecords([]any{call}, nil, parseTimeAny(m["timestamp"]))
		if !isCall || failed || len(work) == 0 || work[0].Role != RoleCommand || work[0].Text != "$ go test ./pool/..." {
			t.Errorf("V3 tool_call = %v %v %v, want the command record", isCall, failed, work)
		}
		return
	}
	t.Error("fixture has no tool_call record")
}

// A record whose type is not a turn must not become one. The file carries the
// agent's own bookkeeping — context usage, turn boundaries, tool calls — and a
// line attributed to a role on the way past is a message with somebody else's
// words in it.
func TestKiroIDEDropsRecordsThatAreNotTurns(t *testing.T) {
	for _, line := range []string{
		`{"payload":{"type":"session_metadata","key":"contextUsage","content":"12.5"}}`,
		`{"payload":{"type":"turn_end","content":""}}`,
		`{"payload":{"type":"usage_summary","content":"elapsed 2500"}}`,
		`{"payload":{"type":"tool_call","content":"fs_read"}}`,
	} {
		var m map[string]any
		if err := jsonUnmarshalString(line, &m); err != nil {
			t.Fatal(err)
		}
		if role, text := kiroIDELine(m); role != "" || text != "" {
			t.Errorf("%s became a %q message %q", line, role, text)
		}
	}
}

// Older builds wrote the flat shape into the same file, so both are read.
func TestKiroIDEReadsTheFlatShape(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "w-api", "sess_old")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "messages.jsonl")
	lines := `{"timestamp":"2026-08-01T09:00:00Z","role":"user","content":"why is the index rebuilt on every start"}
{"timestamp":"2026-08-01T09:00:20Z","role":"assistant","content":"The version in the store was older than the binary's."}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKiroIDEFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || len(ss[0].Messages) != 2 {
		t.Fatalf("the flat shape was not read: %+v", ss)
	}
}

// Which reader gets a path is decided by where it sits, and the two layouts
// share a root.
func TestKiroClaimsItsOwnFilesOnly(t *testing.T) {
	root := kiroFixtureRoot(t)
	cli := filepath.Join(root, "cli", "registry-kiro.jsonl")
	ide := filepath.Join(root, "w-api", "sess_00000000-0000-4000-8000-000000000000", "messages.jsonl")
	if !KiroUnderCLI(cli) || KiroUnderIDE(cli) {
		t.Errorf("the CLI transcript was not claimed by the CLI reader alone: %s", cli)
	}
	if !KiroUnderIDE(ide) || KiroUnderCLI(ide) {
		t.Errorf("the IDE transcript was not claimed by the IDE reader alone: %s", ide)
	}
	if KiroUnderIDE(filepath.Join(root, "w-api", "notes", "messages.jsonl")) {
		t.Error("a messages.jsonl outside a sess_ directory was claimed as a session")
	}
	files := KiroSessionFiles()
	if len(files) != 2 {
		t.Errorf("files = %v, want both transcripts and neither header", files)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "session.json") {
			t.Errorf("the metadata half was listed as a transcript: %s", f)
		}
	}
}

// kiroFixtureRoot points the reader at the committed fixture store.
func kiroFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "registry", "kiro"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_KIRO_ROOT", root)
	t.Setenv("DEJA_KIRO_DB", filepath.Join(t.TempDir(), "data.sqlite3"))
	return root
}

// A sub-agent's header names the session that spawned it; 2.32 also writes
// session_created_reason "subagent" on top-level sessions, which says nothing.
func TestKiroCLISubAgentHangsUnderItsParent(t *testing.T) {
	dir := t.TempDir()
	write := func(id, header string) string {
		if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(header), 0o644); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, id+".jsonl")
		line := `{"version":"v1","kind":"Prompt","data":{"message_id":"m","content":[{"kind":"text","data":"review the jitter"}],"meta":{"timestamp":1792281752}}}` + "\n"
		if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	child := write("e012b5f2-0f80-45a3-a1f6-eccc522bb35e", `{"session_id":"e012b5f2-0f80-45a3-a1f6-eccc522bb35e","cwd":"/w/app","parent_session_id":"136cf72a-585d-4047-997f-d37a0d0f194a","session_created_reason":"subagent"}`)
	top := write("136cf72a-585d-4047-997f-d37a0d0f194a", `{"session_id":"136cf72a-585d-4047-997f-d37a0d0f194a","cwd":"/w/app","session_created_reason":"subagent"}`)
	ss, err := ParseKiroCLIFile(child)
	if err != nil || len(ss) != 1 {
		t.Fatalf("child: %v %d", err, len(ss))
	}
	if ss[0].Kind != "subagent" || ss[0].Parent != "136cf72a-585d-4047-997f-d37a0d0f194a" {
		t.Errorf("child kind=%q parent=%q, want a subagent under its parent", ss[0].Kind, ss[0].Parent)
	}
	ss, err = ParseKiroCLIFile(top)
	if err != nil || len(ss) != 1 {
		t.Fatalf("top: %v %d", err, len(ss))
	}
	if ss[0].Kind != "" || ss[0].Parent != "" {
		t.Errorf("top-level session kind=%q parent=%q, want neither", ss[0].Kind, ss[0].Parent)
	}
}

// kiroRoles collects the text of every message under a role.
func kiroRoles(s model.Session) map[string][]string {
	out := map[string][]string{}
	for _, m := range s.Messages {
		out[m.Role] = append(out[m.Role], m.Text)
	}
	return out
}

func kiroHas(texts []string, want string) bool {
	for _, t := range texts {
		if strings.Contains(t, want) {
			return true
		}
	}
	return false
}

// The TUI writes the work beside the talk: toolUse parts in an
// AssistantMessage, toolResult parts in a ToolResults record. Read as text
// only, a Kiro CLI session had no command, no file and no tool output (#4299).
func TestKiroCLIReadsToolCallsAndResults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := "11111111-2222-4333-8444-555555555555"
	header := `{"session_id":"` + id + `","cwd":"/tmp/proj"}`
	lines := `{"version":"v1","kind":"Prompt","data":{"message_id":"m1","content":[{"kind":"text","data":"fix the retry loop"}],"meta":{"timestamp":1790848800}}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m2","content":[{"kind":"text","data":"Checking the loop."},{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"go test ./retry"}}},{"kind":"toolUse","data":{"toolUseId":"t2","name":"write","input":{"command":"create","path":"/tmp/proj/retry.go","content":"package retry\n\nconst maxAttemptsBeforeGivingUp = 3\n"}}}]}}
{"version":"v1","kind":"ToolResults","data":{"message_id":"m3","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"ok  retry 0.01s","stderr":""}}],"status":"success"}},{"kind":"toolResult","data":{"toolUseId":"t2","content":[{"kind":"text","data":"Successfully created /tmp/proj/retry.go"}],"status":"success"}}]}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m4","content":[{"kind":"text","data":"Now the cap."},{"kind":"toolUse","data":{"toolUseId":"t3","name":"write","input":{"command":"strReplace","path":"/tmp/proj/retry.go","oldStr":"const maxAttemptsBeforeGivingUp = 3","newStr":"const maxAttemptsBeforeGivingUp = 5"}}},{"kind":"toolUse","data":{"toolUseId":"t4","name":"read","input":{"operations":[{"mode":"Line","path":"/tmp/proj/retry_test.go"},{"mode":"Directory","path":"/tmp/proj"}]}}}]}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m4","content":[{"kind":"text","data":" Done."}]}}
{"version":"v1","kind":"AssistantMessage","data":{"message_id":"m5","content":[{"kind":"text","data":"The loop now stops after five attempts."}]}}
`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(header), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseKiroCLIFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	got := kiroRoles(ss[0])
	if !kiroHas(got[RoleCommand], "$ go test ./retry") {
		t.Errorf("the shell call is not a command: %q", got[RoleCommand])
	}
	if !kiroHas(got[RoleFiles], "/tmp/proj/retry.go") || !kiroHas(got[RoleFiles], "/tmp/proj/retry_test.go") {
		t.Errorf("the written and read files are not recorded: %q", got[RoleFiles])
	}
	for _, rec := range got[RoleFiles] {
		for p := range strings.SplitSeq(rec, "\n") {
			if p == "/tmp/proj" {
				t.Errorf("a directory listing was recorded as a file: %q", got[RoleFiles])
			}
		}
	}
	if len(got[RoleWrote]) == 0 {
		t.Errorf("the created file's content is not recorded: %+v", ss[0].Messages)
	}
	if !kiroHas(got[RoleEdit], "const maxAttemptsBeforeGivingUp = 3") {
		t.Errorf("the strReplace span is not recorded: %q", got[RoleEdit])
	}
	if !kiroHas(got[RoleToolOutput], "ok  retry") || !kiroHas(got[RoleToolOutput], "Successfully created") {
		t.Errorf("the tool results are missing: %q", got[RoleToolOutput])
	}
	// The streamed reply still joins, with the work records in between.
	if !kiroHas(got["assistant"], "Now the cap. Done.") {
		t.Errorf("the streamed reply was not joined: %q", got["assistant"])
	}
	if len(got["user"]) != 1 {
		t.Errorf("user turns = %q, want the prompt alone", got["user"])
	}
}

func writeNotes(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	body := ""
	var bodySb13 strings.Builder
	for _, l := range lines {
		bodySb13.WriteString(l + "\n")
	}
	body += bodySb13.String()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A session's state is whatever was recorded last. Getting this backwards means
// a correction is ignored and the thing it corrected is served as current — the
// exact failure this map exists to prevent.
func TestLatestRecordedStateWins(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"accepted","text":"looked right","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s1","state":"rejected","text":"reverted a week later","ts":"2026-01-08T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s2","state":"accepted","text":"still holds","ts":"2026-01-02T00:00:00Z"}`,
	)
	got := promotedLifecyclesFrom(path)
	if len(got) != 2 {
		t.Fatalf("sessions = %v", LifecycleKeys(got))
	}
	if got["claude:s1"].State != "rejected" || got["claude:s1"].Note != "reverted a week later" {
		t.Fatalf("s1 = %+v, want the later rejection", got["claude:s1"])
	}
	if got["claude:s2"].State != "accepted" {
		t.Fatalf("s2 = %+v", got["claude:s2"])
	}
}

// Two entries stamped in the same second still have an order: the file's. A
// correction written moments after the note it corrects is still the correction.
func TestSameTimestampFallsBackToFileOrder(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"accepted","text":"first","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s1","state":"superseded","text":"second","ts":"2026-01-01T00:00:00Z"}`,
	)
	if got := promotedLifecyclesFrom(path)["claude:s1"]; got.State != "superseded" || got.Note != "second" {
		t.Fatalf("got %+v, want the later line", got)
	}
}

// Everything that is not a promotion with a known state is ignored rather than
// guessed at: a plain note, an unknown state, a line with no session, and text
// that is not JSON at all.
func TestOnlyPromotionsWithKnownStatesCount(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"note","project":"p","text":"a plain note","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"claude:s1","state":"invented","text":"nope","ts":"2026-01-01T00:00:00Z"}`,
		`{"kind":"promoted","session":"","state":"rejected","text":"no session","ts":"2026-01-01T00:00:00Z"}`,
		`not json at all`,
		``,
		`{"kind":"promoted","session":"codex:s9","state":"stale","text":"kept","ts":"2026-01-01T00:00:00Z"}`,
	)
	got := promotedLifecyclesFrom(path)
	if len(got) != 1 || got["codex:s9"].State != "stale" {
		t.Fatalf("got %v", LifecycleKeys(got))
	}
}

func TestMissingNotesFileIsNotAnError(t *testing.T) {
	if got := promotedLifecyclesFrom(filepath.Join(t.TempDir(), "absent.jsonl")); got != nil {
		t.Fatalf("got %v, want nothing", LifecycleKeys(got))
	}
	if got := promotedLifecyclesFrom(writeNotes(t)); got != nil {
		t.Fatalf("an empty file produced %v", LifecycleKeys(got))
	}
}

// A note with no timestamp still records a state; it just cannot claim to be
// newer than one that has a date.
func TestUndatedEntriesStillCount(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"rejected","text":"undated"}`,
	)
	got := promotedLifecyclesFrom(path)["claude:s1"]
	if got.State != "rejected" || !got.At.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

func TestPromotedLifecyclesReadsTheConfiguredFile(t *testing.T) {
	path := writeNotes(t,
		`{"kind":"promoted","session":"claude:s1","state":"rejected","text":"why","ts":"2026-01-01T00:00:00Z"}`,
	)
	t.Setenv("DEJA_NOTES_FILE", path)
	if got := PromotedLifecycles()["claude:s1"]; got.State != "rejected" {
		t.Fatalf("got %+v", got)
	}
}

// mcodeFixture is the published transcript: one turn, one command, one edit.
const mcodeFixture = "mcode/v2/sessions/2026/10/20/02-57-27-477-session_bXZzXzBmMWUyZDNjNGI1YTY5Nzg4Nzk2YTViNGMzZDJlMWYw/messages.jsonl"

func mcodeFixturePath(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "fixtures", "registry", filepath.FromSlash(mcodeFixture))
}

func TestMcodeFileReadsTheTurnsAndTheWork(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "mcode" {
		t.Fatalf("harness = %q, want mcode", s.Harness)
	}
	if s.ID != "mvs_0f1e2d3c4b5a69788796a5b4c3d2e1f0" {
		t.Fatalf("id = %q, want the decoded session id", s.ID)
	}
	if s.Project == "" {
		t.Fatal("project is empty")
	}
	if s.Started.IsZero() || s.Updated.IsZero() {
		t.Fatalf("clock is empty: %#v", s)
	}
	roles := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		roles = append(roles, m.Role)
	}
	for _, want := range []string{"user", "assistant", RoleToolOutput, RoleCommand, RoleFiles, RoleEdit} {
		if !containsRole(roles, want) {
			t.Fatalf("roles = %v, want a %s record", roles, want)
		}
	}
}

func TestMcodeFileKeepsOnlyWhatThePersonTyped(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	first := firstUserMessage(ss[0])
	if first == nil {
		t.Fatal("the session holds no user turn")
	}
	if first.Text != "How do I install the mcode harness?" {
		t.Fatalf("user text = %q, want the canonical text alone", first.Text)
	}
	if strings.Contains(first.Text, "system-reminder") {
		t.Fatal("the runtime's own context was indexed as the person's words")
	}
}

// The runtime writes a compaction summary where the turns it replaced were,
// and keeps those turns in its own snapshots. The summary is the only record
// of them the transcript holds, so it is indexed — under the summary role,
// which is what keeps it from reading as speech nobody said (#4801).
func TestMcodeFileIndexesTheCompactionSummaryUnderItsOwnRole(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	var summaries, elsewhere int
	for _, m := range ss[0].Messages {
		if !strings.Contains(m.Text, "MCODE-SUMMARY") {
			continue
		}
		if m.Role == RoleSummary {
			summaries++
		} else {
			elsewhere++
		}
	}
	if summaries != 1 || elsewhere != 0 {
		t.Errorf("summary indexed %d times under its role and %d times under another, want once and never",
			summaries, elsewhere)
	}
}

func TestMcodeFileLeavesTheThinkingOut(t *testing.T) {
	ss, err := ParseMcodeFile(mcodeFixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if strings.Contains(m.Text, "The harness needs a reader") {
			t.Fatal("the model's thinking was indexed as what it said")
		}
	}
}

func TestMcodeFileResumesFromAnOffset(t *testing.T) {
	path := mcodeFixturePath(t)
	full, err := ParseMcodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A resume starts past the first turn, so the title has to come from
	// somewhere else: the line is read again for it. The first line of a
	// compacted session is the summary, which is not a turn — the cut is
	// after the first one that is.
	cut := int64(bytes.IndexByte(b, '\n') + 1)
	if i := bytes.Index(b, []byte(`"role":"user"`)); i >= 0 {
		if j := bytes.IndexByte(b[i:], '\n'); j >= 0 {
			cut = int64(i + j + 1)
		}
	}
	tail, err := ParseMcodeFileFromOffset(path, cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 {
		t.Fatalf("sessions = %d, want 1", len(tail))
	}
	if tail[0].Title != full[0].Title {
		t.Fatalf("resumed title = %q, want %q", tail[0].Title, full[0].Title)
	}
	if len(tail[0].Messages) >= len(full[0].Messages) {
		t.Fatalf("resumed messages = %d, want fewer than the whole session's %d",
			len(tail[0].Messages), len(full[0].Messages))
	}
}

func TestMcodeSessionIDIsReadFromTheManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"),
		[]byte(`{"schemaVersion":1,"sessionId":"mvs_from_manifest"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, mcodeTranscriptName)
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if id := mcodeSessionID(path); id != "mvs_from_manifest" {
		t.Fatalf("id = %q, want the manifest's", id)
	}
}

// The runtime keeps more beside a transcript than the four files it names:
// the snapshots it was working from, and the reports it spilled when a tool
// printed more than it would hold inline. None of them is a conversation, so
// none of them may be counted as a file deja could not read.
func TestMcodeSidecarsAreNamedRatherThanCountedAsUnread(t *testing.T) {
	root := t.TempDir()
	session := filepath.Join(root, "2026", "10", "20", "03-00-00-000-session_abc")
	for _, rel := range []string{
		mcodeTranscriptName,
		"manifest.json",
		"history-catalog.json",
		"llm-call.json",
		"user-message-locators.jsonl",
		filepath.Join("snapshots", "env-g1.json"),
		filepath.Join("snapshots", "g1.jsonl"),
		filepath.Join("reports", "tool-outputs", "call-1.png"),
		filepath.Join("reports", "tool-outputs", "call-1.txt"),
	} {
		p := filepath.Join(session, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_MCODE_ROOT", root)

	seen := McodeSessionFiles()
	if len(seen) != 1 {
		t.Fatalf("transcripts = %d, want 1: %v", len(seen), seen)
	}
	beside := McodeSidecarFiles()
	if len(beside) != 8 {
		t.Fatalf("sidecars = %d, want 8: %v", len(beside), beside)
	}
	for _, p := range beside {
		if strings.HasSuffix(p, mcodeTranscriptName) {
			t.Fatalf("the transcript was filed as bookkeeping: %s", p)
		}
	}
}

// The registry loader finds the session and files it under the manifest's id.
func TestMcodeTheStoreIsReadableUnderTheManifestID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "10", "20", "03-00-00-000-session_abc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schemaVersion":1,"sessionId":"mvs_lookup",` +
		`"createdAtMs":1792465960806,"updatedAtMs":1792465960806}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	turn := `{"message_id":"m1","turn_id":"t1","message":{"role":"user",` +
		`"content":[{"type":"text","text":"what did we decide about mcode?"}],` +
		`"timestamp":1792465960900}}`
	if err := os.WriteFile(filepath.Join(dir, mcodeTranscriptName), []byte(turn+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_MCODE_ROOT", root)

	ss := LoadMcode()
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	if ss[0].ID != "mvs_lookup" {
		t.Fatalf("id = %q, want the manifest's", ss[0].ID)
	}
}

// A summary with nothing in it is not a summary: the runtime writes one
// wherever it cut, and a cut that took nothing leaves the field empty.
func TestMcodeAnEmptyCompactionSummaryIsNotIndexed(t *testing.T) {
	dir := t.TempDir()
	lines := []string{
		`{"message_id":"m1","turn_id":"t1:compaction","message":{"role":"compactionSummary","summary":"   ","timestamp":1792465960900}}`,
		`{"message_id":"m2","turn_id":"t1","message":{"role":"user","content":[{"type":"text","text":"carry on"}],"timestamp":1792465960900}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, mcodeTranscriptName), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseMcodeFile(filepath.Join(dir, mcodeTranscriptName))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleSummary {
			t.Fatal("an empty summary was indexed")
		}
	}
}

func containsRole(roles []string, want string) bool {
	return slices.Contains(roles, want)
}

// firstUserMessage is the first turn the person wrote. A session that has
// been compacted opens with the summary the compaction wrote, so the first
// message of a session is not always the person's.
func firstUserMessage(s model.Session) *model.Message {
	for i, m := range s.Messages {
		if m.Role == "user" {
			return &s.Messages[i]
		}
	}
	return nil
}

// parseMcodeLines writes the lines as one session transcript and reads it.
func parseMcodeLines(t *testing.T, lines ...string) []string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, mcodeTranscriptName)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseMcodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			out = append(out, m.Role+": "+m.Text)
		}
	}
	return out
}

func mcodeUserLine(msg string) string {
	return `{"message_id":"m1","turn_id":"t1","message":{"role":"user",` + msg + `,"timestamp":1792465960900}}`
}

// The runtime writes an empty range at the end of the text plus
// genuineUserQueryText whenever the typed text is not the tail of what it
// assembled (bK in @minimax-ai/code 0.6.12). The range alone cuts to "", and
// the turn went missing from the index (#5593).
func TestMcodeGenuineUserQueryTextIsTheTypedTurn(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<system-reminder>\nagent banner\n</system-reminder>\n\nfix the flaky login test (edited by queue)"}],`+
			`"canonicalTextRange":{"startOffset":93,"endOffset":93},"genuineUserQueryText":"fix the flaky login test"`))
	if len(got) != 1 || got[0] != "user: fix the flaky login test" {
		t.Fatalf("messages = %q, want the typed text alone", got)
	}
}

// genuineUserQueryText wins over a range that would cut something else.
func TestMcodeGenuineUserQueryTextWinsOverTheRange(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"abcdef"}],`+
			`"canonicalTextRange":{"startOffset":0,"endOffset":3},"genuineUserQueryText":"typed words"`))
	if len(got) != 1 || got[0] != "user: typed words" {
		t.Fatalf("messages = %q, want genuineUserQueryText", got)
	}
}

// A turn with no range is the assembled text, and the runtime strips its own
// blocks from it before reading it back (AUt). Kept whole, the banner was
// indexed as the person's words.
func TestMcodeATurnWithNoRangeLosesTheRuntimeBlocks(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<system-reminder>\nbanner\n</system-reminder>\n\nsecond question"},`+
			`{"type":"text","text":"<background-task-finished id=\"x\">task done</background-task-finished><SYSTEM-REMINDER/>"}]`))
	if len(got) != 1 || got[0] != "user: second question" {
		t.Fatalf("messages = %q, want the question alone", got)
	}
}

// A scheduled task's prompt is not a person's turn.
func TestMcodeACronPromptIsNotATurn(t *testing.T) {
	for _, head := range []string{"[System CronTask nightly]", "[cron report 3]"} {
		got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"`+head+` run the checks"}]`))
		if len(got) != 0 {
			t.Fatalf("%s: messages = %q, want none", head, got)
		}
	}
	// The control: the same words without the head are a turn.
	if got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"run the checks"}]`)); len(got) != 1 {
		t.Fatalf("messages = %q, want the turn", got)
	}
}

// The range counts into every text block joined with a newline (YIe), not
// into the first block: here it runs past the first one.
func TestMcodeTheRangeSpansTheJoinedBlocks(t *testing.T) {
	// "<r/>" + "\n" = 5 units, then "second block typed" (18 units).
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<r/>"},{"type":"image","data":"x"},{"type":"text","text":"second block typed"}],`+
			`"canonicalTextRange":{"startOffset":5,"endOffset":23}`))
	if len(got) != 1 || got[0] != "user: second block typed" {
		t.Fatalf("messages = %q, want the second block", got)
	}
}

// The offsets are UTF-16 units, as a JavaScript string counts them.
func TestMcodeTheRangeCountsUTF16Units(t *testing.T) {
	// "前缀\n" is 3 units, "修复登录" 4 more, "😀" 2.
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"前缀\n修复登录😀"}],"canonicalTextRange":{"startOffset":3,"endOffset":9}`))
	if len(got) != 1 || got[0] != "user: 修复登录😀" {
		t.Fatalf("messages = %q", got)
	}
}

// A range that does not fit the text, or that is negative, falls back to the
// text with the runtime's blocks taken out rather than to a panic or the
// banner.
func TestMcodeABrokenRangeFallsBackToTheStrippedText(t *testing.T) {
	for _, r := range []string{
		`{"startOffset":-1,"endOffset":4}`,
		`{"startOffset":2,"endOffset":999}`,
		`{"startOffset":4,"endOffset":2}`,
		`{"startOffset":1.5,"endOffset":4}`,
		`{"startOffset":"0","endOffset":4}`,
	} {
		got := parseMcodeLines(t, mcodeUserLine(
			`"content":[{"type":"text","text":"<system-reminder>x</system-reminder>asked"}],"canonicalTextRange":`+r))
		if len(got) != 1 || got[0] != "user: asked" {
			t.Fatalf("range %s: messages = %q", r, got)
		}
	}
}

// A non-string genuineUserQueryText is a turn the runtime cannot read; it is
// not indexed as the assembled text either.
func TestMcodeANonStringGenuineTextIsNoTurn(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"<system-reminder>x</system-reminder>words"}],"genuineUserQueryText":7`))
	if len(got) != 0 {
		t.Fatalf("messages = %q, want none", got)
	}
	// An empty one is a turn with nothing typed, as the runtime reads it.
	got = parseMcodeLines(t, mcodeUserLine(
		`"content":[{"type":"text","text":"words"}],"genuineUserQueryText":""`))
	if len(got) != 0 {
		t.Fatalf("empty genuineUserQueryText: messages = %q, want none", got)
	}
}

// A content that is a plain string is the text itself (YIe), and the range
// counts into it.
func TestMcodeAStringContentIsTheText(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(`"content":"<system-reminder>r</system-reminder>asked plainly"`))
	if len(got) != 1 || got[0] != "user: asked plainly" {
		t.Fatalf("messages = %q", got)
	}
	got = parseMcodeLines(t, mcodeUserLine(`"content":"xxtyped","canonicalTextRange":{"startOffset":2,"endOffset":7}`))
	if len(got) != 1 || got[0] != "user: typed" {
		t.Fatalf("with a range: messages = %q", got)
	}
}

// The marker a migration leaves when it could not recover older history is
// a user-role line, but nobody typed it; it became the session's title.
func TestMcodeTheLegacyBoundaryMarkerIsNotATurn(t *testing.T) {
	marker := mcodeUserLine(`"content":[{"type":"text","text":"<system-reminder>\nEarlier legacy conversation context could not be safely recovered during migration.\n</system-reminder>"}],` +
		`"archonCompaction":{"schemaVersion":1,"summary":"Earlier legacy conversation context could not be safely recovered during migration."},` +
		`"archonLegacyContextBoundary":{"schemaVersion":1,"reason":"legacy_history_unavailable"}`)
	dir := t.TempDir()
	path := filepath.Join(dir, mcodeTranscriptName)
	body := marker + "\n" + mcodeUserLine(`"content":[{"type":"text","text":"the real question"}]`) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseMcodeFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 || ss[0].Title != "the real question" || len(ss[0].Messages) != 1 {
		t.Fatalf("session = %#v, want one turn titled by the real question", ss)
	}
}

// A compaction marker with a summary of its own is the record of the turns
// it cut, indexed under the summary role and never as speech.
func TestMcodeACompactionMarkerSummaryIsIndexedAsASummary(t *testing.T) {
	got := parseMcodeLines(t,
		mcodeUserLine(`"content":[{"type":"text","text":"<system-reminder>ARCHON-SUMMARY</system-reminder>"}],`+
			`"archonCompaction":{"version":2,"summary":"ARCHON-SUMMARY decided to drop the cache","recentUserQueries":[{"text":"q"}]}`),
		mcodeUserLine(`"content":[{"type":"text","text":"carry on"}]`))
	want := []string{RoleSummary + ": ARCHON-SUMMARY decided to drop the cache", "user: carry on"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

// The directory name carries the id in base64url: a "-" or "_" in it is not
// standard base64.
func TestMcodeTheDirectoryIDIsBase64URL(t *testing.T) {
	// "mvs_?>?" encodes to bXZzXz8-Pw in base64url, bXZzXz8+Pw in standard.
	if id, ok := decodeMcodeID("bXZzXz8-Pw"); !ok || id != "mvs_?>?" {
		t.Fatalf("id = %q ok = %v", id, ok)
	}
	if id, ok := decodeMcodeID("bXZzXz8+Pw=="); !ok || id != "mvs_?>?" {
		t.Fatalf("standard: id = %q ok = %v", id, ok)
	}
}

// What a plugin hook answered rides the tool result, as its own last text
// block, and the turn's text, after a blank line (MAr, uJn, tTn in 0.6.12; the
// tool-result block seen in a live run). With deja installed that is deja's
// own recall, and indexing it would feed recall back into the index. Output
// that only mentions the tag keeps every line.
func TestMcodeHookContextIsNotIndexed(t *testing.T) {
	got := parseMcodeLines(t,
		mcodeUserLine(`"content":[{"type":"text","text":"asked\n\n<plugin-hook-context>\nRECALL-ECHO\n</plugin-hook-context>"}]`),
		`{"message_id":"m2","turn_id":"t1","message":{"role":"toolResult","toolCallId":"c","toolName":"bash",`+
			`"content":[{"type":"text","text":"hi\n"},{"type":"text","text":"<plugin-hook-context>\nRECALL-ECHO\n</plugin-hook-context>"}],"timestamp":1792465960901}}`,
		`{"message_id":"m3","turn_id":"t1","message":{"role":"toolResult","toolCallId":"c2","toolName":"bash",`+
			`"content":[{"type":"text","text":"a <plugin-hook-context> tag\nreal line\n</plugin-hook-context> end"}],"timestamp":1792465960902}}`)
	want := []string{"user: asked", RoleToolOutput + ": hi", RoleToolOutput + ": a <plugin-hook-context> tag\nreal line\n</plugin-hook-context> end"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("messages = %q, want %q", got, want)
	}
}

// A compaction marker heads the history it kept; a read resumed past it takes
// the title from the first real turn, as the full read does.
func TestMcodeTheTitleSurvivesAnAppendAfterACompactionMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mcodeTranscriptName)
	head := mcodeUserLine(`"content":[{"type":"text","text":"<system-reminder>S</system-reminder>"}],"archonCompaction":{"version":2,"summary":"S","recentUserQueries":[]}`) + "\n" +
		mcodeUserLine(`"content":[{"type":"text","text":"original question"}]`) + "\n"
	if err := os.WriteFile(path, []byte(head+mcodeUserLine(`"content":[{"type":"text","text":"later question"}]`)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail, err := ParseMcodeFileFromOffset(path, int64(len(head)))
	if err != nil || len(tail) != 1 {
		t.Fatalf("tail = %v, %v", tail, err)
	}
	if tail[0].Title != "original question" {
		t.Fatalf("title = %q, want the first real turn", tail[0].Title)
	}
}

// A range written as 3266.0 is an integer to the runtime.
func TestMcodeAnIntegralFloatOffsetIsAnOffset(t *testing.T) {
	got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"xxtyped"}],"canonicalTextRange":{"startOffset":2.0,"endOffset":7e0}`))
	if len(got) != 1 || got[0] != "user: typed" {
		t.Fatalf("messages = %q", got)
	}
}

// A history rewrite the runtime staged and left behind is the same session,
// not a second one; it is bookkeeping.
func TestMcodeAStagedHistoryRewriteIsNotASecondSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "v2", "sessions")
	t.Setenv("DEJA_MCODE_ROOT", root)
	dir := filepath.Join(root, "2026", "10", "22", "15-47-55-700-session_x")
	staged := filepath.Join(dir, ".history-mutation-1", mcodeTranscriptName)
	for _, p := range []string{filepath.Join(dir, mcodeTranscriptName), staged} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(mcodeUserLine(`"content":[{"type":"text","text":"hi"}]`)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(McodeSessionFiles()); n != 1 {
		t.Fatalf("transcripts = %d, want 1", n)
	}
	found := false
	for _, p := range McodeSidecarFiles() {
		found = found || p == staged
	}
	if !found {
		t.Fatal("the staged copy is neither a transcript nor bookkeeping")
	}
}

// The transcript records no directory; the runtime's database does
// (local_runtime_sessions.workspace_dir), and the session is filed under it.
func TestMcodeTheProjectIsTheWorkspaceTheRuntimeRecorded(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	base := t.TempDir()
	root := filepath.Join(base, "v2", "sessions")
	t.Setenv("DEJA_MCODE_ROOT", root)
	ws := filepath.Join(base, "work", "billing-api")
	dir := filepath.Join(root, "2026", "10", "22", "15-47-55-700-session_x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"sessionId":"mvs_ws"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, mcodeTranscriptName), []byte(mcodeUserLine(`"content":[{"type":"text","text":"hi"}]`)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without the database the project is unknown.
	if ss := LoadMcode(); len(ss) != 1 || ss[0].Project != "-" {
		t.Fatalf("sessions = %#v, want one with no project", ss)
	}
	db := McodeStateDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(base, "work", "billing-api-wt")
	sql := "create table local_runtime_sessions (session_id text primary key, workspace_dir text, project_workspace_dir text, is_default_workspace integer not null default 0);" +
		"insert into local_runtime_sessions values ('mvs_ws', '" + strings.ReplaceAll(tree, "'", "''") + "', '" + strings.ReplaceAll(ws, "'", "''") + "', 0);" +
		"insert into local_runtime_sessions values ('mvs_default', '" + strings.ReplaceAll(filepath.Join(base, ".minimax", "workspace"), "'", "''") + "', null, 1);" +
		"insert into local_runtime_sessions values ('mvs_rel', 'relative/dir', '', 0);"
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	// A worktree session is filed under the repository, and its workspace is
	// the worktree.
	ss := LoadMcode()
	if len(ss) != 1 || ss[0].Project != projectName(ws) {
		t.Fatalf("project = %q, want %q", ss[0].Project, projectName(ws))
	}
	if got := McodeSessionWorkspace("mvs_ws"); got != tree {
		t.Fatalf("workspace = %q, want %q", got, tree)
	}
	// The runtime's own scratch directory is no project.
	if McodeSessionWorkspace("mvs_default") != "" {
		t.Fatal("a default-workspace chat was given a project")
	}
	if McodeSessionWorkspace("mvs_rel") != "" || McodeSessionWorkspace("") != "" {
		t.Fatal("a relative or missing workspace came back")
	}
}

// A PreCompact hook finds the session by id: the directory name carries it in
// base64url, and the manifest has the last word.
func TestMcodeSessionPathFindsTheSessionByID(t *testing.T) {
	root := filepath.Join(t.TempDir(), "v2", "sessions")
	t.Setenv("DEJA_MCODE_ROOT", root)
	dir := filepath.Join(root, "2026", "10", "22", "15-47-55-700-session_bXZzXz8-Pw")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, mcodeTranscriptName)
	if err := os.WriteFile(path, []byte(mcodeUserLine(`"content":[{"type":"text","text":"hi"}]`)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := McodeSessionPath("mvs_?>?"); got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	if McodeSessionPath("mvs_other") != "" || McodeSessionPath("") != "" {
		t.Fatal("an id the store does not hold came back with a path")
	}
	if _, err := ReadCompactionStore("mcode", "mvs_other"); err == nil {
		t.Fatal("captured a session the store does not hold")
	}
	tr, err := ReadCompactionStore("mcode", "mvs_?>?")
	if err != nil || tr.Harness != "mcode" || tr.Path != path {
		t.Fatalf("capture = %#v, %v", tr, err)
	}
}

// archonCompaction marks a line the way the runtime tests it: by truthiness.
func TestMcodeACompactionMarkerIsTruthy(t *testing.T) {
	for _, v := range []string{`true`, `1`, `"x"`, `[]`} {
		if got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"typed"}],"archonCompaction":`+v)); len(got) != 0 {
			t.Fatalf("archonCompaction %s: messages = %q, want none", v, got)
		}
	}
	for _, v := range []string{`null`, `false`, `0`, `""`} {
		if got := parseMcodeLines(t, mcodeUserLine(`"content":[{"type":"text","text":"typed"}],"archonCompaction":`+v)); len(got) != 1 {
			t.Fatalf("archonCompaction %s: messages = %q, want the turn", v, got)
		}
	}
}

func buildSQLiteFixture(t *testing.T, sqlPath, dbPath string) {
	t.Helper()
	b, err := os.ReadFile(sqlPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sqlite3", dbPath)
	cmd.Stdin = strings.NewReader(string(b))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create sqlite fixture: %v: %s", err, out)
	}
}

func TestMimoParseAndSince(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "mimocode.db")
	buildSQLiteFixture(t, filepath.Join("..", "..", "fixtures", "registry", "mimo", "mimo.sql"), db)
	t.Setenv("DEJA_MIMO_DB", db)

	if got := MimoDB(); got != db {
		t.Fatalf("MimoDB() = %q, want %q", got, db)
	}
	ss := LoadMimo()
	if len(ss) != 1 {
		t.Fatalf("LoadMimo() got %d sessions, want 1", len(ss))
	}
	s := ss[0]
	if s.Harness != "mimo" || s.ID != "ses_mimo_01" {
		t.Fatalf("got harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != "Fix token refresh race in auth middleware" {
		t.Fatalf("Title = %q", s.Title)
	}
	if len(s.Messages) < 2 {
		t.Fatalf("Messages = %d, want >= 2", len(s.Messages))
	}

	since, err := ParseMimoDBSince(db, time.UnixMilli(1744000000000))
	if err != nil || len(since) != 1 {
		t.Fatalf("ParseMimoDBSince: len=%d err=%v", len(since), err)
	}
	zero, err := ParseMimoDBSince(db, time.Time{})
	if err != nil || len(zero) != 1 {
		t.Fatalf("ParseMimoDBSince zero: len=%d err=%v", len(zero), err)
	}
	after, err := ParseMimoDBSince(db, time.UnixMilli(1844000000000))
	if err != nil || len(after) != 0 {
		t.Fatalf("ParseMimoDBSince future: len=%d err=%v", len(after), err)
	}
}

func TestMimoPathsAndSessionFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_MIMO_CONFIG_DIR", "")
	t.Setenv("MIMOCODE_CONFIG_DIR", "")
	t.Setenv("MIMOCODE_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DEJA_MIMO_DB", "")
	t.Setenv("MIMOCODE_DB", "")

	if got, want := MimoConfigDir(), filepath.Join(home, ".config", "mimocode"); got != want {
		t.Fatalf("MimoConfigDir() default = %q, want %q", got, want)
	}
	xdgCfg := filepath.Join(home, "xdg-cfg")
	t.Setenv("XDG_CONFIG_HOME", xdgCfg)
	if got, want := MimoConfigDir(), filepath.Join(xdgCfg, "mimocode"); got != want {
		t.Fatalf("MimoConfigDir() XDG = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_HOME", "~/mimo-home")
	if got, want := MimoConfigDir(), filepath.Join(home, "mimo-home", "config"); got != want {
		t.Fatalf("MimoConfigDir() MIMOCODE_HOME = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_CONFIG_DIR", "~/mimo-cfg")
	if got, want := MimoConfigDir(), filepath.Join(home, "mimo-cfg"); got != want {
		t.Fatalf("MimoConfigDir() MIMOCODE_CONFIG_DIR = %q, want %q", got, want)
	}
	t.Setenv("DEJA_MIMO_CONFIG_DIR", "~/deja-mimo-cfg")
	if got, want := MimoConfigDir(), filepath.Join(home, "deja-mimo-cfg"); got != want {
		t.Fatalf("MimoConfigDir() DEJA_MIMO_CONFIG_DIR = %q, want %q", got, want)
	}

	t.Setenv("MIMOCODE_HOME", "")
	defDB := filepath.Join(home, ".local", "share", "mimocode", "mimocode.db")
	if got := MimoDB(); got != defDB {
		t.Fatalf("MimoDB() default = %q, want %q", got, defDB)
	}

	xdgData := filepath.Join(home, "xdg-data")
	t.Setenv("XDG_DATA_HOME", xdgData)
	xdgDB := filepath.Join(xdgData, "mimocode", "mimocode.db")
	if got := MimoDB(); got != xdgDB {
		t.Fatalf("MimoDB() XDG neither exists = %q, want %q", got, xdgDB)
	}
	if err := os.MkdirAll(filepath.Dir(defDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defDB, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MimoDB(); got != defDB {
		t.Fatalf("MimoDB() XDG fallback to def = %q, want %q", got, defDB)
	}
	if err := os.MkdirAll(filepath.Dir(xdgDB), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(xdgDB, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := MimoDB(); got != xdgDB {
		t.Fatalf("MimoDB() XDG exists = %q, want %q", got, xdgDB)
	}

	mimoHome := filepath.Join(home, "custom-home")
	t.Setenv("MIMOCODE_HOME", mimoHome)
	if got, want := MimoDB(), filepath.Join(mimoHome, "data", "mimocode.db"); got != want {
		t.Fatalf("MimoDB() MIMOCODE_HOME = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_DB", ":memory:")
	if got, want := MimoDB(), filepath.Join(mimoHome, "data", "mimocode.db"); got != want {
		t.Fatalf("MimoDB() :memory: ignored = %q, want %q", got, want)
	}
	t.Setenv("MIMOCODE_DB", "alt.db")
	if got, want := MimoDB(), filepath.Join(mimoHome, "data", "alt.db"); got != want {
		t.Fatalf("MimoDB() relative = %q, want %q", got, want)
	}
	absDB := filepath.Join(t.TempDir(), "abs.db")
	t.Setenv("MIMOCODE_DB", absDB)
	if got := MimoDB(); got != absDB {
		t.Fatalf("MimoDB() abs = %q, want %q", got, absDB)
	}

	// MimoSessionFiles: missing, 0-byte, non-empty.
	missingDB := filepath.Join(t.TempDir(), "missing.db")
	t.Setenv("DEJA_MIMO_DB", missingDB)
	if got := MimoSessionFiles(); got != nil {
		t.Fatalf("MimoSessionFiles() missing = %v, want nil", got)
	}
	emptyDB := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(emptyDB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_MIMO_DB", emptyDB)
	if got := MimoSessionFiles(); got != nil {
		t.Fatalf("MimoSessionFiles() empty = %v, want nil", got)
	}
	nonEmptyDB := filepath.Join(t.TempDir(), "ok.db")
	if err := os.WriteFile(nonEmptyDB, []byte("sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_MIMO_DB", nonEmptyDB)
	if got := MimoSessionFiles(); len(got) != 1 || got[0] != nonEmptyDB {
		t.Fatalf("MimoSessionFiles() non-empty = %v, want [%q]", got, nonEmptyDB)
	}
}

// Claude Code encodes "/" and "-" identically; resolving against the
// filesystem must recover hyphenated project names instead of mangling
// deja-vu into deja/vu.
func TestClaudeProjectNameResolvesHyphenatedDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("claude encodes unix-style absolute paths; resolution is a no-op on windows")
	}
	tmp := t.TempDir()
	resolved := filepath.Join(tmp, "projects", "deja-vu")
	if err := os.MkdirAll(resolved, 0o755); err != nil {
		t.Fatal(err)
	}
	encoded := strings.ReplaceAll(resolved, string(filepath.Separator), "-")
	if got := claudeProjectName(filepath.Join(filepath.FromSlash("/claude/projects"), encoded)); got != "projects/deja-vu" {
		t.Fatalf("resolved name = %q, want projects/deja-vu", got)
	}
	// Non-existent paths keep the old heuristic.
	if got := claudeProjectName("/claude/projects/-no-such-root-deja-vu"); got != "deja/vu" {
		t.Fatalf("fallback name = %q, want deja/vu", got)
	}
}

// The duplicate check reads the file line by line. A note longer than the
// scan's buffer is not compared — that much is by design — but it used to stop
// the scan, so every note written after it was never compared either, and one
// oversized note turned the check off for the whole store (#1812).
func TestOneHugeNoteDoesNotDisableTheDuplicateCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", path)
	now := time.Now()

	huge := strings.Repeat("the shard limit is 64. ", 240000) // ~5.5 MB
	if err := AppendNote("proj", huge, now); err != nil {
		t.Fatal(err)
	}
	if err := AppendNote("proj", "the retry cap is three", now); err != nil {
		t.Fatal(err)
	}
	err := AppendNote("proj", "the retry cap is three", now)
	if err == nil {
		t.Fatal("a note already on file was written again after an oversized one")
	}
	if !errors.Is(err, ErrNoteExists) {
		t.Fatalf("want ErrNoteExists, got %v", err)
	}

	// The control: without the oversized note in front, the same duplicate is
	// refused — so the assertion above is not vacuous.
	plain := filepath.Join(t.TempDir(), "plain.jsonl")
	t.Setenv("DEJA_NOTES_FILE", plain)
	if err = AppendNote("proj", "the retry cap is three", now); err != nil {
		t.Fatal(err)
	}
	if err = AppendNote("proj", "the retry cap is three", now); !errors.Is(err, ErrNoteExists) {
		t.Fatalf("the control does not hold: %v", err)
	}

	// And the oversized note itself is still written, unchanged.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) < len(huge) {
		t.Errorf("the oversized note was not written whole: file is %d bytes", len(body))
	}
}

// The cap is on a note's content: a line of exactly maxNoteLine bytes is still
// compared, and one byte more is skipped. Counting the newline made the
// boundary one byte tight.
func TestTheLineCapCountsTheNoteNotItsNewline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fill    int
		compare bool
	}{
		{"one under", -1, true},
		{"exactly the cap", 0, true},
		{"one over", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "notes.jsonl")
			t.Setenv("DEJA_NOTES_FILE", path)
			// A fixed stamp, because the record carries it: RFC3339Nano trims
			// trailing zeros, so time.Now() changes the stored line's length
			// between the measuring write and the one being measured, and the
			// "exactly the cap" case then lands a byte or two off.
			stamp := time.Date(2026, 8, 25, 9, 30, 0, 123456789, time.UTC)
			// A note whose JSON line lands exactly on the cap.
			body := strings.Repeat("z", 64)
			if err := AppendNote("proj", body, stamp); err != nil {
				t.Fatal(err)
			}
			line, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			pad := maxNoteLine - len(strings.TrimRight(string(line), "\n")) + tc.fill
			if pad < 0 {
				t.Skip("the stored line is already past the cap")
			}
			grown := strings.Repeat("z", 64+pad)
			path2 := filepath.Join(t.TempDir(), "notes2.jsonl")
			t.Setenv("DEJA_NOTES_FILE", path2)
			if err = AppendNote("proj", grown, stamp); err != nil {
				t.Fatal(err)
			}
			err = AppendNote("proj", grown, stamp)
			if tc.compare && !errors.Is(err, ErrNoteExists) {
				t.Errorf("a line at the cap was not compared: %v", err)
			}
			if !tc.compare && err != nil {
				t.Errorf("a line past the cap should be written without comparison: %v", err)
			}
		})
	}
}

func writeOmpSession(t *testing.T, dir, id, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "2026-08-21T17-14-49-300Z_"+id+".jsonl")
	body := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-08-21T17:14:49.300Z","cwd":"` + cwd + `"}
{"type":"message","id":"u1","timestamp":"2026-08-21T17:14:50.000Z","message":{"role":"user","content":[{"type":"text","text":"which profile am i in"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A named profile relocates omp's whole user scope, sessions included, and an
// omp directory under XDG_DATA_HOME relocates it again — with no `agent`
// segment there. Reading only the default profile answers a person who lives in
// one with an empty history and no reason for it.
func TestOmpSessionRootsCoverProfilesAndXDG(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OMP_ROOT", "")
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	writeOmpSession(t, filepath.Join(home, ".omp", "agent", "sessions", "-work-a"), "aaa", "/work/a")
	writeOmpSession(t, filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions", "-work-b"), "bbb", "/work/b")
	writeOmpSession(t, filepath.Join(xdg, "omp", "sessions", "-work-c"), "ccc", "/work/c")
	writeOmpSession(t, filepath.Join(xdg, "omp", "profiles", "side", "sessions", "-work-d"), "ddd", "/work/d")

	if got := len(OmpSessionFiles()); got != 4 {
		t.Fatalf("found %d transcripts, want 4 (default, named profile, XDG, XDG profile):\n%v",
			got, OmpSessionFiles())
	}
	ids := map[string]string{}
	for _, s := range LoadOmp() {
		ids[s.ID] = s.Project
	}
	for _, id := range []string{"aaa", "bbb", "ccc", "ddd"} {
		if _, ok := ids[id]; !ok {
			t.Errorf("session %s was not loaded: %v", id, ids)
		}
	}
	if got := ids["bbb"]; got != "work/b" {
		t.Errorf("profile session project = %q, want work/b", got)
	}
	if got := ids["ddd"]; got != "work/d" {
		t.Errorf("XDG profile session project = %q, want work/d", got)
	}

	// A header without a cwd is what makes the root matter: then the project is
	// read off the directory the transcript sits in, and measuring a profile
	// session against the default root leaves the profile path inside the name.
	dir := filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions", "-work-e")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","version":3,"id":"eee","timestamp":"2026-08-21T17:14:49.300Z"}
{"type":"message","id":"u1","timestamp":"2026-08-21T17:14:50.000Z","message":{"role":"user","content":[{"type":"text","text":"no cwd here"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, s := range LoadOmp() {
		if s.ID != "eee" {
			continue
		}
		if s.Project != "work/e" {
			t.Errorf("cwd-less profile session project = %q, want work/e", s.Project)
		}
	}
}

// The override means that directory and nothing else: a test or a relocated
// install should not pick up this machine's own profiles.
func TestOmpRootOverrideIsTheOnlyRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeOmpSession(t, filepath.Join(home, ".omp", "agent", "sessions", "-work-a"), "aaa", "/work/a")

	only := t.TempDir()
	t.Setenv("DEJA_OMP_ROOT", only)
	writeOmpSession(t, filepath.Join(only, "-work-e"), "eee", "/work/e")

	roots := OmpSessionRoots()
	if len(roots) != 1 || roots[0] != only {
		t.Fatalf("roots = %v, want only %q", roots, only)
	}
	ss := LoadOmp()
	if len(ss) != 1 || ss[0].ID != "eee" {
		t.Fatalf("loaded %#v", ss)
	}
}

// XDG only counts when omp actually lives there: the variable is set on most
// Linux desktops, and treating it as a root regardless would walk a directory
// that has nothing to do with omp.
func TestOmpIgnoresXDGWithoutAnOmpDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OMP_ROOT", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	roots := OmpSessionRoots()
	if len(roots) != 1 {
		t.Fatalf("roots = %v, want just the default profile", roots)
	}
}

// The incremental index finds a changed file's parser by its path. A profile or
// XDG transcript was listed and read on a full build, then matched no kind, so
// every later append to it was skipped until the next rebuild.
func TestEveryOmpRootIsClassifiedAsOmp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_OMP_ROOT", "")
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)

	paths := []string{
		writeOmpSession(t, filepath.Join(home, ".omp", "agent", "sessions", "-work-a"), "aaa", "/work/a"),
		writeOmpSession(t, filepath.Join(home, ".omp", "profiles", "work", "agent", "sessions", "-work-b"), "bbb", "/work/b"),
		writeOmpSession(t, filepath.Join(xdg, "omp", "sessions", "-work-c"), "ccc", "/work/c"),
		writeOmpSession(t, filepath.Join(xdg, "omp", "profiles", "side", "sessions", "-work-d"), "ddd", "/work/d"),
	}
	for _, p := range paths {
		if got := KindForPath(p); got != "omp" {
			t.Errorf("KindForPath(%s) = %q, want omp", p, got)
		}
	}
}

// PRIME_AGENT_CODING_AGENT_DIR moves prime-agent's whole user directory —
// settings, extensions, skills and the default session root — and prime's own
// docs name it as the override (config.ts getAgentDir). deja read only the
// session variables, so on a relocated install it wrote settings.json where
// prime would not look and called it wired (#3276).
func TestPrimeAgentDirMovesConfigAndSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_SESSION_DIR", "")
	t.Setenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR", "")
	moved := filepath.Join(home, "elsewhere", "agent")
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", moved)
	if got := PrimeConfigDir(); got != moved {
		t.Errorf("config dir = %q, want %q", got, moved)
	}
	if got, want := PrimeRoot(), filepath.Join(moved, "sessions"); got != want {
		t.Errorf("session root = %q, want %q", got, want)
	}
}

// With both session variables set prime reads PRIME_AGENT_SESSION_DIR first and
// the legacy PRIME_AGENT_CODING_AGENT_SESSION_DIR second (config.ts:628); deja
// had them the other way round, so the two disagreed on the root (#3277).
func TestPrimeSessionDirPrecedenceMatchesPrime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "")
	t.Setenv("PRIME_AGENT_SESSION_DIR", filepath.Join(home, "new"))
	t.Setenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR", filepath.Join(home, "legacy"))
	if got, want := PrimeRoot(), filepath.Join(home, "new"); got != want {
		t.Errorf("root = %q, want the current variable %q", got, want)
	}
}

// A leading ~ is prime's own spelling for the variable (its README shows it);
// prime expands it, so deja does too.
func TestPrimeAgentDirExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_PRIME_ROOT", "")
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", "~/custom/agent")
	if got, want := PrimeConfigDir(), filepath.Join(home, "custom", "agent"); got != want {
		t.Errorf("config dir = %q, want %q", got, want)
	}
}

// TeleAgent (星辰超级智能体) reads one OpenCode-schema SQLite store per account
// under ~/.local/share/TeleAgent/users/<account>/teleagent.db. The fixture
// builds the same session_v2/session_message tables the mimo fixture does —
// the schemas are identical — into two account directories, and the store
// enumeration has to surface both while Parse stays per-account.

func TestTeleagentParseAndAccounts(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	home := t.TempDir()
	users := filepath.Join(home, "users")
	for _, acct := range []string{"2091696589113404", "2091696589113405"} {
		if err := os.MkdirAll(filepath.Join(users, acct), 0o755); err != nil {
			t.Fatal(err)
		}
		buildSQLiteFixture(t, filepath.Join("..", "..", "fixtures", "registry", "teleagent", "teleagent.sql"), filepath.Join(users, acct, "teleagent.db"))
	}
	t.Setenv("DEJA_TELEAGENT_ROOT", home)
	t.Setenv("TELEAGENT_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	dbs := TeleAgentDBs()
	if len(dbs) != 2 {
		t.Fatalf("TeleAgentDBs() = %d stores, want 2", len(dbs))
	}
	if filepath.Base(dbs[0]) != "teleagent.db" {
		t.Fatalf("store basename = %q", dbs[0])
	}
	ss := LoadTeleAgent()
	if len(ss) != 2 {
		t.Fatalf("LoadTeleAgent() got %d sessions, want 2 (one per account)", len(ss))
	}
	wantPaths := map[string]bool{dbs[0]: true, dbs[1]: true}
	if ss[0].Path == ss[1].Path {
		t.Fatalf("LoadTeleAgent() duplicated Path %q across accounts", ss[0].Path)
	}
	for _, s := range ss {
		if !wantPaths[s.Path] {
			t.Fatalf("Path = %q, want one of %v", s.Path, dbs)
		}
		if s.Harness != "teleagent" || s.ID != "ses_teleagent_01" {
			t.Fatalf("got harness=%q id=%q", s.Harness, s.ID)
		}
		if s.Title != "Fix token refresh race in auth middleware" {
			t.Fatalf("Title = %q", s.Title)
		}
		if len(s.Messages) < 2 {
			t.Fatalf("Messages = %d, want >= 2", len(s.Messages))
		}
	}
	since, err := ParseTeleagentDBSince(dbs[0], time.UnixMilli(1744000000000))
	if err != nil || len(since) != 1 || since[0].Path != dbs[0] {
		t.Fatalf("ParseTeleagentDBSince: ss=%+v err=%v", since, err)
	}
	zero, err := ParseTeleagentDBSince(dbs[0], time.Time{})
	if err != nil || len(zero) != 1 || zero[0].Path != dbs[0] {
		t.Fatalf("ParseTeleagentDBSince zero: ss=%+v err=%v", zero, err)
	}
	after, err := ParseTeleagentDBSince(dbs[0], time.UnixMilli(1844000000000))
	if err != nil || len(after) != 0 {
		t.Fatalf("ParseTeleagentDBSince future: len=%d err=%v", len(after), err)
	}
}

func TestTeleagentPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_TELEAGENT_ROOT", "")
	t.Setenv("TELEAGENT_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")

	if got, want := TeleAgentDataDir(), filepath.Join(home, ".local", "share", "TeleAgent"); got != want {
		t.Fatalf("TeleAgentDataDir() default = %q, want %q", got, want)
	}
	xdg := filepath.Join(home, "xdg-data")
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, want := TeleAgentDataDir(), filepath.Join(xdg, "TeleAgent"); got != want {
		t.Fatalf("TeleAgentDataDir() XDG = %q, want %q", got, want)
	}
	t.Setenv("TELEAGENT_HOME", "~/teleagent-home")
	if got, want := TeleAgentDataDir(), filepath.Join(home, "teleagent-home"); got != want {
		t.Fatalf("TeleAgentDataDir() TELEAGENT_HOME = %q, want %q", got, want)
	}
	t.Setenv("DEJA_TELEAGENT_ROOT", "~/deja-ta")
	if got, want := TeleAgentDataDir(), filepath.Join(home, "deja-ta"); got != want {
		t.Fatalf("TeleAgentDataDir() DEJA_TELEAGENT_ROOT = %q, want %q", got, want)
	}
	if got := TeleAgentDBs(); got != nil {
		t.Fatalf("TeleAgentDBs() on an absent tree = %v, want nil", got)
	}
}
