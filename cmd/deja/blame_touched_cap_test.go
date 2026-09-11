package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// A blame row carried every file its session touched — up to the forty the
// manifest holds. Measured on a real store, that was 3186 of an 8044-byte
// answer, about files the question did not ask about, and the answer is trimmed
// by dropping whole sessions to fit its budget: six real paths came back with 20
// sessions and 9.9 KB of quoted history, and with the list bounded the same
// budget carried 38 sessions and 15.7 KB.
func TestABlameRowNamesAFewTouchedFilesNotForty(t *testing.T) {
	var touched []string
	for i := range 30 {
		touched = append(touched, fmt.Sprintf("/work/app/internal/thing%02d.go", i))
	}
	hit := search.BlameHit{
		Session: model.Session{
			Harness: "claude", ID: "s1", Project: "app",
			Title: "touched a great many files", Touched: touched,
		},
		Count: 1, Snippets: []string{"… internal/thing00.go …"},
	}
	body := mustMarshalBlame([]search.BlameHit{hit}, 0, false)

	var rows []map[string]any
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatal(err)
	}
	var got []any
	for _, row := range rows {
		sess, ok := row["session"].(map[string]any)
		if !ok {
			continue
		}
		if list, ok := sess["touched"].([]any); ok {
			got = list
		}
	}
	if len(got) == 0 {
		t.Fatal("the row names no touched file at all")
	}
	if len(got) > blameTouchedCap {
		t.Errorf("the row names %d touched files, which is the manifest's list rather than a few", len(got))
	}
	// The head of the list is the most-touched end, so that is what survives.
	if first, _ := got[0].(string); !strings.HasSuffix(first, "thing00.go") {
		t.Errorf("the kept file is %q, not the one the session touched most", first)
	}
}
