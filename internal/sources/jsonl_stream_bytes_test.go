package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
)

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
	cases := []string{
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
	rng := rand.New(rand.NewSource(2))
	words := []string{"a", "é", "\\", "\"", "\x00", "\xff", "{", "}", "[", "]", ":", ",", "1", "-", "e", " ", "\\u00", "\\ud83d"}
	for i := 0; i < 3000; i++ {
		b, _ := json.Marshal(map[string]any{"k": randValue(rng, 3)})
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
		var roles []string
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
	rng := rand.New(rand.NewSource(3))
	pat := []byte(`"response_item"`)
	for i := 0; i < 5000; i++ {
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
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		last := int64(bytes.LastIndexByte(body[:len(body)-1], '\n') + 1)
		body = nil
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
		eachLineFrom(p, 0, func(line []byte) bool {
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
