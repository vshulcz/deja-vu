package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/redact"
)

// rawScrub is the rewrite before #5622: the rules over the raw bytes. It stands
// in for a rule that goes wrong later.
func rawScrub(b []byte, kinds map[string]bool) ([]byte, redact.Counts) {
	s, c := redact.TextKinds(string(b), kinds)
	return []byte(s), c
}

// A broken rule must not reach the disk: the check before the rename refuses
// the file and names why, in a dry run as well.
func TestScrubRefusesARewriteThatBreaksALine(t *testing.T) {
	old := scrubContent
	scrubContent = rawScrub
	t.Cleanup(func() { scrubContent = old })

	call := jsonLine(t, map[string]any{"type": "tool_use", "input": map[string]any{"command": "echo \"$(date -u +%H:%M:%SZ)\""}})
	p := scrubFixture(t, "s.jsonl", `{"type":"user"}`, call)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{true, false} {
		o := scrubOne(scrubTarget{path: p, id: "s", kinds: map[string]bool{"command-password": true}}, nil, dry)
		if !strings.Contains(o.skipped, "refused") || !strings.Contains(o.skipped, "line 2") {
			t.Errorf("dry=%v: not refused: %+v", dry, o)
		}
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the file was written:\n%s", after)
	}
	if got := scrubBackupsIn(filepath.Dir(p)); len(got) != 0 {
		t.Errorf("a refused file left copies: %v", got)
	}
	var out strings.Builder
	printScrub(&out, []scrubOutcome{scrubOne(scrubTarget{path: p, kinds: map[string]bool{"command-password": true}}, nil, true)}, nil, true)
	if !strings.Contains(out.String(), "skipped") || !strings.Contains(out.String(), "would break 1 line") {
		t.Errorf("the dry run said:\n%s", out.String())
	}
}

func TestScrubRefusesARewriteThatDropsLines(t *testing.T) {
	old := scrubContent
	scrubContent = rawScrub
	t.Cleanup(func() { scrubContent = old })
	p := scrubFixture(t, "k.jsonl",
		`{"c":"`+pemHead+`"}`, `{"c":"between"}`, `{"c":"`+pemTail+`"}`)
	o := scrubOne(scrubTarget{path: p, kinds: map[string]bool{"private-key": true}}, nil, false)
	if !strings.Contains(o.skipped, "lines into") {
		t.Errorf("not refused: %+v", o)
	}
}

func TestScrubCheck(t *testing.T) {
	for _, tc := range []struct {
		name, before, after, want string
	}{
		{"same", "{\"a\":1}\n", "{\"a\":2}\n", ""},
		{"document", "{\n\"a\": \"x\"\n}", "{\n\"a\": \"x\n}", "no longer valid JSON"},
		{"text line", "not json\n", "still not\n", ""},
		{"crlf", "x\r\n{\"a\":1}\r\n", "x\r\n{\"a\":1\r\n", "first is line 2"},
		{"grew", "a\n", "a\nb\n", "2 lines into 3"},
	} {
		err := scrubCheck([]byte(tc.before), []byte(tc.after))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
}

// Every format scrubOne is handed: a whole JSON document keeps its layout, a
// text file is redacted line by line, JSON nested in a string is walked, and a
// compressed or binary store is left alone.
func TestScrubTranscriptFormats(t *testing.T) {
	kinds := map[string]bool{"url-credentials": true, "private-key": true}

	doc := "{\n  \"messages\": [\n    {\"text\": \"dsn " + fakeDSN + "\"},\n    {\"text\": \"fine\"}\n  ]\n}\n"
	got, counts := scrubTranscript([]byte(doc), kinds)
	want := strings.Replace(doc, "svc:"+fakePass+"@", "svc:[redacted:url-credentials]@", 1)
	if string(got) != want || counts["url-credentials"] != 1 {
		t.Errorf("document:\n%s\n%v", got, counts)
	}

	md := "# notes\n" + pemHead + "\nsome prose here\n" + pemTail + "\nmore\n"
	if got, _ = scrubTranscript([]byte(md), kinds); string(got) != md {
		t.Errorf("a text rule crossed a newline:\n%s", got)
	}
	text := "line one\n" + fakeDSN + "\r\nlast"
	if got, _ = scrubTranscript([]byte(text), kinds); string(got) != "line one\npostgres://svc:[redacted:url-credentials]@db.example.test/app\r\nlast" {
		t.Errorf("text:\n%q", got)
	}

	nested := jsonLine(t, map[string]any{"output": jsonLine(t, map[string]any{"cmd": "say \"hi\"", "dsn": fakeDSN})})
	two := []byte(nested + "\n" + nested + "\n")
	got, _ = scrubTranscript(two, kinds)
	if bytes.Contains(got, []byte(fakePass)) || !bytes.Contains(got, []byte(`say \\\"hi\\\"`)) {
		t.Errorf("nested:\n%s", got)
	}
	if err := scrubCheck(two, got); err != nil {
		t.Errorf("nested line broke: %v", err)
	}

	// An object key is not message text.
	keyed := `{"` + fakeDSN + `": 1}`
	if got, _ = scrubTranscript([]byte(keyed), kinds); string(got) != keyed {
		t.Errorf("a key was rewritten: %s", got)
	}

	for _, b := range [][]byte{{0x28, 0xb5, 0x2f, 0xfd, 0x00}, {0x0a, 0x03, 'a', 'b', 'c', 0x12, 0x01}} {
		if !scrubBinary(b) {
			t.Errorf("binary not detected: %x", b)
		}
	}
	if scrubBinary([]byte("plain\ttext \x1b[0m\n")) {
		t.Error("text taken for binary")
	}
}

func TestScrubSkipsABinaryStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.jsonl.zst")
	if err := os.WriteFile(p, []byte("\x28\xb5\x2f\xfd"+fakeDSN), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	o := scrubOne(scrubTarget{path: p, kinds: map[string]bool{"url-credentials": true}}, nil, true)
	if !strings.Contains(o.skipped, "not a text file") {
		t.Errorf("%+v", o)
	}
}
