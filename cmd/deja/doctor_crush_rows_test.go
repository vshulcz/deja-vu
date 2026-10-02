package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// Crush keeps a store per project and doctor prints a row for the registry and
// one per store. Each row ended with the harness-wide counts, so an empty
// crush.db read "1 indexed session" and N projects looked like N times the
// sessions (#4379). The total belongs to the first row of a harness only.
func TestDoctorCountsAHarnessOnceAcrossItsRows(t *testing.T) {
	tmp := hermeticEnv(t)
	if !sources.SQLite3Available() {
		t.Skip("sqlite3 not installed")
	}
	schema := `create table sessions (id text primary key, parent_session_id text, title text not null,
  message_count integer not null default 0, prompt_tokens integer not null default 0,
  completion_tokens integer not null default 0, cost real not null default 0.0,
  updated_at integer not null, created_at integer not null, summary_message_id text, todos text);
create table messages (id text primary key, session_id text not null, role text not null,
  parts text not null default '[]', model text, created_at integer not null, updated_at integer not null,
  finished_at integer, provider text, is_summary_message integer default 0 not null);
`
	store := func(project, sql string) string {
		db := filepath.Join(tmp, project, ".crush", "crush.db")
		if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sqlite3", db)
		cmd.Stdin = strings.NewReader(schema + sql)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build store: %v: %s", err, out)
		}
		return db
	}
	store("full", `insert into sessions values ('s1',null,'retry',1,0,0,0,1790000000,1790000000,null,null);
insert into messages values ('m1','s1','user','[{"type":"text","data":{"text":"fix the retry loop"}}]','m',1790000000,1790000000,null,'p',0);
`)
	store("empty", "")
	root := filepath.Join(tmp, "crush-data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := `{"projects":[{"path":"` + filepath.ToSlash(filepath.Join(tmp, "full")) + `"},{"path":"` + filepath.ToSlash(filepath.Join(tmp, "empty")) + `"}]}`
	if err := os.WriteFile(filepath.Join(root, "projects.json"), []byte(reg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CRUSH_ROOT", root)

	rows := func(dir string) []string {
		var buf bytes.Buffer
		doctorHarnesses(&buf, dir)
		var out []string
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "crush ") {
				out = append(out, line)
			}
		}
		if len(out) != 3 {
			t.Fatalf("want the registry and two store rows, got:\n%s", buf.String())
		}
		return out
	}
	dir := filepath.Join(tmp, "idx")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got := rows(dir)
	if !strings.Contains(got[0], "1 indexed session") {
		t.Errorf("the registry row lost the total: %q", got[0])
	}
	for _, row := range got[1:] {
		if strings.Contains(row, "indexed session") || strings.Contains(row, "never read") {
			t.Errorf("a store row repeats the harness total: %q", row)
		}
	}
}
