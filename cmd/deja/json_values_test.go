package main

// What the --json surfaces print, held to docs/json-output.md where a key
// check cannot see it: the values of the fields, and the filtering of text a
// transcript supplied. json_surfaces_contract_test.go holds the keys.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// fixtureStore writes Claude sessions (id -> jsonl rows) under one project
// directory and builds the index. It returns the working directory the rows
// name as their cwd, which exists on disk.
func fixtureStore(t *testing.T, sessions map[string][]string) {
	t.Helper()
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for id, lines := range sessions {
		writeClaudeFixture(t, filepath.Join(root, fixtureProjectDir, id+".jsonl"), id, lines)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// fixtureProjectDir is the Claude project directory named after the fixture cwd.
var fixtureProjectDir = "-repo-app"

// fixtureEnv is hermeticEnv plus a real working directory the fixture rows name.
func fixtureEnv(t *testing.T) string {
	t.Helper()
	hermeticEnv(t)
	cwd := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// Claude names the folder after the cwd with every other character a dash,
	// which on Windows also takes the drive's colon and the backslashes.
	defer func() { fixtureProjectDir = nonAlnum.ReplaceAllString(cwd, "-") }()
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = real
	}
	return cwd
}

// fixtureRow is one Claude transcript line. content is a string or a list of
// content parts; ts may be empty for an undated turn.
func fixtureRow(id, role, ts, cwd string, content any) string {
	r := map[string]any{
		"type": role, "sessionId": id, "cwd": cwd,
		"message": map[string]any{"role": role, "content": content},
	}
	if ts != "" {
		r["timestamp"] = ts
	}
	b, _ := json.Marshal(r)
	return string(b)
}

func fixtureToolUse(id, ts, cwd, useID, command string) string {
	return fixtureRow(id, "assistant", ts, cwd, []any{map[string]any{
		"type": "tool_use", "id": useID, "name": "Bash", "input": map[string]any{"command": command},
	}})
}

func fixtureToolResult(id, ts, cwd, useID, out string, isErr bool) string {
	return fixtureRow(id, "user", ts, cwd, []any{map[string]any{
		"type": "tool_result", "tool_use_id": useID, "content": out, "is_error": isErr,
	}})
}

func runJSONOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := captureRun(t, args...)
	if err != nil {
		t.Fatalf("deja %v: %v", args, err)
	}
	return out
}

func runJSONObject(t *testing.T, args ...string) map[string]any {
	t.Helper()
	out := runJSONOut(t, args...)
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("deja %v: not a JSON object: %v\n%s", args, err, out)
	}
	return m
}

func jsonOutputDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// docs: "`messages_total` is how many messages the session holds and
// `messages_capped` says the list is a selection; both are omitted when the
// whole session fits." On the exact tier the hit's session arrives from the
// index holding only its matching records, so boundedHit counts those: a
// two-message session reports messages_total 1 and no messages_capped while
// the list is a selection.
func TestSearchJSONMessagesTotalIsTheSessionsCount(t *testing.T) {
	cwd := fixtureEnv(t)
	fixtureStore(t, map[string][]string{"hunt1": {
		fixtureRow("hunt1", "user", "2026-08-20T10:00:00Z", cwd, "the ingest worker rejects valid unicode"),
		fixtureRow("hunt1", "assistant", "2026-08-20T10:01:00Z", cwd, "traced it to a frobnitz pool sized from the wrong key"),
	}})
	m := runJSONObject(t, "search", "frobnitz", "--json")
	hits, _ := m["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("want 1 hit, got %d: %v", len(hits), m)
	}
	h := hits[0].(map[string]any)
	msgs := h["session"].(map[string]any)["messages"].([]any)
	total, hasTotal := h["messages_total"]
	_, capped := h["messages_capped"]
	if len(msgs) < 2 {
		if !hasTotal || total.(float64) != 2 {
			t.Errorf("messages_total = %v (present=%v), want 2: the session holds two messages", total, hasTotal)
		}
		if !capped {
			t.Errorf("messages_capped absent while messages holds %d of 2", len(msgs))
		}
	} else if hasTotal || capped {
		t.Errorf("whole session fits, yet messages_total=%v messages_capped=%v are present", total, capped)
	}
}

// fixtureFailThenFix is three sessions where a command fails and the next
// command is the remedy.
func fixtureFailThenFix(t *testing.T, cwd string) {
	t.Helper()
	sessions := map[string][]string{}
	for i, day := range []string{"01", "02", "03"} {
		id := "fix" + day
		ts := "2026-09-" + day + "T10:00:00Z"
		u := string(rune('a' + i))
		sessions[id] = []string{
			fixtureRow(id, "user", ts, cwd, "lint the shell scripts"),
			fixtureToolUse(id, ts, cwd, u+"1", "shellcheck scripts/*.sh"),
			fixtureToolResult(id, ts, cwd, u+"1", "zsh:1: command not found: shellcheck", true),
			fixtureToolUse(id, ts, cwd, u+"2", "brew install shellcheck"),
			fixtureToolResult(id, ts, cwd, u+"2", "==> Pouring shellcheck", false),
			fixtureRow(id, "assistant", ts, cwd, "installed shellcheck, the lint runs now"),
		}
	}
	fixtureStore(t, sessions)
}

// docs: `fix --json` rows carry `"command": "brew install shellcheck"` —
// "A caller acting on a fix automatically" runs it. The row carries the
// index's record text instead: a "$ " prompt in front and the "  → exit 0"
// outcome suffix behind (fix.go:249 passes p.Command through without
// index.CommandWithoutExitStatus or the "$ " strip).
func TestFixJSONCommandIsTheCommand(t *testing.T) {
	cwd := fixtureEnv(t)
	fixtureFailThenFix(t, cwd)
	t.Chdir(cwd)
	m := runJSONObject(t, "fix", "command not found: shellcheck", "--json")
	fixes, _ := m["fixes"].([]any)
	if len(fixes) == 0 {
		t.Fatalf("no fixes: %v", m)
	}
	for _, f := range fixes {
		cmd, _ := f.(map[string]any)["command"].(string)
		if cmd != "brew install shellcheck" {
			t.Errorf("fix command = %q, want %q", cmd, "brew install shellcheck")
		}
	}
}

// docs: `how --json` "command" is "The real command this machine runs"
// (`"command": "go test ./... -race"`). The row carries "$ go test …", and a
// command longer than 80 runes is elided with "…" (how.go:39 firstCommandLine
// runs before grouping), so the JSON value cannot be run.
func TestHowJSONCommandIsRunnable(t *testing.T) {
	cwd := fixtureEnv(t)
	long := "go test ./internal/pool/... -run 'TestPoolDrainsOnClose|TestPoolResizes' -count=1 -race -v"
	sessions := map[string][]string{}
	for _, day := range []string{"01", "02"} {
		id := "how" + day
		ts := "2026-09-" + day + "T10:00:00Z"
		sessions[id] = []string{
			fixtureRow(id, "user", ts, cwd, "run the pool tests"),
			fixtureToolUse(id, ts, cwd, "u"+day, long),
			fixtureToolResult(id, ts, cwd, "u"+day, "ok  \texample.com/app/internal/pool\t0.2s", false),
		}
	}
	fixtureStore(t, sessions)
	t.Chdir(cwd)
	m := runJSONObject(t, "how", "test", "--json")
	cmds, _ := m["commands"].([]any)
	if len(cmds) == 0 {
		t.Fatalf("no commands: %v", m)
	}
	got, _ := cmds[0].(map[string]any)["command"].(string)
	if got != long {
		t.Errorf("how command = %q\nwant          %q", got, long)
	}
}

// docs: wip --json `command` is "the last one it ran" and
// `command_failed` says whether it failed (`"command": "go test
// ./internal/worker/..."`). The field is the record text: "$ " in front and
// "  → exit 0" behind, the outcome duplicated into the command string
// (wip.go:66 passes r.Command through).
func TestWipJSONCommandIsTheCommand(t *testing.T) {
	cwd := fixtureEnv(t)
	fixtureStore(t, map[string][]string{"wip01": {
		fixtureRow("wip01", "user", "2026-09-01T10:00:00Z", cwd, "make the pool tests pass"),
		fixtureToolUse("wip01", "2026-09-01T10:01:00Z", cwd, "w1", "go test ./internal/pool/..."),
		fixtureToolResult("wip01", "2026-09-01T10:01:00Z", cwd, "w1", "ok  \texample.com/app/internal/pool\t0.2s", false),
		fixtureRow("wip01", "assistant", "2026-09-01T10:02:00Z", cwd, "root cause: the pool closed before draining. Fixed by draining first."),
	}})
	t.Chdir(cwd)
	m := runJSONObject(t, "wip", "--json")
	if got, _ := m["command"].(string); got != "go test ./internal/pool/..." {
		t.Errorf("wip command = %q, want %q", got, "go test ./internal/pool/...")
	}
}

// docs (doctor/sync paragraph, stated for every surface): "The text a
// session holds is filtered on top of that — the bidi overrides and the
// invisible tag block … nothing recalled from a transcript needs to reorder a
// reader's screen or arrive invisible". search and show filter (#3616);
// wip --json (wip.go:59-69, safeWIPLine is applied on the screen path only)
// and recap --json (session title) emit U+202E and U+E0041 verbatim.
func TestWipAndRecapJSONFilterBidiAndTags(t *testing.T) {
	cwd := fixtureEnv(t)
	ts := timeNowRFC3339(t)
	fixtureStore(t, map[string][]string{"bidi1": {
		fixtureRow("bidi1", "user", ts, cwd, "fix the \u202eevil\u202c importer and the \U000E0041hidden\U000E0042 tag"),
		fixtureRow("bidi1", "assistant", ts, cwd, "root cause: the \u202ereversed\u202c parser dropped the BOM. Fixed by stripping it first."),
	}})
	t.Chdir(cwd)
	bad := regexp.MustCompile("[\u202a-\u202e\u2066-\u2069\U000E0000-\U000E007F]")
	for _, args := range [][]string{{"wip", "--json"}, {"recap", "--json"}, {"search", "importer", "--json"}} {
		out := runJSONOut(t, args...)
		if bad.MatchString(out) {
			t.Errorf("deja %v --json carries bidi/tag characters from the transcript", args[:len(args)-1])
		}
	}
}

func timeNowRFC3339(t *testing.T) string {
	t.Helper()
	return time.Now().Add(-time.Hour).UTC().Format("2006-01-02T15:04:05Z")
}

// docs: blame's `session` "is the session object `search` returns", and
// "Every machine session has `source.origin`". blame rows never get
// SetSource (only search.Print and machine_output.go call it), so a blame
// row's session has no `source`.
func TestBlameJSONSessionCarriesSource(t *testing.T) {
	cwd := fixtureEnv(t)
	fixtureStore(t, map[string][]string{"bl1": {
		fixtureRow("bl1", "user", "2026-09-01T10:00:00Z", cwd, "why does internal/pool/pool.go leak connections"),
		fixtureRow("bl1", "assistant", "2026-09-01T10:01:00Z", cwd, "internal/pool/pool.go never closed idle connections"),
	}})
	t.Chdir(cwd)
	out := runJSONOut(t, "blame", "internal/pool/pool.go", "--json", "--all-projects")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) == 0 {
		t.Fatalf("blame --json: %v %q", err, out)
	}
	for _, r := range rows {
		s := r["session"].(map[string]any)
		src, _ := s["source"].(map[string]any)
		if src["origin"] != "local" {
			t.Errorf("blame row session %v has source %v, want origin local", s["id"], s["source"])
		}
	}
}

// `deja rules candidates --json` prints a top-level array with no
// schema_version (rules_candidates.go:118-124) and is in neither half of the
// stability policy nor anywhere in docs/json-output.md.
func TestRulesCandidatesJSONIsInThePolicy(t *testing.T) {
	fixtureEnv(t)
	fixtureStore(t, map[string][]string{})
	out := strings.TrimSpace(runJSONOut(t, "rules", "candidates", "--json"))
	if !strings.HasPrefix(out, "[") {
		t.Fatalf("expected the bare array this test documents, got %q", out)
	}
	policy := docSection(t, jsonOutputDoc(t), "## Stability policy")
	if policyBulletFor(t, policy, "deja rules candidates --json") == "" {
		t.Errorf("rules candidates --json prints a bare array without schema_version and the stability policy does not name it")
	}
	if !strings.Contains(jsonOutputDoc(t), "## `deja rules candidates --json`") {
		t.Errorf("docs/json-output.md has no section for rules candidates --json")
	}
}

// docs: "Without `--harness`, an id prefix that names one session is
// read". A complete id that also prefixes another id (abc / abc2) resolves to
// exactly that session on the text path (show abc prints abc) but --json
// refuses it with "use a longer prefix", which a complete id cannot follow
// (main.go:800-804 ambiguousJSONPrefix counts prefix matches after
// findByPrefix already took the exact one).
func TestShowJSONReadsAnExactIDThatPrefixesAnother(t *testing.T) {
	cwd := fixtureEnv(t)
	fixtureStore(t, map[string][]string{
		"abc":  {fixtureRow("abc", "user", "2026-09-01T10:00:00Z", cwd, "first session")},
		"abc2": {fixtureRow("abc2", "user", "2026-09-02T10:00:00Z", cwd, "second session")},
	})
	out, err := captureRun(t, "show", "abc", "--json")
	if err != nil {
		t.Fatalf("show abc --json refused a complete id: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(out), &m)
	if s, _ := m["session"].(map[string]any); s["id"] != "abc" {
		t.Errorf("show abc --json read %v", m["session"])
	}
}

// The session object's rule: a missing time "is absent when the
// transcript did not carry one — the zero time reads as a date in the year
// one". secrets findings put `when: time.Time` with no omitempty
// (internal/index/secrets.go:26), so an undated session reports
// "0001-01-01T00:00:00Z".
func TestSecretsJSONUndatedFindingHasNoYearOne(t *testing.T) {
	cwd := fixtureEnv(t)
	fixtureStore(t, map[string][]string{"sec2": {
		fixtureRow("sec2", "user", "", cwd, "token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 for the ci"),
		fixtureRow("sec2", "assistant", "", cwd, "ok"),
	}})
	out := runJSONOut(t, "secrets", "--json")
	if !strings.Contains(out, `"findings"`) || !strings.Contains(out, "sec2") {
		t.Fatalf("fixture produced no finding: %s", out)
	}
	if strings.Contains(out, "0001-01-01") {
		t.Errorf("secrets --json reports an undated finding as year one:\n%s", out)
	}
}

// docs (stats --json): "`since` … omitted when zero, so a store with no
// recall history yet shows neither", and "Optional fields are omitted when
// zero or empty". On a store whose usage log holds only the reader's own
// searches, recall.since is present; on an empty store date_range is `{}`
// and longest_session / busiest_day are `{"messages":0}` hollow objects.
func TestStatsJSONEmptyStoreShape(t *testing.T) {
	fixtureEnv(t)
	fixtureStore(t, map[string][]string{})
	_, _ = captureRun(t, "search", "anything", "--json") // a search, not a recall
	m := runJSONObject(t, "stats", "--json")
	// recall.since counts any event, the search above included, which the
	// section says; the hollow objects are what it says are omitted.
	for _, k := range []string{"date_range", "longest_session", "busiest_day"} {
		if v, ok := m[k]; ok {
			t.Errorf("empty store: %s = %v, documented as omitted when empty", k, v)
		}
	}
}
