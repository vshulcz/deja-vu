package index

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// The substring rung found its candidates in another project, so the project
// filter left none. The close-tier check then had no offsets to read, and no
// offsets meant the whole record log: a recall that found nothing read every
// record on the store first, 13 s on 4050 sessions (#5370).
func TestCloseTierWithNoCandidateLeftReadsNoRecordLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	ss := []model.Session{{ //nolint:prealloc // a fixture literal the loop below grows
		Harness: "claude", ID: "elsewhere", Project: "other",
		Messages: []model.Message{{Role: "user", Text: "the reverseproxy bridge between services kept retrying"}},
	}, {
		Harness: "claude", ID: "here", Project: "mine",
		Messages: []model.Message{{Role: "user", Text: "the bridge between services in our app keeps retrying"}},
	}}
	for i := range 40 {
		ss = append(ss, model.Session{
			Harness: "claude", ID: fmt.Sprintf("filler-%d", i), Project: "mine",
			Messages: []model.Message{
				{Role: "user", Text: fmt.Sprintf("rename the parser field %d", i)},
				{Role: "assistant", Text: fmt.Sprintf("renamed field %d in the token stream", i)},
			},
		})
	}
	writeTestStore(t, dir, ss...)

	read := 0
	wholeLogScanned = func() { read++ }
	t.Cleanup(func() { wholeLogScanned = nil })

	o := query.Options{Query: "proxy bridge services", All: true, Project: "mine"}
	res, err := SearchDetailed(dir, o)
	if err != nil {
		t.Fatal(err)
	}
	if read != 0 {
		t.Errorf("the recall read %d records of the whole log after the filter left no candidate", read)
	}
	if len(res.Sessions) == 0 || res.Sessions[0].ID != "here" {
		ids := make([]string, 0, len(res.Sessions))
		for _, s := range res.Sessions {
			ids = append(ids, s.ID)
		}
		t.Errorf("want the session in this project first, got %v", ids)
	}
}
