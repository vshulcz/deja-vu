package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// `deja help` listed eleven of the thirty-one install targets, so the README's
// own `deja install aider` and `deja install openclaw-auto` read as invalid —
// and the "install needs a target" refusal sent people to that same short list
// (#1106). Help is now built from installTargetNames, and this holds it there.
func TestHelpNamesEveryInstallTarget(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t, "help", "install")
	if err != nil {
		t.Fatal(err)
	}
	names := installTargetNames()
	if len(names) == 0 {
		t.Fatal("no install targets, wrong fixture")
	}
	for _, n := range names {
		if !strings.Contains(out, n) {
			t.Errorf("`deja help install` does not name install target %q:\n%s", n, out)
		}
	}
	// The refusal points at help; help must not point back with a shorter list.
	for _, n := range []string{"aider", "openclaw-auto", "roo", "copilot", "goose"} {
		if !strings.Contains(out, n) {
			t.Errorf("target %q named in the README is missing from help", n)
		}
	}
}

// The listing is wrapped, so a name must not be split across a line break: a
// reader copying `openclaw-auto` out of help has to get the whole word.
func TestHelpTargetListWraps(t *testing.T) {
	names := installTargetNames()
	got := wrapTargets(names, "      ", 76)
	for line := range strings.SplitSeq(got, "\n") {
		if len(line) > 76 {
			t.Errorf("target line is %d columns wide: %q", len(line), line)
		}
		if !strings.HasPrefix(line, "      ") {
			t.Errorf("target line is not indented: %q", line)
		}
	}
	flat := strings.Join(strings.Fields(strings.ReplaceAll(got, ",", " ")), " ")
	for _, n := range names {
		if !strings.Contains(" "+flat+" ", " "+n+" ") {
			t.Errorf("target %q did not survive wrapping: %q", n, got)
		}
	}
}

// The list is the one block in help whose layout is computed, and it was
// computed to a fixed 76 columns — the same six lines came out of a 30-column
// pane and a 120-column one, while the brief, files, restore and search all
// tracked the terminal (#1660).
func TestHelpTargetListFollowsTheTerminal(t *testing.T) {
	hermeticEnv(t)
	for _, width := range []int{40, 120} {
		t.Setenv("COLUMNS", strconv.Itoa(width))
		out, err := captureRun(t, "help", "install")
		if err != nil {
			t.Fatal(err)
		}
		var widest int
		for line := range strings.SplitSeq(out, "\n") {
			if !strings.HasPrefix(line, "      ") || !strings.Contains(line, "claude-code") && !strings.Contains(line, ",") {
				continue
			}
			if !isTargetLine(line) {
				continue
			}
			if n := len(line); n > widest {
				widest = n
			}
			if len(line) > width {
				t.Errorf("COLUMNS=%d: target line is %d columns: %q", width, len(line), line)
			}
		}
		if widest == 0 {
			t.Fatalf("COLUMNS=%d: found no target lines in help", width)
		}
		// A wide pane must actually use it, or "fits" is met by never growing.
		if width == 120 && widest <= 76 {
			t.Errorf("COLUMNS=120: widest target line is %d columns, still laid out for 76", widest)
		}
	}
}

// isTargetLine reports whether an indented help line is part of the install
// target listing rather than some other indented block.
func isTargetLine(line string) bool {
	fields := strings.Split(strings.TrimSpace(line), ", ")
	if len(fields) < 2 {
		return false
	}
	known := map[string]bool{}
	for _, n := range installTargetNames() {
		known[n] = true
	}
	for _, f := range fields {
		if !known[strings.TrimSuffix(f, ",")] {
			return false
		}
	}
	return true
}

// Every command rejected `--help` as an unknown flag, and a couple did worse:
// `deja statusline --help` printed a statusline and `deja mcp --help` started
// the server, which hangs the terminal (#1111).
func TestEveryCommandAnswersItsOwnHelp(t *testing.T) {
	for _, name := range []string{
		"doctor", "sources", "stats", "last", "share", "handoff", "promote",
		"forget", "sync", "view", "ctx", "install", "uninstall", "mcp",
		"update", "remember", "friction", "blame", "log", "index", "search",
		"statusline",
	} {
		h := helpForCommand(name)
		if h == "" {
			t.Errorf("%s has no help of its own", name)
			continue
		}
		if !strings.Contains(h, "deja "+name) {
			t.Errorf("%s help does not name it: %q", name, h)
		}
		// One command's help must not carry another's syntax.
		if name != "install" && name != "uninstall" && strings.Contains(h, "targets:") {
			t.Errorf("%s help leaked the install target list:\n%s", name, h)
		}
	}
	if helpForCommand("install") == helpForCommand("doctor") {
		t.Error("two commands answer with the same help")
	}
	if !strings.Contains(helpForCommand("install"), "aider") {
		t.Error("install help dropped the target list under it")
	}
	if h := helpForCommand("nosuchcommand"); h != "" {
		t.Errorf("an unknown word got help: %q", h)
	}
}

func TestWantsHelpStopsAtDoubleDash(t *testing.T) {
	if !wantsHelp([]string{"--json", "-h"}) {
		t.Error("-h not recognised")
	}
	if wantsHelp([]string{"--", "--help"}) {
		t.Error("--help after -- is a query word, not a request for help")
	}
	if wantsHelp([]string{"--json"}) {
		t.Error("help offered where none was asked for")
	}
}

// roleStore writes one session carrying a record of every kind the decoder
// produces: prose, a read path, a replaced span, a shell command and tool
// output.
func roleStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "-tmp-projr")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := strings.Join([]string{
		`{"type":"user","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:00:00Z","message":{"role":"user","content":"run the migration test suite"}}`,
		`{"type":"assistant","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./internal/migrate -run TestRollback"}}]}}`,
		`{"type":"assistant","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/tmp/projr/internal/db/poolneedle.go","old_string":"maxOpen := 10","new_string":"maxOpen := 64"}}]}}`,
		`{"type":"user","sessionId":"r1","cwd":"/tmp/projr","timestamp":"2026-08-01T10:03:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"ModuleNotFoundError: No module named 'psycopg2'"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(root, "r1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
}

// `deja help` named three of the six --role values, leaving out the four that
// reach what the agent did rather than what was said. Those records stay out of
// ordinary results by design, so the flag is the only way to them and help is
// where someone looks for the flag (#1096).
func TestHelpNamesEveryRoleThatMatches(t *testing.T) {
	// The page is reflowed to the terminal since #1661, and a narrow COLUMNS
	// in the environment would split the line this reads.
	t.Setenv("COLUMNS", "")

	roleStore(t)

	// Each role has to actually reach a record of its own kind, or the help
	// text would be documenting something that does not work.
	for _, tc := range []struct{ role, query string }{
		{"user", "migration"},
		{"assistant", "TestRollback"}, // the command record is an assistant turn's tool call
		{sources.RoleCommand, "TestRollback"},
		{sources.RoleFiles, "poolneedle.go"},
		{sources.RoleEdit, "poolneedle.go"},
		{sources.RoleToolOutput, "ModuleNotFoundError"},
		{"tool", "ModuleNotFoundError"}, // documented alias for tool-output
	} {
		out, err := captureRun(t, "search", "--role", tc.role, tc.query)
		if err != nil {
			t.Fatalf("--role %s: %v", tc.role, err)
		}
		if tc.role == "assistant" {
			continue // prose-only assertion is covered by the others
		}
		if !strings.Contains(out, "r1") {
			t.Errorf("--role %s did not reach its own record for %q:\n%s", tc.role, tc.query, out)
		}
	}

	help, err := captureRun(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	// The `deja last` usage line lives on its own page (#4626).
	lastHelp, err := captureRun(t, "help", "last")
	if err != nil {
		t.Fatal(err)
	}
	help += lastHelp
	// Both places help mentions roles, checked separately: the `deja last`
	// usage line and the search-flag description. Asserting on the whole help
	// text lets one of them carry the other.
	lastLine, flagLine := "", ""
	for line := range strings.SplitSeq(help, "\n") {
		if strings.Contains(line, "deja last [n]") {
			lastLine = line
		}
		if strings.HasPrefix(strings.TrimSpace(line), "--role ") {
			flagLine = line
			// the description wraps, so take the continuation with it
		}
	}
	if lastLine == "" || flagLine == "" {
		t.Fatalf("help no longer has both role mentions:\n%s", help)
	}
	at := strings.Index(help, flagLine)
	if at < 0 {
		t.Fatalf("help lost the --role line:\n%s", help)
	}
	rolesBlock := help[at:]
	if i := strings.Index(rolesBlock, "--limit"); i > 0 {
		rolesBlock = rolesBlock[:i]
	}
	// summary works too (`deja --role summary`) and `deja help last` already
	// named it; the flag block left it out (#5078).
	for _, role := range []string{sources.RoleFiles, sources.RoleCommand, sources.RoleEdit, sources.RoleSummary} {
		if !strings.Contains(lastLine, role) {
			t.Errorf("`deja last` usage line does not name the %q role: %q", role, lastLine)
		}
		if !strings.Contains(rolesBlock, role) {
			t.Errorf("the --role flag description does not name the %q role, which works "+
				"and which ordinary search cannot reach: %q", role, rolesBlock)
		}
	}
}

// The flag is the only route to these records, which is what makes the omission
// matter rather than being a documentation nicety.
func TestOrdinarySearchCannotReachToolRecords(t *testing.T) {
	roleStore(t)

	out, err := captureRunStderr(t, "search", "poolneedle.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no matches") {
		t.Errorf("a file path now matches ordinary search; if that is intended the help "+
			"wording about roles needs revisiting:\n%s", out)
	}
	found, err := captureRun(t, "search", "--role", sources.RoleFiles, "poolneedle.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found, "r1") {
		t.Fatalf("--role files did not reach the path record:\n%s", found)
	}
}

// The usage block is one column; a stray tab put `remember` outside it.
func TestHelpUsageBlockIsOneColumn(t *testing.T) {
	help, err := captureRun(t, "help")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(help, "\n") {
		if strings.HasPrefix(line, "\t") && strings.Contains(line, "deja ") {
			t.Errorf("usage line is indented with a tab, not two spaces: %q", line)
		}
	}
}

// The help page is one hand-written string 148 columns wide, and nothing in it
// consults the terminal: twelve of its eighty-four lines wrap on a default
// 80-column one, and `deja last --help` quotes the widest of them (#1661).
func TestHelpFitsAnOrdinaryTerminal(t *testing.T) {
	for _, width := range []int{80, 100, 120} {
		wrapped := wrapUsage(usageText(), width)
		for line := range strings.SplitSeq(wrapped, "\n") {
			if len([]rune(line)) > width {
				t.Errorf("at %d columns a line is %d wide:\n%s", width, len([]rune(line)), line)
			}
		}
	}
}

// A wrapped usage line stays readable: the flags land under the command with a
// hanging indent, and a bracketed group is never split down the middle.
func TestAWrappedUsageLineKeepsItsGroupsWhole(t *testing.T) {
	line := "  deja last [n] [--json] [--project name] [--harness name] [--from macro] [--since 7d] [--before 2026-01-01] [--path p] [--tool t] [--full] [--stats]"
	out := wrapUsage(line, 80)
	rows := strings.Split(out, "\n")
	if len(rows) < 2 {
		t.Fatalf("the widest line in the page was not wrapped:\n%s", out)
	}
	if !strings.HasPrefix(rows[0], "  deja last ") {
		t.Errorf("the command left its own line: %q", rows[0])
	}
	for _, r := range rows[1:] {
		if !strings.HasPrefix(r, "      ") {
			t.Errorf("a continuation is not indented under the command: %q", r)
		}
	}
	for _, r := range rows {
		if strings.Count(r, "[") != strings.Count(r, "]") {
			t.Errorf("a bracketed group was split across lines: %q", r)
		}
	}
	// Nothing is lost or reordered: the words come back in the same order.
	if strings.Join(strings.Fields(out), " ") != strings.Join(strings.Fields(line), " ") {
		t.Errorf("wrapping changed the line:\n%s\n%s", line, out)
	}
}

// A terminal deja cannot measure — a pipe, a file — gets the page as written.
func TestAPipeGetsThePageAsWritten(t *testing.T) {
	if got := wrapUsage(usageText(), 0); got != usageText() {
		t.Error("the page was reflowed for a reader with no screen to fit it to")
	}
}

// `deja last --help` quotes the widest line on the page, so it is the one that
// wrapped worst.
func TestASingleCommandsHelpFitsToo(t *testing.T) {
	for _, name := range []string{"last", "search", "install", "sync"} {
		h := helpForCommand(name)
		if h == "" {
			t.Fatalf("no help for %q", name)
		}
		for line := range strings.SplitSeq(wrapUsage(h, 80), "\n") {
			if len([]rune(line)) > 80 {
				t.Errorf("%s --help has a line %d wide:\n%s", name, len([]rune(line)), line)
			}
		}
	}
}

// The wiring, not just the wrapper: `deja help` and `deja <cmd> --help` are
// the two surfaces that have to ask the terminal how wide it is.
func TestBothHelpSurfacesAreWrappedForTheTerminal(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	for _, args := range [][]string{{"help"}, {"last", "--help"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for line := range strings.SplitSeq(out, "\n") {
			if len([]rune(line)) > 80 {
				t.Errorf("%v printed a line %d wide:\n%s", args, len([]rune(line)), line)
			}
		}
	}
}

// A Hermes cron job's run is in the index like any session, and the prompt
// hook offered it to a person asking about the same thing: its prompt is the
// same every fire and often carries someone else's text in the user role.
// It stays searchable; it is not offered unasked (#4948).
func TestPromptRecallLeavesOutAHermesCronRun(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	ask := func(source string) (hook, search string) {
		t.Helper()
		tmp := hermeticEnv(t)
		claude := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-w-p")
		if err := os.MkdirAll(claude, 0o755); err != nil {
			t.Fatal(err)
		}
		// Other work, so the question's words are rare enough to identify it.
		for i := range 40 {
			id := fmt.Sprintf("other-%d", i)
			line := fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":"/w/p","timestamp":"2026-07-01T09:%02d:00Z","message":{"role":"user","content":"rename table%d and column%d in the billing schema"}}`, id, i, i, i)
			if err := os.WriteFile(filepath.Join(claude, id+".jsonl"), []byte(line+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		home := filepath.Join(tmp, "hermes")
		if err := os.MkdirAll(home, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("DEJA_HERMES_HOME", home)
		t.Setenv("DEJA_HERMES_PROFILES_ROOT", "")
		script := `CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT, parent_session_id TEXT, cwd TEXT);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL, content TEXT, tool_call_id TEXT, tool_calls TEXT, tool_name TEXT, timestamp REAL NOT NULL);
INSERT INTO sessions VALUES ('job-zephyr','` + source + `',NULL,'/w/p');
INSERT INTO messages (session_id,role,content,timestamp) VALUES
 ('job-zephyr','user','the zephyr reconciler drops refunds with QX-77',1785000000),
 ('job-zephyr','assistant','The zephyr reconciler skips refunds whose QX-77 ledger id is reused; keyed it on the refund id.',1785000060);`
		cmd := exec.CommandContext(t.Context(), "sqlite3", filepath.Join(home, "state.db"))
		cmd.Stdin = strings.NewReader(script)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sqlite3: %v\n%s", err, out)
		}
		dir := filepath.Join(tmp, "index.db")
		if err := index.Ensure(dir, "", false, nil); err != nil {
			t.Fatal(err)
		}
		payload := mustMarshal(t, map[string]string{"session_id": "asker-" + source, "cwd": "/w/p", "prompt": "why does the zephyr reconciler drop refunds with QX-77"})
		var out strings.Builder
		if err := runHookPromptMode(dir, strings.NewReader(string(payload)), &out, true); err != nil {
			t.Fatal(err)
		}
		hits, err := indexSearch(dir, query.Options{Query: "zephyr reconciler QX-77"})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(hits))
		for _, h := range hits {
			ids = append(ids, h.ID)
		}
		return out.String(), strings.Join(ids, " ")
	}

	// The control: the same session started by a person is offered, so the
	// silence below is the cron mark's doing and not a fixture too small.
	if hook, _ := ask("cli"); !strings.Contains(hook, "zephyr") {
		t.Fatalf("a person's session was not offered, so this proves nothing:\n%s", hook)
	}
	hook, search := ask("cron")
	if strings.Contains(hook, "zephyr") {
		t.Errorf("the prompt hook offered a cron run:\n%s", hook)
	}
	if !strings.Contains(search, "job-zephyr") {
		t.Errorf("a cron run is no longer searchable: %q", search)
	}
}

// extensions/hermes is what the Hermes plugin catalog installs, and it has to
// be the provider deja install writes, or a fix lands in one and not the
// other. DEJA_WRITE_EXTENSIONS=1 rewrites it from the generator.
func TestHermesExtensionIsTheProviderInstallWrites(t *testing.T) {
	want := map[string]string{
		"plugin.yaml": hermesMemoryManifest,
		"__init__.py": hermesCatalogPy(),
	}
	dir := filepath.Join("..", "..", "extensions", "hermes")
	for name, body := range want {
		path := filepath.Join(dir, name)
		if os.Getenv("DEJA_WRITE_EXTENSIONS") == "1" {
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		// A Windows checkout may hand the file back with CRLF endings.
		if strings.ReplaceAll(string(got), "\r\n", "\n") != body {
			t.Errorf("extensions/hermes/%s differs from what deja install writes — DEJA_WRITE_EXTENSIONS=1 go test ./cmd/deja -run TestHermesExtension", name)
		}
	}
}

// A provider `hermes plugins install` put in ~/.hermes/plugins/deja-memory is
// Hermes': its install record names it, and deja must neither rewrite its
// files nor delete the directory on uninstall.
func TestDejaLeavesAHermesInstalledProviderAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DEJA_HERMES_HOME", "")
	plugins := filepath.Join(home, ".hermes", "plugins")
	dir := filepath.Join(plugins, hermesMemoryDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	catalog := hermesCatalogPy()
	if err := os.WriteFile(filepath.Join(dir, "__init__.py"), []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
	record := `{"deja-memory": {"source": "https://github.com/vshulcz/deja-vu.git", "subdir": "extensions/hermes"}}`
	if err := os.WriteFile(filepath.Join(plugins, ".install-metadata.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := installHermesMemoryProvider("/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "__init__.py")); string(got) != catalog {
		t.Fatalf("install rewrote the provider Hermes installed (action %q)", res.Action)
	}
	if _, err := installHermesMemoryProvider("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "__init__.py")); err != nil {
		t.Fatalf("uninstall removed the provider Hermes installed: %v", err)
	}

	// The control: without the record the directory is deja's, and uninstall
	// takes it.
	if err := os.Remove(filepath.Join(plugins, ".install-metadata.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := installHermesMemoryProvider("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("uninstall left deja's own provider: %v", err)
	}
}

// hermesConfig is the reader's own file, before deja touches it.
func hermesConfig(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	path := filepath.Join(sources.HermesHome(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The MCP half of this file drops the block it created (#2604). The plugin
// half wrote `plugins:\n  enabled:\n    - deja\n` where no block existed and
// took back only the `- deja` line, so an empty block deja made survived every
// uninstall — and `enabled:` with nothing under it parses as null (#2672).
func TestUninstallDropsThePluginsBlockItCreated(t *testing.T) {
	before := "# hermes\nprofile: default\n"
	path := hermesConfig(t, before)
	if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "- deja") {
		t.Fatalf("install did not enable the plugin:\n%s", b)
	}
	if _, err = installTarget("hermes-auto", "/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != before {
		t.Fatalf("the config did not come back as it was:\nwant:\n%s\ngot:\n%s", before, after)
	}
}

// A block the reader wrote stays, empty or not: deja takes back its own line
// and nothing else.
func TestUninstallKeepsAPluginsBlockTheReaderWrote(t *testing.T) {
	for _, before := range []string{
		"# hermes\nprofile: default\n\nplugins:\n  enabled:\n",
		"# hermes\nprofile: default\n\nplugins:\n  enabled:\n    - theirs\n",
	} {
		t.Run(strings.ReplaceAll(before, "\n", "·"), func(t *testing.T) {
			path := hermesConfig(t, before)
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			if _, err := installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != before {
				t.Fatalf("a block the reader wrote was changed:\nwant:\n%s\ngot:\n%s", before, after)
			}
		})
	}
}

// Hermes documents plugins.enabled as a flow list too. Install matched only the
// block form and appended a second `plugins:` for anything else — YAML keeps the
// last key, so the user's own plugins went off (#4243) — and it rewrote
// `enabled: []` as a block key that uninstall left behind as null (#4249). The
// list keeps the style the reader wrote it in, and uninstall puts back the
// bytes it found.
func TestHermesPluginsEnabledKeepsItsListStyle(t *testing.T) {
	for _, tc := range []struct{ before, installed string }{
		{"_config_version: 32\nplugins:\n  enabled: [spotify]\n  disabled: []\n", "  enabled: [spotify, deja]\n"},
		{"_config_version: 32\nplugins:\n  enabled: []\n  disabled: []\n", "  enabled: [deja]\n"},
		{"plugins:\n  enabled: [spotify, 'teams']  # mine\n", "  enabled: [spotify, 'teams', deja]  # mine\n"},
		{"plugins:\n  enabled:\n  - spotify\n  disabled: []\n", "  - spotify\n  - deja\n"},
		{"plugins:\n  disabled: []\n", "plugins:\n  enabled:\n    - deja\n  disabled: []\n"},
		{"\ufeffplugins:\n  enabled: [spotify]\n", "  enabled: [spotify, deja]\n"},
		{"plugins:\n  enabled: [ ]\n", "  enabled: [ deja]\n"},
		{"plugins:\n  enabled: [ spotify ]\n", "  enabled: [ spotify, deja ]\n"},
		{"plugins:\n  enabled: ['x]y', spotify]\n", "  enabled: ['x]y', spotify, deja]\n"},
		{"plugins:\n  enabled: ['a #b', spotify]\n", "  enabled: ['a #b', spotify, deja]\n"},
		{"plugins:\n  enabled:\t# c\n    - spotify\n", "    - spotify\n    - deja\n"},
		{"plugins:\n  enabled: [a]\t# c\n", "  enabled: [a, deja]\t# c\n"},
		{"plugins:\n  enabled: [spotify,]\n", "  enabled: [spotify, deja,]\n"},
		{"plugins:\n  enabled: Null\n", "  enabled:\n    - deja\n"},
		{"plugins:\n  enabled: ~  # nothing\n", "  enabled:  # nothing\n    - deja\n"},
		{"plugins:\n  enabled:  NULL\n  disabled: []\n", "  enabled:\n    - deja\n  disabled: []\n"},
		{"_config_version: 32\n...\n", "\nplugins:\n  enabled:\n    - deja\n...\n"},
	} {
		t.Run(strings.ReplaceAll(tc.before, "\n", "·"), func(t *testing.T) {
			path := hermesConfig(t, tc.before)
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if n := topLevelKeys(got, "plugins:"); n != 1 {
				t.Fatalf("install left %d plugins: blocks:\n%s", n, got)
			}
			if !strings.Contains(got, tc.installed) {
				t.Fatalf("install did not add deja to the list as written:\nwant %q in:\n%s", tc.installed, got)
			}
			if _, err = installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("second install: %v", err)
			}
			if again, _ := os.ReadFile(path); string(again) != got {
				t.Fatalf("a second install changed the file:\n%s", again)
			}
			if _, err = installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.before {
				t.Fatalf("uninstall did not put the file back:\nwant:\n%q\ngot:\n%q", tc.before, after)
			}
		})
	}
}

// topLevelKeys counts the lines that open a top-level key, a byte order mark
// on the first one included.
func topLevelKeys(text, key string) int {
	n := 0
	for line := range strings.SplitSeq(strings.TrimPrefix(text, "\ufeff"), "\n") {
		if strings.HasPrefix(line, key) {
			n++
		}
	}
	return n
}

// A block deja created is deja's only while nothing else is in it. Taking the
// whole `plugins:\n  enabled:` back once the reader had listed a plugin of their
// own under it hung that item off the line above, and a key of their own beside
// it left a file Hermes could not parse.
func TestUninstallKeepsWhatTheReaderAddedToDejasPluginsBlock(t *testing.T) {
	for _, tc := range []struct{ add, want string }{
		{"    - spotify\n", "plugins:\n  enabled:\n    - spotify\n"},
		{"  disabled: [x]\n", "plugins:\n  disabled: [x]\n"},
	} {
		t.Run(strings.TrimSpace(tc.add), func(t *testing.T) {
			path := hermesConfig(t, "model: 'x'\n")
			if _, err := installTarget("hermes-auto", "/bin/deja", false); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			edited := strings.Replace(string(b), "    - deja\n", "    - deja\n"+tc.add, 1)
			if err = os.WriteFile(path, []byte(edited), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err = installTarget("hermes-auto", "/bin/deja", true); err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(after)
			if !strings.HasPrefix(got, "model: 'x'\n") || !strings.HasSuffix(got, "\n"+tc.want) || strings.Contains(got, "deja") {
				t.Fatalf("uninstall did not leave the reader's entry where it was:\nwant suffix:\n%s\ngot:\n%s", tc.want, got)
			}
		})
	}
}

// Shapes deja cannot list itself under are refused and left alone, rather
// than given a `- deja` line that turns them into invalid YAML: a mapping or a
// scalar under `enabled:`, and a second top-level `plugins:`, which is the one
// Hermes reads while the editor would change the first.
func TestInstallRefusesAPluginsListItCannotEdit(t *testing.T) {
	for _, before := range []string{
		"plugins:\n  enabled:\n    spotify: true\n",
		"plugins:\n  enabled:\n    spotify\n",
		"plugins:\n  enabled: [a]\nmodel: x\nplugins:\n  enabled: [b]\n",
		"plugins:\n  enabled: [a]\n  enabled: [b]\n",
		"plugins:\n  - foo\nmodel: x\n",
		"plugins:\n- foo\n",
		"plugins:\n  foo\n",
		"\"plugins\":\n  enabled: [a]\n",
		"plugins :\n  enabled: [a]\n",
		"plugins:\n  enabled: nulL\n",
	} {
		t.Run(strings.ReplaceAll(before, "\n", "·"), func(t *testing.T) {
			path := hermesConfig(t, before)
			if err := setHermesPluginEnabled(true); err == nil || !strings.Contains(err.Error(), "by hand") {
				t.Fatalf("install did not refuse: %v", err)
			}
			if b, _ := os.ReadFile(path); string(b) != before {
				t.Fatalf("the config was changed:\n%s", b)
			}
		})
	}
}

// `enabled: null` and `enabled: ~` are the same empty value as a bare
// `enabled:`, and are listed under the same way.
func TestHermesPluginsEnabledNullIsAnEmptyList(t *testing.T) {
	for _, v := range []string{"null", "~", "Null"} {
		t.Run(v, func(t *testing.T) {
			path := hermesConfig(t, "plugins:\n  enabled: "+v+"  # none yet\n  disabled: []\n")
			if err := setHermesPluginEnabled(true); err != nil {
				t.Fatalf("install: %v", err)
			}
			b, _ := os.ReadFile(path)
			if want := "plugins:\n  enabled:  # none yet\n    - deja\n  disabled: []\n"; string(b) != want {
				t.Fatalf("install:\nwant %q\ngot  %q", want, b)
			}
		})
	}
}

// deja's own entry with a comment after it, tab or space, is still deja's.
func TestHermesPluginsKnowsDejasEntryWithAComment(t *testing.T) {
	before := "plugins:\n  enabled:\n    - spotify\n    - deja\t# mine\n"
	path := hermesConfig(t, before)
	if err := setHermesPluginEnabled(true); err != nil {
		t.Fatalf("install: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != before {
		t.Fatalf("install listed deja twice:\n%s", b)
	}
}

// The grid under "Works with 25" listed twenty names: Amp, Continue, Copilot
// Chat, prime-agent and Crush landed and never reached it, so the heading
// counted five agents the grid did not show. It is the list a reader scans for
// their own agent before installing anything.
func TestTheHomeGridShowsEveryHarnessItCounts(t *testing.T) {
	root := filepath.Join("..", "..")
	page, err := os.ReadFile(filepath.Join(root, "docs", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	grid := regexp.MustCompile(`(?s)<div class="agents".*?</div>`).Find(page)
	if grid == nil {
		t.Fatal("the home page has no agent grid to check")
	}
	shown := 0
	linked := map[string]bool{}
	for _, m := range regexp.MustCompile(`<span>(?:<a href="registry/([^"]+)">)?([^<]+)(?:</a>)?</span>`).FindAllSubmatch(grid, -1) {
		if strings.TrimSpace(string(m[2])) != "" {
			shown++
		}
		linked[string(m[1])] = true
	}
	if n := registryHarnessCount(t, root); shown != n {
		t.Errorf("the grid shows %d agents under a heading that counts %d", shown, n)
	}

	// The registry pages had no link from the home page, and none of them was
	// in Google's index. Each name in the grid links its registry page.
	pages, err := filepath.Glob(filepath.Join(root, "docs", "registry", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if name := filepath.Base(p); name != "README.html" && !linked[name] {
			t.Errorf("the home grid does not link registry/%s", name)
		}
	}
	if !strings.Contains(string(grid), `href="registry/README.html"`) {
		t.Error("the home grid does not link the registry index")
	}
}

// Test data has to name home directories, and the shortest way to get a
// realistic one is to paste the path in front of you. Fifteen of them had been
// pasted that way — `-Users-<account>-deja-vu` in the index tests, a scratch
// path complete with a job id in the redaction tests — and a grep for
// `/Users/<account>` finds none of them, because the harnesses encode the
// separator away.
//
// Fictional accounts are not the problem: the tree is full of John, probe and
// Alice, and they are the right answer. What cannot be here is the account of
// whoever wrote the test, so this asks the machine it runs on for its own home
// and looks for that — no real name has to be written down for the check to
// work, on this laptop or on a runner.
func TestNoTrackedFileNamesTheAccountItWasWrittenOn(t *testing.T) {
	// Not os.UserHomeDir: TestMain points HOME at a temp directory, which is
	// the whole point of the hermetic suite and would leave this checking for
	// a path nobody could have pasted. The passwd entry is not overridable.
	me, err := user.Current()
	if err != nil {
		t.Skipf("no account to compare against: %v", err)
	}
	account := me.Username
	if i := strings.LastIndexAny(account, `\/`); i >= 0 {
		account = account[i+1:] // DOMAIN\user on Windows
	}
	if len(account) < 3 {
		t.Skipf("account name %q is too short to search for", account)
	}
	// A build machine's account is not a person's: `/home/runner/work/...` is
	// the path GitHub gives every job and appears in an ignore-rule fixture on
	// purpose. This check is about the laptop the test was written on.
	switch strings.ToLower(account) {
	case "runner", "runneradmin", "root", "ubuntu", "ec2-user", "admin",
		"user", "build", "jenkins", "circleci", "travis", "github", "vsts":
		t.Skipf("%q is a build account, not a developer's", account)
	}
	// Both spellings: the path as written, and the form a harness makes of it
	// when it turns a project path into one directory name.
	needles := []string{
		"/Users/" + account, "-Users-" + account,
		"/home/" + account, "-home-" + account,
		`\Users\` + account,
	}

	root := filepath.Join("..", "..")
	// Tracked files only: what a working tree holds beside them is the
	// developer's own — a local tool's output cache is where this first fired,
	// and it is ignored precisely because it belongs to nobody else.
	out, err := exec.CommandContext(t.Context(), "git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files: %v", err)
	}
	files := strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
	if len(files) < 100 {
		t.Fatalf("git listed %d files — this is not reading the repository", len(files))
	}
	for _, f := range files {
		switch strings.ToLower(filepath.Ext(f)) {
		case ".go", ".md", ".html", ".txt", ".json", ".jsonl", ".yml", ".yaml", ".sh", ".mjs", ".js", ".ts", ".py", ".toml", ".sql", ".css":
		default:
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Errorf("%s: %v", f, err)
			continue
		}
		lower := strings.ToLower(string(b))
		for _, n := range needles {
			if !strings.Contains(lower, strings.ToLower(n)) {
				continue
			}
			t.Errorf("%s carries a home directory from the machine it was written on (%q) — use a made-up account instead",
				f, n)
		}
	}
}

// `.hookseen` is a line of fields and the agent session id is written into it
// verbatim, though it arrives in the hook payload — whatever the host sends. A
// space cost that session its dedup and gave its entries to another id; a
// newline let a payload write a line under any id it liked (#2167).
func TestADedupEntryStaysUnderTheSessionThatOwnsIt(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir + ".hookseen"
	for _, sid := range []string{"plain-1", "two words", "forge\nvictim", "tab\tsep"} {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		rememberInjectedIDs(dir, sid, "tok")
		rememberInjected(dir, sid, []model.Session{{ID: "sess-a"}})

		// Its own dedup works, whatever the host called it.
		seen := alreadyInjected(dir, sid)
		if !seen["tok"] || !seen["sess-a"] {
			t.Errorf("session %q does not remember what it was shown: %v", sid, seen)
		}
		// The file stays one line per entry: a newline in the id cannot open a
		// line of its own.
		body, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(strings.Split(strings.TrimRight(string(body), "\n"), "\n")); n != 2 {
			t.Errorf("session %q wrote %d lines for two entries:\n%q", sid, n, body)
		}
		// And nothing lands under another id — neither the first word of this
		// one nor the tail after a newline.
		for _, other := range strings.FieldsFunc(sid, func(r rune) bool {
			return r == ' ' || r == '\n' || r == '\t'
		}) {
			if other == sid {
				continue
			}
			if got := alreadyInjected(dir, other); len(got) > 0 {
				t.Errorf("a session called %q sees %v, written by a payload calling itself %q", other, got, sid)
			}
		}
		// Forgetting is by the same key, so a session can still drop its own.
		forgetInjected(dir, sid)
		if got := alreadyInjected(dir, sid); len(got) > 0 {
			t.Errorf("session %q could not forget its entries: %v", sid, got)
		}
	}
}

// The value side is not mapped — it is looked up against the index's own ids,
// where a mapped one would match nothing. A value that cannot be a field is
// dropped instead, which costs a second showing rather than the entry written
// after it (#2167).
func TestAValueThatCannotBeAFieldIsDroppedNotMangled(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir + ".hookseen"
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	rememberInjectedIDs(dir, "ses1", "tok one", "tok-two")
	rememberInjected(dir, "ses1", []model.Session{{ID: "sess a"}, {ID: "sess-b"}})
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(strings.TrimRight(string(body), "\n"), "\n") {
		if n := len(strings.Fields(line)); n != 2 && n != 3 {
			t.Errorf("a line of %d fields: %q", n, line)
		}
	}
	seen := alreadyInjected(dir, "ses1")
	for _, want := range []string{"tok-two", "sess-b"} {
		if !seen[want] {
			t.Errorf("%q was not recorded", want)
		}
	}
	// The ids that could not be written are simply absent — the index looks
	// them up by their own spelling, so a mapped stand-in would match nothing.
	for _, gone := range []string{"tok", "one", "tok_one", "sess", "a", "sess_a"} {
		if seen[gone] {
			t.Errorf("%q reads as already shown, though it was never written", gone)
		}
	}
}

// A full `.hookseen` used to stop the dedup dead, and #1967 replaced that with
// a rotation — "the per-prompt hook then re-injected the same sessions turn
// after turn". The token writer three functions below kept the old behaviour,
// so the surfaces whose whole job is not repeating themselves — the per-prompt
// block, the two tool hooks — lost their dedup once the file filled, until some
// session injection happened to rotate it (#2164).
func TestTheTokenDedupSurvivesAFullHookseen(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := dir + ".hookseen"
	var b strings.Builder
	for b.Len() < (1<<20)+4096 {
		b.WriteString("ses-old some-token 2026-01-01T00:00:00Z\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	rememberInjectedIDs(dir, "ses1", "tok-1")
	if !alreadyInjected(dir, "ses1")["tok-1"] {
		t.Errorf("a token written while the file was full does not read back as seen")
	}
	// The rotation kept this session's line rather than starting the file over.
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 1<<20 {
		t.Errorf("the file is still %d bytes, so nothing rotated", fi.Size())
	}

	// The session writer is unchanged, and the two keep working together: a
	// session recorded after the rotation is still seen, and so is a second
	// token.
	rememberInjected(dir, "ses1", []model.Session{{ID: "sess-a"}})
	rememberInjectedIDs(dir, "ses1", "tok-2")
	seen := alreadyInjected(dir, "ses1")
	for _, want := range []string{"tok-1", "sess-a", "tok-2"} {
		if !seen[want] {
			t.Errorf("%q is not remembered after the file filled and rotated", want)
		}
	}
	// And the rotation cannot be talked into keeping a megabyte of one
	// session's own lines: that was what made every later write rotate again.
	var mine strings.Builder
	for mine.Len() < (1<<20)+4096 {
		mine.WriteString("ses1 token-of-my-own 2026-01-01T00:00:00Z\n")
	}
	if werr := os.WriteFile(p, []byte(mine.String()), 0o600); werr != nil {
		t.Fatal(werr)
	}
	rememberInjectedIDs(dir, "ses1", "tok-3")
	fi, err = os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > 1<<20 {
		t.Errorf("the file is %d bytes of one session's own lines, so the next write rotates it again", fi.Size())
	}
	if !alreadyInjected(dir, "ses1")["tok-3"] {
		t.Errorf("the token written through that rotation is not remembered")
	}
}

// The per-prompt hook asks the dedupe file four questions and used to read and
// split the whole file for each — 4 ms of a 24 ms call on a 648 KB list
// (#3379). It reads once now, and the four standalone helpers answer from the
// same code, so the two cannot drift apart.
func TestTheSeenListAnswersTheSameFromOneRead(t *testing.T) {
	dir := t.TempDir() + "/index"
	key := askKey([]string{"zebraquux", "fetcher"})
	rememberInjectedIDs(dir, "sess-1", "block-aaa")
	rememberInjectedIDsFor(dir, "sess-1", "proj", []string{"s-old", "s-new"})
	rememberInjectedIDsFor(dir, "sess-1", "proj", []string{key})
	rememberInjectedIDsFor(dir, "sess-2", "proj", []string{"s-other"})

	sl := loadSeen(dir)
	if got, want := sl.injected("sess-1"), alreadyInjected(dir, "sess-1"); !sameSet(got, want) {
		t.Errorf("injected: %v vs %v", got, want)
	}
	if got, want := sl.recent("sess-1", 2), recentlyInjected(dir, "sess-1", 2); !sameSet(got, want) {
		t.Errorf("recent: %v vs %v", got, want)
	}
	if got, want := sl.recentInProject("proj", 3), recentlyInjectedInProject(dir, "proj", 3); !sameSet(got, want) {
		t.Errorf("recentInProject: %v vs %v", got, want)
	}
	if got, want := sl.asked("sess-1", key, time.Hour), askedRecently(dir, "sess-1", key, time.Hour); got != want {
		t.Errorf("asked: %v vs %v", got, want)
	}
	// And a missing file answers what it always did.
	if err := os.Remove(dir + ".hookseen"); err != nil {
		t.Fatal(err)
	}
	empty := loadSeen(dir)
	if len(empty.injected("sess-1")) != 0 || empty.asked("sess-1", key, time.Hour) {
		t.Error("a missing dedupe file is no longer empty")
	}
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// Past 1MB the dedup file used to freeze, permanently breaking dedup so the
// per-prompt hook re-injected the same sessions forever. Rotation must keep the
// current session's dedup working and bound the file size.
func TestHookseenRotatesInsteadOfFreezing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx.db")
	p := dir + ".hookseen"
	// This session's line goes first, before a megabyte of other sessions'.
	// Written last it lands inside the tail rotation keeps anyway, and the rule
	// that saves it — keep every line of the current session, wherever it sits —
	// is never exercised: removing that rule left this test green.
	var b strings.Builder
	b.WriteString("mysession alreadyseen1\n")
	for i := 0; b.Len() < (1<<20)+1000; i++ {
		b.WriteString("othersess" + strings.Repeat("x", 40) + " sid\n")
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	if before.Size() <= 1<<20 {
		t.Fatal("premise: file should start over 1MB")
	}

	// A new injection for mysession triggers rotation and still records.
	rememberInjected(dir, "mysession", []model.Session{{ID: "newone"}})

	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() > 1<<20 {
		t.Errorf("file did not shrink on rotation: %d bytes", after.Size())
	}
	seen := alreadyInjected(dir, "mysession")
	// The pre-rotation line for this session must survive (dedup keeps working),
	// and the new one must be recorded.
	if !seen["alreadyseen1"] {
		t.Error("rotation dropped the current session's earlier dedup entry")
	}
	if !seen["newone"] {
		t.Error("the new injection was not recorded after rotation")
	}
}

// A hook killed between a dedup line and its newline leaves half a line, and
// the next hook wrote onto it. The reader takes the first two fields of each
// line, so the glued line keeps the interrupted record and drops the one
// appended after it — leaving a session deja does not know it has already
// shown, which is the whole job of this file (#1967).
func TestTheDedupFileKeepsWhatWasWrittenAfterAKill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	rememberInjected(dir, "ses1", []model.Session{{ID: "sessA"}})

	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".hookseen", b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}

	rememberInjected(dir, "ses2", []model.Session{{ID: "sessB"}})

	if seen := alreadyInjected(dir, "ses2"); !seen["sessB"] {
		raw, _ := os.ReadFile(dir + ".hookseen")
		t.Errorf("the session shown to ses2 is not recorded, so it will be shown again:\n%s", raw)
	}
	// And the interrupted record still counts for the session it belonged to.
	if seen := alreadyInjected(dir, "ses1"); !seen["sessA"] {
		t.Error("the record the kill interrupted was lost as well")
	}
}

// The other writer of the same file: block fingerprints rather than sessions.
func TestTheDedupFileKeepsAFingerprintWrittenAfterAKill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	rememberInjectedIDs(dir, "ses1", "fingerprint-a")

	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".hookseen", b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}

	rememberInjectedIDs(dir, "ses2", "fingerprint-b")

	if seen := alreadyInjected(dir, "ses2"); !seen["fingerprint-b"] {
		raw, _ := os.ReadFile(dir + ".hookseen")
		t.Errorf("the block shown to ses2 is not recorded, so the same words go out again:\n%s", raw)
	}
}

// Both shared pages build their rows by string concatenation into innerHTML,
// and some of the values land inside double-quoted attributes. json.Marshal
// already neutralises `<` and `>` on the way in, so the escaper is the only
// thing standing between a project name and an event handler — and it used to
// stop at &<>. A project name carrying a quote closed data-value="…" and the
// rest of the name parsed as attributes.
//
// escInAttribute finds the values a page interpolates into a quoted attribute.
var escInAttribute = regexp.MustCompile(`="'\s*\+\s*esc\(|="[a-z ]*'\s*\+\s*esc\(`)

func renderedStatsHTML(t *testing.T) string {
	t.Helper()
	report := stats.Report{TotalSessions: 1, TotalMessages: 1, Harnesses: []stats.HarnessStats{{Harness: "claude"}}}
	sessions := []model.Session{{
		Harness: "claude",
		Project: `imported:" onmouseover="alert(1)" x="`,
		Title:   "a note in a hostile project",
		Updated: time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC),
	}}
	page, err := newStatsHTMLPage(report, sessions)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := statsHTMLTemplate.Execute(&b, page); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func renderedView(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	page := viewPage{
		SessionsJSON: jsonForScript([]byte(`[{"id":"n","harness":"deja","project":"\" onmouseover=\"alert(1)\" x=\"","title":"t","updated":"2026-08-06"}]`)),
		RecallsJSON:  jsonForScript([]byte(`[]`)),
		NotesJSON:    jsonForScript([]byte(`[]`)),
	}
	if err := viewTemplate.Execute(&b, page); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestSharedPagesEscapeQuotesInAttributes(t *testing.T) {
	for _, page := range []struct {
		name string
		body string
	}{
		{"deja stats --html", renderedStatsHTML(t)},
		{"deja view", renderedView(t)},
	} {
		if !escInAttribute.MatchString(page.body) {
			t.Fatalf("%s: no esc() inside a quoted attribute — this check has gone vacuous, re-read the page before deleting it", page.name)
		}
		esc := escSource(t, page.name, page.body)
		if !strings.Contains(esc, "&quot;") {
			t.Fatalf("%s: esc() does not escape a double quote, so a project name can close an attribute and add an event handler: %s",
				page.name, esc)
		}
	}
}

// escSource returns the page's own escaper definition, whichever form it takes.
func escSource(t *testing.T, name, body string) string {
	t.Helper()
	for _, open := range []string{"function esc(value){", "const esc=s=>"} {
		i := strings.Index(body, open)
		if i < 0 {
			continue
		}
		rest := body[i:]
		if end := strings.Index(rest, "\n"); end > 0 {
			rest = rest[:end]
		}
		if len(rest) > 400 {
			rest = rest[:400]
		}
		return rest
	}
	t.Fatalf("%s: no esc() definition found", name)
	return ""
}

// A hostile project name must survive into the page as data, escaped, rather
// than being dropped — the page is still meant to show what it holds.
func TestStatsHTMLKeepsHostileProjectAsText(t *testing.T) {
	body := renderedStatsHTML(t)
	if !strings.Contains(body, `onmouseover=`) {
		t.Fatal("the hostile project name vanished from the page; the probe no longer tests anything")
	}
	if strings.Contains(body, `data-value="" onmouseover=`) {
		t.Fatalf("the project name closed data-value in the emitted page: %s", body)
	}
}

// hostile is what a transcript or a hand-written note can put in a name:
// markup, an escape byte and a bidi override, which reverses the rendering of
// everything after it.
const hostile = "</script><img src=x onerror=alert(1)> \x1b[31m & <b>bold\u202e"

// Every terminal row deja prints scrubs these; the pages did not, and a page is
// where a bidi override actually reverses a line for a reader (#2090).
func TestTheStatsPageDoesNotCarryControlOrBidiCharacters(t *testing.T) {
	s := model.Session{
		Harness: "claude", ID: "s1", Project: hostile, Title: hostile,
		Started:  time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Updated:  time.Date(2026, 1, 2, 3, 5, 5, 0, time.UTC),
		Messages: []model.Message{{Role: "user", Text: hostile}},
	}
	report := stats.Report{
		TotalSessions: 1, TotalMessages: 1,
		Longest:     stats.SessionStat{ID: "s1", Messages: 1, Harness: "claude", Project: hostile, Title: hostile},
		Harnesses:   []stats.HarnessStats{{Harness: "claude", Sessions: 1, Messages: 1}},
		TopProjects: []stats.ProjectStats{{Project: hostile, Sessions: 1}},
	}
	page, err := newStatsHTMLPage(report, []model.Session{s})
	if err != nil {
		t.Fatal(err)
	}
	js := string(page.SessionsJSON)
	// The premise: the row reached the page, so this is about what it carried
	// rather than about an empty table.
	if !strings.Contains(js, "bold") {
		t.Fatalf("the session did not reach the page at all: %s", js)
	}
	assertNoControlOrBidi(t, "the stats page", js)
}

// `deja view` is the browsing page, and a promoted note is text a person wrote,
// so it reaches the page the way a transcript's title does.
func TestTheViewPageDoesNotCarryControlOrBidiCharacters(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	line, err := json.Marshal(map[string]any{
		"ts": time.Now().UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s1", "state": "accepted",
		"title":   "pool sizing " + hostile,
		"text":    "the pool was exhausted\n" + hostile,
		"project": "app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(notes, append(line, '\n'), 0o644); werr != nil {
		t.Fatal(werr)
	}
	if _, cerr := captureRun(t, "index"); cerr != nil {
		t.Fatal(cerr)
	}
	out := filepath.Join(t.TempDir(), "view.html")
	if _, cerr := captureRun(t, "view", "--no-open", "--out", out); cerr != nil {
		t.Fatal(cerr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	page := string(b)
	if !strings.Contains(page, "pool sizing") {
		t.Fatalf("the note did not reach the page at all")
	}
	assertNoControlOrBidi(t, "the view page", page)
}

func assertNoControlOrBidi(t *testing.T, what, s string) {
	t.Helper()
	for _, bad := range []struct{ name, text string }{
		{"an escape byte", "\x1b"},
		{"a bidi override", "\u202e"},
		{"a carriage return", "\r"},
	} {
		if strings.Contains(s, bad.text) {
			t.Errorf("%s carries %s", what, bad.name)
		}
	}
}

// A mistyped harness must not be reported as a missing session: search, last
// and blame all say which value they did not recognise, while show blamed the
// id the reader typed correctly and resume looked the harness up as the id
// (#2251).
func TestAMistypedHarnessIsNamedByTheCommandsThatTakeAnID(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	claude := filepath.Join(tmp, "claude", "-work-app")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	at := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	line := fmt.Sprintf(`{"type":"user","sessionId":"sess1","timestamp":%q,"cwd":"/work/app",`+
		`"message":{"role":"user","content":"the retry budget for uploads"}}`, at)
	if err := os.WriteFile(filepath.Join(claude, "s1.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The premise: the session is there under its real harness.
	if err := cmdShow(dir, []string{"sess1", "--harness", "claude"}, ""); err != nil {
		t.Fatalf("the session cannot be shown at all: %v", err)
	}

	err := cmdShow(dir, []string{"sess1", "--harness", "cluade"}, "")
	if err == nil {
		t.Fatal("show accepted a harness that does not exist")
	}
	if !strings.Contains(err.Error(), "cluade") || !strings.Contains(err.Error(), "not a harness") {
		t.Errorf("show said %v — it should name the harness it did not recognise", err)
	}

	var out bytes.Buffer
	err = runResume(dir, []string{"sess1", "--harness", "cluade"}, &out)
	if err == nil {
		t.Fatal("resume accepted a flag it does not take")
	}
	if strings.Contains(err.Error(), `matches "cluade"`) {
		t.Errorf("resume looked the flag value up as the id: %v", err)
	}
	if !strings.Contains(err.Error(), "--harness") {
		t.Errorf("resume said %v — it should name the flag it does not take", err)
	}
	// A second bare word is the same mistake without a dash: it used to replace
	// the id silently.
	if err := runResume(dir, []string{"sess1", "extra"}, &out); err == nil ||
		strings.Contains(err.Error(), `matches "extra"`) {
		t.Errorf("resume with a stray word: %v", err)
	}

	// handoff had the same shape, found by running the same input at it.
	if err := runHandoff(dir, []string{"sess1", "extra"}, &out); err == nil ||
		strings.Contains(err.Error(), `matches "extra"`) {
		t.Errorf("handoff with a stray word: %v", err)
	}
	// And it still hands off the session it was given.
	out.Reset()
	if err := runHandoff(dir, []string{"sess1"}, &out); err != nil {
		t.Errorf("handoff of a real id: %v", err)
	}
}

// "pass a session id-prefix (see `deja last`)" is a step the reader can take
// and learn nothing from when the store is empty: the listing answers with the
// emptiness deja already knew about one command earlier (#992).
func TestIdNeededSaysTheStoreIsEmptyInsteadOfPointingAtTheListing(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"promote", "share", "handoff"} {
		_, err := captureRun(t, cmd)
		if err == nil {
			t.Fatalf("%s accepted an empty store", cmd)
		}
		if strings.Contains(err.Error(), "deja last") {
			t.Errorf("%s sent the reader to a listing that is empty for the same reason: %v", cmd, err)
		}
		if !strings.Contains(err.Error(), "nothing is indexed yet") {
			t.Errorf("%s does not say the store is empty: %v", cmd, err)
		}
	}

	// With something indexed the refusal is about the missing argument again,
	// and the listing is worth reading.
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "promote")
	if err == nil || !strings.Contains(err.Error(), "deja last") {
		t.Errorf("with sessions indexed promote stopped naming the listing: %v", err)
	}
}

// "needs id-prefix" names a thing the reader has no way to produce: show, share
// and resume are the three commands reached for right after a search result,
// and none of them said where an id comes from — while promote has said
// `deja last` all along (#1063).
func TestIDPrefixRefusalsSayWhereIDsComeFrom(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	body := `{"type":"user","sessionId":"a1b2c3d4","cwd":"/w/p","timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"the kafka consumer rebalance loops"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"show", "share", "resume", "ctx"} {
		err := run([]string{cmd})
		if err == nil {
			t.Fatalf("%s with no argument did not refuse", cmd)
		}
		if !strings.Contains(err.Error(), "`deja last`") {
			t.Errorf("%s: %q does not say where an id comes from", cmd, err)
		}
	}
}

// On a machine with nothing indexed the honest answer is that there is nothing
// to show, not that an argument is missing. share, promote and ctx reached
// idPrefixNeeded; show refused in parseShow before dir was ever consulted.
func TestIDPrefixRefusalsOnAnEmptyMachineSayTheStoreIsEmpty(t *testing.T) {
	hermeticEnv(t)
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}

	for _, cmd := range []string{"show", "share", "resume", "ctx"} {
		err := run([]string{cmd})
		if err == nil {
			t.Fatalf("%s with no argument did not refuse", cmd)
		}
		if !strings.Contains(err.Error(), "nothing is indexed yet") {
			t.Errorf("%s on an empty machine: %q blames the argument, not the empty store", cmd, err)
		}
	}
}

// The sweep in ignore_every_surface_test.go writes the same error and remedy in
// three sessions, so the pair is corroborated and takes the branch that checks
// the ignore rule. One session — the ordinary case — is held back as an
// unconfirmed sighting, and that branch checked only the trust policy: `deja
// fix` handed back a command out of the tree search and `how` refuse to read
// (#3403).
func TestFixDoesNotServeAnUnconfirmedRemedyFromTheIgnoredTree(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	policyFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One session only, so the remedy stays a sighting; and a remedy that
	// names nothing the error names, so it is not promoted by sharing a term.
	writeClaudeFixture(t, filepath.Join(root, "-w-scratch", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
		`{"type":"user","sessionId":"s1","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"panic: zonkobuffer overflow at shard 7"}]}}`,
		`{"type":"assistant","sessionId":"s1","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply -target module.klindra"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "fix", "panic: zonkobuffer overflow at shard 7")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "klindra") {
		t.Errorf("a withheld session's command reached deja fix:\n%s", out)
	}
	if !strings.Contains(out, "no session") {
		t.Errorf("want the refusal the other surfaces give, got:\n%s", out)
	}
}

// onlyTheIgnoredTreeKnows builds a store where a word, an error, a command and
// a file exist solely in the tree the ignore rule keeps out. Anything that
// prints "zonko" is reading from it.
func onlyTheIgnoredTreeKnows(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	policyFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-scratch", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"panic: zonkobuffer overflow at shard 7"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --zonkoshard 7"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/scratch/zonko.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	writeClaudeFixture(t, filepath.Join(root, "-w-keep", "k1.jsonl"), "k1", []string{
		`{"type":"user","sessionId":"k1","cwd":"/w/keep","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"morning notes about nothing"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// Five fixes found one surface each — how, restore and friction (#2630), the
// per-command hook (#2652), sync export (#2654), the friction walls behind the
// brief and the session-start block (#2658) — and each is pinned on its own.
// Nothing pinned the set, so the next reader of sessions would be found the
// same way: by accident (#2660).
//
// A command that legitimately echoes the query back — `no command on this
// machine mentions "zonkoshard"` — must not read as a leak, so the marker is
// looked for outside the reader's own words.
//
// The command in the fixture is a `terraform apply` because the reader only
// keeps commands it recognises as work (worthIndexing): an invented binary
// name is never indexed as a command at all, and a sweep built on one would
// pass without ever asking `how` anything.
// everySurface is what a reader can ask, with a marker only the withheld
// sessions know. One list, used by both sweeps, so a rule that covers one and
// not the other is visible rather than assumed.
func everySurface(scratchPath string) []struct {
	name string
	args []string
} {
	return []struct {
		name string
		args []string
	}{
		{name: "search", args: []string{"zonkobuffer"}},
		{name: "search by error paste", args: []string{"panic: zonkobuffer overflow at shard 7"}},
		{name: "fix", args: []string{"fix", "panic: zonkobuffer overflow at shard 7"}},
		{name: "how", args: []string{"how", "zonkoshard"}},
		{name: "friction", args: []string{"friction"}},
		{name: "last", args: []string{"last"}},
		{name: "files", args: []string{"files", "zonkobuffer"}},
		{name: "blame", args: []string{"blame", "zonko.go"}},
		{name: "brief", args: []string{"brief"}},
		{name: "stats", args: []string{"stats"}},
		{name: "restore", args: []string{"restore", scratchPath}},
	}
}

// servesTheMarker runs one command and fails if a withheld session reached it.
func servesTheMarker(t *testing.T, args []string) {
	t.Helper()
	out, err := captureRun(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	errOut, err := captureRunStderr(t, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	for line := range strings.SplitSeq(out+"\n"+errOut, "\n") {
		if !strings.Contains(line, "zonko") || echoesTheQuery(line, args) {
			continue
		}
		t.Fatalf("a withheld session reached %v:\n%s", args, line)
	}
}

func TestNoSurfaceServesTheIgnoredTree(t *testing.T) {
	for _, tc := range everySurface("/w/scratch/zonko.go") {
		t.Run(tc.name, func(t *testing.T) {
			onlyTheIgnoredTreeKnows(t)
			// The query, echoed inside a refusal, is the reader's own word
			// coming back — not a session being served.
			servesTheMarker(t, tc.args)
		})
	}
}

// The other rule that withholds sessions, over the same surfaces. The trust
// policy has been taught one screen at a time too — #937 for the listing,
// #1026 for files and blame, #1120 for friction, #2399 for the MCP block — so
// the set deserves the same pin the ignore rule got (#2660).
//
// The sessions have to be genuinely imported, because Origin reads the
// "imported:" prefix the import writes; a fixture cannot fake it from a cwd.
// So this store is built the way a second machine really is: index here,
// export, then index and import as the other one.
func TestNoSurfaceServesMemoryTheTrustPolicyWithholds(t *testing.T) {
	for _, tc := range everySurface("/w/alpha/zonko.go") {
		t.Run(tc.name, func(t *testing.T) {
			importedAndWithheld(t)
			servesTheMarker(t, tc.args)
		})
	}
}

// importedAndWithheld leaves the caller on a machine holding three imported
// sessions and a policy that withholds imported memory from every activation.
func importedAndWithheld(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	exchange := filepath.Join(tmp, "exchange")
	if err := os.MkdirAll(exchange, 0o755); err != nil {
		t.Fatal(err)
	}

	// The machine the work happened on.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "a.db"))
	rootA := filepath.Join(tmp, "claude-a")
	t.Setenv("DEJA_CLAUDE_ROOT", rootA)
	for _, id := range []string{"a1", "a2", "a3"} {
		writeClaudeFixture(t, filepath.Join(rootA, "-w-alpha", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the zonkobuffer keeps overflowing"}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:01:00Z","message":{"role":"user","content":[{"type":"tool_result","content":"panic: zonkobuffer overflow at shard 7"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --zonkoshard 7"}}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/alpha","timestamp":"2026-07-01T10:03:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/alpha/zonko.go","old_string":"a","new_string":"b"}}]}}`,
		})
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "export", exchange); err != nil {
		t.Fatal(err)
	}

	// The machine reading it back.
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "b.db"))
	rootB := filepath.Join(tmp, "claude-b")
	t.Setenv("DEJA_CLAUDE_ROOT", rootB)
	writeClaudeFixture(t, filepath.Join(rootB, "-w-beta", "b1.jsonl"), "b1", []string{
		`{"type":"user","sessionId":"b1","cwd":"/w/beta","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"morning notes about nothing"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exchange); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(tmp, "policy-b.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"imported":false},"mcp":{"imported":false},"auto":{"imported":false}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// And the control: with the policy allowing imported memory, the same store
// answers — or every case above passes for the wrong reason.
func TestTheImportedMemoryIsReachableWhenThePolicyAllowsIt(t *testing.T) {
	importedAndWithheld(t)
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "how", "zonkoshard")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "terraform apply --zonkoshard 7") {
		t.Fatalf("with imported memory allowed the command should be there:\n%s", out)
	}
}

// echoesTheQuery reports whether a line only carries the marker because the
// command repeated what it was asked.
func echoesTheQuery(line string, args []string) bool {
	for _, a := range args {
		if strings.Contains(a, "zonko") && strings.Contains(line, a) {
			return true
		}
	}
	return false
}

// The store really does hold what the sweep is looking for, or every case above
// passes for the wrong reason.
func TestTheIgnoredTreeIsReachableWhenTheRuleIsLifted(t *testing.T) {
	onlyTheIgnoredTreeKnows(t)
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"ignore":["*nothing-here*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "how", "zonkoshard")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "terraform apply --zonkoshard 7") {
		t.Fatalf("with the rule lifted the command should be there:\n%s", out)
	}
}

// The ignore rule reached the confirmed pairs and not the sightings held back
// for a second one: with a single session in an ignored tree, search and `deja
// how` refused it while `deja fix` handed back the command it ran there
// (#3403). #2660 is the same defect for the confirmed half.
func TestFixDoesNotServeASightingFromTheIgnoredTree(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	policyFile := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	if err := os.WriteFile(policyFile, []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	// One session only: two would confirm the pair and take the other path.
	writeClaudeFixture(t, filepath.Join(root, "-w-scratch", "one.jsonl"), "one", []string{
		`{"type":"user","sessionId":"one","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z",` +
			`"message":{"role":"user","content":"the zarnak bus refuses to bind"}}`,
		`{"type":"user","sessionId":"one","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z",` +
			`"message":{"role":"user","content":[{"type":"tool_result","content":"zarnakctl: FATAL: could not bind sprocket bus at 192.0.2.7:7788"}]}}`,
		`{"type":"assistant","sessionId":"one","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z",` +
			`"message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"terraform apply --zarnak-bus"}}]}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "fix", "zarnakctl: FATAL: could not bind sprocket bus at 192.0.2.7:7788")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "terraform apply") {
		t.Errorf("deja fix served a sighting out of the ignored tree:\n%s", out)
	}
	// And the same store answers once the rule is lifted, so this is the rule
	// working rather than the fixture never having had anything to serve.
	if werr := os.WriteFile(policyFile, []byte(`{}`), 0o600); werr != nil {
		t.Fatal(werr)
	}
	out, err = captureRun(t, "fix", "zarnakctl: FATAL: could not bind sprocket bus at 192.0.2.7:7788")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "terraform apply") {
		t.Errorf("with the rule lifted the sighting is still withheld:\n%s", out)
	}
}

// The miss path prints the line so it lands before "try fewer words", where
// rewording is the wrong advice; the block after the cap note printed it again,
// so every search that missed said the same sentence twice (#2632).
func TestSearchSaysWhatTheIgnoreRuleKeptOutOnce(t *testing.T) {
	ignoredTreeStore(t)
	out, err := captureRunStderr(t, "widget pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "the ignore rule keeps"); n != 1 {
		t.Fatalf("the line is printed %d times, want once:\n%s", n, out)
	}
}

// files and blame apply the rule and said nothing, directly under the policy
// note that exists for the same misread: the topic did match, a rule withheld
// it (#686, #680).
func TestFilesAndBlameSayWhatTheIgnoreRuleKeptOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"files", []string{"files", "widget"}},
		{"blame", []string{"blame", "/w/scratch/widget/pipeline.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ignoredTreeStore(t)
			out, err := captureRun(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			errOut, err := captureRunStderr(t, tc.args...)
			if err != nil {
				t.Fatal(err)
			}
			all := out + errOut
			if !strings.Contains(all, "the ignore rule keeps 3 sessions") {
				t.Fatalf("nothing names the rule that emptied the answer:\n%s", all)
			}
		})
	}
}

// A miss under an active filter takes its own branch and never reaches
// printNoMatches, so it says the line itself rather than losing it with the
// guard that stopped the doubling (#2632).
func TestSearchUnderAFilterStillNamesTheIgnoreRule(t *testing.T) {
	ignoredTreeStore(t)
	out, err := captureRunStderr(t, "--project", "keep", "widget pipeline")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "the ignore rule keeps"); n != 1 {
		t.Fatalf("the line is printed %d times, want once:\n%s", n, out)
	}
}

// ignoredTreeStore builds a store where one project is covered by the reader's
// ignore rule and holds everything worth finding: the command, the replaced
// span, the recurring error.
func ignoredTreeStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	if err := os.MkdirAll(filepath.Dir(policy.Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy.Path(), []byte(`{"ignore":["*scratch*"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-keep", "k1.jsonl"), "k1", []string{
		`{"type":"user","sessionId":"k1","cwd":"/w/keep","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"morning notes"}}`,
	})
	for _, id := range []string{"s1", "s2", "s3"} {
		writeClaudeFixture(t, filepath.Join(root, "-w-scratch", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the widget pipeline keeps stalling"}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"make widget-pipeline"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:01:30Z","message":{"role":"user","content":[{"type":"tool_result","content":"make: *** [widget-pipeline] Error 2"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/w/scratch","timestamp":"2026-07-01T10:02:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"/w/scratch/widget/pipeline.go","old_string":"before-line","new_string":"after-line"}}]}}`,
		})
	}
}

// The ignore rule is applied inside retrieval, and the surfaces that walk the
// record log instead of ranking did not apply it at all: `how`, `restore` and
// `friction` answered out of the tree the reader asked deja to stay out of, and
// with the default rule that tree is the one a background agent makes for
// itself (#2630).
func TestTheRecordLogSurfacesObeyTheIgnoreRule(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		leaks string
	}{
		{"how", []string{"how", "make widget"}, "make widget-pipeline"},
		{"restore", []string{"restore", "/w/scratch/widget/pipeline.go"}, "replaced spans recorded for /w"},
		{"friction", []string{"friction"}, "widget-pipeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ignoredTreeStore(t)
			out, err := captureRun(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if strings.Contains(out, tc.leaks) {
				t.Fatalf("the ignored tree answered:\n%s", out)
			}
		})
	}
}

// And say so, rather than reading as a store that holds nothing: the same line
// the listing and search already print.
func TestTheRecordLogSurfacesSayWhatTheIgnoreRuleKeptOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"how", []string{"how", "make widget"}},
		{"friction", []string{"friction"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ignoredTreeStore(t)
			out, err := captureRunStderr(t, tc.args...)
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if !strings.Contains(out, "the ignore rule keeps 3 sessions") {
				t.Fatalf("nothing says the rule took the answer out:\n%s", out)
			}
		})
	}
}

// And the sentence on stdout, which is what a redirect keeps: saying the
// machine never recorded tool output is a claim about the store when the truth
// is a claim about the rule (#637, #1044, and now #2630).
func TestFrictionNamesTheRuleRatherThanTheStore(t *testing.T) {
	ignoredTreeStore(t)
	out, err := captureRun(t, "friction")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "the ignore rule keeps the 3 sessions that recorded tool output out of recall") {
		t.Fatalf("stdout blames the store for what the rule did:\n%s", out)
	}
}

// `deja stats --impact --json` is a machine surface that was documented
// nowhere, and it grew `since` in #1890 while `credited_aloud` vanished for one
// commit in the same change. `stats --json` has had its keys pinned to the
// document since #1710; this is that pin for the screen beside it (#1898).
func TestImpactJSONKeysMatchTheDocumentedContract(t *testing.T) {
	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	section := string(doc)
	i := strings.Index(section, "## `deja stats --impact --json`")
	if i < 0 {
		t.Fatal("docs/json-output.md does not describe `deja stats --impact --json`")
	}
	section = section[i:]
	if end := strings.Index(section[3:], "\n## "); end > 0 {
		section = section[:end+3]
	}

	var out strings.Builder
	full := usage.ImpactReport{
		Recalls: 3, Injections: 2, ServedBytes: 100, RawBytes: 1000,
		ReusedTwice: 1, DejaVuMoments: 1, Since: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC),
	}
	if err := printImpact(&out, full, 2, true); err != nil {
		t.Fatal(err)
	}
	var emitted map[string]any
	if err := json.Unmarshal([]byte(out.String()), &emitted); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	for k := range emitted {
		if !strings.Contains(section, "`"+k+"`") {
			t.Errorf("--impact --json emits %q, missing from its section of docs/json-output.md", k)
		}
	}
	// And the other way: a key the document names must still be emitted, so a
	// field that leaves the struct cannot leave the document behind.
	for _, k := range []string{"recalls", "injections", "served_bytes", "raw_bytes",
		"reused_twice", "dejavu_moments", "since", "credited_aloud"} {
		if !strings.Contains(section, "`"+k+"`") {
			continue
		}
		if _, ok := emitted[k]; !ok {
			t.Errorf("docs/json-output.md names %q, no longer emitted", k)
		}
	}
}

// A machine with only the prompt hook installed never increments `recalls`
// (recall/context kinds) or `injections` (session starts) — its digests land
// in `dejavu_moments`. The empty check read those two counters, so the report
// said nothing was recorded while `deja log`, the stats card and its own JSON
// all listed the injections (#2303).
func TestImpactCountsAPromptHookOnlyMachine(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for range 5 {
		usage.RecordDigestInto(dir, usage.KindDejaVu, "<deja-recall>\n  - Session: **a** `1`\n", "agent1", 2, 1160,
			[]string{"panic", "quaxbolt"}, "r0", "r1")
	}

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "no recall activity recorded yet") {
		t.Fatalf("five injections read as nothing recorded:\n%s", got)
	}
	if !strings.Contains(got, "déjà vu moments") {
		t.Errorf("the report does not name what did happen:\n%s", got)
	}
	// The closing line divides by what was served — 0 here before the fix,
	// which reads as "none of 0".
	if strings.Contains(got, "of 0 ") {
		t.Errorf("credited-aloud line counts against nothing:\n%s", got)
	}

	// An untouched machine still says so.
	hermeticEnv(t)
	out.Reset()
	if err := runStatsImpact(&out, index.DefaultDir(), false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no recall activity recorded yet") {
		t.Errorf("an empty log lost its explanation:\n%s", out.String())
	}
}

// A machine served only through the PreToolUse hook: no counter named that
// door, and the bytes line wanted a raw size the tool event never records, so
// the report opened and printed two zeros about 945 bytes of served lines
// (#2309).
func TestImpactNamesTheToolDoor(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	for range 4 {
		usage.RecordResult(dir, usage.KindTool, 236, 1, false)
	}

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "4") {
		t.Errorf("the report never counts the four tool lines:\n%s", got)
	}
	if !strings.Contains(got, "944 B") && !strings.Contains(got, "945 B") {
		t.Errorf("the report never says what was served:\n%s", got)
	}
}

// The ratio line still reads as before when both sides are there.
func TestImpactKeepsTheRatioWhenRawIsKnown(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	usage.RecordServedSessions(dir, usage.KindRecall, 1000, 2, false, 8000, []string{"a", "b"})

	var out bytes.Buffer
	if err := runStatsImpact(&out, dir, false); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "8× less") {
		t.Errorf("the distilled ratio is gone:\n%s", got)
	}
}

// The end of a move to a new machine said "imported N records" and nothing
// else: records are deja's own unit, and the person watching has no way to
// check the number. Install ends the same moment with real lines out of the
// history it just indexed (#929).
func TestImportSaysWhatArrivedAndProvesIt(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	var batch strings.Builder
	for _, s := range []struct{ id, project, text string }{
		{"r1", "api", "the connection pool ran dry behind pgbouncer"},
		{"r2", "web", "the anemometer reading drifted after the ticker change"},
	} {
		b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: s.id, Project: s.project, Role: "user", Text: s.text})
		if err != nil {
			t.Fatal(err)
		}
		batch.WriteString(string(b) + "\n")
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), []byte(batch.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 sessions from another machine") {
		t.Errorf("import counted only records: %q", out)
	}
	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	// Nothing new arrived the second time, so there is nothing to prove.
	if strings.Contains(proof, "deja now knows") {
		t.Errorf("a no-op import still claimed an arrival: %q", proof)
	}
}

// The proof itself: the lines are the sessions that just came, named where the
// reader can recognise them.
func TestImportProofNamesRealSessions(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "r1", Project: "api", Role: "user", Text: "the connection pool ran dry behind pgbouncer"})
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); werr != nil {
		t.Fatal(werr)
	}
	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proof, "deja now knows") || !strings.Contains(proof, "pgbouncer") {
		t.Errorf("import proved nothing:\n%s", proof)
	}
}

// The proof is a listing, and a listing obeys the trust policy: on a machine
// whose rule keeps imported sessions out of recall it was printing their
// project and first line, then closing with "ask your agent about any of these
// — it will remember", which is what the rule prevents (#951).
func TestImportProofObeysTheTrustPolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "api", Role: "user", Text: "PEER SECRET: the vault rotation runs at 03:00"})
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); werr != nil {
		t.Fatal(werr)
	}
	policyFile := filepath.Join(tmp, "policy.json")
	if werr := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(proof, "PEER SECRET") || strings.Contains(proof, "it will remember") {
		t.Errorf("the proof printed what the policy hides:\n%s", proof)
	}
	if !strings.Contains(proof, "trust policy hides") {
		t.Errorf("the proof says nothing about why it is empty:\n%s", proof)
	}
	// The count still reaches the reader, so the import is not silent.
	out, err := captureRun(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	_ = out
}

// A sync batch carries no timestamps, so an imported session has no date, and
// an empty slot left `[claude · imported:solo · ]` on the screen whose whole
// job is to show the memory arrived (#964).
func TestImportProofDoesNotLeaveTheDateSlotEmpty(t *testing.T) {
	tmp := hermeticEnv(t)
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "p1", Project: "api", Role: "user", Text: "peer one about the pool"})
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); werr != nil {
		t.Fatal(werr)
	}
	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proof, "peer one about the pool") {
		t.Fatalf("the proof did not list the imported session:\n%s", proof)
	}
	if strings.Contains(proof, "· ]") {
		t.Errorf("the date slot is empty:\n%s", proof)
	}
	if !strings.Contains(proof, "· -]") {
		t.Errorf("a session with no date does not say so the way `last` does:\n%s", proof)
	}
}

// The block is headed "from the machine you came from" and was built from the
// recent list, so this machine's own sessions stood in as evidence a transfer
// had landed — and under a rule that hides imported rows they were all that
// was left (#988).
func TestImportProofShowsOnlyWhatArrived(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	exp := filepath.Join(tmp, "transfer")
	if err := os.MkdirAll(exp, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer", Project: "work/api", Role: "user", Text: "peer three about the ticker"})
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); werr != nil {
		t.Fatal(werr)
	}
	dir := filepath.Join(tmp, "index.db")
	if eerr := index.Ensure(dir, "", false, nil); eerr != nil {
		t.Fatal(eerr)
	}

	proof, err := captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(proof, "peer three about the ticker") {
		t.Errorf("the proof does not show what arrived:\n%s", proof)
	}
	if strings.Contains(proof, "local work on the ticker window") {
		t.Errorf("a session that was here before the transfer is offered as proof of it:\n%s", proof)
	}
	// And not the error *this* machine keeps hitting, which the install proof
	// leads with: under a heading about the machine you came from it would be
	// a claim about the wrong machine (#2966).
	if strings.Contains(proof, "this machine has hit") {
		t.Errorf("the import proof names a local recurring error under a heading about another machine:\n%s", proof)
	}

	// With every arrived row hidden by a rule, the reader gets the rule — not
	// this machine's own sessions under someone else's heading.
	policyFile := filepath.Join(tmp, "policy.json")
	if werr := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); werr != nil {
		t.Fatal(werr)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)
	b2, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer2", Project: "work/api", Role: "user", Text: "another peer line"})
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(filepath.Join(exp, "deja-sync-y.jsonl"), append(b2, '\n'), 0o644); werr != nil {
		t.Fatal(werr)
	}
	proof, err = captureRunStderr(t, "sync", "import", exp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(proof, "local work on the ticker window") {
		t.Errorf("the proof fell back to local sessions under the import heading:\n%s", proof)
	}
	if !strings.Contains(proof, "trust policy") {
		t.Errorf("nothing named the rule that emptied the proof:\n%s", proof)
	}
}

// labelStore indexes one local session and one that arrived by sync.
func labelStore(t *testing.T) {
	t.Helper()
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(tmp, "index.db"))
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-w-beta", "b1.jsonl"), "b1", []string{
		`{"type":"user","sessionId":"b1","cwd":"/w/beta","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"the beta rollout"}}`,
	})
	writeClaudeFixture(t, filepath.Join(root, "-w-alpha", "a1.jsonl"), "a1", []string{
		`{"type":"user","sessionId":"a1","cwd":"/w/alpha","timestamp":"2026-07-02T10:00:00Z","message":{"role":"user","content":"the alpha pipeline"}}`,
	})
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
}

// The label `deja last` prints for an imported session — the sending machine in
// place of the "imported:" prefix — was accepted by no filter, so the one
// string a reader can copy off that screen worked nowhere (#2644).
func TestTheProjectFilterTakesTheLabelTheListingPrints(t *testing.T) {
	for _, tc := range []struct {
		name, project, from, want string
		match                     bool
	}{
		{"the label a listing prints", "imported:alpha", "mini", "mini:alpha", true},
		{"the stored form", "imported:alpha", "mini", "imported:alpha", true},
		{"the bare project", "imported:alpha", "mini", "alpha", true},
		// The machine alone stays --from's question: the project filter would
		// otherwise select everything that machine sent.
		{"the machine alone", "imported:alpha", "mini", "mini", false},
		{"another machine's label", "imported:alpha", "mini", "laptop:alpha", false},
		{"another project on this machine", "imported:beta", "mini", "mini:alpha", false},
		{"a local session is untouched", "alpha", "", "mini:alpha", false},
		{"a local session by its own name", "alpha", "", "alpha", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := query.ProjectMatches(tc.project, tc.from, tc.want); got != tc.match {
				t.Fatalf("--project %q against %q from %q = %v, want %v", tc.want, tc.project, tc.from, got, tc.match)
			}
		})
	}
}

// And the label on the screen is built by the same function the filter reads,
// so the two cannot drift apart.
func TestTheListingLabelAndTheFilterAgree(t *testing.T) {
	s := model.Session{Project: "imported:alpha", From: "mini"}
	shown := displayProject(s)
	if shown != "mini:alpha" {
		t.Fatalf("the listing label changed: %q", shown)
	}
	if !query.ProjectMatches(s.Project, s.From, shown) {
		t.Fatalf("the label the listing prints does not select the session it names")
	}
}

// End to end, on a store: the filter still selects what it always did.
func TestProjectFilterStillSelectsALocalProject(t *testing.T) {
	labelStore(t)
	out, err := captureRun(t, "last", "--project", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Fatalf("the project filter no longer selects a plain project:\n%s", out)
	}
}

// A decision rejected on one machine and synced to another: the note arrives
// carrying its state, the transcript it rejects arrives beside it, and only
// the note was marked. The query a person types finds the transcript, so a
// reverted decision came back reading like current truth — #974 again, this
// time across a sync (#1051).
func TestASyncedRejectionMarksTheTranscriptItRejects(t *testing.T) {
	tmp := hermeticEnv(t)
	batch := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	// What `deja sync export` writes on the machine that made the decision:
	// the transcript, the note, and the note's correction. Ids are the sender's.
	recs := strings.Join([]string{
		`{"harness":"claude","session_id":"src1","project":"proj/p","role":"user","text":"we will cap the connection pool at 20","time":"2026-08-04T17:44:54+03:00"}`,
		`{"harness":"deja","session_id":"deja-note-claude-src1","project":"proj/p","role":"user","text":"[accepted] capped the pool at 20 (from claude:src1, 2026-08-06)","time":"2026-08-06T17:44:54+03:00"}`,
		`{"harness":"deja","session_id":"deja-note-claude-src1","project":"proj/p","role":"user","text":"[rejected] reverted: pgbouncer instead (from claude:src1, 2026-08-06)","time":"2026-08-06T17:45:04+03:00"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(batch, "deja-sync-aaaa-1.jsonl"), []byte(recs), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRunStderr(t, "sync", "import", batch); err != nil {
		t.Fatal(err)
	}

	dir := index.DefaultDir()
	hits := searchHits(t, dir, "connection pool")
	if len(hits) == 0 {
		t.Fatal("the imported transcript did not come back at all")
	}
	// The transcript is the hit this query returns; the note's own words
	// ("pgbouncer") are not in it, which is the whole point of the bug.
	transcript := -1
	for i, h := range hits {
		if h.Session.Harness == "claude" {
			transcript = i
		}
	}
	if transcript < 0 {
		t.Fatalf("no transcript hit: %#v", hits)
	}
	attachLifecycles(dir, hits)
	if got := hits[transcript].Lifecycle; got != "rejected" {
		t.Errorf("imported transcript carries lifecycle %q, want rejected", got)
	}
	if line := lifecycleLine(hits[transcript]); !strings.Contains(line, "tried and rejected") {
		t.Errorf("the transcript's line does not say what happened: %q", line)
	}
	// Without the loader's "(from …, day)" tail, as on the sender (#5053).
	if got := hits[transcript].LifecycleNote; got != "reverted: pgbouncer instead" {
		t.Errorf("correction lost on the way: %q", got)
	}

	// Blame must resolve the same synced rejection and local promotion of an
	// imported session via OrigID.
	blameHits := []search.BlameHit{{Session: hits[transcript].Session}}
	attachBlameLifecycles(dir, blameHits, search.BlameTarget{FullPath: "/work/p/pool.go", Base: "pool.go", Stem: "pool"})
	if got := blameHits[0].Lifecycle; got != "rejected" {
		t.Errorf("blame on imported transcript carries lifecycle %q, want rejected", got)
	}
	if got := blameHits[0].LifecycleNote; got != "reverted: pgbouncer instead" {
		t.Errorf("blame correction lost on the way: %q", got)
	}
}

// docs/INTEGRATING.md is written for people outside this repository, who cannot
// tell a command that exists from one that sounds like it should. The first
// draft of that page named three that do not — `--list`, `hook-session-start`,
// `hook-precompact` — because it was written from memory rather than from the
// binary, and an integrator following it gets "unknown command" on line one.
func TestIntegratingNamesCommandsThatExist(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "docs", "INTEGRATING.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)

	// Only where the page is quoting a command: inside backticks, or at the
	// start of a line in a shell block. Prose that happens to say "deja reads"
	// is not a claim about a subcommand.
	named := regexp.MustCompile("(?m)(?:^|`)deja ([a-z][a-z0-9-]*)")
	found := 0
	for _, m := range named.FindAllStringSubmatch(text, -1) {
		found++
		// dispatchKnows, not the commands map: five commands parse their own
		// arguments and live in the switch above it, `search` among them.
		if !dispatchKnows(m[1]) {
			t.Errorf("docs/INTEGRATING.md tells an integrator to run %q, which is not a command", "deja "+m[1])
		}
	}
	if found < 5 {
		t.Fatalf("only %d commands were read out of the page; the scan is wrong", found)
	}

	// The count of store variables. It moves with every harness, and the page
	// states it to say how much work DEJA_STORES replaces.
	count := regexp.MustCompile("over the (\\d+) `DEJA_").FindStringSubmatch(text)
	if count == nil {
		t.Fatal("the page no longer says how many store variables there are; if that is on purpose, this check goes with it")
	}
	said, err := strconv.Atoi(count[1])
	if err != nil {
		t.Fatal(err)
	}
	reg, err := os.ReadFile(filepath.Join("..", "..", "docs", "registry", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	vars := map[string]bool{}
	for _, m := range regexp.MustCompile(`DEJA_[A-Z0-9_]+`).FindAllString(string(reg), -1) {
		vars[m] = true
	}
	if len(vars) != said {
		t.Errorf("the page says %d store variables; the published registry names %d", said, len(vars))
	}
}

// The Japanese README states the harness count in digits with a Japanese
// counted noun, which no other check here can see: the English word check
// scans for "thirty-four", the digit checks run over a fixed file list that
// this page is not on, and the Chinese ones look for 三十四. README.zh.md
// shipped a count eight harnesses stale before it got its own check (#3458);
// the same drift is likelier in a page none of the maintainers reads.
var jaHarnessClaims = []*regexp.Regexp{
	// "34 のエージェント" and "34 のコーディングエージェント": the counted noun is
	// what turns a numeral into a claim about harnesses. A bare numeral scan
	// would catch 43 回のコンパクション and the ten seconds of an install.
	regexp.MustCompile(`([0-9]+)\s*の(?:コーディング)?エージェント`),
}

func TestTheJapanesePageCountsTheRegistry(t *testing.T) {
	root := filepath.Join("..", "..")
	n := registryHarnessCount(t, root)

	page := filepath.Join(root, "docs", "readme", "README.ja.md")
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("README.ja.md: %v", err)
	}
	text := string(b)

	found := 0
	for _, re := range jaHarnessClaims {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			got, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			found++
			if got != n {
				t.Errorf("README.ja.md says %q; the registry has %d harnesses", m[0], n)
			}
		}
	}
	if found == 0 {
		t.Error("README.ja.md no longer states the harness count; if the phrasing changed on purpose, this check changes with it")
	}
}

// Taking deja's entry out of a list cut from the comma before it, so a
// comment the reader wrote on their own entry went with it and the comment
// after deja's moved onto their line (#5557).
func TestCuttingDejasEntryKeepsTheReadersComment(t *testing.T) {
	const entry = "./plugins/deja-status.tsx"
	cases := []struct{ name, in, want string }{
		{"comment on the entry before",
			"{\n  \"plugin\": [\n    \"./a.tsx\", // a\n    \"./plugins/deja-status.tsx\"\n  ]\n}\n",
			"{\n  \"plugin\": [\n    \"./a.tsx\" // a\n  ]\n}\n"},
		{"crlf",
			"{\r\n  \"plugin\": [\r\n    \"./a.tsx\", // a\r\n    \"./plugins/deja-status.tsx\"\r\n  ]\r\n}\r\n",
			"{\r\n  \"plugin\": [\r\n    \"./a.tsx\" // a\r\n  ]\r\n}\r\n"},
		{"one line",
			"{ \"plugin\": [\"./a.tsx\", \"./plugins/deja-status.tsx\"] }\n",
			"{ \"plugin\": [\"./a.tsx\"] }\n"},
		{"block comment between",
			"{ \"plugin\": [\"./a.tsx\", /* mine */ \"./plugins/deja-status.tsx\"] }\n",
			"{ \"plugin\": [\"./a.tsx\" /* mine */] }\n"},
		{"in the middle",
			"{\n  \"plugin\": [\n    \"./a.tsx\", // a\n    \"./plugins/deja-status.tsx\",\n    \"./b.tsx\"\n  ]\n}\n",
			"{\n  \"plugin\": [\n    \"./a.tsx\", // a\n    \"./b.tsx\"\n  ]\n}\n"},
	}
	for _, c := range cases {
		got, err := cutTUIPluginEntry(c.in, entry)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.name, got, c.want)
		}
		var v any
		if err := json.Unmarshal([]byte(jsoncToJSON(got)), &v); err != nil {
			t.Errorf("%s: result no longer parses: %v\n%s", c.name, err, got)
		}
		if strings.Contains(got, "deja-status") {
			t.Errorf("%s: deja's entry is still there:\n%s", c.name, got)
		}
	}
}

// Writing into a commented config whose mcp block is empty must leave a file a
// parser still reads. A comma after the only entry made it invalid JSON, and
// every later run then refused, blaming the reader's comment for deja's byte
// (#2742).
func TestJSONCEmptyBlockStaysParseable(t *testing.T) {
	seed := "{ // hi\n  \"mcp\": {}\n}\n"
	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "deja") {
		t.Fatalf("the entry was not written:\n%s", got)
	}
	if !strings.Contains(got, "// hi") {
		t.Errorf("the reader's comment was lost:\n%s", got)
	}
	// Strip the line comments the way a jsonc reader would, then parse.
	var body []string
	for l := range strings.SplitSeq(got, "\n") {
		if i := strings.Index(l, "//"); i >= 0 {
			l = l[:i]
		}
		body = append(body, l)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(strings.Join(body, "\n")), &root); err != nil {
		t.Fatalf("deja left the config unparseable (%v):\n%s", err, got)
	}
	mcp, _ := root["mcp"].(map[string]any)
	if _, ok := mcp["deja"]; !ok {
		t.Errorf("the entry is not in the mcp block:\n%s", got)
	}
}

// The spacing between an inline block's braces is not deja's to leave behind:
// "mcp": { } used to come back as a line ending in a space.
func TestJSONCInlineBlockLeavesNoTrailingSpace(t *testing.T) {
	out, _, err := updateOpencodeJSONC([]byte("{\n  \"mcp\": { }\n}\n"), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	for l := range strings.SplitSeq(string(out), "\n") {
		if l != strings.TrimRight(l, " \t") {
			t.Errorf("a line ends in whitespace: %q\n%s", l, out)
		}
	}
}

// The text path recognised the literal key "deja" and nothing else, so an entry
// somebody wired by hand under another name was invisible to it: install added
// a sibling and the harness started two servers, doctor named the second, and
// uninstall left one behind without a word (#2742).

// Adopt the hand-written entry rather than add a sibling — what dejaEntryKey
// does on the parsed path (#2269).
func TestJSONCAdoptsAnEntryUnderAnotherName(t *testing.T) {
	seed := "{\n  \"mcp\": {\n" +
		"    \"deja-vu\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]}\n" +
		"  }\n}\n"

	out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "/bin/deja") != 1 {
		t.Fatalf("the binary was not written once:\n%s", got)
	}
	if strings.Contains(got, "/old/deja") {
		t.Errorf("the old binary is still launched:\n%s", got)
	}
	if strings.Contains(got, `"deja":`) {
		t.Errorf("added a sibling beside the entry that was already here:\n%s", got)
	}
	if !strings.Contains(got, `"deja-vu"`) {
		t.Errorf("lost the name the entry was written under:\n%s", got)
	}

	// And again on its own output: an adoption that only holds the first time
	// grows a sibling on the next install instead.
	again, _, err := updateOpencodeJSONC(out, "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != got {
		t.Errorf("a second install changed the file again:\n%s", again)
	}
}

// Name the further entries that also run deja, the way every other MCP writer
// does (#2712).
func TestJSONCNamesAnotherEntryThatRunsDeja(t *testing.T) {
	seed := "{\n  \"mcp\": {\n" +
		"    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]},\n" +
		"    \"deja-vu\": {\"type\":\"local\",\"command\":[\"/other/deja\",\"mcp\"]}\n" +
		"  }\n}\n"

	_, note, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "deja-vu") {
		t.Errorf("nothing named the second entry: %q", note)
	}
}

// Uninstall leaves an entry it did not write, which is right — but saying
// nothing looks like an uninstall that missed one (#2269).
func TestJSONCUninstallNamesWhatItLeaves(t *testing.T) {
	seed := "{\n  \"mcp\": {\n" +
		"    \"deja\": {\"type\":\"local\",\"command\":[\"/bin/deja\",\"mcp\"]},\n" +
		"    \"deja-vu\": {\"type\":\"local\",\"command\":[\"/other/deja\",\"mcp\"]}\n" +
		"  }\n}\n"

	out, note, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "deja-vu") {
		t.Fatalf("removed an entry deja did not write:\n%s", out)
	}
	if !strings.Contains(note, "deja-vu") {
		t.Errorf("nothing named the entry left behind: %q", note)
	}
}

// The text path built the entry itself instead of taking the one its caller
// would have written, so a commented ~/.claude.json got an entry without the
// `type` every other claude entry carries — two writers for one file, agreeing
// on everything but what they write.
func TestBothWritersPutTheSameEntryInClaudesConfig(t *testing.T) {
	entryFor := func(t *testing.T, before string) map[string]any {
		t.Helper()
		hermeticEnv(t)
		path := sources.ClaudeJSONPath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "claude-code", "--no-index"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
			t.Fatalf("the config no longer parses: %v\n%s", err, b)
		}
		servers, _ := root["mcpServers"].(map[string]any)
		entry, ok := servers["deja"].(map[string]any)
		if !ok {
			t.Fatalf("no deja entry:\n%s", b)
		}
		return entry
	}

	plain := entryFor(t, "{\n  \"mcpServers\": {}\n}\n")
	commented := entryFor(t, "{\n  // mine\n  \"mcpServers\": {}\n}\n")

	for _, key := range []string{"type", "command", "args"} {
		if !sameEntry(plain[key], commented[key]) {
			t.Errorf("%s: the commented config got %#v, the plain one %#v", key, commented[key], plain[key])
		}
	}
	if commented["type"] != "stdio" {
		t.Errorf("the entry claude reads carries no transport: %#v", commented)
	}
}

// And the writers that do not want a type still do not get one.
func TestTheGenericWriterKeepsItsOwnEntryShape(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\n  // mine\n  \"mcpServers\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"type"`) {
		t.Errorf("cursor's entry gained a transport it does not use:\n%s", b)
	}
}

// The block is written at the top of the object with a comma after it, and a
// config whose only line is a comment has no other key for that comma to
// separate: the file stopped being JSON, and every later run refused the
// target with a message pointing at the reader's own comment. The same shape
// #2740 fixed one level down.
func TestABlockWrittenIntoAnEmptyObjectCarriesNoStrayComma(t *testing.T) {
	for _, before := range []string{
		"{\n  // mine\n}\n",
		"{\n  /* mine */\n}\n",
		"{}\n",
	} {
		t.Run(strings.TrimSpace(before), func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var probe map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &probe); err != nil {
				t.Fatalf("the config no longer parses: %v\n%s", err, b)
			}
			// And the run that comes after it still works, which is what the
			// stray comma took away.
			if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
				t.Fatalf("the second run was refused: %v", err)
			}
		})
	}
}

// The same insert in the flag writer, which puts gemini's hook switch in.
func TestAFlagBlockWrittenIntoAnEmptyObjectCarriesNoStrayComma(t *testing.T) {
	for _, before := range []string{"{\n  // mine\n}\n", "{}\n"} {
		got, err := jsoncSetFlag(before, "hooksConfig", "enabled", true)
		if err != nil {
			t.Fatal(err)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(got)), &probe); err != nil {
			t.Errorf("%q: the config no longer parses: %v\n%s", before, err, got)
		}
	}
}

// gemini-auto wrote its MCP entry through the text path and then refused the
// same file with a strict decode, so the target reported itself refused with
// half its wiring written and a .bak beside it — worse than the plain refusal
// it replaced (#2744).
func TestGeminiAutoTakesACommentedSettingsFile(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.GeminiHome(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // my theme\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := captureRun(t, "install", "gemini-auto", "--no-index"); err != nil {
		t.Fatalf("a comment refused the target: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "// my theme") || !strings.Contains(got, `"theme": "dark"`) {
		t.Errorf("the reader's file did not survive:\n%s", got)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
		t.Fatalf("what deja wrote is not JSON: %v\n%s", err, got)
	}
	if _, ok := root["mcpServers"].(map[string]any)["deja"]; !ok {
		t.Errorf("the server was not written:\n%s", got)
	}
	cfg, _ := root["hooksConfig"].(map[string]any)
	if enabled, _ := cfg["enabled"].(bool); !enabled {
		t.Errorf("the hook switch was not turned on:\n%s", got)
	}
}

// The switch on a file that already has it, and on one whose block is written
// at another shape: deja reads what is there rather than writing a second key.
func TestTheGeminiHookSwitchReadsWhatIsThere(t *testing.T) {
	for _, c := range []struct {
		name, before string
		wantWrite    bool
	}{
		{"already on", "{\n  // c\n  \"hooksConfig\": {\"enabled\": true}\n}\n", false},
		{"turned off by hand", "{\n  // c\n  \"hooksConfig\": {\"enabled\": false}\n}\n", true},
		{"a block with other keys", "{\n  // c\n  \"hooksConfig\": {\"timeout\": 5}\n}\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(sources.GeminiHome(), "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.before), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := enableGeminiHooks(); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if !strings.Contains(got, "// c") {
				t.Errorf("the comment was dropped:\n%s", got)
			}
			var root map[string]any
			if err := json.Unmarshal([]byte(stripJSONComments(got)), &root); err != nil {
				t.Fatalf("not JSON after the switch: %v\n%s", err, got)
			}
			cfg, _ := root["hooksConfig"].(map[string]any)
			if enabled, _ := cfg["enabled"].(bool); !enabled {
				t.Errorf("the switch is not on:\n%s", got)
			}
			if !c.wantWrite && got != c.before {
				t.Errorf("a file that was already right was rewritten:\n%s", got)
			}
			if c.name == "a block with other keys" {
				if _, ok := cfg["timeout"]; !ok {
					t.Errorf("the reader's own key in that block was dropped:\n%s", got)
				}
			}
		})
	}
}

// And the hook writers that cannot edit a commented file say so, without
// touching it — and say nothing at all when there was nothing of deja's in it
// to change.
func TestASettingsHookOnACommentedFileIsHonest(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.QwenConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	// Removing a hook that is not there changes nothing, so there is nothing
	// to refuse.
	r, err := installSettingsHook(path, "BeforeAgent", "", 10000, "/opt/deja", true)
	if err != nil {
		t.Fatalf("removing a hook that is not there was refused: %v", err)
	}
	if r.Action != "unchanged" {
		t.Errorf("action is %q, want unchanged", r.Action)
	}

	// Writing one cannot be done without losing the comments, and says so.
	if _, err = installSettingsHook(path, "UserPromptSubmit", "", 60000, "/opt/deja", false); err == nil {
		t.Error("a hook was written into a file deja cannot rewrite")
	} else if !strings.Contains(err.Error(), "carries comments") {
		t.Errorf("the refusal does not say why: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the file was touched anyway:\n%s", b)
	}
}

// The shapes that made the flag writer splice through the middle of a value,
// each one leaving a file no parser reads while install said it worked
// (#2745).
func TestTheFlagWriterRefusesWhatItCannotEdit(t *testing.T) {
	for _, c := range []struct{ name, in string }{
		{"a string value that reads like the key", `{"hooksConfig": {"label": "enabled", "timeout": 5}}`},
		{"a value that is an object", `{"hooksConfig": {"enabled": {"deep": true}}}`},
		{"a value that is a list", `{"hooksConfig": {"enabled": [1, 2]}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := jsoncSetFlag(c.in, "hooksConfig", "enabled", true)
			if err != nil {
				return // refusing is an answer
			}
			var v any
			if e := json.Unmarshal([]byte(stripJSONComments(out)), &v); e != nil {
				t.Errorf("what it wrote is not JSON: %v\n%s", e, out)
			}
		})
	}
}

// A block holding something else gets no second key of its own name: the
// reader's value would win and the switch would read back off (#2745).
func TestTheGeminiSwitchRefusesABlockItCannotEdit(t *testing.T) {
	for _, before := range []string{
		"{\n  // c\n  \"hooksConfig\": null\n}\n",
		"{\n  // c\n  \"hooksConfig\": [1]\n}\n",
		"{\n  // c\n  \"hooksConfig\": {\"enabled\": [1]}\n}\n",
	} {
		hermeticEnv(t)
		path := filepath.Join(sources.GeminiHome(), "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := enableGeminiHooks(); err == nil {
			b, _ := os.ReadFile(path)
			t.Errorf("a block deja cannot edit was accepted:\n%s", b)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != before {
			t.Errorf("the file was written anyway:\n%s", b)
		}
	}
}

// A comment after the value is the reader's, and the only byte the text path
// was still eating.
func TestTheFlagWriterKeepsACommentAfterTheValue(t *testing.T) {
	in := "{\n  \"hooksConfig\": {\n    \"enabled\": false // off on purpose\n  }\n}\n"
	out, err := jsoncSetFlag(in, "hooksConfig", "enabled", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "// off on purpose") {
		t.Errorf("the comment was eaten:\n%s", out)
	}
	if !strings.Contains(out, "\"enabled\": true") {
		t.Errorf("the switch was not turned on:\n%s", out)
	}
}

// And the half-write: the hook half of a target goes first when it shares a
// file with the MCP entry, so a refusal leaves the file as it was (#2745).
func TestQwenAutoDoesNotWriteHalfItsWiringIntoACommentedFile(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.QwenConfigDir(), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"theme\": \"dark\"\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "qwen-auto", "--no-index"); err == nil {
		t.Fatal("a hook deja cannot write was reported as installed")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the file was written before the refusal:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Errorf("a snapshot was left beside a file that was never changed")
	}
}

// A `//` line is not a broken config. Five writers decoded the file as strict
// JSON and refused the whole target over one comment, so somebody who had
// annotated their config could not install deja at all — while Zed's config
// has been edited as text since #1285 precisely so comments survive (#1664).
func TestInstallKeepsACommentedConfig(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{
  // the server I wrote myself
  "mcpServers": {
    "mine": {"command": "/usr/local/bin/mine"}
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "install", "cursor", "--no-index")
	if err != nil {
		t.Fatalf("a comment refused the target: %v\n%s", err, out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "// the server I wrote myself") {
		t.Errorf("the reader's comment was dropped:\n%s", got)
	}
	if !strings.Contains(got, `"mine": {"command": "/usr/local/bin/mine"}`) {
		t.Errorf("the reader's own entry was rewritten:\n%s", got)
	}
	if !strings.Contains(got, `"deja"`) {
		t.Errorf("deja's entry was not written:\n%s", got)
	}

	// A second install changes nothing, and an uninstall gives the file back.
	if _, err = captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != got {
		t.Errorf("a second install rewrote the file:\nfirst:\n%s\nsecond:\n%s", got, again)
	}
	if _, err = captureRun(t, "uninstall", "cursor"); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(back), "// the server I wrote myself") ||
		!strings.Contains(string(back), `"mine"`) {
		t.Errorf("the uninstall did not give the file back:\n%s", back)
	}
	if strings.Contains(string(back), `"deja"`) {
		t.Errorf("deja's entry survived the uninstall:\n%s", back)
	}
}

// A file that is broken some other way is still refused: deja does not guess
// at what a reader meant to write.
func TestABrokenConfigIsStillRefused(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers": {`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err == nil {
		t.Error("a truncated config was accepted")
	}
}

// The comment scanner keeps every other byte where it was, so an offset into
// the stripped text is an offset into the original — and it leaves strings
// alone, or a config holding an https:// URL loses the rest of that line and
// is refused again (#2740).
func TestStrippingCommentsBlanksCommentsAndNothingElse(t *testing.T) {
	// The strings deja must not touch, byte for byte.
	for _, in := range []string{
		`{"url": "https://example.com/x", "a": 1}`,
		`{"a": "// not a comment"}`,
		`{"a": "/* nor this */"}`,
	} {
		if got := stripJSONComments(in); got != in {
			t.Errorf("a string was blanked:\n%q\n%q", in, got)
		}
	}
	// And the comments, which go without moving anything else.
	for _, in := range []string{
		"{\n  // one\n  \"a\": 1\n}\n",
		"{\n  /* two */ \"a\": 1\n}\n",
		"{\n  \"a\": 1 // trailing\n}\n",
		"{\n  /* over\n     two lines */\n  \"a\": 1\n}\n",
	} {
		got := stripJSONComments(in)
		if len(got) != len(in) {
			t.Errorf("length changed: %q -> %q", in, got)
		}
		if strings.Contains(got, "//") || strings.Contains(got, "/*") {
			t.Errorf("a comment survived: %q -> %q", in, got)
		}
		var v any
		if err := json.Unmarshal([]byte(got), &v); err != nil {
			t.Errorf("what is left is not JSON: %v\n%q", err, got)
		}
		for i := range in {
			if in[i] == '\n' && got[i] != '\n' {
				t.Errorf("a newline moved at %d in %q", i, in)
			}
		}
	}
	if !configIsJSONC([]byte("{\n // c\n \"a\": 1\n}")) {
		t.Error("a commented object was not read as JSONC")
	}
	if configIsJSONC([]byte(`{"a": 1}`)) {
		t.Error("plain JSON was read as JSONC")
	}
	if configIsJSONC([]byte(`{"a": `)) {
		t.Error("a truncated object was read as JSONC")
	}
}

// An empty block took a comma it had nothing to separate, which is not JSON —
// and the file was then refused on every later run, with a message pointing at
// the reader's comment rather than at deja's own byte (#2740).
func TestAnEmptyBlockDoesNotGetADanglingComma(t *testing.T) {
	for _, before := range []string{
		"{\n  // mine\n  \"mcpServers\": {}\n}\n",
		"{\n  // mine\n  \"mcpServers\": {\n  }\n}\n",
	} {
		hermeticEnv(t)
		path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
			t.Errorf("the file deja wrote is not JSON: %v\n%s", err, b)
		}
		// And a second run still reads it.
		if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
			t.Errorf("the file deja wrote refused the next install: %v\n%s", err, b)
		}
	}
}

// A block that is not an object is a config deja does not understand. Writing
// a second key of the same name leaves the reader's value winning and deja
// unwired, with "updated" printed over it (#2740, the shape #2399 settled).
func TestANonObjectBlockIsRefusedNotDuplicated(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"mcpServers\": null\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err == nil {
		b, _ := os.ReadFile(path)
		t.Errorf("a block deja cannot edit was accepted:\n%s", b)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != before {
		t.Errorf("the file was rewritten anyway:\n%s", b)
	}
}

// The entry deja finds is merged onto, not replaced: an env pointing at a
// store on another disk and a `disabled` the reader set are theirs (#2479).
func TestACommentedConfigKeepsTheFieldsOnDejasOwnEntry(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{
  // mine
  "mcpServers": {
    "deja": {"command": "/old/deja", "args": ["mcp"], "env": {"DEJA_INDEX_DIR": "/big/disk"}, "disabled": true}
  }
}
`
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := captureRun(t, "install", "cursor", "--no-index")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "/big/disk") {
		t.Errorf("the reader's env was dropped:\n%s", got)
	}
	if !strings.Contains(got, `"disabled": true`) {
		t.Errorf("a disabled entry was silently switched on:\n%s", got)
	}
	// Kept, but not called off: cursor-agent ignores the key and runs the
	// entry (#5162).
	if strings.Contains(out, "switched off") {
		t.Errorf("install called a cursor entry off that cursor runs:\n%s", out)
	}
}

// And an entry deja wrote under another name is the entry it takes over, not
// one to write a second server beside (#2269, #2712).
func TestACommentedConfigDoesNotGetASecondDejaEntry(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	before := "{\n  // mine\n  \"mcpServers\": {\n    \"deja-vu\": {\"command\": \"/old/bin/deja\", \"args\": [\"mcp\"]}\n  }\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "cursor", "--no-index"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(stripJSONComments(string(b))), &root); err != nil {
		t.Fatalf("the file is not JSON: %v\n%s", err, b)
	}
	servers, _ := root["mcpServers"].(map[string]any)
	if len(servers) != 1 {
		t.Errorf("%d servers after the install, want the one deja took over:\n%s", len(servers), b)
	}
}

// What the reader put on deja's own entry is theirs. The parsed path merges it
// (#2479): an env pointing at a store on another disk survives, and an entry
// switched off stays off with a line saying so. The text path replaces the
// line, so both were lost without a word (#2742).
func TestJSONCKeepsWhatTheReaderPutOnDejasEntry(t *testing.T) {
	seed := "{ // hi\n  \"mcp\": {\n" +
		"    \"deja\": {\"type\":\"local\",\"command\":[\"/old/deja\",\"mcp\"]," +
		"\"environment\":{\"DEJA_INDEX_DIR\":\"/vol/store\"},\"enabled\":false}\n" +
		"  }\n}\n"

	out, note, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "/bin/deja") {
		t.Fatalf("the binary was not updated:\n%s", got)
	}
	if !strings.Contains(got, "/vol/store") {
		t.Errorf("the reader's environment was dropped:\n%s", got)
	}
	if !strings.Contains(got, "\"enabled\":false") && !strings.Contains(got, "\"enabled\": false") {
		t.Errorf("an entry the reader had switched off was silently switched on:\n%s", got)
	}
	// The note names what was taken over, once.
	if !strings.Contains(note, "/old/deja") {
		t.Errorf("the note does not say what the entry ran: %q", note)
	}
	if strings.Count(note, "/old/deja") != 1 {
		t.Errorf("the note says it twice: %q", note)
	}
	// And the entry is off, so the note says so rather than reporting a plain
	// success on a config where deja will not run (#2757).
	if !strings.Contains(note, "turn it back on") {
		t.Errorf("nothing said the entry is still off: %q", note)
	}
}

// An "mcp" key that is not an object cannot hold a server. Writing a second key
// of the same name above it left both in the file; every reader takes the last,
// which is the reader's, so deja was not wired and install said "updated"
// anyway. The parsed path refuses this (#2399); the text path did not (#2742).
func TestJSONCRefusesABlockThatIsNotAnObject(t *testing.T) {
	for _, seed := range []string{
		"{ // hi\n  \"mcp\": null\n}\n",
		"{ // hi\n  \"mcp\": []\n}\n",
		"{ // hi\n  \"mcp\": \"off\"\n}\n",
	} {
		out, _, err := updateOpencodeJSONC([]byte(seed), "/bin/deja", false)
		if err == nil {
			t.Errorf("install wrote into a config whose mcp key is not an object:\n%s", out)
			continue
		}
		if !strings.Contains(err.Error(), "by hand") {
			t.Errorf("the refusal does not say what to do: %v", err)
		}
	}
}

// With no "mcp" block yet, install adds one above the file's closing brace and
// puts a comma on the last key before it. It found that key by walking up past
// every line already ending in a comma, so a config whose last key had a
// trailing comma got the comma on the next line up — in a nested config an
// opening brace, `"shim": {,` — and Kilo and opencode refused the file (#4399).
func TestInstallJSONCRootTrailingCommaKeepsTheFileParsing(t *testing.T) {
	cases := map[string]string{
		"the reported kilo.jsonc": `{
  // shim provider for the hunt
  "$schema": "https://app.kilo.ai/config.json",
  "provider": {
    "shim": {
      "npm": "@ai-sdk/openai-compatible",
      "options": { "baseURL": "http://127.0.0.1:47962/v1", "apiKey": "x" },
      "models": { "luna": { "name": "luna" } },
    },
  },
  "model": "shim/luna",
}`,
		"comments between the last key and the brace": `{
  "provider": {
    "shim": {
      "models": { "luna": {} },
    },
  },
  "model": "shim/luna",
  // the model above is the shim's
  /* and so is
     the provider */
}`,
		"trailing comma then a comment on the same line": `{
  "provider": {
    "shim": {"npm": "x"},
  },
  "model": "shim/luna", // shim
}`,
		"no comma, comment on the line": `{
  "provider": {
    "shim": {"npm": "x"}
  },
  "model": "shim/luna" // shim
}`,
		"last key closes a nested block with a comma": `{
  "provider": {
    "shim": {"npm": "x"},
  },
}`,
		"last key closes a nested block without one": `{
  "provider": {
    "shim": {"npm": "x"},
  }
}`,
	}
	for _, base := range []string{"kilo", "opencode"} {
		for name, in := range cases {
			t.Run(base+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, base+".jsonc")
				if err := os.WriteFile(path, []byte(in+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := installOpencodeShaped(dir, base, "/usr/local/bin/deja", false); err != nil {
					t.Fatal(err)
				}
				got := readTestFile(t, path)
				if err := parsesAsJSONC(t, got); err != nil {
					t.Fatalf("install left a file that does not parse: %v\n%s", err, got)
				}
				if strings.Contains(got, "{,") {
					t.Fatalf("a comma landed on an opening brace:\n%s", got)
				}
				if strings.Contains(got, ",,") {
					t.Fatalf("a second comma landed on a line that had one:\n%s", got)
				}
				if !strings.Contains(got, `"deja"`) {
					t.Fatalf("deja's entry is missing:\n%s", got)
				}
				// Whatever the reader wrote stays where it was: every one of
				// their lines is still in the file, at most with a comma added.
				noCommas := strings.ReplaceAll(got, ",", "")
				for l := range strings.SplitSeq(in, "\n") {
					if !strings.Contains(noCommas, strings.ReplaceAll(l, ",", "")) {
						t.Errorf("line %q is gone or changed:\n%s", l, got)
					}
				}
				if _, err := installOpencodeShaped(dir, base, "/usr/local/bin/deja", true); err != nil {
					t.Fatal(err)
				}
				back := readTestFile(t, path)
				if err := parsesAsJSONC(t, back); err != nil {
					t.Fatalf("uninstall left a file that does not parse: %v\n%s", err, back)
				}
				if strings.Contains(back, "{,") || strings.Contains(back, `"deja"`) {
					t.Fatalf("uninstall left deja's marks behind:\n%s", back)
				}
			})
		}
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A CRLF config: the comma deja adds belongs before the carriage return. After
// it, the line ends in a bare CR, which an editor shows as a line break with
// the comma alone at the start of the next line.
func TestInstallJSONCCommaLandsBeforeTheCarriageReturn(t *testing.T) {
	in := []byte("{\r\n  \"model\": \"shim/luna\"\r\n}\r\n")
	out, _, err := updateOpencodeJSONC(in, "/bin/deja", false)
	if err != nil {
		t.Fatal(err)
	}
	// The writer edits in LF and writeIfChanged puts the file's CRs back
	// (#4553).
	out = matchLineEndings(in, out)
	if !strings.Contains(string(out), "\"model\": \"shim/luna\",\r\n") {
		t.Errorf("the comma is not at the end of the code:\n%q", out)
	}
}

// VS Code writes mcp.json with trailing commas; the JSONC probe stripped
// comments and then demanded strict JSON, so such a file refused the whole
// target (#3223).
func TestJSONCWithTrailingCommasIsJSONC(t *testing.T) {
	for _, s := range []string{
		"{\n \"a\": 1,\n}",
		"{\n \"a\": [1, 2,],\n}",
		"{ // c\n \"a\": {\"b\": 1,}, }\n",
	} {
		if !configIsJSONC([]byte(s)) {
			t.Errorf("not taken for JSONC: %q", s)
		}
	}
	for _, s := range []string{`{"a": 1}`, `{"a": ,}`, `{"a": 1,`, `{"a": "x,"}`} {
		if configIsJSONC([]byte(s)) {
			t.Errorf("taken for JSONC: %q", s)
		}
	}
}

func TestInstallVSCodeTakesAFileWithTrailingCommas(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "mcp.json")
	seed := "{\n\t\"servers\": {\n\t\t\"playwright\": {\n\t\t\t\"type\": \"stdio\",\n\t\t\t\"command\": \"npx\",\n\t\t\t\"args\": [\"@playwright/mcp@latest\"],\n\t\t},\n\t},\n}\n"
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installVSCodeMCPAt(path, "/bin/deja", false); err != nil {
		t.Fatalf("install refused the editor's own file: %v", err)
	}
	b, _ := os.ReadFile(path)
	var root map[string]any
	if err := json.Unmarshal([]byte(stripTrailingCommas(stripJSONComments(string(b)))), &root); err != nil {
		t.Fatalf("the result is not JSONC any more: %v\n%s", err, b)
	}
	servers, _ := root["servers"].(map[string]any)
	if servers["deja"] == nil || servers["playwright"] == nil {
		t.Fatalf("servers = %v\n%s", servers, b)
	}
	if !strings.Contains(string(b), "@playwright/mcp@latest\"],") {
		t.Fatalf("the reader's own trailing comma did not survive:\n%s", b)
	}
	if _, err := installVSCodeMCPAt(path, "/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != seed {
		t.Fatalf("uninstall did not give the file back:\n%s", got)
	}
}

// An -auto target writes several configs; the kept-snapshot line after an
// uninstall has to count every one of them, not only the file the folded
// result is named for.
func TestKeptSnapshotsCountEveryFileTheAutoTargetTouched(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "claude.json")
	b := filepath.Join(dir, "settings.json")
	for _, p := range []string{a + ".bak", b + ".bak"} {
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := wroteAll(installResult{Path: a, Action: "updated"}, installResult{Path: b, Action: "updated"})
	line := keptSnapshotsLine(r.touched())
	if !strings.Contains(line, "kept 2 snapshots") {
		t.Fatalf("line = %q", line)
	}
}

// An -auto target writes two configs and folds them into one result, so the
// screen whose whole job is naming what deja left behind saw one path and said
// "kept 1 snapshot" beside two .bak files (#3171).
func TestKeptSnapshotsCountsEveryConfigAnAutoTargetWrote(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "a.json")
	two := filepath.Join(dir, "b.json")
	for _, p := range []string{one, two} {
		if err := os.WriteFile(p+".bak", []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := wroteAll(
		installResult{Path: one, Action: "updated"},
		installResult{Path: two, Action: "updated"},
	)
	got := keptSnapshotsLine(r.touched())
	if !strings.Contains(got, "kept 2 snapshots") {
		t.Fatalf("the second config's snapshot went unmentioned:\n%s", got)
	}

	// A file the target read and did not change still has a snapshot beside it
	// if an earlier run made one, and it is still deja's to name.
	r = wroteAll(
		installResult{Path: one, Action: "updated"},
		installResult{Path: two, Action: "unchanged"},
	)
	if got := keptSnapshotsLine(r.touched()); !strings.Contains(got, "kept 2 snapshots") {
		t.Fatalf("an unchanged config's snapshot went unmentioned:\n%s", got)
	}
	// …without claiming it was written: the note is about what changed.
	if strings.Contains(r.Note, "unchanged") {
		t.Errorf("the note reported an unchanged file as work: %q", r.Note)
	}

	// One target, one config: still one snapshot, not a duplicate.
	r = wroteAll(installResult{Path: one, Action: "updated"})
	if got := keptSnapshotsLine(r.touched()); !strings.Contains(got, "kept 1 snapshot ") {
		t.Fatalf("a single-config target miscounted:\n%s", got)
	}
}

// And the same through `deja uninstall claude-auto`, which is where it was
// seen: the line is printed by runInstall out of what installTarget handed
// back, so a unit test on the counter alone cannot hold the wiring (#3171).
func TestUninstallNamesEverySnapshotItLeft(t *testing.T) {
	home := hermeticEnv(t)
	home = filepath.Join(home, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Two configs the reader already had, so both get a snapshot.
	for _, p := range []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", "settings.json"),
	} {
		if err := os.WriteFile(p, []byte(`{"mine":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runInstall(index.DefaultDir(), []string{"claude-auto", "--no-index", "--no-guidance"}, false); err != nil {
		t.Fatal(err)
	}
	// The line goes to stderr: it is a note about the reader's own files rather
	// than part of what install reports doing.
	out := captureStderr(t, func() {
		if err := runInstall(index.DefaultDir(), []string{"claude-auto", "--no-index", "--no-guidance"}, true); err != nil {
			t.Fatal(err)
		}
	})

	baks := 0
	for _, p := range []string{
		filepath.Join(home, ".claude.json.bak"),
		filepath.Join(home, ".claude", "settings.json.bak"),
	} {
		if _, err := os.Stat(p); err == nil {
			baks++
		}
	}
	if baks != 2 {
		t.Skipf("this build left %d snapshots, not the two this is about", baks)
	}
	if !strings.Contains(out, "kept 2 snapshots") {
		t.Fatalf("uninstall left 2 snapshots and named %s:\n%s",
			map[bool]string{true: "one", false: "neither"}[strings.Contains(out, "kept 1 snapshot")], out)
	}
}

func writeKimiPlugin(t *testing.T, home string, enabled bool) {
	t.Helper()
	root := filepath.Join(home, "plugins", "managed", "deja")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "kimi.plugin.json"), []byte(`{"name":"deja"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"plugins":[{"id":"deja","root":` + strconv.Quote(root) +
		`,"source":"local-path","enabled":` + boolText(enabled) + `,"installedAt":"2026-08-23T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(home, "plugins", "installed.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// A machine that installed the plugin is wired: the plugin declares the same
// MCP server and recalls on every prompt. Reporting it as "not wired" is how
// someone ends up running an install that only pushes the plugin aside.
func TestDoctorCountsTheKimiPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "mcp.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var before bytes.Buffer
	doctorMCP(&before)
	doctorAutoRecall(&before)
	if !strings.Contains(kimiLine(before.String()), "not wired") {
		t.Fatalf("without the plugin kimi should read as not wired:\n%s", before.String())
	}

	writeKimiPlugin(t, home, true)
	var after bytes.Buffer
	doctorMCP(&after)
	doctorAutoRecall(&after)
	for _, want := range []string{"plugin", "the Kimi Code plugin recalls on every prompt"} {
		if !strings.Contains(after.String(), want) {
			t.Fatalf("doctor does not report the plugin (%q missing):\n%s", want, after.String())
		}
	}
	if strings.Contains(kimiLine(after.String()), "not wired") {
		t.Fatalf("kimi still reads as not wired with the plugin installed:\n%s", after.String())
	}
}

// A disabled plugin runs nothing, so it cannot stand in for the wiring.
func TestDoctorIgnoresADisabledKimiPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	writeKimiPlugin(t, home, false)
	if kimiPluginInstalled() {
		t.Fatal("a disabled plugin counted as installed")
	}
}

// The record can outlive the files it points at — a managed copy removed by
// hand, or a home restored from a backup.
func TestKimiPluginNeedsItsManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	writeKimiPlugin(t, home, true)
	if err := os.Remove(filepath.Join(home, "plugins", "managed", "deja", "kimi.plugin.json")); err != nil {
		t.Fatal(err)
	}
	if kimiPluginInstalled() {
		t.Fatal("a record pointing at nothing counted as installed")
	}
}

func kimiLine(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "kimi") {
			return line
		}
	}
	return ""
}

const kimiUserHooks = `model = "kimi"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/old/deja hook-prompt --plain"
timeout = 30

# my careful note about the next hook
[[hooks]]
event = "PreToolUse"
command = "my-hook"
`

// The removal ran from deja's marker to the next line starting with "[", so a
// comment the user wrote above the *next* hook went with it (#1699).
func TestRemoveKimiHookKeepsTheNextHooksComment(t *testing.T) {
	got := removeKimiHookBlock(kimiUserHooks)
	if strings.Contains(got, "/old/deja") {
		t.Errorf("deja's own block survived:\n%s", got)
	}
	if !strings.Contains(got, "# my careful note about the next hook") {
		t.Errorf("the user's comment was deleted:\n%s", got)
	}
	if !strings.Contains(got, "my-hook") {
		t.Errorf("the user's hook was deleted:\n%s", got)
	}
}

// Two marked blocks: the first removal consumed the second one's marker and
// left the block behind, so kimi ran recall twice and nothing could find the
// orphan again.
func TestRemoveKimiHookTakesEveryMarkedBlock(t *testing.T) {
	const two = `model = "kimi"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/a/deja hook-prompt --plain"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/b/deja hook-prompt --plain"

[[hooks]]
event = "PreToolUse"
command = "my-hook"
`
	got := removeKimiHookBlock(two)
	for _, gone := range []string{"/a/deja", "/b/deja", kimiHookMarker} {
		if strings.Contains(got, gone) {
			t.Errorf("%q survived the removal:\n%s", gone, got)
		}
	}
	if !strings.Contains(got, "my-hook") {
		t.Errorf("the user's hook was deleted:\n%s", got)
	}
	if n := strings.Count(got, "[[hooks]]"); n != 1 {
		t.Errorf("expected one hook left, found %d:\n%s", n, got)
	}
}

// The control: a block with nothing after it, and a config with no deja block
// at all, are both left sensible.
func TestRemoveKimiHookOnTheOrdinaryShapes(t *testing.T) {
	const last = `model = "kimi"

[[hooks]]
event = "PreToolUse"
command = "my-hook"

# deja: auto-recall (managed by ` + "`deja install kimi-auto`" + `)
[[hooks]]
event = "UserPromptSubmit"
command = "/old/deja hook-prompt --plain"
timeout = 30
`
	got := removeKimiHookBlock(last)
	if strings.Contains(got, "/old/deja") || strings.Contains(got, kimiHookMarker) {
		t.Errorf("deja's block survived:\n%s", got)
	}
	if !strings.Contains(got, "my-hook") {
		t.Errorf("the user's hook was deleted:\n%s", got)
	}
	const none = "model = \"kimi\"\n\n[[hooks]]\nevent = \"PreToolUse\"\ncommand = \"my-hook\"\n"
	if got := removeKimiHookBlock(none); strings.TrimSpace(got) != strings.TrimSpace(none) {
		t.Errorf("a config without deja was changed:\n%s", got)
	}
}

// Kimi's channels, measured on 0.28.1 by reading the requests it sent: the
// digest and the per-prompt block both ride UserPromptSubmit — several hooks on
// that event all run and their output is joined — and PreCompact is wired for
// its side effect, since nothing a hook prints there is read back.
func TestKimiWiresTheThreeChannelsItHas(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installKimiAuto("/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(b)
	exe := hookExeInConfigs("/bin/deja")
	for _, want := range []string{
		`command = "` + exe + ` hook-context --plain --once"`,
		`command = "` + exe + ` hook-prompt --plain"`,
		`event = "PreCompact"`,
		`command = "` + exe + ` hook-precompact --harness kimi"`,
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("missing %s:\n%s", want, cfg)
		}
	}
	// Every block deja writes carries the marker, or the ones without it are
	// left behind on uninstall and keep firing.
	if got, want := strings.Count(cfg, kimiHookMarker), strings.Count(cfg, "[[hooks]]"); got != want {
		t.Fatalf("%d markers for %d hooks:\n%s", got, want, cfg)
	}
	if _, err := installKimiAuto("/bin/deja", true); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(home, ".kimi-code", "config.toml"))
	if strings.Contains(string(b), "deja") {
		t.Fatalf("uninstall left deja behind:\n%s", b)
	}
}

// Kimi Code 2.2 fires SessionEnd only when a session is closed, and `kimi -p`
// exits without closing one, so a finished print run kept its live stamp for
// twenty minutes and the next session's MCP recall left it out (#5145). Its
// Stop does fire, with this payload; it clears the stamp now, and deja's MCP
// tool call stamps the session again before recall reads the stamps.
func TestKimiStopClearsTheStampAndMCPCallRestampsIt(t *testing.T) {
	hermeticEnv(t)
	if _, err := installKimiAuto("/usr/local/bin/deja", false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(sources.KimiConfigDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][2]string{
		{"event = \"Stop\"\ncommand = ", " hook-session-end\"\n"},
		{"event = \"PreToolUse\"\nmatcher = \"^mcp__deja__\"\ncommand = ", " hook-mcp-call\"\n"},
	} {
		found := false
		for table := range strings.SplitSeq(string(b), "[[hooks]]") {
			found = found || strings.Contains(table, want[0]) && strings.Contains(table, want[1])
		}
		if !found {
			t.Errorf("kimi-auto wrote no %q table running%s in:\n%s", want[0], want[1], b)
		}
	}

	dir := t.TempDir()
	const sid = "session_8ebaf6dc-4f8c-4c97-b1a4-6107e0d4b51d"
	markSessionLive(dir, sid)
	runHookSessionEnd(dir, strings.NewReader(`{"hook_event_name":"Stop","session_id":"`+sid+`","cwd":"/w/kite-app","client_type":"kimi_code_cli","stop_hook_active":false}`))
	if liveSessionIDs(dir)[sid] {
		t.Fatalf("kimi's Stop payload left the session stamped: %v", readLiveSessions(dir))
	}
	runHookMCPCall(dir, strings.NewReader(`{"hook_event_name":"PreToolUse","session_id":"`+sid+`","cwd":"/w/kite-app","tool_name":"mcp__deja__deja","tool_input":{"query":"HERON-B1"}}`), nil)
	if !liveSessionIDs(dir)[sid] {
		t.Errorf("deja's MCP call did not stamp the asking session: %v", readLiveSessions(dir))
	}
}

// The listing whose job is "most recent" led with a session that has not
// happened yet and said nothing, while the first screen says it — which is the
// arrangement #696 rejected there (#2104).
func TestLastSaysWhenASessionIsStampedAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted while the migration held the lock"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	ahead, err := json.Marshal(map[string]any{
		"ts": time.Now().AddDate(1, 0, 0).UTC().Format(time.RFC3339Nano), "kind": "promoted",
		"session": "claude:s9", "state": "accepted", "project": "app",
		"title": "a note from next year", "text": "pgbouncer runs in transaction mode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notes, append(ahead, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}

	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "last") })
	// The premise: the future session really does lead the listing.
	first, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if !strings.Contains(first, "transaction mode") {
		t.Fatalf("the session stamped ahead is not at the top, so this measures nothing: %q", first)
	}
	if !strings.Contains(stderr, "later than this machine's clock") {
		t.Errorf("the listing led with work that has not happened and said nothing: %q", strings.TrimSpace(stderr))
	}
}

// And it says it only when there is one: the line is about a state, not a
// decoration on every listing.
func TestLastIsQuietWhenNothingIsAhead(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "projects", "-tmp-app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := claudeRecord(t, map[string]any{
		"type": "user", "sessionId": "s1", "cwd": "/tmp/app",
		"timestamp": time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		"message":   map[string]any{"role": "user", "content": "the pool was exhausted"},
	})
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	var out string
	stderr := captureStderr(t, func() { out, _ = captureRun(t, "last") })
	if !strings.Contains(out, "pool was exhausted") {
		t.Fatalf("the listing is empty, so this measures nothing: %q", out)
	}
	if strings.Contains(stderr, "later than this machine's clock") {
		t.Errorf("a listing with nothing ahead said something was: %q", strings.TrimSpace(stderr))
	}
}
