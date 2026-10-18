package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A Qwen /branch copies its source's records at their original times, each
// marked forkedFrom, so the write_file that made a line sits in both files at
// one instant and the branch took the credit (#5224).
func TestBlameLineGivesAQwenBranchCopyToItsSource(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(root, "qwen"))
	repo := filepath.Join(root, "my-app")
	const line = "WALLABY3 parent line written by the parent session itself"
	file := filepath.Join(repo, "parent.txt")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jq := func(v string) string { b, _ := json.Marshal(v); return string(b) }
	const src, branch = "8bde8fee-08e8-4950-b83b-2e23c486eb7d", "42355bb7-67a9-47b4-8436-796a7075a970"
	records := func(id, forked string) string {
		ff := ""
		if forked != "" {
			ff = `,"forkedFrom":{"sessionId":"` + forked + `"}`
		}
		r := func(typ, at, msg string) string {
			return `{"sessionId":"` + id + `","timestamp":"2026-10-18T06:03:` + at + `Z","type":"` + typ + `","cwd":` + jq(repo) + ff + `,` + msg + "}\n"
		}
		return r("user", "05.759", `"message":{"role":"user","parts":[{"text":"write parent.txt yourself"}]}`) +
			r("assistant", "05.848", `"message":{"role":"model","parts":[{"functionCall":{"id":"c1","name":"write_file","args":{"file_path":`+jq(file)+`,"content":"`+line+`\n"}}}]}`) +
			r("tool_result", "05.974", `"message":{"role":"user","parts":[{"functionResponse":{"id":"c1","name":"write_file","response":{"output":"ok"}}}]}`)
	}
	parse := func(id, body string) model.Session {
		t.Helper()
		p := filepath.Join(root, "qwen", "projects", "-w-my-app", "chats", id+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		ss, err := sources.ParseQwenFile(p)
		if err != nil || len(ss) != 1 {
			t.Fatalf("parse %s: %d sessions, %v", id, len(ss), err)
		}
		return ss[0]
	}
	source := parse(src, records(src, ""))
	copied := parse(branch, records(branch, src))
	if copied.Kind != "fork" || copied.Parent != src {
		t.Fatalf("branch read as kind=%q parent=%q", copied.Kind, copied.Parent)
	}
	target := search.BlameTarget{FullPath: file, Base: "parent.txt", Line: 1}
	commit := lineCommit{SHA: "e6ec5986", When: time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC)}
	for _, ss := range [][]model.Session{{copied, source}, {source, copied}} {
		got, ok := attributeLine(ss, target, commit, nil)
		if !ok || got.Session.ID != src {
			t.Errorf("order %s,%s: credited %q, want the source %s", ss[0].ID[:8], ss[1].ID[:8], got.Session.ID, src)
		}
	}
	// Control: the branch's own later write stays the branch's.
	own := parse(branch, strings.ReplaceAll(records(branch, src), "06:03:05", "06:09:05"))
	own.Kind, own.Parent = "fork", src
	if got, _ := attributeLine([]model.Session{source, own}, target, commit, nil); got.Session.ID != branch {
		t.Errorf("a branch's own later write went to %q", got.Session.ID)
	}
}
