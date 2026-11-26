package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// doctor prints the rule's text, and the text is not its effect: `search
// local-only` reads the same whether it withholds nothing or the whole index,
// while every other surface says what it kept back (#978).
func TestDoctorPolicySaysHowMuchTheRuleWithholds(t *testing.T) {
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
	b, err := json.Marshal(index.SyncRecord{Harness: "claude", SessionID: "peer9", Project: "work/api", Role: "user", Text: "peer work on the vault rotation"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exp, "deja-sync-x.jsonl"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "sync", "import", exp); err != nil {
		t.Fatal(err)
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	var out bytes.Buffer
	doctorPolicy(&out, dir)
	got := out.String()
	if !strings.Contains(got, "withholds 1 of 2 indexed sessions") {
		t.Errorf("doctor does not say what the rule keeps back:\n%s", got)
	}
	// The paths the rule leaves alone must not claim to withhold anything.
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "local+imported") && strings.Contains(line, "withholds") {
			t.Errorf("an unrestricted path was said to withhold rows: %q", line)
		}
	}

	// A rule that matches nothing in this index is the case the count exists
	// to separate from a rule that empties it.
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":true,"imported:nobody":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorPolicy(&out, dir)
	if strings.Contains(out.String(), "withholds") {
		t.Errorf("a rule that hides nothing was counted as withholding:\n%s", out.String())
	}
}

// The text report has had a trust-policy block since #661; `--json` had no key
// for it at all, so a script could not see that recall is off on a machine
// (#1027).
func TestDoctorJSONReportsThePolicy(t *testing.T) {
	tmp := hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","message":{"role":"user","content":"local work on the ticker window"},"timestamp":"2026-07-11T10:00:00Z","sessionId":"loc","cwd":"/proj"}` + "\n"
	if err := os.WriteFile(filepath.Join(store, "loc.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"search":{"local":false,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	got := collectDoctorPolicy(dir)
	if got.State != "active" || got.Path != policyFile {
		t.Errorf("policy state = %q at %q, want active at %q", got.State, got.Path, policyFile)
	}
	if got.Total != 1 {
		t.Errorf("indexed sessions = %d, want 1", got.Total)
	}
	search := got.Activations["search"]
	if search.Rule != "nothing activates" || search.Withheld != 1 {
		t.Errorf("search rule = %+v, want {nothing activates 1}", search)
	}
	if auto := got.Activations["auto"]; auto.Withheld != 0 {
		t.Errorf("a path the rule does not touch withheld %d", auto.Withheld)
	}
}

// The path scan skips a path with a space on purpose: the boundary that ends
// every other path ends that one in the middle. A quoted span has ends that
// can be read exactly, and since #3692 deja quotes its own hook lines where
// the shell needs it — so the file deja wrote is one doctor can answer for.
func TestDoctorNamesAMissingBinaryInsideAQuotedPath(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, "a path with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "deja-hook")
	path := filepath.Join(home, "hooks.json")
	b, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"UserPromptSubmit": []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": hookRun(gone, "hook-prompt")}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaHookCommandMissing(path); got != gone {
		t.Errorf("the missing binary inside a quoted path was not named: %q", got)
	}
	// And a binary that is there is not reported.
	if err := os.WriteFile(gone, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := dejaHookCommandMissing(path); got != "" {
		t.Errorf("a hook whose binary is there was called broken: %q", got)
	}
}

// A quoted path that is not deja's own is not this check's business: the
// subcommand beside it is what says the line is deja's.
func TestDoctorLeavesSomebodyElsesQuotedCommandAlone(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	path := filepath.Join(home, "hooks.json")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"'/opt/their tool/bin/run' --watch"}]}]}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := dejaHookCommandMissing(path); got != "" {
		t.Errorf("a command of somebody else's was reported as deja's: %q", got)
	}
	if strings.Contains(body, "deja") {
		t.Fatal("the fixture names deja, which is not the case under test")
	}
}

// A build called anything but `deja` is what `go build -o /tmp/deja-probe` and
// every install run from it leave behind, and an entry naming one was invisible
// to every check: the row said `wired` because the server key is `deja`, and
// both binary checks stayed silent because the name was unknown (#3659).
func TestDejaCommandInReadsAnEntryUnderDejasOwnKey(t *testing.T) {
	dir := t.TempDir()

	// JSON, the shape most hosts use.
	jsonPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(jsonPath, []byte(`{"mcpServers":{"deja":{"command":"/tmp/probe/deja-cont","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(jsonPath); got != "/tmp/probe/deja-cont" {
		t.Errorf("json: dejaCommandIn = %q, want the command under the deja key", got)
	}

	// YAML, which is where this was found.
	yamlPath := filepath.Join(dir, "config.yaml")
	yaml := "mcp:\n  servers:\n    deja:\n      command: \"/tmp/probe/deja-cont\"\n      args:\n        - mcp\n"
	if err := os.WriteFile(yamlPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(yamlPath); got != "/tmp/probe/deja-cont" {
		t.Errorf("yaml: dejaCommandIn = %q, want the command under the deja key", got)
	}

	// TOML's table header spelling of the same claim.
	tomlPath := filepath.Join(dir, "config.toml")
	toml := "[mcp_servers.deja]\ncommand = \"/tmp/probe/deja-cont\"\nargs = [\"mcp\"]\n"
	if err := os.WriteFile(tomlPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(tomlPath); got != "/tmp/probe/deja-cont" {
		t.Errorf("toml: dejaCommandIn = %q, want the command under the deja key", got)
	}

	// And somebody else's server keeps its own command out of this.
	otherPath := filepath.Join(dir, "other.json")
	if err := os.WriteFile(otherPath, []byte(`{"mcpServers":{"memory":{"command":"/tmp/other/thing"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dejaCommandIn(otherPath); got != "" {
		t.Errorf("another server's command was read as deja's: %q", got)
	}
}

// With the command readable, the report can say what it says about any other
// stale path.
func TestAStrangeNamedBuildIsReported(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if err := os.WriteFile(filepath.Join(dir, "deja"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "scratch", "deja-cont")
	// Everything a test writes is in a temp directory, so the judgement is
	// driven here rather than inherited: one path counts as disposable, the
	// rest do not.
	was := wiringPathIsTemporary
	wiringPathIsTemporary = func(p string) bool { return p == stray }
	t.Cleanup(func() { wiringPathIsTemporary = was })
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stray, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "mcp.json")
	// Marshalled, not pasted: a Windows path glued into a JSON literal makes
	// `\U` and `\A` out of the directory names and the file stops being JSON,
	// which is how this passed everywhere but the windows leg.
	body, err := json.Marshal(map[string]any{
		"mcpServers": map[string]any{"deja": map[string]any{"command": stray, "args": []string{"mcp"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, body, 0o600); err != nil {
		t.Fatal(err)
	}
	note := otherBinaryNote(cfg, "hermes")
	if note == "" || !strings.Contains(note, stray) {
		t.Errorf("note = %q, want it to name %s", note, stray)
	}
}

const screenText = `Harness stores:
  claude       found     ~/.claude/projects  (3 files, 3 indexed sessions)
  37 more stores not found on this machine — ` + "`deja doctor --all`" + ` lists them

MCP wiring:
  claude-code  config missing guidance missing     ~/.claude.json
  gemini       config missing guidance missing     ~/.gemini/settings.json
  zed          wired          guidance missing     ~/.config/zed/settings.json
  grok         config missing guidance missing     ~/.grok/config.toml
               a note that keeps the row

Commands:
  claude-code  missing        ~/.claude/commands/deja.md
  gemini       missing        ~/.agents/skills/deja-history/SKILL.md
               ` + "`skill`" + ` means there is no file of deja's to install: the skill is the command there

Hooks:
  claude-code  missing     ~/.claude/settings.json
  codex-hook   missing     ~/.codex/hooks.json

Index:
  location ~/.cache/deja
  exclusions 0 active patterns
  status   built (size=1 KB)
  last sync 3 records in (2026-10-01 10:00)
`

func screenReport() doctorReport {
	return doctorReport{
		Stores: []doctorStore{{Name: "claude", State: "ok", IndexedSessions: 3}, {Name: "gemini", State: "missing"}},
		MCP: []doctorMCPStatus{
			{Name: "claude-code", State: "config-missing"},
			{Name: "gemini", State: "config-missing"},
			{Name: "zed", State: "wired"},
			{Name: "grok", State: "config-missing"},
		},
	}
}

// On a terminal the rows about agents that are not here fold into one line,
// and everything that says something stays.
func TestDoctorScreenFoldsAgentsNotOnTheMachine(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), false, false, 0)
	for _, want := range []string{
		"3 sessions from 1 store; 1 of 2 agents wired — `deja install --auto`\n",
		"  claude-code  config missing guidance missing     ~/.claude.json\n",
		"  zed          wired",
		"  grok         config missing guidance missing     ~/.grok/config.toml\n               a note that keeps the row\n",
		"  1 more agent not on this machine — `deja doctor --all` lists them\n",
		"  claude-code  missing     ~/.claude/settings.json\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, gone := range []string{"  gemini ", "  codex-hook "} {
		if strings.Contains(got, gone) {
			t.Errorf("%q is not on this machine and still has a row:\n%s", gone, got)
		}
	}
	if n := strings.Count(got, "1 more agent not on this machine"); n != 3 {
		t.Errorf("%d fold lines, want one per section:\n%s", n, got)
	}
	// The only skill row folded, so the note about it goes with it.
	if strings.Contains(got, "`skill` means") {
		t.Errorf("the skill note outlived every skill row:\n%s", got)
	}
}

func TestDoctorScreenAllKeepsEveryRow(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), true, false, 0)
	if !strings.Contains(got, "  gemini       config missing") || !strings.Contains(got, "  gemini       missing") {
		t.Errorf("--all folded a row:\n%s", got)
	}
	if strings.Contains(got, "more agent") {
		t.Errorf("--all printed a fold line:\n%s", got)
	}
}

func TestDoctorScreenShowsMcodeGuidanceAfterInstall(t *testing.T) {
	hermeticEnv(t)
	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installMcodeAuto(exe, false); err != nil {
		t.Fatalf("install: %v", err)
	}

	var text strings.Builder
	doctorMCP(&text)
	report := doctorReport{MCP: collectDoctorMCP()}
	got := doctorScreen(text.String(), report, false, false, 0)
	row := harnessRow(t, got, "mcode")
	if !strings.Contains(row, "guidance written") {
		t.Errorf("mcode row should report guidance written, got: %s", row)
	}
}

func TestDoctorScreenAlignsTheIndexKeys(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), false, false, 0)
	for _, want := range []string{
		"  location   ~/.cache/deja\n",
		"  exclusions 0 active patterns\n",
		"  status     built (size=1 KB)\n",
		"  last sync  3 records in",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A wrap falls at a space, never inside a word or a quoted command, and the
// rest hangs under the row's value column.
func TestDoctorWrapBreaksAtSpacesUnderTheValueColumn(t *testing.T) {
	line := "  timer        not scheduled — `deja install sync-timer` runs `deja sync` every 30 min"
	got := doctorWrap(line, 60)
	if len(got) < 2 {
		t.Fatalf("not wrapped: %q", got)
	}
	words := strings.Fields(line)
	if strings.Join(strings.Fields(strings.Join(got, " ")), " ") != strings.Join(words, " ") {
		t.Errorf("wrap changed the words: %q", got)
	}
	for _, l := range got {
		if len([]rune(l)) > 60 {
			t.Errorf("line over the width: %q", l)
		}
		if strings.Count(l, "`")%2 != 0 {
			t.Errorf("a quoted command was split: %q", l)
		}
	}
	if !strings.HasPrefix(got[1], strings.Repeat(" ", 15)) || got[1][15] == ' ' {
		t.Errorf("continuation not under the value column: %q", got[1])
	}
}

func TestDoctorScreenColoursOnlyTheState(t *testing.T) {
	hermeticEnv(t)
	got := doctorScreen(screenText, screenReport(), false, true, 0)
	if !strings.Contains(got, "  claude       "+statGreen+"found"+statReset+"     ~/.claude/projects") {
		t.Errorf("found is not green:\n%q", got)
	}
	if !strings.Contains(got, statDim+"config missing"+statReset+" guidance "+statDim+"missing"+statReset) {
		t.Errorf("missing is not dim:\n%q", got)
	}
	if strings.Contains(got, "~/.claude"+statReset) {
		t.Errorf("a path was coloured:\n%q", got)
	}
}

const sourcesTSV = "claude\t/h/.claude/projects\tsessions=3 messages=9 size=1.2 KB redacted=1\n" +
	"codex\t/h/.codex/sessions\tsessions=12 messages=40 size=10.5 KB redacted=0\n" +
	"trae\t/h/.trae/cli\tsessions=0 messages=0 size=0 B redacted=0\n" +
	"roo\t\tsessions=0 messages=0 size=0 B redacted=0\n" +
	"grok\t/h/.grok\tsessions=0 messages=0 size=0 B redacted=0\t(cannot be read — permission denied on /h/.grok)\n" +
	"aider\t/h/.aider.chat.history.md\tsessions=0 messages=0 size=0 B redacted=0\tnote=aider writes it in each project\n"

func TestSourcesScreenIsATable(t *testing.T) {
	got := sourcesScreen(sourcesTSV, false, 0)
	want := "store   sessions  messages     size  location\n" +
		"claude         3         9   1.2 KB  /h/.claude/projects  (1 redacted)\n" +
		"codex         12        40  10.5 KB  /h/.codex/sessions\n" +
		"grok           0         0      0 B  /h/.grok  (cannot be read — permission denied on /h/.grok)\n" +
		"3 more stores with no sessions — `deja sources | cat` lists every store\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSourcesScreenWithNothingAnywhere(t *testing.T) {
	got := sourcesScreen("trae\t/h/.trae\tsessions=0 messages=0 size=0 B redacted=0\n", false, 0)
	if got != "no sessions in the 1 store deja reads — `deja sources | cat` lists them\n" {
		t.Errorf("got %q", got)
	}
}

// `2 files, 1 indexed session` has two causes that read the same: a file that
// failed to parse, and two files sharing an id. The manifest knows which, and
// the row did not say (#1101).
func TestDoctorSaysWhenARowCoversTwoTranscripts(t *testing.T) {
	hermeticEnv(t)
	store := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"a.jsonl", "b.jsonl"} {
		rec := `{"type":"user","message":{"role":"user","content":"conversation ` + name + `"},"timestamp":"2026-08-0` + string(rune('1'+i)) + `T10:00:00Z","sessionId":"dup1","cwd":"/proj"}` + "\n"
		if err := os.WriteFile(filepath.Join(store, name), []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorHarnesses(&out, dir)
	row := ""
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "claude ") {
			row = line
		}
	}
	if !strings.Contains(row, "2 files, 1 indexed session") {
		t.Fatalf("the premise changed: %q", row)
	}
	if !strings.Contains(row, "shared by two transcripts") {
		t.Errorf("the row does not say why the counts differ: %q", row)
	}
}

// writeTruncatedSidecar lays down a sidecar with a whole header and a third of
// one vector record after it — a process killed mid-write, which is the failure
// #1319 was about.
func writeTruncatedSidecar(t *testing.T, dir string) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("DJV1")
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))  // version
	_ = binary.Write(&b, binary.LittleEndian, uint16(8))  // dim
	writeSidecarString(&b, "test-model")                  // model
	writeSidecarString(&b, "gen-1")                       // generation
	_ = binary.Write(&b, binary.LittleEndian, uint64(12)) // count
	_ = binary.Write(&b, binary.LittleEndian, uint64(12)) // covered
	b.Write(make([]byte, 6))                              // and then it stopped
	if err := os.WriteFile(embed.Path(dir), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSidecarString(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.LittleEndian, uint32(len(s))) //nolint:gosec // fixture strings are short
	b.WriteString(s)
}

// A sidecar deja cannot parse is a different thing from no sidecar, and every
// other section of the report already says so — sync, policy and a session
// store each have an unreadable state with the reason. Embedding did not, so
// the file that #1319 is about was reported as a file that was never built
// (#1960).
func TestDoctorNamesASidecarItCannotRead(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTruncatedSidecar(t, dir)

	got := collectDoctorEmbed(dir)
	if got == nil {
		t.Fatal("the report has no embedding section at all, so a corrupt sidecar is invisible")
	}
	if got.Sidecar != "unreadable" {
		t.Errorf("sidecar = %q, want unreadable — the file is there and deja cannot parse it", got.Sidecar)
	}
	if got.Error == "" {
		t.Error("nothing says why the sidecar could not be read")
	}

	var out bytes.Buffer
	doctorEmbed(&out, *got)
	// Named as the sidecar, with the reason. "endpoint unreadable" would carry
	// the same word and send the reader after the endpoint instead.
	line := out.String()
	if !strings.Contains(line, "sidecar    unreadable") || !strings.Contains(line, got.Error) {
		t.Errorf("the text report does not name the sidecar and why it failed:\n%s", line)
	}
	if !strings.Contains(line, "endpoint") {
		t.Errorf("the endpoint line is gone, and it is what says whether re-embedding can fix this:\n%s", line)
	}
}

// And silence stays silence: no sidecar and no endpoint is not a fault to
// report, it is a deja nobody has asked to embed anything.
func TestDoctorSaysNothingWhenThereIsNoSidecarAtAll(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Nothing embedded and no endpoint is not a fault: the section is left out
	// entirely, which is what the caller checks for.
	if got := collectDoctorEmbed(dir); got != nil {
		t.Errorf("a deja that was never asked to embed anything reported: %#v", got)
	}
}

// A harness's own bookkeeping is not a transcript deja failed to read. Counting
// it said "11 not recognised here" for qwen and "1" for openclaw on a machine
// whose every session was indexed — the number a reader takes as a parser that
// cannot cope with their store (#3676).
func TestAHarnessOwnStateIsNotAnUnreadTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// The suite points the readers at its own directories, so the two under
	// test are named explicitly rather than inherited from HOME.
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(home, ".qwen"))
	t.Setenv("DEJA_OPENCLAW_ROOT", filepath.Join(home, ".openclaw", "agents"))

	write := func(path, text string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// qwen: one chat, and the three files it keeps beside them.
	chats := filepath.Join(home, ".qwen", "projects", "-work-api", "chats")
	write(filepath.Join(chats, "a.jsonl"),
		`{"uuid":"u1","parentUuid":null,"sessionId":"a","timestamp":"2026-09-17T10:00:00Z","type":"user","cwd":"/work/api","version":"0.20.0","message":{"role":"user","parts":[{"text":"why is the migration slow"}]}}`+"\n")
	write(filepath.Join(chats, "a.runtime.json"), `{"model":"qwen3"}`)
	write(filepath.Join(home, ".qwen", "projects", "-work-api", "meta.json"), `{"cwd":"/work/api"}`)
	write(filepath.Join(home, ".qwen", "projects", "-work-api", "extract-cursor.json"), `{}`)

	// openclaw: one session, and the agent's model catalogue.
	sessions := filepath.Join(home, ".openclaw", "agents", "main", "sessions")
	write(filepath.Join(sessions, "s1.jsonl"),
		`{"type":"session","id":"s1","timestamp":"2026-09-17T10:00:00Z","cwd":"/work/api"}`+"\n")
	write(filepath.Join(sessions, "sessions.json"), `[]`)
	write(filepath.Join(home, ".openclaw", "agents", "main", "agent", "models.json"), `{"models":[]}`)

	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	for line := range strings.SplitSeq(out.String(), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "qwen ") && !strings.HasPrefix(trimmed, "openclaw ") {
			continue
		}
		if strings.Contains(line, "not recognised here") {
			t.Errorf("a store deja reads whole reports unread files: %s", trimmed)
		}
	}
}

// doctor has to be able to say when deja last read this machine's stores, and
// not only when the index was last written. On a machine that imports from a
// peer on a timer those are different days: the import rewrites the index and
// opens no transcript, and every surface that compared against the build time
// reported nothing to do (#3747).
func TestDoctorReportsWhenTheStoresWereLastRead(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_INDEX_DIR")
	root := filepath.Join(t.TempDir(), "claude", "projects", "-work-pool")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Dir(root))
	session := `{"type":"user","sessionId":"aaa1","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"the pool hands out dead connections after a deploy"}}
{"type":"assistant","sessionId":"aaa1","timestamp":"2026-09-01T10:01:00Z","message":{"role":"assistant","content":"the proxy closes idle backends at five minutes"}}
`
	if err := os.WriteFile(filepath.Join(root, "aaa1.jsonl"), []byte(session), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	report := inspectDoctorIndex(dir, nil)
	if report.SourcesReadAt == "" {
		t.Fatal("doctor --json does not say when the stores were read")
	}
	if report.SourcesReadAt == "never" {
		t.Fatalf("a build that read a transcript reports %q", report.SourcesReadAt)
	}
	if _, err := time.Parse(time.RFC3339, report.SourcesReadAt); err != nil {
		t.Errorf("sources_read_at is %q, which is not RFC 3339: %v", report.SourcesReadAt, err)
	}

	// A store this fresh has nothing to report beyond the build time, so the
	// one-line form stays: the second date is for the machines where the two
	// have drifted apart.
	var out bytes.Buffer
	doctorIndex(&out, report, dir)
	if got := out.String(); strings.Contains(got, "stores never read") {
		t.Errorf("the text report calls a store that was just read unread:\n%s", got)
	}
}

// A sqlite3 that is on PATH and prints nothing made doctor say "sqlite3 found"
// above an opencode row that parsed to zero. It now names the binary as broken
// in the tools line, the JSON component, and the store's own state.
func TestDoctorNamesABrokenSQLite3(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stub")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	db := filepath.Join(home, "opencode.db")
	t.Setenv("DEJA_OPENCODE_DB", db)
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	stub := filepath.Join(bin, "sqlite3")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	sources.ResetSQLite3Probe()
	t.Cleanup(sources.ResetSQLite3Probe)
	problem := "sqlite3 at " + stub + " did not answer a probe query (no output)"

	var tools strings.Builder
	doctorTools(&tools)
	if !strings.Contains(tools.String(), "found but not working") || !strings.Contains(tools.String(), "warning      "+problem) {
		t.Errorf("tools line:\n%s", tools.String())
	}
	if c := doctorSQLite3(); c.State != "broken" || c.Path != stub || c.Error != problem {
		t.Errorf("json component = %+v", c)
	}

	var store doctorStore
	for _, check := range doctorStoreChecks() {
		if check.name == "opencode" {
			store, _ = inspectDoctorStore(check)
		}
	}
	if store.State != "needs-sqlite3" || store.Skipped != problem {
		t.Fatalf("opencode store = %+v", store)
	}
	var warn strings.Builder
	printDoctorStoreWarnings(&warn, []doctorStore{store})
	if !strings.Contains(warn.String(), "opencode store needs a working sqlite3 CLI: "+problem) {
		t.Errorf("warning:\n%s", warn.String())
	}
	if got := doctorSQLiteDetail(db, false); !strings.Contains(got, "sqlite3 CLI not working") {
		t.Errorf("row detail = %q", got)
	}
}

// Removing the sqlite3 CLI told the user their harness had changed its format
// and asked them to report it — two lines above deja naming the missing CLI
// itself (#792).
func TestDoctorSeparatesAMissingToolFromAChangedFormat(t *testing.T) {
	for _, name := range []string{"opencode", "cursor", "grok", "hermes", "goose"} {
		if !storeNeedsSQLite3(name) {
			t.Errorf("%s reads through the sqlite3 CLI but is not listed", name)
		}
	}
	for _, name := range []string{"claude", "codex", "gemini", "deja"} {
		if storeNeedsSQLite3(name) {
			t.Errorf("%s does not use sqlite3 but is listed as needing it", name)
		}
	}

	// The state itself, not just its wording: a parser that could not run must
	// not be filed as a store deja could not understand.
	dir := t.TempDir()
	db := filepath.Join(dir, "opencode.db")
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir) // no sqlite3 anywhere on it
	failed := func(string) ([]model.Session, error) {
		return nil, errors.New("exec: \"sqlite3\": executable file not found in $PATH")
	}
	store, _ := inspectDoctorStore(doctorStoreCheck{name: "opencode", paths: []string{db}, files: []string{db}, parse: failed})
	if store.State != "needs-sqlite3" {
		t.Errorf("state = %q, want needs-sqlite3", store.State)
	}
	// A harness that reads plain files is still an unreadable store.
	store, _ = inspectDoctorStore(doctorStoreCheck{name: "claude", paths: []string{db}, files: []string{db}, parse: failed})
	if store.State != "unreadable" {
		t.Errorf("state = %q, want unreadable", store.State)
	}

	var out strings.Builder
	printDoctorStoreWarnings(&out, []doctorStore{
		{Name: "opencode", State: "needs-sqlite3"},
		{Name: "cline", State: "unreadable"},
		{Name: "codex", State: "parsed-zero"},
		{Name: "claude", State: "ok"},
	})
	got := out.String()
	if !strings.Contains(got, "opencode store needs the sqlite3 CLI") {
		t.Errorf("missing tool not named:\n%s", got)
	}
	if strings.Contains(got, "opencode store cannot be read") {
		t.Errorf("a missing tool is still reported as a format change:\n%s", got)
	}
	if !strings.Contains(got, "cline store cannot be read") {
		t.Errorf("a genuinely unreadable store lost its warning:\n%s", got)
	}
	if !strings.Contains(got, "codex files found but newest parsed to zero") {
		t.Errorf("parsed-zero lost its warning:\n%s", got)
	}
	if strings.Contains(got, "claude") {
		t.Errorf("a healthy store was warned about:\n%s", got)
	}
}

// doctor is the surface that answers "where did deja look", so a store it reads
// and never names is a store whose absence from recall cannot be diagnosed.
// Both lists are hand-written; the registry is the one that decides what is
// indexed, so it is what they are checked against (#1738, the shape of #999).
func TestDoctorNamesEveryStoreTheIndexerReads(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := tmp + "/index.db"

	var text bytes.Buffer
	// --all: a store that is simply absent folds into one line otherwise
	// (#4625), and this asks whether doctor can name each one at all.
	if err := runDoctor(&text, []string{"--all"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	inStores := false
	for line := range strings.SplitSeq(text.String(), "\n") {
		if strings.HasPrefix(line, "Harness stores:") {
			inStores = true
			continue
		}
		if inStores {
			if strings.TrimSpace(line) == "" {
				break
			}
			if f := strings.Fields(line); len(f) > 0 {
				rows[f[0]] = true
			}
		}
	}

	var js bytes.Buffer
	if err := runDoctor(&js, []string{"--json"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name string `json:"name"`
		} `json:"stores"`
	}
	if err := json.Unmarshal(js.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	inJSON := map[string]bool{}
	for _, s := range report.Stores {
		inJSON[s.Name] = true
	}

	for _, h := range sources.Registry() {
		if !rows[h.Name] {
			t.Errorf("doctor never names the %s store in its Harness stores block", h.Name)
		}
		if !inJSON[h.Name] {
			t.Errorf("doctor --json never names the %s store", h.Name)
		}
	}
}

// The two forms have to agree about why a store cannot be read: the text row
// names the missing sqlite3 CLI, and the JSON said "unreadable" — the split
// #999 closed for the stores that existed then.
func TestZedSaysWhySqliteIsWhatIsMissing(t *testing.T) {
	tmp := hermeticEnv(t)
	db := sources.ZedDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")
	store, _ := inspectDoctorStore(doctorCheckNamed(t, "zed"))
	if store.State != "needs-sqlite3" {
		t.Errorf("zed without the sqlite3 CLI reports %q, while the text row names the CLI", store.State)
	}
	_ = tmp
}

func doctorCheckNamed(t *testing.T, name string) doctorStoreCheck {
	t.Helper()
	for _, c := range doctorStoreChecks() {
		if c.name == name {
			return c
		}
	}
	t.Fatalf("no doctor store check named %q", name)
	return doctorStoreCheck{}
}

// The text row says a store's sqlite half is unreadable — "sqlite3 CLI missing
// — IDE sessions unavailable" — while the JSON form called the same store ok.
// Two answers about one store is what #999 closed for the states that existed
// then (#1758).
func TestBothFormsAgreeWhenHalfAStoreNeedsSqlite(t *testing.T) {
	tmp := hermeticEnv(t)
	ide := filepath.Join(tmp, "cursor-user", "globalStorage")
	if err := os.MkdirAll(ide, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file that is not a readable database is enough: the point is the CLI,
	// and without it deja cannot tell one from the other.
	if err := os.WriteFile(filepath.Join(ide, "state.vscdb"), bytes.Repeat([]byte("x"), 64), 0o644); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(tmp, "cursor-cli", "projects", "p", "agent-transcripts")
	if err := os.MkdirAll(cli, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"c1","cwd":"/w","timestamp":"2026-08-08T01:00:00Z","message":{"role":"user","content":"snorklewhip"}}` + "\n"
	if err := os.WriteFile(filepath.Join(cli, "t.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CURSOR_ROOT", filepath.Join(tmp, "cursor-user"))
	t.Setenv("DEJA_CURSOR_CLI_ROOT", filepath.Join(tmp, "cursor-cli"))
	t.Setenv("PATH", "")

	var check doctorStoreCheck
	for _, c := range doctorStoreChecks() {
		if c.name == "cursor" {
			check = c
		}
	}
	store, _ := inspectDoctorStore(check)
	if store.State == "ok" {
		t.Errorf("the JSON form calls a store ok while its text row says the sqlite3 CLI is missing")
	}
	if store.State != "needs-sqlite3" {
		t.Errorf("state = %q, want needs-sqlite3", store.State)
	}
}

// Without --all, a store that is simply not on this machine folds into one
// line, and a row with something to say keeps it (#4625).
func TestDoctorFoldsStoresThatAreSimplyAbsent(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := tmp + "/index.db"
	var text bytes.Buffer
	if err := runDoctor(&text, nil, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	stores := text.String()
	if i := strings.Index(stores, "\n\n"); i >= 0 {
		stores = stores[:i]
	}
	if strings.Contains(stores, "codex        missing") {
		t.Errorf("a plainly absent store kept its row:\n%s", stores)
	}
	if !strings.Contains(stores, "not found on this machine — `deja doctor --all` lists them") {
		t.Errorf("no fold line names how to list the folded stores:\n%s", stores)
	}
	// aider's row carries advice for someone who uses it, so it stays.
	if !strings.Contains(stores, "aider        missing") {
		t.Errorf("a missing store with advice was folded:\n%s", stores)
	}

	var all bytes.Buffer
	if err := runDoctor(&all, []string{"--all"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all.String(), "codex        missing") || strings.Contains(all.String(), "doctor --all") {
		t.Errorf("--all did not list every store unfolded:\n%s", all.String())
	}
}

func TestOnlyAbsent(t *testing.T) {
	for detail, want := range map[string]bool{
		"":                  true,
		"0 files":           true,
		"0 CLI transcripts": true,
		"0 stores":          true,
		"notes":             false,
		"1 file":            false,
		"0 files — aider writes .aider.chat.history.md in each project": false,
	} {
		if got := onlyAbsent(detail); got != want {
			t.Errorf("onlyAbsent(%q) = %v, want %v", detail, got, want)
		}
	}
}

// A store deja refuses is the loudest thing doctor can learn, and it used to
// arrive as one word. #1642 is what that costs: a person with an opencode
// database deja would not read, told their format may have changed and asked to
// report it, and nothing in the report said which refusal it was — a renamed
// column, a locked database, or a file that is not a database at all.
func TestDoctorSaysWhyAStoreWouldNotRead(t *testing.T) {
	tmp := hermeticEnv(t)
	db := filepath.Join(tmp, "opencode.db")
	// Not a database. Any refusal will do: what is pinned is that the reason
	// reaches the reader, not which reason it was.
	if err := os.WriteFile(db, []byte("this is not a sqlite file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", db)

	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("2.0.0", false), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "opencode store cannot be read") {
		t.Skipf("this machine's opencode row did not reach the warning:\n%s", got)
	}
	if strings.Contains(got, "its format may have changed") {
		t.Errorf("the warning still says nothing about the refusal:\n%s", got)
	}
}

// The reason is bounded and one line: sqlite3 quotes the query it failed on,
// and that query is a screenful.
func TestAStoreErrorIsOneBoundedLine(t *testing.T) {
	long := "near \"select\": syntax error " + strings.Repeat("select s.id, ", 80)
	got := boundedStoreError(long + "\nwhile preparing the statement")
	if strings.ContainsAny(got, "\r\n") {
		t.Errorf("the reason spans lines: %q", got)
	}
	if len(got) > storeErrorMax+3 {
		t.Errorf("the reason is %d bytes; the bound is %d", len(got), storeErrorMax)
	}
	if !strings.HasPrefix(got, "near \"select\": syntax error") {
		t.Errorf("the head of the message, the part that names the cause, was cut: %q", got)
	}
}

// The row said "N subagent transcripts skipped (DEJA_INCLUDE_SUBAGENTS=1)",
// which names the variable as the reason they were skipped. It is the remedy:
// a reader who wants them indexed has to set it, and the old line told them it
// was already set.
func TestDoctorSaysHowToIndexSubagentTranscripts(t *testing.T) {
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	// The rule is the path, not the isSidechain flag: what deja declines is a
	// file under a subagents/ directory.
	parent := `{"type":"user","sessionId":"11111111-2222-3333-4444-555555555555","cwd":"/w/p","timestamp":"2026-07-22T10:00:00Z","message":{"role":"user","content":"the parent's own turn"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "11111111-2222-3333-4444-555555555555.jsonl"), []byte(parent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	side := `{"type":"user","sessionId":"11111111-2222-3333-4444-555555555555","isSidechain":true,"agentId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","cwd":"/w/p","timestamp":"2026-07-22T10:05:00Z","message":{"role":"user","content":"the subagent's own work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "subagents", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl"), []byte(side), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	// The default reads them as their task and their answer, so the row says
	// which half is searchable and how to get the rest (#3009).
	if !strings.Contains(out, "subagent transcripts read as task, answer and what they changed") {
		t.Fatalf("the row does not say how these files are read:\n%s", out)
	}
	if !strings.Contains(out, "set DEJA_INCLUDE_SUBAGENTS=1 for the whole run") {
		t.Errorf("the row does not say how to index them in full:\n%s", out)
	}
	if strings.Contains(out, "skipped (DEJA_INCLUDE_SUBAGENTS=1)") {
		t.Error("the row still reads as if the variable caused a skip")
	}

	// And with the opt-out set, the row goes back to naming the skip and its
	// remedy — the wording that was wrong before is still the right one there.
	t.Setenv("DEJA_INCLUDE_SUBAGENTS", "0")
	out, err = captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "subagent transcripts skipped") ||
		!strings.Contains(out, "set DEJA_INCLUDE_SUBAGENTS=1 to index them") {
		t.Errorf("with the opt-out set, the row does not name the skip:\n%s", out)
	}
}

// The text report says what arrived from a machine there is no peer row for
// (#2379); `doctor --json` said nothing, so a tool watching sync saw a machine
// that had never exchanged anything (#2382).
func TestDoctorJSONReportsWhatArrivedWithoutAPeerRow(t *testing.T) {
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(root, "-tmp-mine", "m.jsonl"), "minesess", []string{
		`{"type":"user","sessionId":"minesess","timestamp":"2026-08-03T12:00:00Z","message":{"role":"user","content":"my own question"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	importFrom := func(machine string, ids ...string) {
		t.Helper()
		batch := t.TempDir()
		lines := make([]string, 0, len(ids))
		for _, id := range ids {
			lines = append(lines, fmt.Sprintf(
				`{"harness":"claude","session_id":%q,"project":"work/app","role":"user",`+
					`"text":"work from %s","time":%q,"origin":%q}`,
				id, machine, time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC).Format(time.RFC3339), machine))
		}
		if err := os.WriteFile(filepath.Join(batch, "batch.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runSync(dir, []string{"import", batch}); err != nil {
			t.Fatal(err)
		}
	}
	importFrom("laptop.local", "a0", "a1", "a2")
	importFrom("desktop", "b0", "b1")

	raw, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers    []map[string]any `json:"peers"`
			Imported []struct {
				Machine  string `json:"machine"`
				Sessions int    `json:"sessions"`
			} `json:"imported"`
		} `json:"sync"`
	}
	if err = json.Unmarshal([]byte(raw), &report); err != nil {
		t.Fatalf("doctor --json is not JSON: %v", err)
	}
	if len(report.Sync.Peers) != 0 {
		t.Fatalf("a peer row appeared from nowhere: %+v", report.Sync.Peers)
	}
	got := map[string]int{}
	for _, im := range report.Sync.Imported {
		got[im.Machine] = im.Sessions
	}
	if got["laptop.local"] != 3 || got["desktop"] != 2 {
		t.Errorf("the machines that sent work are missing or miscounted: %+v", report.Sync.Imported)
	}

	// A machine that has exchanged nothing says nothing rather than an empty
	// list, so a reader can tell it from a deja too old to report this.
	hermeticEnv(t)
	if err = index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	raw, err = captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, `"imported"`) {
		t.Errorf("a store with no imports carries an imported key:\n%s", raw)
	}
}

// A sync that stops does not announce itself, which is why the text report has
// a Sync section — and `--json`, the one reader that could watch it unattended,
// had no key for it at all (#1838). Same gap the policy block had in #1027.
func TestTheJSONReportCarriesTheSyncState(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	body := `{"peers":[
	{"host":"laptop","last_push":"2026-08-22T10:00:00Z","last_pull":"2026-08-22T10:00:00Z"},
	{"host":"build-box","last_push":"2026-08-20T10:00:00Z","last_error":"ssh build-box: exit status 255"}]}`
	if err := os.WriteFile(peersFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatalf("the report is not JSON: %v\n%s", err, out.String())
	}
	sync, ok := report["sync"].(map[string]any)
	if !ok {
		t.Fatalf("no sync key in the report; keys are %v", reportKeys(report))
	}
	rows, ok := sync["peers"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("want two peers under sync, got %#v", sync["peers"])
	}
	first, _ := rows[0].(map[string]any)
	if first["host"] != "build-box" && first["host"] != "laptop" {
		t.Errorf("a peer row does not name its machine: %#v", first)
	}
	// The thing the section exists for: a peer that keeps failing.
	var failing map[string]any
	for _, r := range rows {
		row, _ := r.(map[string]any)
		if row["host"] == "build-box" {
			failing = row
		}
	}
	if failing == nil {
		t.Fatalf("the failing peer is missing: %#v", rows)
	}
	if msg, ok := failing["last_error"].(string); !ok || !strings.Contains(msg, "255") {
		t.Errorf("the failure a script would watch for is not in the row: %#v", failing)
	}
	if failing["last_pull"] != nil {
		t.Errorf("a machine that has never sent anything claims a pull: %#v", failing)
	}
	if _, ok := failing["last_push"].(string); !ok {
		t.Errorf("the row does not say when this machine last pushed: %#v", failing)
	}
	if _, ok := failing["sessions_from_there"]; !ok {
		t.Errorf("the row does not say how much of this index came from there: %#v", failing)
	}
}

// A machine with no peers still gets the key, so a script can tell "no machines
// configured" from "this deja is too old to say".
func TestTheJSONReportSaysWhenThereAreNoPeers(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_PEERS_FILE", filepath.Join(tmp, "peers.json"))
	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	sync, ok := report["sync"].(map[string]any)
	if !ok {
		t.Fatalf("no sync key on a machine with no peers; keys are %v", reportKeys(report))
	}
	if rows, ok := sync["peers"].([]any); ok && len(rows) != 0 {
		t.Errorf("peers were invented: %#v", rows)
	}
}

// reportKeys names what the report carried, for a failure message.
func reportKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A script reads this to act on it — `deja sync ssh <host>` — so the name goes
// out as written. The encoder escapes a control byte by itself, which is why
// the text report needs a bound here and the JSON does not (#1838).
func TestTheJSONReportNamesThePeerExactly(t *testing.T) {
	tmp := hermeticEnv(t)
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	long := strings.Repeat("w", 300) + ".example.net"
	body, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": long, "last_push": "2026-08-22T10:00:00Z"},
		{"host": "red\x1b[31malert", "last_push": "2026-08-22T10:00:00Z"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peersFile, body, 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	// The bytes on the wire carry no raw escape or rewind: an escape byte is
	// written as the six characters of a \u001b sequence, and this asserts on
	// the bytes rather than on what a terminal would make of them.
	if strings.ContainsAny(out.String(), "\x1b\r") {
		t.Errorf("the encoded report carries a raw escape or rewind")
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host string `json:"host"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	hosts := make([]string, 0, len(report.Sync.Peers))
	for _, p := range report.Sync.Peers {
		hosts = append(hosts, p.Host)
	}
	if len(hosts) != 2 {
		t.Fatalf("want two peers, got %d", len(hosts))
	}
	found := map[string]bool{hosts[0]: true, hosts[1]: true}
	if !found[long] {
		t.Errorf("the long host came back changed, so a script cannot address it: %q", hosts)
	}
	if !found["red\x1b[31malert"] {
		t.Errorf("the host with an escape byte came back changed: %q", hosts)
	}
}

// sessions_from_there is the number that says whether anything ever actually
// arrived from a machine, so it has to be the real count rather than a key.
func TestTheJSONReportCountsWhatArrivedFromEachPeer(t *testing.T) {
	tmp := hermeticEnv(t)
	// A name wider than the column the text report bounds to, so that keying
	// the count on the displayed name instead of the stored one shows up as a
	// zero rather than passing by luck.
	const machine = "mini-with-a-name-longer-than-the-eighty-columns-the-text-report-bounds-a-host-to.example.net"
	t.Setenv("DEJA_MACHINE", machine)
	src := filepath.Join(tmp, "src.db")
	root := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"type":"user","sessionId":"fromMini","cwd":"/proj","timestamp":"2026-08-22T01:00:00Z","message":{"role":"user","content":"the pool keeps running dry on staging"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "a.jsonl"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := index.Ensure(src, "", true, nil); err != nil {
		t.Fatal(err)
	}
	batch := filepath.Join(tmp, "batch")
	if _, err := index.ExportFull(src, batch); err != nil {
		t.Fatal(err)
	}

	// A different machine reads that batch in.
	t.Setenv("DEJA_MACHINE", "laptop")
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	dst := index.DefaultDir()
	if _, err := index.Import(dst, batch); err != nil {
		t.Fatal(err)
	}
	peersFile := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", peersFile)
	peersBody, err := json.Marshal(map[string]any{"peers": []map[string]any{
		{"host": machine, "last_pull": "2026-08-22T10:00:00Z"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(peersFile, peersBody, 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), dst); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host     string `json:"host"`
				Sessions int    `json:"sessions_from_there"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 {
		t.Fatalf("want one peer, got %#v", report.Sync.Peers)
	}
	if got := report.Sync.Peers[0].Sessions; got != 1 {
		t.Errorf("sessions_from_there = %d, want the one session that arrived from that machine", got)
	}
}

// A machine named once and never reached is a row with neither timestamp. Three
// surfaces depend on reading it that way and none of them said so (#1859): the
// text line, the JSON row, and a consumer deciding whether an absent key means
// "never" or "a deja too old to report it".
func TestAPeerNeverReachedIsRecognisableOnBothSurfaces(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	body := `{"peers":[
	{"host":"named-never-reached"},
	{"host":"desk","last_push":"2026-08-24T10:00:00Z"},
	{"host":"receives-only","last_pull":"2026-08-24T10:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	doctorPeers(&text, t.TempDir(), time.Now())
	if !strings.Contains(text.String(), "never exchanged") {
		t.Errorf("the text does not say a machine has never been reached:\n%s", text.String())
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []map[string]any `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]any{}
	for _, r := range report.Sync.Peers {
		host, _ := r["host"].(string)
		rows[host] = r
	}
	if len(rows) != 3 {
		t.Fatalf("want three peers, got %d: %#v", len(rows), report.Sync.Peers)
	}

	never := rows["named-never-reached"]
	if _, ok := never["last_push"]; ok {
		t.Errorf("a machine never reached carries a push: %#v", never)
	}
	if _, ok := never["last_pull"]; ok {
		t.Errorf("a machine never reached carries a pull: %#v", never)
	}
	// The row is still there, which is what separates "never exchanged" from
	// "a deja too old to report peers at all" — that one has no sync key.
	if _, ok := never["sessions_from_there"]; !ok {
		t.Errorf("the row is missing the key that proves it is a row: %#v", never)
	}

	// The two directions stay independent: a machine that only receives keeps
	// its pull and reports no push, which one "never" flag would flatten.
	only := rows["receives-only"]
	if _, ok := only["last_pull"]; !ok {
		t.Errorf("a machine that only receives lost its pull: %#v", only)
	}
	if _, ok := only["last_push"]; ok {
		t.Errorf("a machine that never sent carries a push: %#v", only)
	}
}

// The Sync section of docs/json-output.md described both strings a peer row
// carries as bounded. Only the error is: a host is a name deja is handed back
// to act on — `deja sync ssh <host>` — and a bounded name names no machine
// (#1839). The encoder escapes a control byte on its own, which is why the text
// report needs a bound and this does not.
//
// This pins the two halves the paragraph describes against what deja emits,
// which is the check the section has not had while it grew (#1869).
func TestTheSyncSectionDescribesWhatTheRowsCarry(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	host := "box" + strings.Repeat("y", 400) + "\x1b[31m"
	body, err := json.Marshal(map[string]any{"peers": []map[string]string{{
		"host":       host,
		"last_push":  "2026-08-20T10:00:00Z",
		"last_error": "boom " + strings.Repeat("z", 400),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err = runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host      string `json:"host"`
				LastError string `json:"last_error"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err = json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 {
		t.Fatalf("want one peer, got %#v", report.Sync.Peers)
	}
	row := report.Sync.Peers[0]
	if row.Host != host {
		t.Errorf("the host was altered, so what the JSON hands back is not a name to sync with: %q", row.Host)
	}
	// In columns, which is the bound safeForStatusline applies: a byte count
	// would call a 200-column line of CJK unbounded and a bound written in
	// bytes correct.
	if cols := termwidth.Columns(row.LastError); cols > 201 {
		t.Errorf("the error is unbounded at %d columns; a remote writes it", cols)
	}
	// The escape reaches the reader as an escape sequence in the JSON string,
	// never as a raw byte a terminal would act on.
	if strings.Contains(out.String(), "\x1b") {
		t.Error("a control byte was written into the JSON unescaped")
	}

	// The same bound with a wide script: two columns to the rune, so a bound
	// that counted bytes or runes would cut in the wrong place — and a test
	// fed only ASCII could not tell the three apart.
	wide := strings.Repeat("計", 400)
	if got := termwidth.Columns(safeForStatusline(wide, 200)); got > 201 {
		t.Errorf("a wide-script error came back %d columns wide", got)
	}

	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	// Line breaks are the document's own layout: the claim is about the words.
	prose := strings.Join(strings.Fields(string(doc)), " ")
	if strings.Contains(prose, "Both `host` and `last_error` are written elsewhere") {
		t.Error("docs/json-output.md still says both strings are bounded; the host is reported as written")
	}
}

// writeSyncPlist puts the file install writes there by hand. Nothing is loaded
// into launchd: the suite must never register a live agent (see
// runServiceManager's guard).
func writeSyncPlist(t *testing.T, exe string) {
	t.Helper()
	path := syncAutoPlistPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(syncAutoPlist(exe)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSyncUnit(t *testing.T, exe string) {
	t.Helper()
	dir := syncAutoUnitDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deja-sync.service"), []byte(syncAutoService(exe)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deja-sync.timer"), []byte(syncAutoTimer()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSyncTimerFor(t *testing.T, exe string) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		writeSyncPlist(t, exe)
	case "linux":
		writeSyncUnit(t, exe)
	default:
		t.Skip("no timer on this platform")
	}
}

func doctorSyncSection(t *testing.T, dir string) string {
	t.Helper()
	var b bytes.Buffer
	doctorPeers(&b, dir, time.Now())
	return b.String()
}

// The Sync section exists because a sync that stops does not announce itself.
// The thing that runs the sync was not on it: `install --all` writes a timer
// and nothing afterwards reported on it (#2636).
func TestDoctorSaysTheSyncTimerIsNotScheduled(t *testing.T) {
	if !syncTimerSchedulable(runtime.GOOS) {
		t.Skip("no timer on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	out := doctorSyncSection(t, t.TempDir())
	if !strings.Contains(out, "timer") {
		t.Fatalf("the Sync section never mentions the timer:\n%s", out)
	}
	if !strings.Contains(out, "deja install sync-timer") {
		t.Fatalf("nothing says how to schedule it:\n%s", out)
	}
}

// Scheduled, and pointing at a binary that is gone: deja moved or was upgraded,
// the file kept the old path, and the hourly sync stopped.
func TestDoctorSaysTheSyncTimerPointsAtAMissingBinary(t *testing.T) {
	if !syncTimerSchedulable(runtime.GOOS) {
		t.Skip("no timer on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeSyncTimerFor(t, filepath.Join(home, "gone", "deja"))
	out := doctorSyncSection(t, t.TempDir())
	if !strings.Contains(out, "no longer there") {
		t.Fatalf("a timer pointing at a missing binary reads as healthy:\n%s", out)
	}
}

// Scheduled and pointing at a binary that exists: say so plainly and stop.
func TestDoctorSaysTheSyncTimerIsScheduled(t *testing.T) {
	if !syncTimerSchedulable(runtime.GOOS) {
		t.Skip("no timer on this platform")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	exe := filepath.Join(home, "deja")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSyncTimerFor(t, exe)
	out := doctorSyncSection(t, t.TempDir())
	if !strings.Contains(out, "every 30 min") {
		t.Fatalf("the schedule is not reported:\n%s", out)
	}
	if strings.Contains(out, "no longer there") {
		t.Fatalf("a healthy timer was called broken:\n%s", out)
	}
}

func mcpLine(name, status string) string {
	return fmt.Sprintf("  %-12s %-14s", name, status)
}

func stubLookup(latest string, ok bool) doctorVersionLookup {
	return func() (string, bool) { return latest, ok }
}

func TestDoctorFullReport(t *testing.T) {
	tmp := hermeticEnv(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-proj", "sess.jsonl"), "sess", []string{
		`{"type":"user","sessionId":"sess","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"hello doctor"}}`,
	})
	if err := os.WriteFile(os.Getenv("DEJA_OPENCODE_DB"), []byte("sqlite db bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeJSON := filepath.Join(tmp, "home", ".claude.json")
	if err := os.MkdirAll(filepath.Dir(claudeJSON), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeJSON, []byte(`{"mcpServers":{"deja":{"command":"/bin/deja","args":["mcp"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claudeSettings := filepath.Join(tmp, "home", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(claudeSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeSettings, []byte(`{"hooks":{"PreCompact":[{"matcher":"manual|auto","hooks":[{"type":"command","command":"/bin/deja hook-precompact"}]}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	old := version
	version = "1.0.0"
	defer func() { version = old }()

	var out bytes.Buffer
	if err := runDoctor(&out, nil, stubLookup("9.9.9", true), index.DefaultDir()); err != nil {
		t.Fatalf("runDoctor: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"Harness stores:", "Tools:", "MCP wiring:", "Hooks:", "claude-code", "Index:", "Version:",
		"claude", "opencode", "aider", "gemini", "cursor", "antigravity", "grok", "hermes",
		"1 file", mcpLine("claude-code", "wired"), "config missing",
		"not built", "current  1.0.0", "latest   v9.9.9", "update available",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("report missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("report contains color codes:\n%s", got)
	}
}

func TestDoctorStoreStates(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("chmod permissions are not meaningful on windows or as root")
	}
	tmp := t.TempDir()
	unreadable := filepath.Join(tmp, "unreadable")
	if err := os.Mkdir(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(unreadable, 0o700) }()

	cases := []struct {
		name  string
		check doctorStoreCheck
		want  string
	}{
		{"missing", doctorStoreCheck{name: "x", paths: []string{filepath.Join(tmp, "missing")}}, "missing"},
		// Its own directory: a root holding a locked subdirectory is reported
		// as denied now, which is the point of #802 and not what this case is
		// about.
		{"empty", doctorStoreCheck{name: "x", paths: []string{t.TempDir()}}, "missing"},
		{"denied", doctorStoreCheck{name: "x", paths: []string{unreadable}}, "denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := inspectDoctorStore(tc.check)
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q", got.State, tc.want)
			}
		})
	}

	// Its own directory: tmp still holds the locked one above, and a store root
	// containing it is reported as denied now (#816).
	parseRoot := t.TempDir()
	file := filepath.Join(parseRoot, "session.jsonl")
	// The file has to look like a transcript: a store whose newest file holds
	// no conversation at all is an unused session, not a parse failure.
	if err := os.WriteFile(file, []byte(`{"type":"user","message":{"role":"user","content":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		parse func(string) ([]model.Session, error)
		want  string
	}{
		{"ok", func(string) ([]model.Session, error) { return []model.Session{{ID: "1"}}, nil }, "ok"},
		{"parsed-zero", func(string) ([]model.Session, error) { return nil, nil }, "parsed-zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := inspectDoctorStore(doctorStoreCheck{name: "x", paths: []string{parseRoot}, files: []string{file}, parse: tc.parse})
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q", got.State, tc.want)
			}
		})
	}
}

func TestDoctorStoreChecksIncludeHermes(t *testing.T) {
	hermeticEnv(t)
	for _, check := range doctorStoreChecks() {
		if check.name == "hermes" {
			return
		}
	}
	t.Fatal("Hermes store missing from machine doctor report")
}

func TestDoctorParsesOnlyNewestFile(t *testing.T) {
	tmp := t.TempDir()
	oldPath := filepath.Join(tmp, "old")
	newPath := filepath.Join(tmp, "new")
	for _, path := range []string{oldPath, newPath} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	var parsed []string
	check := doctorStoreCheck{
		name: "x", paths: []string{tmp}, files: []string{newPath, oldPath},
		parse: func(path string) ([]model.Session, error) {
			parsed = append(parsed, path)
			return []model.Session{{ID: "1"}}, nil
		},
	}
	got, _ := inspectDoctorStore(check)
	if got.State != "ok" || len(parsed) != 1 || parsed[0] != newPath {
		t.Fatalf("state=%q parsed=%v, want newest only", got.State, parsed)
	}
}

func TestDoctorJSONGolden(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(tmp, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "project", "session.jsonl"), "session", []string{
		`{"type":"user","sessionId":"session","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"history"}}`,
	})
	t.Setenv("PATH", "")
	oldVersion := version
	version = "1.0.0"
	defer func() { version = oldVersion }()

	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--json"}, stubLookup("2.0.0", true), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	// JSON escapes windows separators as \\, which ToSlash would turn
	// into //; collapse them to / before substituting the temp dir.
	got := strings.ReplaceAll(out.String(), `\\`, `/`)
	got = filepath.ToSlash(got)
	got = strings.ReplaceAll(got, filepath.ToSlash(tmp), "<tmp>")
	// Goose is the one harness whose config lives under a vendor directory on
	// Windows rather than ~/.config, so the golden keeps the posix path and
	// the platform difference is normalised here.
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/Block/goose/config", "<tmp>/home/.config/goose")
	// Zed is the other one: %APPDATA%\Zed on Windows, ~/.config/zed elsewhere.
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/Zed", "<tmp>/home/.config/zed")
	// Reasonix's home: %APPDATA%\reasonix on Windows, ~/.reasonix elsewhere.
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/reasonix", "<tmp>/home/.reasonix")
	// VS Code's User folder: ~/Library/Application Support/Code on macOS and
	// %APPDATA%/Code on Windows both normalise to the posix ~/.config/Code the
	// golden stores.
	got = strings.ReplaceAll(got, "<tmp>/home/Library/Application Support/Code/", "<tmp>/home/.config/Code/")
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/Code/", "<tmp>/home/.config/Code/")
	// Cherry Studio's app data, the same three homes.
	got = strings.ReplaceAll(got, "<tmp>/home/Library/Application Support/CherryStudio", "<tmp>/home/.config/CherryStudio")
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/CherryStudio", "<tmp>/home/.config/CherryStudio")
	// TRAE IDE keeps the same layout as VS Code.
	got = strings.ReplaceAll(got, "<tmp>/home/Library/Application Support/Trae", "<tmp>/home/.config/Trae")
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/Trae", "<tmp>/home/.config/Trae")
	// JetBrains IDEs' config directory, the same three homes.
	got = strings.ReplaceAll(got, "<tmp>/home/Library/Application Support/JetBrains", "<tmp>/home/.config/JetBrains")
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/JetBrains", "<tmp>/home/.config/JetBrains")
	// Devin's store and config both live under %APPDATA%\devin on Windows;
	// normalise the store tree to ~/.local/share/devin and the config files
	// to ~/.config/devin, which is what the golden keeps.
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/devin/cli", "<tmp>/home/.local/share/devin/cli")
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/devin", "<tmp>/home/.config/devin")
	// Orca's userData directory on macOS and Windows normalises to ~/.config/orca.
	got = strings.ReplaceAll(got, "<tmp>/home/Library/Application Support/orca", "<tmp>/home/.config/orca")
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Roaming/orca", "<tmp>/home/.config/orca")
	// Doubao Desktop's user data directory normalises across macOS and Windows to ~/.config/Doubao.
	got = strings.ReplaceAll(got, "<tmp>/home/Library/Application Support/Doubao", "<tmp>/home/.config/Doubao")
	got = strings.ReplaceAll(got, "<tmp>/home/AppData/Local/Doubao/User Data", "<tmp>/home/.config/Doubao")
	// The 4.x store's platform-specific data directory is normalised to the
	// Linux spelling the golden keeps. On Windows Roaming is a second root.
	for _, platform := range []string{
		"<tmp>/home/Library/Application Support/CodeBuddyExtension/Data",
		"<tmp>/home/AppData/Local/CodeBuddyExtension/Data",
		"<tmp>/home/AppData/Roaming/CodeBuddyExtension/Data",
	} {
		got = strings.ReplaceAll(got, platform, "<tmp>/home/.local/share/CodeBuddyExtension/Data")
	}
	wantRaw, err := os.ReadFile(filepath.Join("testdata", "doctor.json"))
	if err != nil {
		t.Fatal(err)
	}
	// The golden may be checked out with CRLF on windows.
	want := strings.ReplaceAll(string(wantRaw), "\r\n", "\n")
	if got != want {
		t.Fatalf("doctor JSON mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestDoctorIndexStates(t *testing.T) {
	hermeticEnv(t)
	if got := inspectDoctorIndex(index.DefaultDir(), nil).State; got != "missing" {
		t.Fatalf("missing index state = %q", got)
	}
	dir := os.Getenv("DEJA_INDEX_DIR")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real index, not two files named like one: a manifest that will not
	// decode is damage by deja's own definition (#735), and since #2292 the
	// report says so — which is the right answer for a fixture like that and
	// the wrong one for the staleness arithmetic this test is about.
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	manifestTime := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "manifest.gob"), manifestTime, manifestTime); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		storeMods []time.Time
		want      string
		stale     int
	}{
		{"ok", []time.Time{manifestTime.Add(-time.Minute)}, "ok", 0},
		{"stale-one", []time.Time{manifestTime.Add(time.Minute)}, "stale", 1},
		{"stale-two", []time.Time{manifestTime.Add(time.Minute), manifestTime.Add(2 * time.Minute)}, "stale", 2},
		{"mixed", []time.Time{manifestTime.Add(-time.Minute), manifestTime.Add(time.Minute), {}}, "stale", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inspectDoctorIndex(index.DefaultDir(), tc.storeMods)
			if got.State != tc.want {
				t.Fatalf("state = %q, want %q", got.State, tc.want)
			}
			if got.StaleStores != tc.stale {
				t.Fatalf("stale stores = %d, want %d", got.StaleStores, tc.stale)
			}
		})
	}
}

func TestDoctorIndexFreshnessOutput(t *testing.T) {
	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "ok", Path: "/tmp/index"}, index.DefaultDir())
	if !strings.Contains(out.String(), "freshness up to date") {
		t.Fatalf("fresh output = %q", out.String())
	}
	out.Reset()
	doctorIndex(&out, doctorIndexReport{State: "stale", Path: "/tmp/index", StaleStores: 3}, index.DefaultDir())
	got := out.String()
	if !strings.Contains(got, "freshness 3 stores changed since deja last read them") {
		t.Fatalf("stale output = %q", got)
	}
	out.Reset()
	doctorIndex(&out, doctorIndexReport{State: "stale", Path: "/tmp/index", StaleStores: 1}, index.DefaultDir())
	if !strings.Contains(out.String(), "freshness 1 store changed since deja last read it") {
		t.Fatalf("singular stale output = %q", out.String())
	}
}

func TestDoctorRejectsUnknownFlag(t *testing.T) {
	if err := runDoctor(io.Discard, []string{"--yaml"}, stubLookup("", false), index.DefaultDir()); err == nil {
		t.Fatal("expected unknown flag error")
	}
}

func TestDoctorParserDispatch(t *testing.T) {
	tmp := t.TempDir()
	history := filepath.Join(tmp, "history.jsonl")
	writeFileMkdir(t, history, `{"session_id":"s","text":"hello","ts":1}`+"\n")
	rollout := filepath.Join(tmp, "rollout-s.jsonl")
	writeFileMkdir(t, rollout, `{"type":"response_item","timestamp":"2026-01-01T00:00:00Z","payload":{"role":"user","content":"hello"}}`+"\n")
	transcript := filepath.Join(tmp, "cursor.jsonl")
	writeFileMkdir(t, transcript, "not json\n")
	db := filepath.Join(tmp, "state.vscdb")
	writeFileMkdir(t, db, "")

	for _, tc := range []struct {
		name string
		path string
		fn   func(string) ([]model.Session, error)
	}{
		{"codex history", history, parseDoctorCodex},
		{"codex rollout", rollout, parseDoctorCodex},
		{"cursor transcript", transcript, parseDoctorCursor},
		{"cursor db", db, parseDoctorCursor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.fn(tc.path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDoctorVersionReportStates(t *testing.T) {
	cases := []struct {
		current string
		latest  string
		ok      bool
		want    string
	}{
		{"1.0.0", "", false, "unknown"},
		{"dev", "2.0.0", true, "dev"},
		{"1.0.0", "2.0.0", true, "update-available"},
		{"2.0.0", "1.0.0", true, "ahead"},
		{"1.0.0", "1.0.0", true, "ok"},
	}
	oldVersion := version
	defer func() { version = oldVersion }()
	for _, tc := range cases {
		version = tc.current
		got := collectDoctorVersion(stubLookup(tc.latest, tc.ok))
		if got.State != tc.want {
			t.Fatalf("version %q latest %q state = %q, want %q", tc.current, tc.latest, got.State, tc.want)
		}
	}
}

func TestDoctorVersionBranches(t *testing.T) {
	cases := []struct {
		current, latest string
		ok              bool
		want            string
	}{
		{"1.0.0", "9.9.9", true, "update available"},
		{"9.9.9", "9.9.9", true, "up to date"},
		{"9.9.9", "1.0.0", true, "ahead of latest release"},
		{"dev", "9.9.9", true, "dev build"},
		{"1.0.0", "", false, "unable to check"},
	}
	for _, tc := range cases {
		old := version
		version = tc.current
		var out bytes.Buffer
		doctorVersion(&out, stubLookup(tc.latest, tc.ok))
		version = old
		if !strings.Contains(out.String(), tc.want) {
			t.Fatalf("version(%q,%q,%v) = %q want %q", tc.current, tc.latest, tc.ok, out.String(), tc.want)
		}
	}
}

func TestDoctorMCPWiringStates(t *testing.T) {
	tmp := hermeticEnv(t)
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	grokHome := filepath.Join(tmp, "grok-home")
	t.Setenv("GROK_HOME", grokHome)

	// wired via JSON
	writeFileMkdir(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"deja":{}}}`)
	// present but not wired (TOML without our block)
	writeFileMkdir(t, filepath.Join(home, ".codex", "config.toml"), "[cli]\nauto_update = false\n")
	// wired via TOML at the Grok home, separate from the session read root
	writeFileMkdir(t, filepath.Join(grokHome, "config.toml"), "[mcp_servers.deja]\ncommand = \"x\"\n")
	// gemini settings.json left absent -> config missing

	var out bytes.Buffer
	doctorMCP(&out)
	got := out.String()
	for _, want := range []string{
		mcpLine("claude-code", "wired"),
		mcpLine("codex", "not wired"),
		mcpLine("grok", "wired"),
		mcpLine("gemini", "config missing"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MCP wiring missing %q:\n%s", want, got)
		}
	}
}

func TestDoctorJSONCWiringFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(path, []byte("{\n  // comment\n  \"mcp\": { \"deja\": {} }\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorJSONWired("mcp")(path) {
		t.Fatal("jsonc with deja should read as wired via fallback")
	}
	if doctorJSONWired("mcp")(filepath.Join(dir, "absent.json")) {
		t.Fatal("absent config must not read as wired")
	}
}

func TestDoctorGuidanceFileState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if doctorFileWired(path) {
		t.Fatal("missing guidance file reported as present")
	}
	if err := os.WriteFile(path, []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !doctorFileWired(path) {
		t.Fatal("written guidance file reported as missing")
	}
}

func TestDoctorDispatchHermetic(t *testing.T) {
	hermeticEnv(t)
	old := doctorLookup
	doctorLookup = stubLookup("9.9.9", true)
	defer func() { doctorLookup = old }()
	out, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatalf("doctor dispatch err=%v", err)
	}
	if !strings.Contains(out, "Harness stores:") || !strings.Contains(out, "latest   v9.9.9") {
		t.Fatalf("doctor dispatch out=%q", out)
	}
}

func TestDoctorOfflineSkipsLookup(t *testing.T) {
	hermeticEnv(t)
	var out bytes.Buffer
	err := runDoctor(&out, []string{"--offline"}, func() (string, bool) {
		t.Fatal("offline doctor performed version lookup")
		return "", false
	}, index.DefaultDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "version: check skipped (offline)") {
		t.Fatalf("offline output = %q", out.String())
	}
}

func TestDoctorOfflineEnvironmentSkipsLookupInJSON(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("DEJA_OFFLINE", "1")
	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--json"}, func() (string, bool) {
		t.Fatal("offline doctor performed version lookup")
		return "", false
	}, index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"state": "offline"`) {
		t.Fatalf("offline JSON = %q", out.String())
	}
}

func writeFileMkdir(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorDeepCleanAndDrift(t *testing.T) {
	hermeticEnv(t)
	src := filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "sess.jsonl")
	writeClaudeFixture(t, src, "sess", []string{
		`{"type":"user","sessionId":"sess","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"deep verify me"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--deep", "--offline"}, nil, dir); err != nil {
		t.Fatalf("clean deep doctor must exit 0: %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "Deep verification:") || !strings.Contains(got, "message count matches its source") {
		t.Fatalf("missing clean deep section:\n%s", got)
	}

	// One transcript gone, its directory still there: the client's cleanup,
	// which the index keeps on purpose — not drift, and not a reason to
	// rebuild, since a rebuild is what would lose it (#2970).
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runDoctor(&out, []string{"--deep", "--offline"}, nil, dir); err != nil {
		t.Fatalf("a transcript the client cleaned up must not fail deep doctor: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "kept") || strings.Contains(out.String(), "orphan-file") {
		t.Fatalf("a kept transcript is reported as drift, or not at all:\n%s", out.String())
	}

	// The whole store gone is drift.
	if err := os.RemoveAll(filepath.Dir(src)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := runDoctor(&out, []string{"--deep", "--offline"}, nil, dir)
	if err == nil || !strings.Contains(err.Error(), "index drift") {
		t.Fatalf("a vanished store must fail deep doctor, err=%v", err)
	}
	if !strings.Contains(out.String(), "orphan-file") || !strings.Contains(out.String(), "deja index --rebuild") {
		t.Fatalf("drift output missing finding or fix hint:\n%s", out.String())
	}
}

func TestDoctorDeepJSON(t *testing.T) {
	hermeticEnv(t)
	writeClaudeFixture(t, filepath.Join(os.Getenv("DEJA_CLAUDE_ROOT"), "-tmp-proj", "s.jsonl"), "s", []string{
		`{"type":"user","sessionId":"s","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"json deep"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--deep", "--json", "--offline"}, nil, dir); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Deep *index.DeepReport `json:"deep"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Deep == nil || got.Deep.FilesChecked != 1 || !got.Deep.Clean() {
		t.Fatalf("json deep report wrong: %#v", got.Deep)
	}
}

func TestDoctorHooksMatchAbsolutePathCommands(t *testing.T) {
	tmp := hermeticEnv(t)
	settings := filepath.Join(tmp, "home", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	// Real installs write the absolute binary path, not the bare subcommand.
	entries := make([]string, 0, len(claudeHookWiring))
	for _, h := range claudeHookWiring {
		entries = append(entries, `"`+h.Event+`":[{"matcher":"`+h.Matcher+
			`","hooks":[{"type":"command","command":"/Users/x/.local/bin/deja `+h.Sub+`"}]}]`)
	}
	body := `{"hooks":{` + strings.Join(entries, ",") + `}}`
	if err := os.WriteFile(settings, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	doctorHooks(&out)
	if !strings.Contains(out.String(), "claude-code  wired") {
		t.Fatalf("absolute-path hooks must count as wired:\n%s", out.String())
	}
}

func TestDoctorCodexHookStates(t *testing.T) {
	tmp := hermeticEnv(t)
	codexHome := filepath.Join(tmp, "home", ".codex")
	t.Setenv("CODEX_HOME", codexHome)
	if err := os.MkdirAll(codexHome, 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	doctorHooks(&out)
	if !strings.Contains(out.String(), "codex-hook   missing") {
		t.Fatalf("missing state wrong:\n%s", out.String())
	}
	// Every event deja installs, so what this measures is the trust state and
	// not whether the wiring is complete.
	entries := make([]string, 0, len(codexHookWiring))
	for _, h := range codexHookWiring {
		entries = append(entries, `"`+h.Event+`":[{"matcher":"","hooks":[{"type":"command","command":"/x/deja `+
			h.Sub+`"}]}]`)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "hooks.json"),
		[]byte(`{"hooks":{`+strings.Join(entries, ",")+`}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("[hooks.state."+strconv.Quote(filepath.Join(codexHome, "hooks.json")+":session_start:0:0")+"]\ntrusted_hash = \"sha256:aa\"\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if !strings.Contains(out.String(), "codex-hook   disabled") || !strings.Contains(out.String(), "re-enable") {
		t.Fatalf("disabled state wrong:\n%s", out.String())
	}

	// A config codex has written but which says nothing about our hook. That is
	// a fresh install, and it is the state where codex silently runs nothing:
	// measured on 0.142.4, `codex exec` produced no hook here, and the same run
	// with --dangerously-bypass-hook-trust produced it. deja used to call this
	// "wired", which is the report that leaves someone believing memory is on.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-5\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if !strings.Contains(out.String(), "codex-hook   untrusted") {
		t.Fatalf("a hook codex has never been shown must not read as wired:\n%s", out.String())
	}

	// And once codex has recorded an opinion, it is wired. deja cannot check the
	// pin — its value is not the hook file's sha256 — and comparing the two
	// reported every working install as untrusted.
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"),
		[]byte("[hooks.state."+strconv.Quote(filepath.Join(codexHome, "hooks.json")+":session_start:0:0")+"]\ntrusted_hash = \"sha256:aa\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorHooks(&out)
	if !strings.Contains(out.String(), "codex-hook   wired") {
		t.Fatalf("a trusted hook must read as wired:\n%s", out.String())
	}
}

func TestHookContextDoesNotBlockOnSilentStdin(t *testing.T) {
	hermeticEnv(t)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close(); _ = r.Close() }()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	done := make(chan error, 1)
	go func() { done <- runHookContext(index.DefaultDir(), false) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hook-context must not hang when the host never closes stdin (codex does this)")
	}
}

// The one mechanism separating local memory from imported was invisible to the
// command whose job is answering "is my setup right" (#661).
func TestDoctorReportsThePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", path)

	var buf bytes.Buffer
	doctorPolicy(&buf, t.TempDir())
	if !strings.Contains(buf.String(), "every origin activates everywhere") {
		t.Errorf("no file: %q", buf.String())
	}

	buf.Reset()
	if err := os.WriteFile(path, []byte("{ oops"), 0o600); err != nil {
		t.Fatal(err)
	}
	doctorPolicy(&buf, t.TempDir())
	// The permissive default is what is in force, and the file reads like a
	// restriction — so saying only "unreadable" would leave the wrong belief.
	for _, want := range []string{"unreadable", "every origin activates everywhere until it parses"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("malformed: %q lacks %q", buf.String(), want)
		}
	}

	buf.Reset()
	body := `{"activations":{"mcp":{"local":false},"nosuch":{"local":false}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	doctorPolicy(&buf, t.TempDir())
	for _, want := range []string{"mcp", "deny local", "ignored", "this rule does nothing"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("valid: %q lacks %q", buf.String(), want)
		}
	}
}

// One command, two answers about the same store: the text row said `found`
// where the machine form said `unreadable`, `unplugged` existed only in the
// text, and cline and roo were in neither the JSON nor the list that decides
// whether this machine has any history at all (#999).
func TestDoctorsTwoFormsAgreeAboutEveryStore(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")

	names := map[string]bool{}
	for _, check := range doctorStoreChecks() {
		names[check.name] = true
	}
	for _, want := range []string{"cline", "roo"} {
		if !names[want] {
			t.Errorf("%s is missing from the store list the JSON form and the history check both read", want)
		}
	}

	// A store nobody can parse says so in both forms.
	oc := filepath.Join(tmp, "opencode.db")
	if err := os.WriteFile(oc, []byte("not a db"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCODE_DB", oc)

	var text bytes.Buffer
	doctorHarnesses(&text, dir)
	row := harnessRow(t, text.String(), "opencode")
	if strings.Contains(row, "found") {
		t.Errorf("the text row calls an unreadable store found: %q", row)
	}

	var out bytes.Buffer
	if err := runDoctor(&out, []string{"--json"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"stores"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, s := range report.Stores {
		states[s.Name] = s.State
	}
	// Without the sqlite3 CLI — which CI on windows does not have — the same
	// store is `needs-sqlite3`; either way it is not a healthy one, and the
	// point is that both forms say the same word.
	switch states["opencode"] {
	case "unreadable", "needs-sqlite3":
	default:
		t.Errorf("the machine form calls it %q", states["opencode"])
	}
	if !strings.Contains(row, states["opencode"]) {
		t.Errorf("the two forms use different words: %q vs %q", row, states["opencode"])
	}

	// An empty notes file is not a store with something in it, in either form.
	notes := filepath.Join(tmp, "notes.jsonl")
	t.Setenv("DEJA_NOTES_FILE", notes)
	if err := os.WriteFile(notes, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runDoctor(&out, []string{"--json"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	for _, s := range report.Stores {
		if s.Name == "deja" && s.State == "ok" {
			t.Errorf("an empty notes file is reported as a store with content")
		}
	}
}

// A harness that changes its layout presents as quietly fewer sessions, no
// error, and a directory size that still looks right — and doctor, the command
// that exists to rule that out, counted only what the parser already saw
// (#701).
func TestUnplacedFiles(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	seen := []string{mk("proj/chats/a.jsonl"), mk("proj/chats/b.jsonl")}
	mk("proj/stray.jsonl")
	mk("proj/other/deeper.json")
	// Not a transcript: extensions deja never reads must not be counted as
	// missed history.
	mk("proj/notes.md")
	mk("proj/db.sqlite")

	if got, _ := unplacedFiles(root, seen, nil); got != 2 {
		t.Errorf("unplaced = %d, want 2", got)
	}
	// Everything accounted for: nothing to report.
	all := slices.Concat(seen, []string{filepath.Join(root, "proj", "stray.jsonl"), filepath.Join(root, "proj", "other", "deeper.json")})
	if got, _ := unplacedFiles(root, all, nil); got != 0 {
		t.Errorf("unplaced = %d with every file seen", got)
	}
	// A root that does not exist is not a complaint.
	if got, _ := unplacedFiles(filepath.Join(root, "nope"), nil, nil); got != 0 {
		t.Errorf("missing root reported %d", got)
	}
	if got, _ := unplacedFiles("", nil, nil); got != 0 {
		t.Errorf("empty root reported %d", got)
	}
}

// The note is only worth printing when something is actually unaccounted for:
// ", 0 not recognised here" on every harness is noise.
func TestDoctorHarnessRowSaysNothingWhenEverythingIsPlaced(t *testing.T) {
	tmp := hermeticEnv(t)
	qwen := filepath.Join(tmp, "qwen", "projects", "proj", "chats")
	if err := os.MkdirAll(qwen, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qwen, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_QWEN_ROOT", filepath.Join(tmp, "qwen"))
	var buf bytes.Buffer
	doctorHarnesses(&buf, t.TempDir())
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(line, "qwen") && strings.Contains(line, "not recognised") {
			t.Errorf("clean store complains: %q", line)
		}
	}

	// One file in the wrong place, and the row says so.
	if err := os.WriteFile(filepath.Join(tmp, "qwen", "projects", "proj", "stray.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	doctorHarnesses(&buf, t.TempDir())
	found := false
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.Contains(line, "qwen") && strings.Contains(line, "1 not recognised here") {
			found = true
		}
	}
	if !found {
		t.Errorf("stray file not reported:\n%s", buf.String())
	}
}

// doctor's job is to rule out the tool failing to read the user's history, so
// its own count must not read as that failure. Everything deja did not parse
// was one number: on this machine that was "1192 not recognised here" for
// claude, of which 596 were subagent transcripts deja is written to leave
// alone, and "482" for codex, of which 452 sat in `.tmp`.
func TestUnplacedSeparatesDeliberateSkipsFromTheUnread(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	read := write("proj/read.jsonl")
	write("proj/session/subagents/agent-1.jsonl")
	write("proj/session/subagents/agent-2.jsonl")
	write(".tmp/scratch.json")
	write("proj/strange.jsonl")

	skipped := func(p string) bool { return filepath.Base(filepath.Dir(p)) == "subagents" }
	unread, byRule := unplacedFiles(root, []string{read}, skipped)
	if byRule != 2 {
		t.Errorf("deliberate skips counted as %d, want 2", byRule)
	}
	// Only the one file that is neither read nor skipped by a rule. The
	// dot-directory is a store's own scratch, not a transcript.
	if unread != 1 {
		t.Errorf("unread counted as %d, want 1", unread)
	}
}

// Without a rule the count behaves as it did, minus the scratch.
func TestUnplacedWithoutARuleStillCounts(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a.jsonl", "b.jsonl", ".cache/c.jsonl"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unread, byRule := unplacedFiles(root, nil, nil)
	if byRule != 0 {
		t.Errorf("skips reported without a rule: %d", byRule)
	}
	if unread != 2 {
		t.Errorf("unread counted as %d, want 2", unread)
	}
}

// "Unplugged" is a claim about the disk, and it was made for every store
// buried more than two levels under a home that is right there: goose, cline,
// pi and deja's own notes read as a vanished volume on any machine that never
// installed them. Cursor's row never said anything else — its location is two
// roots joined for display, which is no path to walk up from.
func TestDoctorDoesNotCallADeepUninstalledStoreUnplugged(t *testing.T) {
	tmp := hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(home, ".local", "share", "goose", "sessions"),
		filepath.Join(home, ".cline", "data", "sessions"),
		filepath.Join(home, ".pi", "agent", "sessions"),
		filepath.Join(home, ".local", "share", "deja", "notes.jsonl"),
	} {
		if storeDiskGone(path) {
			t.Errorf("an uninstalled store was called unplugged: %s", path)
		}
	}
	// Cursor hands doctor two roots joined with ", " — not a path, but still
	// under the home that is there.
	if storeDiskGone(doctorCursorLocation()) {
		t.Error("cursor's joined location was called unplugged")
	}

	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	for _, name := range []string{"goose", "cline", "pi", "cursor", "deja"} {
		if row := harnessRow(t, out.String(), name); strings.Contains(row, "unplugged") {
			t.Errorf("row for an uninstalled %s store: %q", name, row)
		}
	}
}

// Cherry Studio's row named a relative placeholder when the app was not
// installed, and a relative path has no disk to lose: walked up from the
// working directory it read as `unplugged` on every machine without the app.
// No row on a bare home may say it, whatever its location looks like.
func TestDoctorCallsNoStoreOnABareHomeUnplugged(t *testing.T) {
	tmp := hermeticEnv(t)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	// hermeticEnv points zed at a root outside the home; give it its parent
	// so the row reads as an uninstalled store, which is what it is here.
	if err := os.MkdirAll(filepath.Join(tmp, "zed"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	for line := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(line, "unplugged") {
			t.Errorf("row on a bare home: %q", line)
		}
	}
	if row := harnessRow(t, out.String(), "cherrystudio"); !strings.Contains(filepath.ToSlash(row), "CherryStudio/Data/Agents/.claude") {
		t.Errorf("cherrystudio row does not name the app's default store: %q", row)
	}
	if storeDiskGone(filepath.Join("CherryStudio", "Data", "Agents", ".claude")) {
		t.Error("a relative location was called unplugged")
	}
}

// A store whose disk went away is not a store that was deleted, and "missing"
// on a row of transcripts reads as the second thing (#933). What separates the
// two is how much of the way there is gone: a harness that was never installed
// loses one directory and its home is right there.
func TestDoctorTellsAnUnpluggedStoreFromAnUninstalledHarness(t *testing.T) {
	tmp := hermeticEnv(t)
	home := os.Getenv("HOME")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}

	// Never installed: one level missing under a home that is there.
	if storeDiskGone(filepath.Join(home, ".kimi-code", "sessions")) {
		t.Error("an uninstalled harness was called unplugged")
	}
	// Present: nothing missing at all.
	present := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatal(err)
	}
	if storeDiskGone(present) {
		t.Error("a store that is there was called unplugged")
	}
	// Ejected volume: the whole chain is gone.
	if !storeDiskGone(filepath.Join(tmp, "gone-volume", "home", ".claude", "projects")) {
		t.Error("a store on a vanished volume was not called unplugged")
	}

	// And the row says it.
	t.Setenv("HOME", filepath.Join(tmp, "gone-volume", "home"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "gone-volume", "home", ".claude", "projects"))
	var out bytes.Buffer
	doctorHarnesses(&out, filepath.Join(tmp, "index.db"))
	row := harnessRow(t, out.String(), "claude")
	if !strings.Contains(row, "unplugged") {
		t.Errorf("row for a store on a vanished volume: %q", row)
	}
}

// A built index whose directory cannot be read (permissions, a restricted
// mount) was reported as "not built (run `deja warmup`)" — advice that fails
// the same way and hides a build sitting behind a closed door (#1116).
func TestDoctorNamesAnUnreadableIndex(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not deny reads here")
	}
	dir := filepath.Join(t.TempDir(), "idx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file makes it a real, non-empty index directory.
	if err := os.WriteFile(filepath.Join(dir, "manifest.gob"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "missing", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "unreadable") || !strings.Contains(got, "permission denied") {
		t.Errorf("a blocked index was not named unreadable:\n%s", got)
	}
	// And what to do about it. doctor is the command someone runs to find out
	// why nothing is recalled, so the half that names the fix is the half that
	// earns the line — measured, it could be deleted with every test still
	// passing, because "unreadable" alone appears in more than one of doctor's
	// lines. The variable is the unambiguous part of that advice: "permission"
	// appears in the diagnosis too.
	if !strings.Contains(got, "DEJA_INDEX_DIR") {
		t.Errorf("the line does not say what to do about it:\n%s", got)
	}
	if strings.Contains(got, "not built (run `deja warmup`)") {
		t.Errorf("a blocked index was called not-built:\n%s", got)
	}
}

// The same shape one branch over: an index that is behind and cannot be
// written says so, and says what to do. Nothing covered that line either.
func TestDoctorNamesAnIndexItCannotWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorIndex(&out, doctorIndexReport{State: "stale-readonly", Path: dir, StaleStores: 2}, dir)
	got := out.String()

	if !strings.Contains(got, "cannot be written") {
		t.Errorf("a read-only index was not named:\n%s", got)
	}
	if !strings.Contains(got, "DEJA_INDEX_DIR") {
		t.Errorf("the line does not say what to do about it:\n%s", got)
	}
	// And it still says what is stale, or the reader cannot tell whether it
	// matters: two stores behind is a different sentence from none.
	if !strings.Contains(got, "2 stores") {
		t.Errorf("the line does not say how much is behind:\n%s", got)
	}
}

// `update` stages into `.deja-update-*` beside the binary and renames over it;
// a crash between the two strands a whole binary's worth of bytes under a name
// deja itself wrote, and every later run walked past it (#1109).
func TestDoctorNoticesAStrandedUpdateStaging(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable path here")
	}
	dir := filepath.Dir(exe)
	staged := filepath.Join(dir, ".deja-update-audit-probe")
	if err := os.WriteFile(staged, []byte("half a binary"), 0o644); err != nil {
		t.Skipf("cannot write beside the test binary: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(staged) })

	n, got := strandedUpdateStagings()
	if n < 1 {
		t.Errorf("a stranded staging file was not counted (dir %s)", got)
	}
	if got != dir {
		t.Errorf("counted in %s, want %s", got, dir)
	}
	if err := os.Remove(staged); err != nil {
		t.Fatal(err)
	}
	// Nothing left: the line must not appear on a tidy machine.
	if n, _ := strandedUpdateStagings(); n != 0 {
		t.Errorf("a tidy directory reported %d leftovers", n)
	}
}

// stubWiredVersions makes the version reader answer from a table, so a test
// can stand up "an older deja" without building one.
func stubWiredVersions(t *testing.T, running string, table map[string]string) {
	t.Helper()
	savedV, savedR := version, dejaBinaryVersion
	t.Cleanup(func() { version, dejaBinaryVersion = savedV, savedR })
	version = running
	dejaBinaryVersion = func(p string) string { return table[p] }
}

func writeExe(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeWiring(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Wiring that names an older build in full is the plain case: the row said
// `wired`, and every fix since that build was missing from the sessions. Two
// harnesses running the same build are one finding, not two.
func TestOlderWiredBinariesGroupsAnOlderBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	dir := t.TempDir()
	t.Setenv("PATH", filepath.Join(dir, "empty"))
	old := filepath.Join(dir, "old", "deja")
	writeExe(t, old)
	stubWiredVersions(t, "0.21.5", map[string]string{old: "0.21.3"})

	hooks := filepath.Join(dir, "hooks.json")
	writeWiring(t, hooks, `{"command":"`+old+` hook-context"}`)
	mcp := filepath.Join(dir, "mcp.json")
	writeWiring(t, mcp, `{"mcpServers":{"deja":{"command":"`+old+`","args":["mcp"]}}}`)
	files := []wiredFile{{"codex-hook", hooks}, {"cursor", mcp}, {"cursor", mcp}}

	got := olderWiredBinaries(files)
	if len(got) != 1 {
		t.Fatalf("want one older binary, got %+v", got)
	}
	if got[0].path != old || got[0].version != "0.21.3" || strings.Join(got[0].names, ",") != "codex-hook,cursor" {
		t.Errorf("unexpected finding: %+v", got[0])
	}

	// The same version, a newer one, or a source build on either side: quiet.
	for _, c := range []struct{ running, wired string }{
		{"0.21.5", "0.21.5"}, {"0.21.5", "0.22.0"}, {"dev", "0.21.3"}, {"0.21.5", ""},
	} {
		stubWiredVersions(t, c.running, map[string]string{old: c.wired})
		if got := olderWiredBinaries(files); len(got) != 0 {
			t.Errorf("running %s, wired %q: unexpected finding %+v", c.running, c.wired, got)
		}
	}
}

// The launcher is what every hook names, so the question is which binary it
// picks — in the order the installed script lists them, which is how a stale
// ~/.local/bin build kept winning over a newer Homebrew one.
func TestOlderWiredBinariesFollowsTheLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no launcher on windows")
	}
	tmp := hermeticEnv(t)
	t.Setenv("PATH", filepath.Join(tmp, "empty-path"))
	t.Setenv("DEJA_BIN", "")
	saved := launcherWellKnown
	t.Cleanup(func() { launcherWellKnown = saved })
	newer := filepath.Join(tmp, "brew", "deja")
	launcherWellKnown = []string{newer}

	older := filepath.Join(tmp, "local", "deja")
	writeExe(t, older)
	writeExe(t, newer)
	if _, err := writeDejaLauncher(older); err != nil {
		t.Fatal(err)
	}
	stubWiredVersions(t, "0.21.5", map[string]string{older: "0.21.3", newer: "0.21.5"})

	wiring := filepath.Join(tmp, "settings.json")
	writeWiring(t, wiring, `{"command":"`+dejaLauncherPath()+` hook-prompt"}`)
	files := []wiredFile{{"claude-code", wiring}}
	if got := olderWiredBinaries(files); len(got) != 1 || got[0].path != older {
		t.Fatalf("the launcher's pick was not named: %+v", got)
	}

	// DEJA_BIN wins in the script, so it wins here.
	t.Setenv("DEJA_BIN", newer)
	if got := olderWiredBinaries(files); len(got) != 0 {
		t.Errorf("DEJA_BIN points at the current build, yet: %+v", got)
	}
	t.Setenv("DEJA_BIN", "")

	// The old one gone, the launcher falls through to the current build.
	if err := os.Remove(older); err != nil {
		t.Fatal(err)
	}
	if got := olderWiredBinaries(files); len(got) != 0 {
		t.Errorf("the launcher now runs the current build, yet: %+v", got)
	}
}

// An MCP entry naming a bare `deja` runs the PATH's, and a package manager's
// binary gets that manager's command rather than `deja update`, which would
// refuse it.
func TestDoctorOlderBinariesBarePathAndManager(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix paths")
	}
	tmp := hermeticEnv(t)
	keg := filepath.Join(tmp, "Cellar", "deja-vu", "0.18.0", "bin")
	writeExe(t, filepath.Join(keg, "deja"))
	t.Setenv("PATH", keg)
	t.Setenv("DEJA_BIN", "")
	stubWiredVersions(t, "0.21.5", map[string]string{filepath.Join(keg, "deja"): "0.18.0"})

	cursor := filepath.Join(sources.CursorCLIHome(), "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cursor), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWiring(t, cursor, `{"mcpServers":{"deja":{"command":"deja","args":["mcp"]}}}`)
	got := olderWiredBinaries([]wiredFile{{"cursor", cursor}})
	if len(got) != 1 || got[0].version != "0.18.0" {
		t.Fatalf("bare deja on PATH was not followed: %+v", got)
	}

	var out bytes.Buffer
	doctorOlderBinaries(&out)
	line := out.String()
	for _, want := range []string{"v0.18.0", "v0.21.5", "brew upgrade deja-vu"} {
		if !strings.Contains(line, want) {
			t.Errorf("doctor row lacks %q: %s", want, line)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("one older binary should be one row:\n%s", line)
	}
}

// The reader runs a binary only when its build info says it is deja-vu's: a
// config can name anything, and doctor must not execute it on that say-so.
func TestDejaBinaryVersionNeverRunsAStranger(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	p := filepath.Join(dir, "deja")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ntouch "+marker+"\necho deja 9.9.9\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v := readDejaBinaryVersion(p); v != "" {
		t.Errorf("a shell script was read as deja %s", v)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("doctor executed a binary that is not deja's")
	}
}

// Every config deja writes holds an absolute path to the binary. Move it and
// the harness fails to start deja on every session — while doctor printed
// "wired" for a hook that cannot run. The repair from #773 runs from the hook
// path, which is the one path a dead binary cannot reach (#876).
func TestDoctorNamesConfigsPointingAtAMissingBinary(t *testing.T) {
	tmp := hermeticEnv(t)
	cfg := filepath.Join(tmp, "config")
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if err := os.MkdirAll(filepath.Join(cfg, "deja"), 0o700); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(tmp, "old", "deja")
	write := func(exe string) {
		b, err := json.Marshal(wiringState{Version: "dev", Targets: []string{"claude-auto"}, Exe: exe})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(gone)
	var out bytes.Buffer
	doctorWiringExe(&out)
	got := out.String()
	if !strings.Contains(got, gone) || !strings.Contains(got, "which is not there") {
		t.Errorf("doctor says nothing about a binary that moved:\n%s", got)
	}
	if !strings.Contains(got, "deja install claude-auto") {
		t.Errorf("doctor does not say how to fix it:\n%s", got)
	}

	// And the row reaches the command, not only the helper: a doctor that
	// never calls this is a doctor that still says "wired".
	full, err := captureRun(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "which is not there") {
		t.Errorf("`deja doctor` does not mention the moved binary:\n%s", full)
	}

	// A binary that is where the configs say: nothing to report.
	here := filepath.Join(tmp, "deja")
	if err = os.WriteFile(here, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(here)
	out.Reset()
	doctorWiringExe(&out)
	if got := out.String(); got != "" {
		t.Errorf("a healthy wiring was reported as stale: %q", got)
	}

	// Nothing wired at all: no row either.
	b, err := json.Marshal(wiringState{Version: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorWiringExe(&out)
	if got := out.String(); got != "" {
		t.Errorf("an unwired machine got a wiring row: %q", got)
	}
}

// doctor is what someone runs when memory is not working, so it must not go
// quiet on the harnesses they actually have. It read Claude Code's settings
// first and returned early when that file was missing — which is most machines
// — and with it went the wiring table for every other harness: a correctly
// wired qwen showed up nowhere at all.
func TestDoctorShowsEveryHarnessWithoutClaudeCode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(home, "index.db"))

	// A binary that is really there: a hook naming one that is not gets a
	// second line of its own, and this test is about the table, not that.
	exe := filepath.Join(home, "bin", "deja")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installTarget("qwen-auto", exe, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err == nil {
		t.Fatal("this home was supposed to have no Claude Code config")
	}

	var out bytes.Buffer
	doctorHooks(&out)
	got := out.String()

	if !strings.Contains(got, "qwen") {
		t.Fatalf("doctor said nothing about qwen:\n%s", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "qwen") && !strings.Contains(line, "wired") {
			t.Errorf("a wired qwen is reported as %q", strings.TrimSpace(line))
		}
	}
	// And the rest of the table is there to compare against.
	for _, name := range []string{"opencode", "gemini", "goose", "cline"} {
		if !strings.Contains(got, name) {
			t.Errorf("doctor lists no row for %s:\n%s", name, got)
		}
	}
}

// The desktop's MCP server and its hooks do not live in the same file on a
// 3.17+ build: install writes the server to setting.json (#4429) and a second
// copy of the hooks into config.json (#5960), while the runtime loads only
// config.json. zcodeDoctorPath() names setting.json as soon as it exists, so
// both doctor rows can point at a file the desktop never reads. Each row must
// name the live config only when that file really carries its own wiring.

const zcodeLiveHooks = `"hooks":{"enabled":true,"events":{"SessionStart":[{"hooks":[{"type":"command","command":"/opt/bin/deja.exe hook-context --strict","timeout":30}]}]}}`

func setupZCodeDoctorFixture(t *testing.T, config, setting string, marker bool, version [3]int, versionOK bool) (configPath, settingPath string) {
	t.Helper()
	hermeticEnv(t)
	home := os.Getenv("HOME")
	dir := filepath.Join(home, ".zcode", "cli")
	configPath = filepath.Join(dir, "config.json")
	settingPath = filepath.Join(dir, "setting.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if config != "" {
		if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if setting != "" {
		if err := os.WriteFile(settingPath, []byte(setting), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if marker {
		if err := os.MkdirAll(filepath.Join(dir, "migrations"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "migrations", "settings-v1.json"), []byte(`{"schemaVersion":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := zcodeDesktopVersion
	zcodeDesktopVersion = func() ([3]int, bool) { return version, versionOK }
	t.Cleanup(func() { zcodeDesktopVersion = old })
	return configPath, settingPath
}

func TestZCodeDoctorMCPPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string // "" means no config.json
		setting   string // "" means no setting.json
		marker    bool
		version   [3]int
		versionOK bool
		wantPath  string // "config" or "setting"
		wantWired bool
	}{
		{
			name:     "fresh machine keeps the golden setting.json path",
			wantPath: "setting",
		},
		{
			name:      "migrated 3144 runtime reads setting.json",
			setting:   `{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}},"hooks":{"enabled":true,"events":{}}}`,
			marker:    true,
			wantPath:  "setting",
			wantWired: true,
		},
		{
			// zcodeDoctorPath() already names config.json here: setting.json is
			// absent and config.json is there. No deja server in it, so the row
			// must report missing rather than claim the live file is wired.
			name:    "317 live config without a server stays missing",
			config:  `{"mcp":{"servers":{"docs":{"command":"docs-server"}}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
		},
		{
			name:    "317 live config with a hand-wired server is reported there",
			config:  `{"mcp":{"servers":{"deja-vu":{"type":"stdio","command":"node","args":["/opt/deja/bin/deja","mcp"]}}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath:  "config",
			wantWired: true,
		},
		{
			name:    "317 live config with a literal deja key is reported there",
			config:  `{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath:  "config",
			wantWired: true,
		},
		{
			name:    "setting.json present but runtime still uses config.json",
			config:  `{"mcp":{"servers":{"deja-vu":{"command":"node","args":["/opt/deja/bin/deja","mcp"]}}},` + zcodeLiveHooks + `}`,
			setting: `{"mcp":{"servers":{"deja":{"command":"/usr/local/bin/deja","args":["mcp"]}}}}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath:  "config",
			wantWired: true,
		},
		{
			// An unparseable setting.json beside an empty config.json: the live
			// check falls back to zcodeDoctorPath(), and doctorZCodeWired takes
			// its substring fallback rather than crashing on the bad JSON.
			name:     "unparseable setting.json with an empty config.json",
			config:   `{}`,
			setting:  `{"mcp": {"servers": {`,
			wantPath: "setting",
		},
		{
			// The live config itself does not parse: the row must fall back to
			// zcodeDoctorPath() instead of naming a file it cannot read.
			name:    "unparseable live config falls back",
			config:  `{"mcp": {"servers": {`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, setting := setupZCodeDoctorFixture(t, tc.config, tc.setting, tc.marker, tc.version, tc.versionOK)
			got := zcodeDoctorMCPPath()
			want := setting
			if tc.wantPath == "config" {
				want = config
			}
			if got != want {
				t.Errorf("zcodeDoctorMCPPath() = %q, want %q", got, want)
			}
			if wired := doctorZCodeWired(got); wired != tc.wantWired {
				t.Errorf("doctorZCodeWired(%q) = %v, want %v", got, wired, tc.wantWired)
			}
		})
	}
}

func TestZCodeDoctorHooksPath(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		setting   string
		marker    bool
		version   [3]int
		versionOK bool
		wantPath  string
		wantRun   bool // zcodeHooksRun on the reported path
	}{
		{
			name:     "fresh machine keeps the golden setting.json path",
			wantPath: "setting",
		},
		{
			name:     "migrated 3144 runtime reads setting.json",
			setting:  `{"mcp":{"servers":{}},` + zcodeLiveHooks + `}`,
			marker:   true,
			wantPath: "setting",
			wantRun:  true,
		},
		{
			name:    "317 live config with deja hooks is reported there",
			config:  `{"mcp":{"servers":{}},` + zcodeLiveHooks + `}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
			wantRun:  true,
		},
		{
			name:    "setting.json present but runtime still uses config.json",
			config:  `{"mcp":{"servers":{}},` + zcodeLiveHooks + `}`,
			setting: `{"mcp":{"servers":{}},"hooks":{"enabled":true,"events":{"SessionStart":[{"hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
			wantRun:  true,
		},
		{
			// A live config without deja's hooks must not be named as wired:
			// fall back to zcodeDoctorPath() (config.json here, still unwired).
			name:    "317 live config without deja hooks stays on the fallback",
			config:  `{"mcp":{"servers":{}},"hooks":{"enabled":true,"events":{"PreToolUse":[{"hooks":[{"type":"command","command":"/tmp/mine.sh","timeout":10}]}]}}}`,
			version: [3]int{3, 17, 2}, versionOK: true,
			wantPath: "config",
		},
		{
			// An unparseable setting.json beside an empty config.json, the same
			// shape as the MCP row's fallback case: neither row should name a
			// live config that carries no deja wiring.
			name:     "unparseable setting.json with an empty config.json",
			config:   `{}`,
			setting:  `{"mcp": {"servers": {`,
			wantPath: "setting",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, setting := setupZCodeDoctorFixture(t, tc.config, tc.setting, tc.marker, tc.version, tc.versionOK)
			got := zcodeDoctorHooksPath()
			want := setting
			if tc.wantPath == "config" {
				want = config
			}
			if got != want {
				t.Errorf("zcodeDoctorHooksPath() = %q, want %q", got, want)
			}
			b, _ := os.ReadFile(got)
			if run := zcodeHooksRun(b); run != tc.wantRun {
				t.Errorf("zcodeHooksRun(%q) = %v, want %v", got, run, tc.wantRun)
			}
		})
	}
}

// Two stores, one row. The file list carries the CLI database as well as the
// transcripts, and the transcript reader answers zero for a database — so the
// store probe has to be handed the transcripts alone or the row reports
// `parsed-zero` about a store whose sessions deja has just indexed (#3675).
func TestZCodeRowCountsTranscriptsAndNamesTheDatabase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".zcode", "projects", "--work--")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"role":"user","content":"hello","timestamp":"2026-09-17T10:00:00Z","sessionId":"s1"}` + "\n"
	if err := os.WriteFile(filepath.Join(root, "s1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	db := sources.ZCodeDB()
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	// The file being there is what the row reports; its contents are the
	// reader's business and have their own test in internal/sources.
	if err := os.WriteFile(db, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := len(sources.ZCodeTranscriptFiles()); got != 1 {
		t.Fatalf("transcripts = %d, want 1 — the database must not be in that list", got)
	}
	named := false
	for _, p := range sources.ZCodeSessionFiles() {
		if p == db {
			named = true
		}
	}
	if !named {
		t.Error("the ingest file list does not name the database")
	}

	var out bytes.Buffer
	doctorHarnesses(&out, t.TempDir())
	for line := range strings.SplitSeq(out.String(), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "zcode ") {
			continue
		}
		if strings.Contains(line, "parsed-zero") {
			t.Errorf("the row calls a readable store broken: %s", line)
		}
		if !strings.Contains(line, "CLI store present") {
			t.Errorf("the row does not say the database is there: %s", line)
		}
		return
	}
	t.Fatalf("no zcode row:\n%s", out.String())
}

// Zed's entry can name a binary or defer to an extension. Deferring to an
// extension that is not installed — a dangling symlink into a scratch
// directory is how it happened — leaves an enabled server with nothing behind
// it, and the row said `wired` because the id was in the file (#3660).
func TestZedRowSaysWhenNothingCanStart(t *testing.T) {
	if runtime.GOOS != "darwin" {
		// The extension directory is per-platform and the point of the test is
		// the pair of answers, not the path: check it where it was found.
		t.Skip("extension path is platform-specific; exercised on darwin")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	settings := filepath.Join(home, "settings.json")
	deferring := `{
  "context_servers": {
    "deja-context-server": {
      "enabled": true,
      "remote": false,
      "settings": {}
    }
  }
}
`
	if err := os.WriteFile(settings, []byte(deferring), 0o600); err != nil {
		t.Fatal(err)
	}
	note := zedUnreachableNote(settings)
	if note == "" {
		t.Fatal("an entry deferring to a missing extension was reported as fine")
	}
	if !strings.Contains(note, "deja install zed") {
		t.Errorf("the note does not say how to fix it: %q", note)
	}

	// A dangling symlink is the case this exists for: it is present in a
	// listing and resolves to nothing.
	installed := filepath.Join(home, "Library", "Application Support", "Zed", "extensions", "installed")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "gone"), filepath.Join(installed, zedServerID)); err != nil {
		t.Fatal(err)
	}
	if zedUnreachableNote(settings) == "" {
		t.Error("a dangling extension symlink was read as installed")
	}

	// With the extension actually there, nothing to say.
	if err := os.Remove(filepath.Join(installed, zedServerID)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(installed, zedServerID), 0o755); err != nil {
		t.Fatal(err)
	}
	if note := zedUnreachableNote(settings); note != "" {
		t.Errorf("an installed extension was reported as missing: %q", note)
	}

	// And an entry that names its own command answers for itself, extension or
	// not — that is what `deja install zed` writes.
	own := filepath.Join(home, "own.json")
	if err := os.WriteFile(own, []byte(`{"context_servers":{"deja-context-server":{"command":"/usr/local/bin/deja","args":["mcp"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(installed, zedServerID)); err != nil {
		t.Fatal(err)
	}
	if note := zedUnreachableNote(own); note != "" {
		t.Errorf("an entry with its own command was reported as needing an extension: %q", note)
	}
}
