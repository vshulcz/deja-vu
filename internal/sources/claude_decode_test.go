package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The typed parser replaces the generic one for the harness that holds most of
// everyone's history. A rewrite of a parser is the change most likely to lose
// data quietly, so the two are compared field by field on every shape the
// format is known to take — and, when a real store is present, on that.
func sameSessions(t *testing.T, want, got []model.Session, ctx string) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: %d sessions vs %d", ctx, len(want), len(got))
	}
	for i := range want {
		a, b := want[i], got[i]
		if a.ID != b.ID || a.Harness != b.Harness || a.Project != b.Project || a.Path != b.Path {
			t.Fatalf("%s: session %d differs:\n old %+v\n new %+v", ctx, i, a, b)
		}
		if !a.Started.Equal(b.Started) || !a.Updated.Equal(b.Updated) {
			t.Fatalf("%s: session %d times differ: %v/%v vs %v/%v", ctx, i, a.Started, a.Updated, b.Started, b.Updated)
		}
		if len(a.Messages) != len(b.Messages) {
			t.Fatalf("%s: session %d has %d messages vs %d", ctx, i, len(a.Messages), len(b.Messages))
		}
		for j := range a.Messages {
			x, y := a.Messages[j], b.Messages[j]
			if x.Role != y.Role || x.Text != y.Text || !x.Time.Equal(y.Time) {
				t.Fatalf("%s: session %d message %d differs:\n old %+v\n new %+v", ctx, i, j, x, y)
			}
		}
	}
}

func TestTypedClaudeParserMatchesTheGenericOne(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]string{
		"plain.jsonl": {
			`{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello"}}`,
			`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"assistant","content":"world"}}`,
		},
		"blocks.jsonl": {
			`{"type":"assistant","sessionId":"s2","timestamp":"2026-01-02T03:04:05Z","message":{"role":"assistant","content":[{"type":"text","text":"first"},{"type":"tool_use","id":"t"},{"type":"text","text":"second"}]}}`,
		},
		// A content item carrying "content" rather than "text", and a bare
		// string in the array: both are shapes the generic parser tolerated.
		"mixed.jsonl": {
			`{"type":"user","sessionId":"s3","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[{"content":"from content"},"a bare string",{"type":"text","text":"from text"}]}}`,
		},
		// Numeric timestamps, which is why the generic scanner used UseNumber.
		"numeric-time.jsonl": {
			`{"type":"user","sessionId":"s4","timestamp":1767323045,"message":{"role":"user","content":"unix seconds"}}`,
			`{"type":"user","sessionId":"s4","timestamp":1767323045123,"message":{"role":"user","content":"unix millis"}}`,
		},
		"no-role.jsonl": {
			`{"type":"assistant","sessionId":"s5","timestamp":"2026-01-02T03:04:05Z","message":{"content":"role falls back to type"}}`,
		},
		"skipped-types.jsonl": {
			`{"type":"summary","sessionId":"s6","timestamp":"2026-01-02T03:04:05Z","message":{"content":"ignored"}}`,
			`{"type":"user","sessionId":"s6","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"kept"}}`,
		},
		"empty-and-broken.jsonl": {
			``,
			`   `,
			`{"type":"user","sessionId":"s7"`,
			`{"type":"user","sessionId":"s7","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":""}}`,
			`{"type":"user","sessionId":"s7","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"survives the broken line above"}}`,
		},
		"no-message.jsonl": {
			`{"type":"user","sessionId":"s8","timestamp":"2026-01-02T03:04:05Z"}`,
		},
		"null-fields.jsonl": {
			`{"type":"user","sessionId":"s9","timestamp":null,"message":{"role":null,"content":null}}`,
			`{"type":"user","sessionId":"s9","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"after nulls"}}`,
		},
		"unicode.jsonl": {
			`{"type":"user","sessionId":"s10","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"кириллица 中文 emoji 🙂 \"quoted\" \\ backslash"}}`,
		},
		// MCP tool results carry content as an array of blocks rather than a
		// string; the text lives one level down.
		"tool-result-blocks.jsonl": {
			`{"type":"user","sessionId":"s11","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"3 tests failed"}]}]}}`,
			`{"type":"user","sessionId":"s11","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]}}`,
		},
	}
	for name, lines := range cases {
		path := filepath.Join(dir, name)
		body := ""
		for _, l := range lines {
			body += l + "\n"
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		want, wantErr := parseClaudeGenericFromOffset(path, 0)
		got, gotErr := parseClaudeTypedFromOffset(path, 0)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("%s: errors differ: %v vs %v", name, wantErr, gotErr)
		}
		sameSessions(t, want, got, name)
	}
}

// End to end: a user line whose only content is a block-array tool result must
// produce a tool-output record, not vanish from the session.
func TestBlockToolResultBecomesARecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.jsonl")
	line := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"chapter marked"}]}]}}`
	if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	parsers := map[string]func(string, int64) ([]model.Session, error){
		"typed":   parseClaudeTypedFromOffset,
		"generic": parseClaudeGenericFromOffset,
	}
	for name, parse := range parsers {
		ss, err := parse(path, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ss) != 1 || len(ss[0].Messages) != 1 {
			t.Fatalf("%s: sessions = %+v", name, ss)
		}
		if m := ss[0].Messages[0]; m.Role != RoleToolOutput || m.Text != "chapter marked" {
			t.Fatalf("%s: message = %+v", name, m)
		}
	}
}

// Resuming mid-file is how incremental indexing reads an appended transcript,
// so the two parsers have to agree from an arbitrary offset too.
func TestTypedClaudeParserMatchesFromAnOffset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume.jsonl")
	first := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"first"}}` + "\n"
	rest := `{"type":"user","sessionId":"s1","timestamp":"2026-01-02T03:04:06Z","message":{"role":"user","content":"second"}}` + "\n" +
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-01-02T03:04:07Z","message":{"role":"assistant","content":"third"}}` + "\n"
	if err := os.WriteFile(path, []byte(first+rest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, off := range []int64{0, int64(len(first))} {
		want, _ := parseClaudeGenericFromOffset(path, off)
		got, _ := parseClaudeTypedFromOffset(path, off)
		sameSessions(t, want, got, "offset")
	}
}

// The one that matters: the machine's own history, if it has any. Synthetic
// cases cover the shapes someone thought of; a real store covers the ones
// nobody did.
func TestTypedClaudeParserMatchesOnTheRealCorpus(t *testing.T) {
	root := ClaudeRoot()
	if _, err := os.Stat(root); err != nil {
		t.Skip("no claude history on this machine")
	}
	var files []string
	_ = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && filepath.Ext(p) == ".jsonl" {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		t.Skip("no transcripts")
	}
	if len(files) > 40 {
		files = files[:40]
	}
	start := time.Now()
	for _, p := range files {
		want, _ := parseClaudeGenericFromOffset(p, 0)
		got, _ := parseClaudeTypedFromOffset(p, 0)
		sameSessions(t, want, got, filepath.Base(p))
	}
	t.Logf("compared %d real transcripts in %s", len(files), time.Since(start).Round(time.Millisecond))
}
