package sources

import "testing"

// On Postgres a /branch copy and a delegate_task child came out as sessions of
// their own in project "hermes": the read took cwd, source and parent only, not
// model_config's _branched_from / _delegate_from that the SQLite read uses
// (#5294). A row whose model_config is not JSON keeps everything else.
func TestParseHermesPGReadsBranchAndDelegateMarks(t *testing.T) {
	f := &fakePG{
		rows: `[
		{"session_id":"s-src","role":"user","content":"plan the migration","timestamp":1785000000},
		{"session_id":"s-br","role":"user","content":"try it the other way","timestamp":1785000010},
		{"session_id":"s-del","role":"user","content":"read the schema","timestamp":1785000020},
		{"session_id":"s-odd","role":"user","content":"anything","timestamp":1785000030}
	]`,
		columns: `["id","source","parent_session_id","cwd","model_config"]`,
		sessions: `[{"id":"s-src","source":"tui","cwd":"/w/app","model_config":"{\"max_iterations\":90}"},
		{"id":"s-br","source":"tui","model_config":"{\"_branched_from\":\"s-src\"}"},
		{"id":"s-del","source":"tui","model_config":"{\"_delegate_from\":\"s-src\"}"},
		{"id":"s-odd","source":"cli","cwd":"/w/lib","model_config":"not json"}]`,
	}
	defer SetHermesPGRunner(f.run)()
	ss, err := ParseHermesPG("postgres://deja@192.0.2.1/hermes", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range ss {
		got[s.ID] = s.Kind + "/" + s.Parent + "/" + s.Project
	}
	want := map[string]string{
		"s-src": "//w/app",
		"s-br":  "fork/s-src/w/app",
		"s-del": "subagent/s-src/w/app",
		"s-odd": "//w/lib",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}
}

func TestHermesModelConfigMarks(t *testing.T) {
	tests := []struct {
		config, branched, delegated string
	}{
		{`{"_branched_from":"a"}`, "a", ""},
		{`{"_delegate_from":" b "}`, "", "b"},
		{`{"_branched_from":"a","_delegate_from":"b"}`, "a", "b"},
		{`{"_branched_from":7}`, "", ""},
		{`{}`, "", ""},
		{``, "", ""},
		{`null`, "", ""},
		{`[1,2]`, "", ""},
		{`not json`, "", ""},
	}
	for _, tc := range tests {
		b, d := hermesModelConfigMarks(tc.config)
		if b != tc.branched || d != tc.delegated {
			t.Errorf("hermesModelConfigMarks(%q) = %q, %q; want %q, %q", tc.config, b, d, tc.branched, tc.delegated)
		}
	}
}
