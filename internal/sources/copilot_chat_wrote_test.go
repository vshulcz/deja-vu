package sources

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Copilot Chat keeps an edit as a range plus the text that replaced it, so the
// written side is there in full and the replaced side is not there at all. The
// store held 655 of these and the index read none (#595).
func TestCopilotChatRecordsTheWrittenSideOfAnEdit(t *testing.T) {
	const written = "func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"
	const second = "\treturn p.acquireWithTimeout(ctx, p.cfg.AcquireTimeout)"
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{
		"version":3,"sessionId":"s","creationDate":1763727100000,
		"requests":[{
			"timestamp":1763727104742,
			"message":{"text":"give the pool an acquire timeout"},
			"response":[
				{"kind":"textEditGroup",
					"uri":{"$mid":1,"fsPath":"/w/app/internal/pool/pool.go","path":"/w/app/internal/pool/pool.go","scheme":"file"},
					"edits":[[{"text":"` + written + `\n` + strings.ReplaceAll(second, "\t", "\\t") + `","range":{"startLineNumber":1,"startColumn":1,"endLineNumber":3,"endColumn":2}}],[]],
					"done":true}
			],
			"responseTimestamp":1763727400000
		}]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	var files, wrote []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleFiles:
			files = append(files, m.Text)
		case RoleWrote:
			wrote = append(wrote, m.Text)
		}
	}
	if strings.Join(files, ",") != "/w/app/internal/pool/pool.go" {
		t.Fatalf("files = %v", files)
	}
	if len(wrote) != 1 {
		t.Fatalf("wrote = %q, want one record", wrote)
	}
	// The record's hash set is exactly the two written lines: the path line is
	// not a written line, and a record holding anything else would attribute a
	// line this session never wrote.
	var want []string
	for _, line := range []string{written, second} {
		h, ok := HashWrittenLine(line)
		if !ok {
			t.Fatalf("%q is too short to be evidence", line)
		}
		want = append(want, strconv.FormatUint(h, 16))
	}
	path, rest, ok := strings.Cut(wrote[0], "\n")
	if !ok || path != "/w/app/internal/pool/pool.go" {
		t.Fatalf("the record is filed under %q", path)
	}
	got := strings.Fields(rest)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("hashes = %v, want the two written lines %v", got, want)
	}
	// Nothing in the store says what was replaced, so nothing may claim to.
	for _, m := range ss[0].Messages {
		if m.Role == RoleEdit {
			t.Fatalf("a replaced span was invented: %q", m.Text)
		}
	}
}

// The switches are the contract: a store that indexes neither writes nor tool
// paths gets neither from this reader.
func TestCopilotChatEditsRespectTheIndexSwitches(t *testing.T) {
	t.Setenv("DEJA_INDEX_WRITES", "0")
	t.Setenv("DEJA_INDEX_PATHS", "0")
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{
		"version":3,"sessionId":"s","creationDate":1763727100000,
		"requests":[{
			"timestamp":1763727104742,
			"message":{"text":"give the pool an acquire timeout"},
			"response":[
				{"kind":"textEditGroup",
					"uri":{"path":"/w/app/internal/pool/pool.go"},
					"edits":[[{"text":"func (p *Pool) Acquire(ctx context.Context) (*Conn, error) {"}]],
					"done":true}
			],
			"responseTimestamp":1763727400000
		}]
	}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCopilotChatFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("%v %#v", err, ss)
	}
	for _, m := range ss[0].Messages {
		if m.Role == RoleWrote || m.Role == RoleFiles {
			t.Fatalf("%s survived its switch: %q", m.Role, m.Text)
		}
	}
}
