package main

import (
	"bytes"
	"github.com/vshulcz/deja-vu/internal/index"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// goose skips a relative GOOSE_PATH_ROOT or XDG_CONFIG_HOME for its default,
// so deja writing under one wired a config goose never opens (#4285).
func TestGooseConfigDirSkipsARelativeRoot(t *testing.T) {
	hermeticEnv(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GOOSE_PATH_ROOT", filepath.Join("rel", "root"))
	if d := gooseConfigDirFor("linux"); !filepath.IsAbs(d) {
		t.Errorf("a relative GOOSE_PATH_ROOT gave %q", d)
	}
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join("rel", "config"))
	if d, want := gooseConfigDirFor("linux"), filepath.Join(homeDir(), ".config", "goose"); d != want {
		t.Errorf("a relative XDG_CONFIG_HOME gave %q, want the default %q", d, want)
	}
	abs := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", abs)
	if d := gooseConfigDirFor("linux"); d != filepath.Join(abs, "goose") {
		t.Errorf("an absolute XDG_CONFIG_HOME gave %q", d)
	}
}

// On Windows goose's config is under %APPDATA%\Block\goose\config whatever
// XDG_CONFIG_HOME says: etcetera's Windows strategy never reads it, and Git
// Bash or scoop setups export it (#4286). GOOSE_PATH_ROOT still wins.
func TestGooseConfigDirOnWindowsIgnoresXDG(t *testing.T) {
	hermeticEnv(t)
	appData := filepath.Join(t.TempDir(), "AppData")
	t.Setenv("APPDATA", appData)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	t.Setenv("GOOSE_PATH_ROOT", "")
	if d, want := gooseConfigDirFor("windows"), filepath.Join(appData, "Block", "goose", "config"); d != want {
		t.Errorf("windows config dir = %q, want %q", d, want)
	}
	root := t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", root)
	if d := gooseConfigDirFor("windows"); d != filepath.Join(root, "config") {
		t.Errorf("GOOSE_PATH_ROOT on windows gave %q", d)
	}
}

// deja used to write ~/.config/goose/.goosehints and now writes AGENTS.md; the
// retired file goes only when it holds nothing but deja's block. A person's
// own hints there were deleted on every turn (#3196).
func TestRetiredGooseHintsKeepsThePersonsOwnFile(t *testing.T) {
	hermeticEnv(t)
	path := retiredGooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	own := "my own hints, written by the user\n"
	if err := os.WriteFile(path, []byte(own+gooseRecallStart+"\nrecall\n"+gooseRecallEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the person's file is gone: %v", err)
	}
	if strings.TrimRight(string(got), "\n") != strings.TrimRight(own, "\n") {
		t.Fatalf("file = %q, want the person's hints alone", got)
	}
	// A file with no deja block at all is not touched either.
	if err := os.WriteFile(path, []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != own {
		t.Fatalf("a file with no deja block was changed: %q", got)
	}
	// deja's block alone: the file goes, as before.
	if err := os.WriteFile(path, []byte(gooseRecallStart+"\nrecall\n"+gooseRecallEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := dropRetiredGooseHints(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a file holding only deja's block was kept")
	}
}

// The goose hooks are the two doors #2183's guard cannot see, because they live
// beside the installer rather than in `hook_*.go`. Both threw the payload away
// and recalled from wherever the process stood, so a host that runs its hooks
// from a plugin directory rather than the project got an empty file and no
// reason why. Goose's own payload fields are not documented to include cwd, so
// this is the door being right about a field it is handed, not a fix that
// changes goose's behaviour today (#2187).
func TestTheGooseHooksRecallForTheProjectTheirPayloadNames(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	at := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "beta", "one.jsonl"), "beta1", []string{
		`{"type":"user","sessionId":"beta1","timestamp":"` + at +
			`","message":{"role":"user","content":"the beta work: the kafka consumer keeps rebalancing"}}`,
	})
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	beta := filepath.Join(base, "tmp", "beta")
	elsewhere := filepath.Join(base, "plugin-dir")
	for _, d := range []string{beta, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	moim := filepath.Join(base, "moim.md")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	recalled := func() string {
		b, err := os.ReadFile(moim)
		if err != nil {
			return ""
		}
		return string(b)
	}

	// The premise: standing in the project, the session-start hook recalls it.
	t.Chdir(beta)
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "s", "cwd": beta}))
	if err := cmdGooseHook("", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recalled(), "rebalancing") {
		t.Fatalf("the hook recalled nothing from inside the project, so this measures nothing:\n%s", recalled())
	}
	if err := os.Remove(moim); err != nil {
		t.Fatal(err)
	}

	// The case: the hook runs where the host put it, and the payload is the
	// only thing that names the project.
	t.Chdir(elsewhere)
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "s", "cwd": beta}))
	if err := cmdGooseHook("", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recalled(), "rebalancing") {
		t.Errorf("the session-start hook ignored the project its payload named:\n%s", recalled())
	}

	// And the prompt half, which re-encodes a payload of its own and used to
	// drop the project on the way.
	if err := os.Remove(moim); err != nil {
		t.Fatal(err)
	}
	payload := hookPayload(t, map[string]string{
		"session_id": "s", "cwd": beta, "message": "why does the kafka consumer keep rebalancing",
	})
	if err := refreshGooseForPrompt(index.DefaultDir(), []byte(payload)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recalled(), "rebalanc") {
		t.Errorf("the prompt hook ignored the project its payload named:\n%s", recalled())
	}
}

// YAML lets a mapping be indented by any consistent amount, and the writer
// used to assume two spaces: a four-space block took our entry as a sibling at
// two, which made the user's own extension a key inside ours, and the uninstall
// that followed removed both (#2614).
func TestInstallGooseFollowsTheIndentTheBlockUses(t *testing.T) {
	dir := gooseHomeForTest(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "GOOSE_MODEL: gpt-5\nextensions:\n    mine:\n        enabled: true\n"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installGoose("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	conf := gooseConf(t, dir)
	if !strings.Contains(conf, "\n    deja:\n") {
		t.Fatalf("our entry is not at the indent the block uses, so the extension after it is nested in ours:\n%s", conf)
	}
	if !strings.Contains(conf, "\n    mine:\n        enabled: true\n") {
		t.Fatalf("the user's extension is no longer an extension:\n%s", conf)
	}
	if _, err := installGoose("/bin/deja", true); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	conf = gooseConf(t, dir)
	if strings.Contains(conf, "deja") {
		t.Fatalf("uninstall left our entry:\n%s", conf)
	}
	if conf != existing {
		t.Fatalf("uninstall did not give the config back as it was:\nwant:\n%s\ngot:\n%s", existing, conf)
	}
}

// A goose session that came back from both of that harness's stores is one
// conversation, so the id-collision warning is wrong about it — but the
// per-harness line still counted it twice, and the totals that reconcile the
// two used to print only alongside that warning (#2066, #1091).
func TestAMigratedGooseSessionSaysTheTotalsWithoutTheWarning(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not available")
	}
	hermeticEnv(t)
	root := filepath.Join(t.TempDir(), "goose")
	sessions := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_GOOSE_ROOT", root)
	db := filepath.Join(sessions, "sessions.db")
	t.Setenv("DEJA_GOOSE_DB", db)

	jsonl := `{"id":"20260101_120000","description":"pgbouncer pool","working_dir":"/tmp/app"}
{"role":"user","content":[{"type":"text","text":"why does pgbouncer time out"}],"created":1767268800}
`
	if err := os.WriteFile(filepath.Join(sessions, "20260101_120000.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatal(err)
	}
	sql := `create table sessions (id text primary key, name text, description text, working_dir text, created_at text, updated_at text);
create table messages (id integer primary key autoincrement, session_id text, role text, content_json text, created_timestamp integer);
insert into sessions values ('20260101_120000','n','pgbouncer pool','/tmp/app','2026-01-01 12:00:00','2026-01-01 12:05:00');
insert into messages values (1,'20260101_120000','user','[{"type":"text","text":"why does pgbouncer time out"}]',1767268800);
insert into messages values (2,'20260101_120000','user','[{"type":"text","text":"and after the restart"}]',1767269100);`
	if out, err := exec.CommandContext(t.Context(), "sqlite3", db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 seed: %v %s", err, out)
	}

	var err error
	stderr := captureStderr(t, func() { _, err = captureRun(t, "index") })
	if err != nil {
		t.Fatal(err)
	}
	// The premise: the per-harness line really did count both copies, so the
	// reconciling line has something to reconcile.
	if !strings.Contains(stderr, "goose: 2 sessions") {
		t.Fatalf("the two stores were not both read, so this measures nothing: %q", stderr)
	}
	if strings.Contains(stderr, "an id with another transcript") {
		t.Errorf("a migrated session was reported as an id collision: %q", strings.TrimSpace(stderr))
	}
	if !strings.Contains(stderr, "count transcripts, not rows") {
		t.Errorf("the per-harness line says 2 sessions and nothing says the index holds 1: %q", strings.TrimSpace(stderr))
	}
}

// goose looks for user plugins under $GOOSE_PATH_ROOT/.agents/plugins when
// that is set (config/paths.rs get_dir), not under the home directory, so a
// hook written to ~/.agents/plugins never ran (#4569).
func TestGooseAutoHookFollowsGoosePathRoot(t *testing.T) {
	hermeticEnv(t)
	root := t.TempDir()
	t.Setenv("GOOSE_PATH_ROOT", root)
	exe := filepath.Join(t.TempDir(), "deja")
	if _, err := installTarget("goose-auto", exe, false); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, ".agents", "plugins", "deja", "hooks", "hooks.json")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("no hook where goose looks with GOOSE_PATH_ROOT set: %v", err)
	}
	home := filepath.Join(homeDir(), ".agents", "plugins", "deja")
	if isRealDir(home) {
		t.Errorf("hook written under the home directory, which goose does not read with GOOSE_PATH_ROOT set: %s", home)
	}

	// A relative root is ignored by goose, so the home directory stays.
	t.Setenv("GOOSE_PATH_ROOT", "rel/root")
	if got, want := gooseHookPath(), filepath.Join(homeDir(), ".agents", "plugins", "deja", "hooks", "hooks.json"); got != want {
		t.Errorf("gooseHookPath with a relative GOOSE_PATH_ROOT = %s, want %s", got, want)
	}
}

// Plain goose has one global AGENTS.md for every session on the machine, and
// it re-reads it every turn. Per-prompt recall written there reached every
// other running session: on a stand, B's recall arrived in A's next request
// (#4795). So without the wrapper's own MOIM file the prompt hook leaves the
// file alone, the session-start digest and the reader's lines included.
func TestThePlainGoosePromptHookLeavesTheSharedFileAlone(t *testing.T) {
	hermeticEnv(t)
	dir := gooseRecallFixture(t)
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	path := gooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# my own notes\n\nalways use pgx\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshGooseForPrompt(dir, []byte(`{"prompt":"pgbouncer timing out","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != mine {
		t.Errorf("a prompt's recall went into the file every goose session reads:\n%s", b)
	}
}

// And the writer both halves share keeps the markers on the file that is the
// reader's, while the MOIM file — deja's own — is written whole.
func TestGooseRecallWritesAMarkedBlockOnlyInTheReadersFile(t *testing.T) {
	goose := gooseHomeForTest(t)
	agents := filepath.Join(goose, "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(agents), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agents, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(agents)
	if !strings.Contains(string(b), gooseRecallStart) || !strings.Contains(string(b), "mine") {
		t.Errorf("AGENTS.md was not edited in place:\n%s", b)
	}

	moim := filepath.Join(t.TempDir(), "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	m, err := os.ReadFile(moim)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(m), gooseRecallStart) {
		t.Errorf("the MOIM file is deja's own and needs no markers:\n%s", m)
	}
	if strings.TrimSpace(string(m)) != "recalled text" {
		t.Errorf("MOIM = %q", m)
	}
}

// And under the wrapper it has to actually write: a test that only checks the
// shared file is untouched passes just as well when the hook does nothing.
func TestTheGoosePromptHookWritesWhatItFound(t *testing.T) {
	hermeticEnv(t)
	dir := gooseRecallFixture(t)
	moim := filepath.Join(t.TempDir(), "deja-recall-1.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)

	if err := refreshGooseForPrompt(dir, []byte(`{"prompt":"pgbouncer timing out","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(moim)
	if err != nil {
		t.Fatalf("the prompt hook wrote nothing: %v", err)
	}
	if !strings.Contains(string(b), "pgbouncer") {
		t.Errorf("the block does not carry what was asked about:\n%s", b)
	}
	// A second write replaces the first without a backup: the wrapper removes
	// its file on exit, and a .bak beside it would stay behind every run.
	if err := refreshGooseForPrompt(dir, []byte(`{"prompt":"default_pool_size raised","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(moim)); len(entries) != 1 {
		t.Errorf("the recall file left company behind: %v", entries)
	}
}

// gooseRecallFixture indexes one session the prompt "pgbouncer timing out"
// recalls, and returns the index.
func gooseRecallFixture(t *testing.T) string {
	t.Helper()
	claude := os.Getenv("DEJA_CLAUDE_ROOT")
	proj := filepath.Join(claude, "-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(role, text string) string {
		return `{"type":"` + role + `","sessionId":"gp1","cwd":"/app",` +
			`"timestamp":"2026-07-30T03:04:05Z","message":{"role":"` + role + `","content":"` + text + `"}}`
	}
	body := line("user", "why does pgbouncer keep timing out") + "\n" +
		line("assistant", "pgbouncer was timing out on a full pool; the fix was raising default_pool_size to 40.") + "\n"
	if err := os.WriteFile(filepath.Join(proj, "gp1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

// DEJA_RECALL=off is the documented kill switch, and every hook reads it —
// except this one. So on goose, off left deja writing a block into the
// reader's AGENTS.md, and that file is re-read every turn, so it kept reaching
// the model. Found while building the control arm of an A/B: both arms shipped
// a block, which is what an off arm is supposed to prove cannot happen.
func TestTheGooseRecallHonoursTheKillSwitch(t *testing.T) {
	gooseHomeForTest(t)

	// Something to write, then the switch.
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(gooseHintsPath())
	if err != nil || !strings.Contains(string(before), gooseRecallStart) {
		t.Fatalf("nothing was written to switch off: %v\n%s", err, before)
	}

	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor("", ""); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(gooseHintsPath()); err == nil && strings.Contains(string(b), gooseRecallStart) {
		t.Errorf("the block survived the kill switch:\n%s", b)
	}
}

// Off must not take the reader's own lines with it: the switch is about deja's
// block, not about the file.
func TestTheKillSwitchLeavesTheReadersLines(t *testing.T) {
	gooseHomeForTest(t)
	path := gooseHintsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\n\nalways use pgx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor("", ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the kill switch deleted the reader's file: %v", err)
	}
	if !strings.Contains(string(b), "always use pgx") {
		t.Errorf("the reader's lines went with the block:\n%s", b)
	}
	if strings.Contains(string(b), gooseRecallStart) {
		t.Errorf("the block survived:\n%s", b)
	}
}

// And with the switch off, deja writes nothing where there was nothing —
// "No matching history yet." is a claim about the store, and off is not that.
func TestTheKillSwitchWritesNothingAtAll(t *testing.T) {
	gooseHomeForTest(t)
	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor("", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gooseHintsPath()); err == nil {
		b, _ := os.ReadFile(gooseHintsPath())
		t.Errorf("the switch created a file anyway:\n%s", b)
	}
}

// Under the wrapper the recall lives in the MOIM file, which is deja's own —
// off has to empty it too, or the last thing deja wrote keeps being read every
// turn with the switch thrown.
func TestTheKillSwitchEmptiesTheMoimFile(t *testing.T) {
	gooseHomeForTest(t)
	moim := filepath.Join(t.TempDir(), "recall.md")
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", moim)
	if err := writeGooseRecall("recalled text"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(moim); err != nil {
		t.Fatalf("nothing was written to switch off: %v", err)
	}

	t.Setenv("DEJA_RECALL", "off")
	if err := refreshGooseHintsFor("", ""); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(moim); err == nil && strings.TrimSpace(string(b)) != "" {
		t.Errorf("the MOIM file still holds a recall with the switch off:\n%s", b)
	}
}

// The goose recipe is the last file with the broken promise #2600 named:
// install replaces the one already at that path and keeps the copy it promises,
// uninstall removes deja's version and leaves the reader with a .bak beside
// nothing (#2602).
func TestUninstallPutsBackTheGooseRecipeItReplaced(t *testing.T) {
	hermeticEnv(t)
	path := gooseRecipePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "# my own recipe\nversion: 1.0.0\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "goose", "--no-index"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path + ".bak"); err != nil || string(b) != mine {
		t.Fatalf("install kept no copy of the recipe it replaced: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "goose"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reader's own recipe is gone: %v", err)
	}
	if string(b) != mine {
		t.Errorf("what is at that path is not theirs:\n%s", b)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("the copy was put back and the backup left behind: %v", err)
	}
}

// A recipe deja wrote itself goes, with nothing left beside it.
func TestUninstallRemovesTheGooseRecipeItWrote(t *testing.T) {
	hermeticEnv(t)
	if _, err := captureRun(t, "install", "goose", "--no-index"); err != nil {
		t.Fatal(err)
	}
	path := gooseRecipePath()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("install wrote no recipe: %v", err)
	}
	if _, err := captureRun(t, "uninstall", "goose"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("deja's own recipe survived: %v", err)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("a backup survived: %v", err)
	}
}

// Plain goose keeps the session-start digest in one AGENTS.md that every
// session re-reads each turn. `goose session -r` fires SessionStart under the
// resumed id, and the hook wrote that session into its own digest; a session
// still open when a later one started in its project got itself back on its
// next turn; and the prompt hook never stamped the session live, so the MCP
// tool answered it with its own transcript.
func TestAPlainGooseSessionIsNotRecalledToItself(t *testing.T) {
	hermeticEnv(t)
	dir := gooseRecallFixture(t)
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	shared := func() string {
		b, _ := os.ReadFile(gooseHintsPath())
		return string(b)
	}

	// Control: a different session starting in the project is shown gp1.
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "other", "cwd": "/app"}))
	if err := cmdGooseHook(dir, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shared(), "`gp1`") {
		t.Fatalf("the digest does not name gp1 even for another session, so this measures nothing:\n%s", shared())
	}

	// gp1 is resumed: SessionStart names it.
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "gp1", "cwd": "/app"}))
	if err := cmdGooseHook(dir, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shared(), "`gp1`") {
		t.Errorf("a resumed goose session was given itself as history:\n%s", shared())
	}

	// A later session puts gp1 back into the shared file while gp1 is open.
	withHookStdin(t, hookPayload(t, map[string]string{"session_id": "other", "cwd": "/app"}))
	if err := cmdGooseHook(dir, nil); err != nil {
		t.Fatal(err)
	}
	before := shared()
	if !strings.Contains(before, "`gp1`") {
		t.Fatalf("the later session's digest does not name gp1:\n%s", before)
	}
	// Another session's prompt leaves the shared file as it is.
	if err := refreshGooseForPrompt(dir, []byte(`{"session_id":"third","message":"hi","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	if shared() != before {
		t.Errorf("a prompt from a session the digest does not name rewrote the shared file:\n%s", shared())
	}
	// gp1's next prompt takes itself out before goose reads the file, and
	// leaves the reader's own lines where they were.
	if err := os.WriteFile(gooseHintsPath(), []byte("# my notes\n\n"+before), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := refreshGooseForPrompt(dir, []byte(`{"session_id":"gp1","message":"hi","cwd":"/app"}`)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shared(), "`gp1`") {
		t.Errorf("an open goose session was shown itself on its next turn:\n%s", shared())
	}
	if !strings.HasPrefix(shared(), "# my notes\n") {
		t.Errorf("taking gp1 out lost the reader's lines:\n%s", shared())
	}
	if !liveSessionIDs(dir)["gp1"] {
		t.Errorf("the plain goose prompt hook did not stamp gp1 live, so MCP recall answers it with itself")
	}
}

// Only the asker's entry leaves the shared block. The rest is what another
// session started with, possibly in another project, and rebuilding it for the
// asker swapped that session's digest for the asker's.
func TestDropDigestEntryKeepsTheOtherSessions(t *testing.T) {
	digest := "<deja-recall>\nlead\n" +
		"✓ recalled from goose session · today\n  - Session: **p/a** `s_3`\n  - User: one\n" +
		"✓ recalled from goose session · today\n  - Session: **p/a** `s_31`\n  - User: two\n  - Assistant: `x`\n" +
		"✓ recalled from claude session · today\n  - Session: **p/b** `c9`\n  - User: three\n</deja-recall>\n"
	got, ok := dropDigestEntry(digest, "s_31")
	want := "<deja-recall>\nlead\n" +
		"✓ recalled from goose session · today\n  - Session: **p/a** `s_3`\n  - User: one\n" +
		"✓ recalled from claude session · today\n  - Session: **p/b** `c9`\n  - User: three\n</deja-recall>\n"
	if !ok || got != want {
		t.Errorf("dropDigestEntry(s_31) = %v\n%s", ok, got)
	}
	if _, ok := dropDigestEntry(digest, "s_9"); ok {
		t.Error("an id the digest does not name was reported dropped")
	}
}

// goose reads the MOIM file by path and ignores the session, so the wrapper's
// file has to be its own: with one path for every `deja goose`, two sessions
// in two projects read whichever recall was written last (#4795). And it goes
// with the session, or every run leaves a file behind.
func TestEachGooseWrapperHasItsOwnRecallFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub goose is a shell script")
	}
	hermeticEnv(t)
	t.Setenv("GOOSE_MOIM_MESSAGE_FILE", "")
	bin := t.TempDir()
	seen := filepath.Join(t.TempDir(), "seen")
	script := "#!/bin/sh\nprintf '%s\\n' \"$GOOSE_MOIM_MESSAGE_FILE\" >> " + seen + "\n" +
		"test -f \"$GOOSE_MOIM_MESSAGE_FILE\" && echo present >> " + seen + "\n"
	if err := os.WriteFile(filepath.Join(bin, "goose"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := cmdGoose(t.TempDir(), []string{"run", "--text", "hi"}, ""); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(seen)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 || lines[1] != "present" {
		t.Fatalf("goose did not find a recall file waiting:\n%s", b)
	}
	if got, want := lines[0], gooseWrapperMOIMPath(os.Getpid()); got != want {
		t.Errorf("goose was handed %s, want this process's own %s", got, want)
	}
	if _, err := os.Stat(lines[0]); !os.IsNotExist(err) {
		t.Errorf("the recall file outlived its session: %v", err)
	}
}

// A key is empty when nothing is nested under it, and "nested" was read as
// "starts with two spaces". One space is valid YAML and is not two, so an
// uninstall dropped the key and left the reader's own entries behind as a
// stray list — a config goose then cannot read at all (#2724).
func TestAKeyWithEntriesIsNotEmptyAtAnyIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"one space", " "},
		{"two spaces", "  "},
		{"four spaces", "    "},
		{"a tab, which YAML does not allow but this must not mangle", "\t"},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := "slash_commands:\n" +
				c.indent + "- command: \"mine\"\n" +
				c.indent + "  recipe_path: /tmp/mine.yaml\nother: 1\n"
			got := dropEmptyYAMLKey(in, "slash_commands:")
			if !strings.Contains(got, "slash_commands:") {
				t.Errorf("the key was dropped while its entries stayed:\n%s", got)
			}
			if got != in {
				t.Errorf("a key with entries was rewritten:\nwant:\n%s\ngot:\n%s", in, got)
			}
		})
	}
}

// And the reason the function exists: a key with nothing under it parses as
// null, and goose refuses the whole config.
func TestAKeyWithNothingUnderItStillGoes(t *testing.T) {
	// The trailing newline goes with the key when the key is the last line;
	// that is what this has always done, and the caller writes the file back
	// through writeIfChanged either way.
	for _, c := range []struct{ name, in, want string }{
		{"nothing after it", "other: 1\nslash_commands:\n", "other: 1"},
		{"blank lines after it", "slash_commands:\n\n\nother: 1\n", "other: 1\n"},
		{"another key after it", "slash_commands:\nother: 1\n", "other: 1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := dropEmptyYAMLKey(c.in, "slash_commands:"); got != c.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, c.want)
			}
		})
	}
}

// The shape every YAML serializer writes: a sequence at the key's own indent.
// Its entries are the key's, and reading them as a stray list dropped the key
// on install — before any uninstall (#2724).
func TestASequenceAtTheKeysOwnIndentBelongsToIt(t *testing.T) {
	in := "other: 1\nslash_commands:\n- command: \"mine\"\n  recipe_path: /m\n"
	if got := dropEmptyYAMLKey(in, "slash_commands:"); got != in {
		t.Errorf("the key was dropped from under its own entries:\n%s", got)
	}
}

// And the whole round trip through the writers, at every indent a person may
// have used. What goes in must come back out byte for byte.
func TestGooseCommandRoundTripsAtEveryIndent(t *testing.T) {
	for _, c := range []struct{ name, indent string }{
		{"a sequence in the first column", ""},
		{"one space", " "},
		{"two spaces", "  "},
		{"three spaces", "   "},
		{"four spaces", "    "},
	} {
		t.Run(c.name, func(t *testing.T) {
			hermeticEnv(t)
			path := filepath.Join(gooseConfigDir(), "config.yaml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			before := "other: 1\nslash_commands:\n" +
				c.indent + "- command: \"mine\"\n" +
				c.indent + "  recipe_path: /m\n"
			if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := installGooseCommand("/opt/deja", false); err != nil {
				t.Fatal(err)
			}
			wired, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(wired), `- command: "deja"`) {
				t.Fatalf("the command was not written:\n%s", wired)
			}
			if !strings.Contains(string(wired), `- command: "mine"`) {
				t.Errorf("the reader's own entry did not survive the install:\n%s", wired)
			}
			// One list, one indent: an entry spliced in at another width is a
			// file no YAML parser will read.
			for line := range strings.SplitSeq(string(wired), "\n") {
				if strings.HasPrefix(strings.TrimLeft(line, " \t"), "- command:") &&
					line[:len(line)-len(strings.TrimLeft(line, " \t"))] != c.indent {
					t.Errorf("an entry was written at another indent than the list:\n%s", wired)
					break
				}
			}

			if _, err = installGooseCommand("/opt/deja", true); err != nil {
				t.Fatal(err)
			}
			back, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(back) != before {
				t.Errorf("the config did not come back:\nwas:\n%q\nnow:\n%q", before, back)
			}
		})
	}
}

// A config with windows line endings is read line by line like any other, and
// every line ended in a carriage return, so nothing matched: a second install
// wrote a second key, and uninstall left the empty one this file exists to
// prevent (#2724).
func TestGooseCommandSurvivesWindowsLineEndings(t *testing.T) {
	hermeticEnv(t)
	path := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other: 1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := installGooseCommand("/opt/deja", false); err != nil {
			t.Fatal(err)
		}
	}
	wired, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(wired), "slash_commands:"); n != 1 {
		t.Errorf("%d slash_commands keys after two installs:\n%s", n, wired)
	}
	if _, err = installGooseCommand("/opt/deja", true); err != nil {
		t.Fatal(err)
	}
	back, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(back), "slash_commands:") {
		t.Errorf("an empty key was left behind, which goose reads as null:\n%q", back)
	}
}

// goose keys extensions by name. Writing deja's mapping entry under a key whose
// value is a sequence leaves a mapping and a sequence under one key, which no
// YAML parser accepts — and install said "updated" and exited 0 (#1697).
func TestInstallGooseRefusesAnExtensionsList(t *testing.T) {
	hermeticEnv(t)
	dir := os.Getenv("DEJA_GOOSE_ROOT")
	if dir == "" {
		t.Skip("no goose root in this environment")
	}
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	const list = "extensions:\n  - developer\n  - fetch\n"
	if err := os.WriteFile(cfg, []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := captureRun(t, "install", "goose")
	if err == nil {
		t.Error("install accepted an extensions: list")
	} else if !strings.Contains(err.Error(), "extensions") {
		t.Errorf("the refusal does not name what it could not read: %s", err)
	}
	after, readErr := os.ReadFile(cfg)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != list {
		t.Errorf("install changed a config it could not read:\n%s", after)
	}
}

// An inline value is not a block: the insert missed it and appended a second
// `extensions:` key, and a parser takes the last of two — deja's — so the
// user's extensions vanished without a word (#1697).
func TestInstallGooseRefusesAnInlineExtensions(t *testing.T) {
	hermeticEnv(t)
	if os.Getenv("DEJA_GOOSE_ROOT") == "" {
		t.Skip("no goose root in this environment")
	}
	for _, inline := range []string{"extensions: [a, b]\n", "extensions: {a: {enabled: true}}\n"} {
		cfg := filepath.Join(gooseConfigDir(), "config.yaml")
		if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, []byte(inline), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := captureRun(t, "install", "goose"); err == nil {
			after, _ := os.ReadFile(cfg)
			t.Errorf("install accepted %q:\n%s", inline, after)
		}
		after, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != inline {
			t.Errorf("install changed a config it could not edit:\n%s", after)
		}
	}
}

// The control: a mapping — the shape goose actually uses — still gets deja's
// entry, keeps the user's, and survives a second install and an uninstall.
func TestInstallGooseKeepsAHandWrittenBlock(t *testing.T) {
	hermeticEnv(t)
	if os.Getenv("DEJA_GOOSE_ROOT") == "" {
		t.Skip("no goose root in this environment")
	}
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	const hand = "# mine\nGOOSE_PROVIDER: anthropic\nextensions:\n  # my own\n  developer:\n    enabled: true\n    type: builtin\n"
	if err := os.WriteFile(cfg, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "install", "goose"); err != nil {
		t.Fatal(err)
	}
	names := gooseExtensionNames(t, cfg)
	if !names["deja"] || !names["developer"] {
		t.Fatalf("install lost an extension: %v", names)
	}
	first, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = captureRun(t, "install", "goose"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("a second install rewrote the config:\n%s\n---\n%s", first, second)
	}
	if _, err := captureRun(t, "uninstall", "goose"); err != nil {
		t.Fatal(err)
	}
	names = gooseExtensionNames(t, cfg)
	if names["deja"] || !names["developer"] {
		t.Errorf("uninstall did not put the block back: %v", names)
	}
}

// gooseExtensionNames reads the entry names under `extensions:` and fails if
// the block mixes a mapping with a sequence — deja has no YAML parser, and
// pulling one in for a test would check a library rather than this code.
func gooseExtensionNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	inBlock := false
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(line, "extensions:") {
			inBlock = true
			continue
		}
		if !inBlock {
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			break
		}
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		// Only at the entry level: `- "mcp"` inside an entry's args is fine.
		if strings.HasPrefix(line, "  - ") {
			t.Fatalf("the extensions block holds a sequence item where entries go:\n%s", b)
		}
		// Entry names sit at exactly one level of indent; their fields deeper.
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(trim, ":") {
			names[strings.TrimSuffix(trim, ":")] = true
		}
	}
	return names
}

// An entry whose first field is a non-stdio type and that has no cmd: the
// type fix and the cmd insert land on one line, and the type must not be
// written twice — goose drops the whole file over a duplicate key.
func TestInstallGooseFixesTheTypeOfAnEntryWithoutCmd(t *testing.T) {
	hermeticEnv(t)
	if os.Getenv("DEJA_GOOSE_ROOT") == "" {
		t.Skip("no goose root in this environment")
	}
	cfg := filepath.Join(gooseConfigDir(), "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}
	const hand = "extensions:\n  deja:\n    type: streamable_http\n    uri: http://127.0.0.1:9/mcp\n    enabled: true\n    name: deja\n    timeout: 300\n"
	if err := os.WriteFile(cfg, []byte(hand), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if _, err := captureRun(t, "install", "goose"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(cfg)
		if err != nil {
			t.Fatal(err)
		}
		s := string(got)
		if n := strings.Count(s, "    type:"); n != 1 || !strings.Contains(s, "    type: stdio\n") || !strings.Contains(s, "    cmd: ") {
			t.Fatalf("install %d left %d type keys:\n%s", i+1, n, s)
		}
	}
}
