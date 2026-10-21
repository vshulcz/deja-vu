package index

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The build derives each row's text fields ahead of the write loop, in
// parallel. The row has to be the one metaForSession gives for the same
// session, including when the loop hands the session an earlier row's title.
func TestSessionTextsRowIsMetaForSession(t *testing.T) {
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	openings := []string{
		"export GITHUB_TOKEN=ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8 then push",
		"  --password [redacted:credential] and postgres://app:S3cretPass@db.internal:5432/prod  ",
		"пароль: БазаПароль2026годаДлинный",
		"hi",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdefghijk",
		strings.Repeat("x", maxIndexedText-10) + " password: hunter2hunter2",
		"",
	}
	var ss []model.Session
	for i, o := range openings {
		s := model.Session{Harness: "codex", ID: "s" + string(rune('a'+i)), Path: "/tmp/s.jsonl", Project: "p", Started: at, Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: o, Time: at},
				{Role: "assistant", Text: "ran go test ./... and it failed: undefined: foo", Time: at.Add(time.Second)},
				{Role: roleCommand, Text: "go test ./...", Time: at.Add(2 * time.Second)},
				{Role: "user", Text: "now fix the failing build in internal/index/ingest.go", Time: at.Add(3 * time.Second)},
			}}
		if i%2 == 1 {
			s.Title = o
		}
		ss = append(ss, s)
	}
	preRedactSessions(nil, ss)
	texts := sessionTexts(ss)
	for i, s := range ss {
		if got, want := texts[i].row(s), metaForSession(s); !reflect.DeepEqual(got, want) {
			t.Errorf("session %d: row\n got %+v\nwant %+v", i, got, want)
		}
		// What the loop changes before it writes the row.
		s.Title, s.Project, s.Started = "token sk-ant-api03-"+strings.Repeat("A", 90), "history", at.Add(-time.Hour)
		if got, want := texts[i].row(s), metaForSession(s); !reflect.DeepEqual(got, want) {
			t.Errorf("session %d with a borrowed title: row\n got %+v\nwant %+v", i, got, want)
		}
	}
}
