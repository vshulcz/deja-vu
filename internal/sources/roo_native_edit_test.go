package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Current Roo offers apply_patch, search_replace, edit_file and edit next to
// apply_diff, and the last three name the file `file_path`. The reader knew
// none of them, so a task edited through one had no files, edit or wrote
// record (#4419). Shapes from the Roo CLI 0.1.17 bundle,
// core/prompts/tools/native-tools.
func TestRooNativeEditToolsLeaveRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks", "1788845325720")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const old, neu = "max_backoff_seconds = 30  # cap for the retry loop", "max_backoff_seconds = 120  # cap for the retry loop"
	body := `[
	 {"role":"user","content":[{"type":"text","text":"<task>\nraise the retry backoff cap\n</task>"}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"apply_patch","input":{"patch":"*** Begin Patch\n*** Update File: conf/a.cfg\n@@\n-` + old + `\n+` + neu + `\n*** End Patch"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"search_replace","input":{"file_path":"/w/conf/b.cfg","old_string":"` + old + `","new_string":"` + neu + `"}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t3","name":"edit_file","input":{"file_path":"/w/conf/c.cfg","old_string":"` + old + `","new_string":"` + neu + `","expected_replacements":1}}]},
	 {"role":"assistant","content":[{"type":"tool_use","id":"t4","name":"edit","input":{"file_path":"/w/conf/d.cfg","old_string":"` + old + `","new_string":"` + neu + `","replace_all":false}}]}
	]`
	path := filepath.Join(dir, "api_conversation_history.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "history_item.json"), []byte(`{"id":"1788845325720","ts":1788845325720,"task":"raise the cap","workspace":"/w"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h, ok := HashWrittenLine(neu)
	if !ok {
		t.Fatal("the new line is too short to be evidence")
	}
	for _, reader := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
	}{
		{"roo", ParseRooTask},
		{"kilocode", ParseKiloTask},
	} {
		ss, err := reader.parse(path)
		if err != nil || len(ss) != 1 {
			t.Fatalf("%s parse: %v, %d sessions", reader.name, err, len(ss))
		}
		files, edits, wrote := map[string]bool{}, map[string]bool{}, map[string]bool{}
		for _, m := range ss[0].Messages {
			switch m.Role {
			case RoleFiles:
				for _, p := range strings.Split(m.Text, "\n") {
					files[p] = true
				}
			case RoleEdit:
				p, span, _ := strings.Cut(m.Text, "\n")
				if span == old {
					edits[p] = true
				}
			case RoleWrote:
				if p, has := WroteRecordHas(m.Text, h); has {
					wrote[p] = true
				}
			}
		}
		for _, f := range []string{"/w/conf/a.cfg", "/w/conf/b.cfg", "/w/conf/c.cfg", "/w/conf/d.cfg"} {
			if !files[f] {
				t.Errorf("%s: no files record for %s: %v", reader.name, f, files)
			}
			if !edits[f] {
				t.Errorf("%s: no edit record for %s: %v", reader.name, f, edits)
			}
			if !wrote[f] {
				t.Errorf("%s: no wrote record for %s: %v", reader.name, f, wrote)
			}
		}
	}
}
