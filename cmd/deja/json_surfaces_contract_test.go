package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// docs/json-output.md is the contract for every --json surface, and most of
// them had no pin against it: fix, how, wip, secrets and recap printed values
// and keys their sections never named, and rules candidates had no section at
// all. This runs each surface on one fixture and holds it to its own section,
// both ways:
//
//   - every key it prints is named in its section (or in a section it shares,
//     the session object for the surfaces that carry sessions);
//   - every top-level key of the section's example is printed, unless a
//     sentence of the section names it as omitted or optional.
type jsonSurface struct {
	heading string
	args    []string
	// shared are other sections whose keys this surface prints too.
	shared []string
	// dynamic are keys whose children are data, not contract: a map keyed by
	// harness, path or project. data are keys whose whole value is data, a
	// map of maps keyed by data at both levels.
	dynamic, data []string
}

const sessionObjectHeading = "### The session object"

// sessionSections are where the session object's keys are named: the search
// example shows the ones always there, the table the rest.
var sessionSections = []string{"## `deja search --json`", sessionObjectHeading}

func jsonSurfaces(sessionID string) []jsonSurface {
	return []jsonSurface{
		{heading: "## `deja search --json`", args: []string{"search", "shellcheck", "--json"}, shared: sessionSections},
		{heading: "## `deja last --json`", args: []string{"last", "--json"}, shared: sessionSections},
		{heading: "## `deja show <exact-id> --harness <name> --json`", args: []string{"show", sessionID, "--harness", "claude", "--json"}, shared: sessionSections},
		{heading: "## `deja stats --json`", args: []string{"stats", "--json"}, dynamic: []string{"harnesses", "roles", "projects", "by_harness", "records"}},
		{heading: "## `deja doctor --json`", args: []string{"doctor", "--json", "--offline"}, dynamic: []string{"ingest_health", "ingest_files", "activations", "commands"}},
		{heading: "## `deja friction --json`", args: []string{"friction", "--json"}},
		{heading: "## `deja secrets --json`", args: []string{"secrets", "--json"}, dynamic: []string{"counted"}},
		{heading: "## `deja tests --json`", args: []string{"tests", "--json"}, dynamic: []string{"tools"}},
		{heading: "## `deja recap --json`", args: []string{"recap", "--since", "3650d", "--json"}, dynamic: []string{"masked"}},
		{heading: "## `deja fix <error> --json`", args: []string{"fix", "command not found: shellcheck", "--json"}},
		{heading: "## `deja how <what> --json`", args: []string{"how", "shellcheck", "--json"}},
		{heading: "## `deja files <topic> --json`", args: []string{"files", "pool", "--json"}},
		{heading: "## `deja stats --impact --json`", args: []string{"stats", "--impact", "--json"}},
		{heading: "## `deja stats --redaction --json`", args: []string{"stats", "--redaction", "--json"}, data: []string{"by_harness", "by_kind", "kinds"}},
		{heading: "## `deja stats --year --json`", args: []string{"stats", "--year", "--json"}, dynamic: []string{"harnesses"}},
		{heading: "## `deja log --json`", args: []string{"log", "--json"}},
		{heading: "## `deja blame <path> --json`", args: []string{"blame", "internal/pool/pool.go", "--json", "--all-projects"}, shared: sessionSections},
		{heading: "## `deja blame <path>:<line> --attribution --json`", args: []string{"blame", "internal/pool/pool.go:3", "--attribution", "--json"}},
		{heading: "## `deja wip --json`", args: []string{"wip", "--json"}},
		{heading: "## `deja rules candidates --json`", args: []string{"rules", "candidates", "--json"}},
	}
}

// contractFixture is one project holding a little of everything the surfaces
// report on: a failure and its fix, a command run more than once, a file
// edited and talked about, a correction, and a secret.
func contractFixture(t *testing.T, cwd string) string {
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
			fixtureToolUse(id, ts, cwd, u+"3", "go test ./internal/pool/..."),
			fixtureToolResult(id, ts, cwd, u+"3", "--- FAIL: TestPoolDrains (0.01s)\nFAIL\texample.com/app/internal/pool\t0.2s", true),
			fixtureRow(id, "assistant", ts, cwd, []any{map[string]any{
				"type": "tool_use", "id": u + "4", "name": "Edit",
				"input": map[string]any{"file_path": cwd + "/internal/pool/pool.go", "old_string": "close()", "new_string": "drain(); close()"},
			}}),
			fixtureToolResult(id, ts, cwd, u+"4", "The file has been updated.", false),
			fixtureRow(id, "assistant", ts, cwd, "root cause: internal/pool/pool.go closed before draining. Fixed by draining first."),
		}
	}
	sessions["sec01"] = []string{
		fixtureRow("sec01", "user", "2026-09-04T10:00:00Z", cwd, "token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 for the ci"),
		fixtureRow("sec01", "assistant", "2026-09-04T10:01:00Z", cwd, "noted"),
	}
	// A correction, in a project outside the temp directory: rules candidates
	// leaves out sessions that ran in a scratch directory.
	candidateTempRoots = func() []string { return nil }
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-work-app", "corr01.jsonl"), "corr01", []string{
		fixtureRow("corr01", "user", "2026-09-05T10:00:00Z", "/work/app", "add tests for the pool"),
		fixtureRow("corr01", "assistant", "2026-09-05T10:01:00Z", "/work/app", "I'll add a mock for the pool so the test runs without a database."),
		fixtureRow("corr01", "user", "2026-09-05T10:02:00Z", "/work/app", "no, don't mock the pool in these tests"),
	})
	fixtureStore(t, sessions)
	return "fix03"
}

func TestEveryJSONSurfaceMatchesItsSection(t *testing.T) {
	doc := jsonOutputDoc(t)
	old := candidateTempRoots
	t.Cleanup(func() { candidateTempRoots = old })
	cwd := fixtureEnv(t)
	id := contractFixture(t, cwd)
	t.Chdir(cwd)
	for _, s := range jsonSurfaces(id) {
		t.Run(strings.Join(s.args, " "), func(t *testing.T) {
			out, err := captureRun(t, s.args...)
			if err != nil {
				t.Fatalf("deja %v: %v", s.args, err)
			}
			var v any
			if err := json.Unmarshal([]byte(out), &v); err != nil {
				t.Fatalf("deja %v: not JSON: %v\n%s", s.args, err, out)
			}
			section := docSection(t, doc, s.heading)
			named := section
			for _, h := range s.shared {
				named += docSection(t, doc, h)
			}
			emitted := map[string]bool{}
			collectJSONKeys(v, s.dynamic, s.data, emitted)
			for _, k := range sortedSet(emitted) {
				if !namesKey(named, k) {
					t.Errorf("deja %v prints %q, and its section never names it", s.args, k)
				}
			}
			top := map[string]bool{}
			if m, ok := v.(map[string]any); ok {
				for k := range m {
					top[k] = true
				}
			}
			for _, k := range exampleTopKeys(section) {
				if top[k] || markedOptional(section, k) {
					continue
				}
				if _, isArray := v.([]any); isArray {
					continue
				}
				t.Errorf("the section for deja %v shows %q, which it did not print, and no sentence says it is omitted", s.args, k)
			}
		})
	}
}

// collectJSONKeys gathers every object key in v, below the keys whose children
// are data rather than contract.
func collectJSONKeys(v any, dynamic, data []string, into map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			into[k] = true
			if contains(data, k) {
				continue
			}
			if contains(dynamic, k) {
				// The map's own keys are data; the shape of its values is not.
				if m, ok := c.(map[string]any); ok {
					for _, cc := range m {
						collectJSONKeys(cc, dynamic, data, into)
					}
					continue
				}
			}
			collectJSONKeys(c, dynamic, data, into)
		}
	case []any:
		for _, c := range x {
			collectJSONKeys(c, dynamic, data, into)
		}
	}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// namesKey is whether text names key as a key: quoted the way an example
// shows it, or in backticks the way the prose does.
func namesKey(text, key string) bool {
	return strings.Contains(text, `"`+key+`"`) || strings.Contains(text, "`"+key+"`") ||
		strings.Contains(text, "`"+key+".") || strings.Contains(text, "."+key+"`")
}

var jsonBlockRE = regexp.MustCompile("(?s)```json\n(.*?)```")

// exampleTopKeys is the top-level keys of the section's first JSON example
// that parses as an object.
func exampleTopKeys(section string) []string {
	for _, m := range jsonBlockRE.FindAllStringSubmatch(section, -1) {
		var obj map[string]any
		if json.Unmarshal([]byte(m[1]), &obj) != nil {
			continue
		}
		return sortedSet(func() map[string]bool {
			s := map[string]bool{}
			for k := range obj {
				s[k] = true
			}
			return s
		}())
	}
	return nil
}

var everyFieldRE = regexp.MustCompile(`(?i)\bevery (field|key)\b`)

var optionalWordRE = regexp.MustCompile(`(?i)\b(omitted|optional|absent|present only|only when|only under|only after|when (?:there is|it has|any)|appears only)\b`)

// markedOptional is whether one sentence of the section names key and says it
// can be missing. The sentence, not a window around the key: a neighbouring
// sentence's "omitted" is about something else.
func markedOptional(section, key string) bool {
	for _, sentence := range docSentences(section) {
		if !optionalWordRE.MatchString(sentence) {
			continue
		}
		// "Every field but `schema_version` is omitted when ..." covers them
		// all, except the ones it names as the exception.
		if everyFieldRE.MatchString(sentence) && !namesKey(sentence, key) {
			return true
		}
		if namesKey(sentence, key) {
			return true
		}
	}
	return false
}

// docSentences is the section's prose cut into sentences, with the examples
// taken out and the line breaks inside a sentence joined.
func docSentences(section string) []string {
	prose := jsonBlockRE.ReplaceAllString(section, " ")
	prose = regexp.MustCompile("(?s)```.*?```").ReplaceAllString(prose, " ")
	prose = strings.Join(strings.Fields(prose), " ")
	var out []string
	start := 0
	for i := 0; i < len(prose); i++ {
		switch prose[i] {
		case '.', '!', '?', ';':
			// A dot inside a backticked name (`recall.since`) does not end a
			// sentence: only one followed by a space or the end does.
			if i+1 < len(prose) && prose[i+1] != ' ' {
				continue
			}
			out = append(out, prose[start:i+1])
			start = i + 1
		}
	}
	return append(out, prose[start:])
}
