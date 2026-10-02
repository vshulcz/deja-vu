package sources

import (
	"strings"
	"testing"
)

// A thread deja cannot decode is skipped, and the skip is counted against the
// store the way an unusable JSONL line is. Dropped in silence, a new data_type
// from Zed would have emptied the harness while doctor still said "found"
// (#4341).
func TestParseZedDBCountsTheThreadsItCannotDecode(t *testing.T) {
	zedHome(t)
	good := zedZstdHex(t, zedModernBody)
	sql := zedSchema + `
insert into threads (id,summary,updated_at,data_type,data,folder_paths,created_at) values
 ('broken-frame','truncated','2026-07-19T09:00:00+00:00','zstd',x'28b52ffd00',  '/w/p','2026-07-19T09:00:00+00:00'),
 ('unknown-type','future encoding','2026-07-19T09:00:01+00:00','brotli',x'00',   '/w/p','2026-07-19T09:00:01+00:00'),
 ('not-json','json but not a thread','2026-07-19T09:00:02+00:00','json','not json at all','/w/p','2026-07-19T09:00:02+00:00'),
 ('no-messages','empty thread','2026-07-19T09:00:03+00:00','json','{"version":"0.3.0","title":"t","messages":[]}','/w/p','2026-07-19T09:00:03+00:00'),
 ('ok','fine','2026-07-19T09:00:04+00:00','zstd',x'` + good + `','/w/p','2026-07-19T09:00:04+00:00');`
	db := zedTestDB(t, sql)
	DiagSnapshot()
	if _, err := ParseZedDB(db); err != nil {
		t.Fatal(err)
	}
	reasons := DiagReasons()
	malformed, _ := DiagSnapshot()
	// An empty thread is a thread with nothing in it, not one deja failed on.
	if malformed[db] != 3 {
		t.Fatalf("unreadable threads counted = %d, want 3: %v", malformed[db], malformed)
	}
	// Rows come back by updated_at, so the last skip is the JSON one; the
	// brotli row has to name its encoding when it is the only one.
	if r := reasons[db]; !strings.Contains(r, "not-json") {
		t.Fatalf("reason = %q, want the last skipped thread named", r)
	}
	brotli := zedTestDB(t, zedSchema+`
insert into threads (id,summary,updated_at,data_type,data) values ('unknown-type','s','2026-07-19T09:00:01+00:00','brotli',x'00');`)
	if _, err := ParseZedDB(brotli); err != nil {
		t.Fatal(err)
	}
	if r := DiagReasons()[brotli]; !strings.Contains(r, `unknown data_type "brotli"`) {
		t.Fatalf("reason = %q, want the unknown data_type named", r)
	}
	DiagSnapshot()
}
