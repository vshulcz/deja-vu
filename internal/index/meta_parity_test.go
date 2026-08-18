package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The same corpus, reached two ways: built in one go, or grown a session at a
// time. Two bugs came from these paths disagreeing about what they derive, and
// one of them (friction signatures hashed from unredacted text) sat in a field
// of this struct. So compare the whole row rather than one field at a time —
// whatever diverges next will be a field nobody thought to check.
func TestSessionMetadataIsTheSameWhicheverWayTheIndexGotThere(t *testing.T) {
	type sess struct{ name, ts, body, tail string }
	corpus := []sess{
		{name: "alpha", ts: "2026-01-02T03:04:05Z", body: `{"type":"user","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"the quokkasprocket deadlock in scheduler.go hangs every deploy"}}
{"type":"assistant","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/app/scheduler.go","old_string":"mu.Lock()"}}]}}
{"type":"assistant","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":"took the queue lock before the worker lock"}}
`},
		{name: "beta", ts: "2026-01-03T03:04:05Z", body: `{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"build broken again"}}
{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"undefined: renderInvoice in vendor/billing/api.go"}]}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go mod vendor && go build ./..."}}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":"vendoring fixed it"}}
`, tail: `{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"connection refused: cache-a1:6379 for sk-live-ZZTOPSECRET1234567890abcd"}]}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":"restarted the cache too"}}
`},
		// One whose text redaction rewrites. Without it the two paths agree even
		// when one of them skips redaction entirely, because raw and scrubbed are
		// the same string — and the fields derived from that text (the friction
		// signatures in Asked/Hit) would compare equal for the wrong reason. This
		// is what makes the comparison able to see #1300 at all.
		{name: "delta", ts: "2026-01-05T03:04:05Z", body: `{"type":"user","sessionId":"delta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"why does it say connection refused: db.internal:5432 for sk-live-ZZTOPSECRET1234567890abcd?"}}
{"type":"user","sessionId":"delta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","content":[{"type":"text","text":"connection refused: db.internal:5432 for sk-live-ZZTOPSECRET1234567890abcd"}]}]}}
{"type":"assistant","sessionId":"delta","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":"rotated the credential and it came back"}}
`},
		// One that gives up: GaveUp is derived, and derived is where paths drift.
		{name: "gamma", ts: "2026-01-04T03:04:05Z", body: `{"type":"user","sessionId":"gamma","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"why does the token refresh loop spin"}}
{"type":"assistant","sessionId":"gamma","cwd":"/w/app","timestamp":"TS","message":{"role":"assistant","content":"reverted the token refresh change; it was not the loop"}}
`},
	}

	build := func(t *testing.T, grow bool) map[string]SessionMeta {
		t.Helper()
		tmp := t.TempDir()
		setHome(t, filepath.Join(tmp, "home"))
		claude := filepath.Join(tmp, "claude")
		proj := filepath.Join(claude, "-w-app")
		if err := os.MkdirAll(proj, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_CLAUDE_ROOT", claude)
		dir := filepath.Join(tmp, "index.db")
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(proj, name+".jsonl"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		appendTo := func(name, body string) {
			f, err := os.OpenFile(filepath.Join(proj, name+".jsonl"), os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString(body); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if grow {
			for _, s := range corpus {
				body := strings.ReplaceAll(s.body, "TS", s.ts)
				// A transcript that grows rather than appears takes a third path:
				// canAppendIncremental only says yes for a file already indexed,
				// and that path writes records in place and touches no sidecar.
				// Splitting one session across two passes is what reaches it.
				if s.tail != "" {
					write(s.name, body)
					if err := Ensure(dir, "", false, nil); err != nil {
						t.Fatal(err)
					}
					appendTo(s.name, strings.ReplaceAll(s.tail, "TS", s.ts))
				} else {
					write(s.name, body)
				}
				if err := Ensure(dir, "", false, nil); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			for _, s := range corpus {
				write(s.name, strings.ReplaceAll(s.body+s.tail, "TS", s.ts))
			}
			if err := Ensure(dir, "", false, nil); err != nil {
				t.Fatal(err)
			}
		}
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m.Sessions
	}

	whole, grown := build(t, false), build(t, true)
	if len(whole) != len(corpus) {
		t.Fatalf("built whole indexed %d sessions, want %d", len(whole), len(corpus))
	}
	if len(whole) != len(grown) {
		t.Fatalf("built whole has %d sessions, grown has %d", len(whole), len(grown))
	}

	keys := make([]string, 0, len(whole))
	for k := range whole {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a, b := whole[k], grown[k]
		// Ord is the position a session got during its build and is expected to
		// differ; Path holds a temp directory unique to each build.
		a.Ord, b.Ord = 0, 0
		a.Path, b.Path = "", ""
		// Words, Asked, Hit, Touched and GaveUp were excluded here while a
		// session that grew never updated them (#1304). They are compared now:
		// the append path carries enough state to fold only what it has not
		// seen, so growing a session one message at a time reaches the same row
		// as building the whole thing at once. LastMsg fingerprints the newest
		// message either way and is part of that guarantee, so it is compared
		// too.
		if reflect.DeepEqual(a, b) {
			continue
		}
		va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
		var diffs []string
		for i := 0; i < va.NumField(); i++ {
			if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
				diffs = append(diffs, fmt.Sprintf("%s: whole=%v grown=%v",
					va.Type().Field(i).Name, va.Field(i).Interface(), vb.Field(i).Interface()))
			}
		}
		t.Errorf("%s differs by build path — %s", k, strings.Join(diffs, "; "))
	}
}
