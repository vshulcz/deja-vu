package sources

import (
	"path/filepath"
	"strings"
	"testing"
)

// The sessions table as Hermes 0.17 writes it for delegate_task: the child's
// row has no cwd (only a cli row records one), its source is the parent's
// when the parent ran under --source, the TUI or a gateway, and
// model_config._delegate_from names the parent even after a delete clears
// parent_session_id (tools/delegate_tool.py).
func TestHermesSubagentHangsUnderItsParentAndFilesItsWritesThere(t *testing.T) {
	db := writeHermesDB(t, filepath.Join(t.TempDir(), "p"), `
		CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT, model_config TEXT);
		INSERT INTO sessions VALUES
		 ('20261018_084704_a4b1b7','cli',NULL,'/w/app','{"max_iterations":90}'),
		 ('20261018_084719_441ac7','subagent','20261018_084704_a4b1b7',NULL,'{"_delegate_from":"20261018_084704_a4b1b7"}'),
		 ('20261018_084938_89405b','tool',NULL,NULL,'{"max_iterations":90}'),
		 ('20261018_084941_17754d','tool','20261018_084938_89405b',NULL,'{"_delegate_from":"20261018_084938_89405b"}'),
		 ('20261018_085000_cccccc','subagent',NULL,NULL,'{"_delegate_from":"20261018_084704_a4b1b7"}'),
		 ('20261018_085100_dddddd','cli','20261018_084704_a4b1b7','/w/app','{"max_iterations":90}');
		INSERT INTO messages (session_id,role,content,tool_call_id,tool_calls,tool_name,timestamp) VALUES
		 ('20261018_084704_a4b1b7','user','write the parent file',NULL,NULL,NULL,1792302400.0),
		 ('20261018_084704_a4b1b7','assistant',NULL,NULL,'[{"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"parent.txt\", \"content\": \"PARENT_LINE written by the parent session\\n\"}"}}]',NULL,1792302401.0),
		 ('20261018_084719_441ac7','user','write the child file',NULL,NULL,NULL,1792302410.0),
		 ('20261018_084719_441ac7','assistant',NULL,NULL,'[{"id":"c2","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"child.txt\", \"content\": \"CHILD_LINE written by the delegated subagent\\n\"}"}}]',NULL,1792302411.0),
		 ('20261018_084719_441ac7','assistant',NULL,NULL,'[{"id":"c3","type":"function","function":{"name":"patch","arguments":"{\"mode\": \"patch\", \"patch\": \"*** Begin Patch\\n*** Update File: sub/a.py\\n@@\\n-old_value = compute_the_old_way()\\n+new_value = compute_the_new_way()\\n*** End Patch\"}"}}]',NULL,1792302412.0),
		 ('20261018_084719_441ac7','assistant',NULL,NULL,'[{"id":"c4","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"~/notes/todo.md\", \"content\": \"a line in the home directory, left as written\\n\"}"}},{"id":"c5","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"/elsewhere/abs.txt\", \"content\": \"an absolute path stays exactly as it was\\n\"}"}}]',NULL,1792302413.0),
		 ('20261018_084938_89405b','user','delegate from a tool session',NULL,NULL,NULL,1792302420.0),
		 ('20261018_084941_17754d','user','the tool session child',NULL,NULL,NULL,1792302421.0),
		 ('20261018_084941_17754d','assistant',NULL,NULL,'[{"id":"c6","type":"function","function":{"name":"write_file","arguments":"{\"path\": \"tool-child.txt\", \"content\": \"a session with no cwd keeps the path it wrote\\n\"}"}}]',NULL,1792302422.0),
		 ('20261018_085000_cccccc','user','a child whose parent row was deleted',NULL,NULL,NULL,1792302430.0),
		 ('20261018_085100_dddddd','user','summary of the earlier turns',NULL,NULL,NULL,1792302440.0);`)
	ss, err := ParseHermesDB(db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	heads := map[string][]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
		for _, m := range s.Messages {
			if m.Role == RoleWrote || m.Role == RoleEdit || m.Role == RoleFiles {
				head, _, _ := strings.Cut(m.Text, "\n")
				if m.Role == RoleFiles {
					head = m.Text
				}
				heads[s.ID] = append(heads[s.ID], m.Role+" "+head)
			}
		}
	}
	want := map[string]string{
		"20261018_084704_a4b1b7": "//w/app",
		"20261018_084719_441ac7": "subagent/20261018_084704_a4b1b7/w/app",
		"20261018_084938_89405b": "//p",
		"20261018_084941_17754d": "subagent/20261018_084938_89405b/p",
		"20261018_085000_cccccc": "subagent/20261018_084704_a4b1b7/w/app",
		// A compression's continuation names its parent without the marker.
		"20261018_085100_dddddd": "//w/app",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}
	wantHeads := map[string]string{
		"20261018_084704_a4b1b7": "files /w/app/parent.txt|wrote /w/app/parent.txt",
		"20261018_084719_441ac7": "files /w/app/child.txt|wrote /w/app/child.txt|" +
			"files /w/app/sub/a.py|wrote /w/app/sub/a.py|edit /w/app/sub/a.py|" +
			"files ~/notes/todo.md|wrote ~/notes/todo.md|" +
			"files /elsewhere/abs.txt|wrote /elsewhere/abs.txt",
		"20261018_084941_17754d": "files tool-child.txt|wrote tool-child.txt",
	}
	for id, w := range wantHeads {
		if g := strings.Join(heads[id], "|"); g != w {
			t.Errorf("%s records:\n got %s\nwant %s", id, g, w)
		}
	}
}
