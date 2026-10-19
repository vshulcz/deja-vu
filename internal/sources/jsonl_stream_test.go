package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

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
	cases := []string{
		`{}`, `{"a":1}`, `  {"a" : [1, 2.5e3, -0, true, false, null, {"b":"c"}]}  `,
		`{"s":"tab\tquote\" slash\/ back\\ \b\f\n\r"}`,
		`{"u":"\u00e9\u20ac\ud83d\ude00"}`, `{"u":"\ud83d"}`, `{"u":"\ud83dx"}`, `{"u":"\ud83d\u0041"}`,
		`{"u":"\ud83d\ud83d\ude00"}`, `{"u":"\ude00"}`, `{"u":"\ud83d\n"}`, `{"u":"\ud83d\"}`,
		"{\"raw\":\"\xff\xfe ok \xe2\x82\"}", "{\"ctl\":\"a\x01b\"}", `{"bad":"\x"}`, `{"bad":"\u12g4"}`,
		`{"n":01}`, `{"n":1.}`, `{"n":-}`, `{"n":1e}`, `{"n":1e+5}`, `{"n":-1.5E-3}`,
		`null`, `[1,2]`, `"str"`, `{"a":1}}`, `{"a":1} trailing`, `{"a":1}{"b":2}`, `{"a":1,}`, `{"a"}`,
		`{"a":1`, `{"dup":1,"dup":2}`, `{"a":[]}`, `{"a":{}}`, `{"a":[{}]}`, `{"t":tru}`, `{,}`, ``, `   `,
	}
	rng := rand.New(rand.NewSource(1))
	words := []string{"a", "é", "\U0001F600", "\\", "\"", "\n", "\x00", "\xff", "{", "}", "[", "]", ":", ",", "1", "-", ".", "e", " ", "\\u00", "\\ud83d", "\\ude00"}
	for i := 0; i < 3000; i++ {
		v := map[string]any{"k": randValue(rng, 3)}
		b, _ := json.Marshal(v)
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
	line, _ := json.Marshal(map[string]any{
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
	meta, _ := json.Marshal(map[string]any{"timestamp": "2026-06-01T10:00:00.000Z", "type": "session_meta", "payload": map[string]any{
		"instructions": strings.Repeat("be brief. ", 300_000), "id": "019a0000-0000-7000-8000-000000000001", "cwd": "/w/app"}})
	if err := os.WriteFile(p, append(meta, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	id, cwd, payload, at := codexRolloutHead(p)
	if at.IsZero() {
		t.Error("the long meta line lost its time")
	}
	if id != "019a0000-0000-7000-8000-000000000001" || cwd != "/w/app" || len(payload["instructions"].(string)) != longStringCap {
		t.Errorf("head = %q %q, %d bytes of instructions", id, cwd, len(payload["instructions"].(string)))
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
