package index

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/vshulcz/deja-vu/internal/search"
)

// A dsh session that arrives as session.v3.jsonl after the first build has to
// reach the index through the incremental path, and so does every turn it gains
// after that. The path sees the file either way; what decides it is whether the
// deepseek kind matches the name. When it did not, the pass reported
// "changed_files=1 ... sessions=0" and the session stayed unsearchable until a
// rebuild.
func TestDeepSeekV3LogReachesTheIncrementalIndex(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)

	legacy := filepath.Join("..", "..", "fixtures", "registry", "deepseek", "sessions",
		"--work-pgbouncer-lab--", "session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1", "session.jsonl")
	body, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	write := func(session, name string, b []byte) string {
		t.Helper()
		dir := filepath.Join(root, "--work-pgbouncer-lab--", session)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("session-eaf5c9ac-0e47-4d2f-b982-8bae306062d1", "session.jsonl", body)

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if n := len(sessionsOf(t, dir, "deepseek")); n != 1 {
		t.Fatalf("first build indexed %d deepseek sessions, want 1", n)
	}

	v3 := []byte(`{"type":"session","version":0,"id":"session-0b7f3c1e-5a2d-4e8f-9c61-2d4b8a7e1f30","createdAt":1787420263519,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
{"type":"user/message","seq":7,"time":1787420263580,"data":{"content":[{"type":"text","text":"shardneedle where does the shard map live"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":20,"time":1787420263700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"the router reads it from config"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"turn/end","seq":21,"time":1787420263701,"data":{"turn":1}}
`)
	path := write("session-0b7f3c1e-5a2d-4e8f-9c61-2d4b8a7e1f30", "session.v3.jsonl", v3)
	var progress bytes.Buffer
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	if n := len(sessionsOf(t, dir, "deepseek")); n != 2 {
		t.Fatalf("after a v3 log arrived there are %d deepseek sessions, want 2\n%s", n, progress.String())
	}
	assertFound := func(query string) {
		t.Helper()
		ss, err := Search(dir, search.Options{Query: query, All: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(ss) != 1 {
			t.Errorf("%q is in %d sessions, want 1\n%s", query, len(ss), progress.String())
		}
	}
	assertFound("shardneedle")

	grown := slices.Concat(v3, []byte(`{"type":"user/message","seq":30,"time":1787420264580,"data":{"content":[{"type":"text","text":"growthneedle and after a reshard"}],"source":{"kind":"user"},"role":"user"}}
`))
	if err := os.WriteFile(path, grown, 0o644); err != nil {
		t.Fatal(err)
	}
	progress.Reset()
	if err := Ensure(dir, "", false, &progress); err != nil {
		t.Fatal(err)
	}
	assertFound("growthneedle")
	if n := len(sessionsOf(t, dir, "deepseek")); n != 2 {
		t.Errorf("growing the v3 log left %d deepseek sessions, want 2", n)
	}
}
