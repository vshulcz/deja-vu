package index

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The same corpus, reached two ways: built in one go, or grown a session at a
// time. Two bugs came from these paths disagreeing about what they derive, and
// one of them (friction signatures hashed from unredacted text) sat in a field
// of this struct. So compare the whole row rather than one field at a time —
// whatever diverges next will be a field nobody thought to check.
func TestSessionMetadataIsTheSameWhicheverWayTheIndexGotThere(t *testing.T) {
	type sess struct{ name, ts, body, tail string }
	corpus := []sess{
		{name: "alpha", ts: "2026-01-02T03:04:05Z", body: `{"type":"user","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","isSidechain":true,"parentUuid":"root","agentId":"worker-1","message":{"role":"user","content":"the quokkasprocket deadlock in scheduler.go hangs every deploy"}}
{"type":"assistant","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":120,"output_tokens":45},"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/app/scheduler.go","old_string":"mu.Lock()"}}]}}
{"type":"assistant","sessionId":"alpha","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":80,"output_tokens":30},"role":"assistant","content":"took the queue lock before the worker lock"}}
`},
		{name: "beta", ts: "2026-01-03T03:04:05Z", body: `{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":"build broken again"}}
{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"undefined: renderInvoice in vendor/billing/api.go"}]}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":200,"output_tokens":60,"cache_read_input_tokens":500},"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go mod vendor && go build ./..."}}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":90,"output_tokens":25},"role":"assistant","content":"vendoring fixed it"}}
`, tail: `{"type":"user","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"role":"user","content":[{"type":"tool_result","is_error":true,"content":[{"type":"text","text":"connection refused: cache-a1:6379 for sk-live-ZZTOPSECRET1234567890abcd"}]}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-opus-4-1-20250805","usage":{"input_tokens":150,"output_tokens":40},"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"systemctl restart redis"}}]}}
{"type":"assistant","sessionId":"beta","cwd":"/w/app","timestamp":"TS","message":{"model":"claude-opus-4-1-20250805","usage":{"input_tokens":60,"output_tokens":20},"role":"assistant","content":"restarted the cache too"}}
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
		for i := range va.NumField() {
			if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
				diffs = append(diffs, fmt.Sprintf("%s: whole=%v grown=%v",
					va.Type().Field(i).Name, va.Field(i).Interface(), vb.Field(i).Interface()))
			}
		}
		t.Errorf("%s differs by build path — %s", k, strings.Join(diffs, "; "))
	}
}

// Every field added to SessionMeta has to say which of the three row-transfer
// paths (deleted-copy keep across versions, deleted-copy keep within the same
// version, incremental sync batch carry/reset) owns it. Leaving a new derived
// field out of withKeptRow or carryAccumulatedFrom silently drifts incremental
// passes and sync imports away from a rebuild.
func TestEverySessionMetaFieldIsClassifiedForCarryAndKeep(t *testing.T) {
	watermarkFields := map[string]bool{
		"Counted": true, "Words": true, "LastMsg": true, "Opening": true,
		"Kind": true, "Parent": true, "Agent": true,
		"Model": true, "Tokens": true, "ToolCalls": true, "ToolErrors": true,
	}
	keptSameVersionOnly := map[string]bool{
		"Settled": true, "RanCommand": true, "Asked": true, "Hit": true, "GaveUp": true,
		"Touched": true, "TouchHits": true, "Rules": true, "Revokes": true, "Planted": true,
	}
	syncCarryExtra := map[string]bool{
		"Asked": true, "Hit": true, "GaveUp": true, "NoText": true,
		"Touched": true, "TouchHits": true, "Settled": true, "RanCommand": true,
	}
	resetZeroed := map[string]bool{
		"Counted": true, "Words": true, "LastMsg": true, "Opening": true,
		"Asked": true, "Hit": true, "GaveUp": true, "NoText": true,
		"Touched": true, "TouchHits": true, "Settled": true, "RanCommand": true,
	}
	identityOrSpan := map[string]bool{
		"ID": true, "Harness": true, "Project": true, "Path": true,
		"Title": true, "AgentTitle": true, "Started": true, "Updated": true,
		"Shared": true, "SharedStarted": true, "SharedUpdated": true, "Copies": true,
		"OrigID": true, "RemoteID": true,
		"From": true, "Lifecycle": true, "LifecycleNote": true, "LifecycleAt": true,
		"Ord": true, "NoText": true, "Rules": true, "Revokes": true, "Planted": true,
	}

	rt := reflect.TypeFor[SessionMeta]()
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if !watermarkFields[name] && !keptSameVersionOnly[name] && !syncCarryExtra[name] && !identityOrSpan[name] {
			t.Errorf("SessionMeta.%s is unclassified: update withKeptRow / carryAccumulatedFrom / resetAccumulated in ingest_derived.go and classify it here", name)
		}
	}

	full := SessionMeta{
		Counted: 7, Words: 42, LastMsg: 99, Opening: 123,
		Kind: "subagent", Parent: "root-1", Agent: "worker",
		Model:      &model.Model{Provider: "openai", ID: "gpt-5.4"},
		Tokens:     &model.Tokens{Input: 100, Output: 50},
		ToolCalls:  map[string]int{"Bash": 2},
		ToolErrors: 1,
		Settled:    "fixed", RanCommand: true, Asked: []uint64{1}, Hit: []uint64{2}, GaveUp: true,
		Touched: []string{"a.go"}, TouchHits: []int{3}, NoText: true,
		Rules: []string{"make test"}, Revokes: []string{"go test"}, Planted: []string{"plan"},
	}

	keptSame := withKeptRow(SessionMeta{}, full, true)
	vFull := reflect.ValueOf(full)
	vKeptSame := reflect.ValueOf(keptSame)
	vKeptDiff := reflect.ValueOf(withKeptRow(SessionMeta{}, full, false))
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if watermarkFields[name] || keptSameVersionOnly[name] {
			if !reflect.DeepEqual(vKeptSame.Field(i).Interface(), vFull.Field(i).Interface()) {
				t.Errorf("withKeptRow(sameVersion=true) did not copy SessionMeta.%s", name)
			}
		}
		if watermarkFields[name] {
			if !reflect.DeepEqual(vKeptDiff.Field(i).Interface(), vFull.Field(i).Interface()) {
				t.Errorf("withKeptRow(sameVersion=false) did not copy watermark field SessionMeta.%s", name)
			}
		} else if keptSameVersionOnly[name] {
			if !vKeptDiff.Field(i).IsZero() {
				t.Errorf("withKeptRow(sameVersion=false) leaked version-bound field SessionMeta.%s", name)
			}
		}
	}

	var carried SessionMeta
	carryAccumulatedFrom(&carried, full)
	vCarried := reflect.ValueOf(carried)
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if watermarkFields[name] || syncCarryExtra[name] {
			if !reflect.DeepEqual(vCarried.Field(i).Interface(), vFull.Field(i).Interface()) {
				t.Errorf("carryAccumulatedFrom did not copy SessionMeta.%s", name)
			}
		}
	}

	resetAccumulated(&carried, false)
	vReset := reflect.ValueOf(carried)
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		if resetZeroed[name] {
			if !vReset.Field(i).IsZero() {
				t.Errorf("resetAccumulated did not zero SessionMeta.%s", name)
			}
		}
	}
}
