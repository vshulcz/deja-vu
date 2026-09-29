package sources

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

const piToolsHeader = `{"type":"session","id":"tools","timestamp":"2026-01-02T03:04:05Z","cwd":"/fixture/project"}` + "\n"
const piToolsMessage = `{"type":"message","timestamp":"2026-01-02T03:04:06Z","message":{"role":"assistant","content":[
{"type":"toolCall","name":"read","arguments":{"path":"src/read.go"}},
{"type":"toolCall","name":"edit","arguments":{"path":"src/edit.go","oldText":"legacy before","newText":"legacy after with enough detail for attribution"}},
{"type":"toolCall","name":"edit","arguments":{"path":"src/edit.go","edits":[{"oldText":"first before","newText":"first after with enough detail for attribution"},{"oldText":"second before","newText":"second after with enough detail for attribution"}]}},
{"type":"toolCall","name":"write","arguments":{"path":"src/new.go","content":"package newfile with enough detail for attribution\n"}},
{"type":"toolCall","name":"bash","arguments":{"command":"go test ./..."}}
]}}`

func piToolsEnvironment(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, name := range []string{"PI", "OMP", "GJC", "PRIME", "SENPI", "KIMCHI", "OPENCLAW"} {
		t.Setenv("DEJA_"+name+"_ROOT", home)
	}
	for _, name := range []string{"PATHS", "EDITS", "WRITES", "COMMANDS"} {
		t.Setenv("DEJA_INDEX_"+name, "1")
	}
}

func piToolRoles(messages []model.Message) map[string][]string {
	out := map[string][]string{}
	for _, m := range messages {
		out[m.Role] = append(out[m.Role], m.Text)
	}
	return out
}

func checkPiToolRecords(t *testing.T, messages []model.Message) {
	t.Helper()
	got := piToolRoles(messages)
	want := map[string][]string{
		RoleFiles:   {"src/read.go\nsrc/edit.go\nsrc/new.go"},
		RoleEdit:    {"src/edit.go\nlegacy before", "src/edit.go\nfirst before", "src/edit.go\nsecond before"},
		RoleCommand: {"$ go test ./..."},
	}
	for role, texts := range want {
		if !reflect.DeepEqual(got[role], texts) {
			t.Errorf("%s = %q, want %q", role, got[role], texts)
		}
	}
	if len(got[RoleWrote]) != 4 {
		t.Fatalf("written records = %q, want four", got[RoleWrote])
	}
	for i, path := range []string{"src/edit.go", "src/edit.go", "src/edit.go", "src/new.go"} {
		line := []string{"legacy after with enough detail for attribution", "first after with enough detail for attribution", "second after with enough detail for attribution", "package newfile with enough detail for attribution"}[i]
		hash, ok := HashWrittenLine(line)
		if recordedPath, has := WroteRecordHas(got[RoleWrote][i], hash); !ok || !has || recordedPath != path {
			t.Errorf("written record does not attribute %q to %q", line, path)
		}
		if !strings.HasPrefix(got[RoleWrote][i], path+"\n") {
			t.Errorf("written record = %q, want path %q", got[RoleWrote][i], path)
		}
	}
}

func TestPiToolCallsAcrossReadersAndOffsets(t *testing.T) {
	piToolsEnvironment(t)
	var compact strings.Builder
	for _, line := range strings.Split(piToolsMessage, "\n") {
		compact.WriteString(line)
	}
	message := compact.String() + "\n"
	path := filepath.Join(t.TempDir(), "tools.jsonl")
	if err := os.WriteFile(path, []byte(piToolsHeader+message+message), 0600); err != nil {
		t.Fatal(err)
	}
	readers := []struct {
		name   string
		parse  func(string) ([]model.Session, error)
		offset func(string, int64) ([]model.Session, error)
	}{
		{"pi", ParsePiFile, ParsePiFileFromOffset},
		{"omp", ParseOmpFile, ParseOmpFileFromOffset},
		{"gjc", ParseGjcFile, ParseGjcFileFromOffset},
		{"prime", ParsePrimeFile, ParsePrimeFileFromOffset},
		{"senpi", ParseSenpiFile, ParseSenpiFileFromOffset},
		{"kimchi", ParseKimchiFile, ParseKimchiFileFromOffset},
		{"openclaw", ParseOpenClawFile, ParseOpenClawFileFromOffset},
	}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			full, err := reader.parse(path)
			if err != nil || len(full) != 1 {
				t.Fatalf("full parse = %v, %v", full, err)
			}
			tail, err := reader.offset(path, int64(len(piToolsHeader)+len(message)))
			if err != nil || len(tail) != 1 {
				t.Fatalf("incremental parse = %v, %v", tail, err)
			}
			if tail[0].ID != "tools" || tail[0].Harness != reader.name {
				t.Fatalf("session identity = %+v", tail[0])
			}
			checkPiToolRecords(t, tail[0].Messages)
			n := len(tail[0].Messages)
			if len(full[0].Messages) != 2*n || !reflect.DeepEqual(full[0].Messages[n:], tail[0].Messages) {
				t.Fatal("incremental parse must contain only the newly appended turn")
			}
		})
	}
}

func TestPiToolCallSwitches(t *testing.T) {
	for _, tc := range []struct{ env, role string }{
		{"PATHS", RoleFiles}, {"EDITS", RoleEdit}, {"WRITES", RoleWrote}, {"COMMANDS", RoleCommand},
	} {
		t.Run(tc.env, func(t *testing.T) {
			piToolsEnvironment(t)
			t.Setenv("DEJA_INDEX_"+tc.env, "0")
			var event map[string]any
			if err := json.Unmarshal([]byte(piToolsMessage), &event); err != nil {
				t.Fatal(err)
			}
			var s model.Session
			piShapedLine(&s, event, false)
			roles := piToolRoles(s.Messages)
			if len(roles[tc.role]) != 0 {
				t.Fatalf("disabled %s retained: %q", tc.role, roles[tc.role])
			}
			if len(roles) != 3 {
				t.Fatalf("unrelated records lost: %v", roles)
			}
		})
	}
}

func TestPiMalformedToolCallsAndSpeech(t *testing.T) {
	piToolsEnvironment(t)
	for _, role := range []string{"assistant", "user", "toolResult"} {
		t.Run(role, func(t *testing.T) {
			content := `[{"type":"text","text":"still readable"},null,42,{"type":"toolCall","name":"unknown","arguments":{"path":"ignored.go"}},{"type":"toolCall","name":"read","arguments":"bad"},{"type":"toolCall","name":"read","arguments":{"path":42}},{"type":"toolCall","name":"edit","arguments":{"path":"bad\npath","oldText":"discard"}},{"type":"toolCall","name":"bash","arguments":{"command":"pwd"}}]`
			if role != "assistant" {
				content = `[{"type":"text","text":"still readable"},{"type":"toolCall","name":"read","arguments":{"path":"ignored.go"}}]`
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(`{"type":"message","message":{"role":"`+role+`","content":`+content+`}}`), &event); err != nil {
				t.Fatal(err)
			}
			var s model.Session
			piShapedLine(&s, event, false)
			if len(s.Messages) != 1 || s.Messages[0].Text != "still readable" {
				t.Fatalf("messages = %+v", s.Messages)
			}
		})
	}
}

func TestPiToolCallsInOpenClawSQLite(t *testing.T) {
	piToolsEnvironment(t)
	_, db := openclawTestDB(t)
	escaped := strings.ReplaceAll(piToolsMessage, "'", "''")
	cmd := exec.Command("sqlite3", db, "UPDATE transcript_events SET event_json='"+escaped+"' WHERE seq=2;")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("update fixture: %v: %s", err, out)
	}
	sessions, err := ParseOpenClawDB(db)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range sessions {
		if len(piToolRoles(s.Messages)[RoleCommand]) > 0 {
			checkPiToolRecords(t, s.Messages)
			found = true
		}
	}
	if !found {
		t.Fatal("SQLite reader dropped tool calls")
	}
}
