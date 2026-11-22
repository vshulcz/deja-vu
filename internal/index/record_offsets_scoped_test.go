package index

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestEachRecordOfRoleWhereUsesOffsetsWhenScoped(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	sessions := make([]model.Session, 0, 6)
	sessions = append(sessions, model.Session{
		Harness: "claude",
		ID:      "scoped-target",
		Project: "proj-target",
		Path:    "/work/proj-target/t1.jsonl",
		Started: base,
		Updated: base.Add(time.Minute),
		Messages: []model.Message{
			{Role: "user", Time: base, Text: "run targeted unit tests"},
			{Role: "command", Time: base.Add(10 * time.Second), Text: "$ go test ./internal/index"},
			{Role: "assistant", Time: base.Add(time.Minute), Text: "ok"},
		},
	})
	for i := range 5 {
		sessions = append(sessions, model.Session{
			Harness: "claude",
			ID:      fmt.Sprintf("scoped-other-%d", i),
			Project: fmt.Sprintf("proj-other-%d", i),
			Path:    fmt.Sprintf("/work/proj-other-%d/s.jsonl", i),
			Started: base.Add(time.Duration(i+1) * time.Minute),
			Updated: base.Add(time.Duration(i+1)*time.Minute + time.Second),
			Messages: []model.Message{
				{Role: "user", Time: base, Text: "unrelated project work"},
				{Role: "command", Time: base.Add(5 * time.Second), Text: "$ make build"},
			},
		})
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}

	before := atomic.LoadInt64(&recordOffsetReads)
	var got []string
	err := EachRecordOfRoleWhere(dir, "command", func(m SessionMeta) bool {
		return m.Project == "proj-target"
	}, func(m SessionMeta, r Record) {
		got = append(got, m.ID+"|"+r.Text)
	})
	if err != nil {
		t.Fatalf("EachRecordOfRoleWhere: %v", err)
	}
	if atomic.LoadInt64(&recordOffsetReads) == before {
		t.Fatal("scoped EachRecordOfRoleWhere walked the log instead of reading recordoffsets.bin")
	}
	if len(got) != 1 || got[0] != "scoped-target|$ go test ./internal/index" {
		t.Fatalf("got = %v, want [scoped-target|$ go test ./internal/index]", got)
	}
}

func TestRoleFilteredWalkShareFallsBackOverCutoff(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(filepath.Join(dir+".tmp", "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC)
	mkMsgs := func(n int) []model.Message {
		msgs := make([]model.Message, 0, n)
		for i := range n {
			msgs = append(msgs, model.Message{
				Role: "command",
				Time: base.Add(time.Duration(i) * time.Millisecond),
				Text: "$ go test",
			})
		}
		return msgs
	}
	sessions := make([]model.Session, 0, 9)
	sessions = append(sessions,
		model.Session{Harness: "claude", ID: "heavy-a", Project: "heavy", Path: "/work/heavy/a.jsonl", Started: base, Updated: base, Messages: mkMsgs(4200)},
		model.Session{Harness: "claude", ID: "heavy-b", Project: "heavy", Path: "/work/heavy/b.jsonl", Started: base, Updated: base, Messages: mkMsgs(4200)},
	)
	for i := range 7 {
		sessions = append(sessions, model.Session{
			Harness:  "claude",
			ID:       fmt.Sprintf("light-%d", i),
			Project:  fmt.Sprintf("light-%d", i),
			Path:     fmt.Sprintf("/work/light-%d/c.jsonl", i),
			Started:  base,
			Updated:  base,
			Messages: mkMsgs(200),
		})
	}
	if err := writeSessions(dir+".tmp", dir, sessions, nil, ""); err != nil {
		t.Fatal(err)
	}

	before := atomic.LoadInt64(&recordOffsetReads)
	var count int
	err := EachRecordOfRoleWhere(dir, "command", func(m SessionMeta) bool {
		return m.Project == "heavy"
	}, func(SessionMeta, Record) {
		count++
	})
	if err != nil {
		t.Fatalf("EachRecordOfRoleWhere: %v", err)
	}
	if count != 8400 {
		t.Fatalf("count = %d, want 8400", count)
	}
	// len(want)=2, offs=8400 > maxAddressedOffsets (8192) and 8400 > 8500/4:
	// role-filtered queries must also obey the sequential walk cutoff.
	if atomic.LoadInt64(&recordOffsetReads) != before {
		t.Fatalf("expected role-filtered >25%% cutoff read to walk sequentially, but recordOffsetReads incremented")
	}
}
