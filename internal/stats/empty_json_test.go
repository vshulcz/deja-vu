package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// On a fresh machine `harnesses` marshalled as [] and `top_projects` as null,
// though both are declared as slices and the envelope is documented as a stable
// shape. A consumer iterating top_projects threw on the first run and worked
// ever after — the worst shape to diagnose (#1649).
func TestEmptyReportHasNoNullCollections(t *testing.T) {
	// The fresh-machine path: Build over no sessions at all.
	r := Build(nil, time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC))
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"harnesses", "top_projects", "monthly"} {
		v, ok := got[key]
		if !ok {
			t.Errorf("%s is absent from the envelope", key)
			continue
		}
		if v == nil {
			t.Errorf("%s is null on an empty report; an empty collection is []", key)
		}
	}
	// The control: the sibling scalars still read as zero rather than absent,
	// and nothing became a string.
	if !strings.Contains(string(b), `"total_sessions":0`) {
		t.Errorf("total_sessions changed shape: %s", b)
	}
}
