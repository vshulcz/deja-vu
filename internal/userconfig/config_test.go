package userconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPathResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DEJA_CONFIG_FILE", "")

	wantDefault := filepath.Join(home, ".config", "deja", "config.toml")
	if got := Path(); got != wantDefault {
		t.Fatalf("default Path() = %q, want %q", got, wantDefault)
	}

	xdg := filepath.Join(t.TempDir(), "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	wantXDG := filepath.Join(xdg, "deja", "config.toml")
	if got := Path(); got != wantXDG {
		t.Fatalf("XDG Path() = %q, want %q", got, wantXDG)
	}

	custom := filepath.Join(t.TempDir(), "custom.toml")
	t.Setenv("DEJA_CONFIG_FILE", custom)
	if got := Path(); got != custom {
		t.Fatalf("DEJA_CONFIG_FILE Path() = %q, want %q", got, custom)
	}

	t.Setenv("DEJA_CONFIG_FILE", "~/my-deja.toml")
	wantTilde := filepath.Join(home, "my-deja.toml")
	if got := Path(); got != wantTilde {
		t.Fatalf("tilde DEJA_CONFIG_FILE Path() = %q, want %q", got, wantTilde)
	}
}

func TestLoadPathMissingReturnsEmpty(t *testing.T) {
	cfg, err := LoadPath(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("missing config returned error: %v", err)
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Fatalf("missing config = %#v, want zero Config", cfg)
	}
}

func TestLoadPathFullSchemaAndDuration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := filepath.Join(t.TempDir(), "config.toml")
	src := `
# Global interval alias or inside [embed]
embed_every = "45m"

[embed]
url = "https://embed.example.invalid/v1"
model = "text-embedding-3-small"
api_key = "sk-test-token"
batch = 64
query_prefix = "query: "
passage_prefix = 'passage: '
off = false

[hooks]
recall = "aggressive"
recall_global = true
autorecall_local_only = true
no_source_hints = false
no_auto_index = true

[index]
dir = "~/custom-index"
policy_file = "~/custom-policy.json"
notes_file = "~/custom-notes.jsonl"
include_subagents = "whole"
tool_output = false
paths = true
edits = false
writes = true
commands = false
exclude_projects = [
  "tmp/*",
  "scratch-*", # inline comment
]
exclude_harnesses = ["opencode", "aider"]
stores = ["claude", "codex"]
search_only = true
`
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath error: %v", err)
	}
	if cfg.Embed.Every != "45m" {
		t.Fatalf("Embed.Every = %q, want 45m", cfg.Embed.Every)
	}
	if got := cfg.EmbedEveryDuration(); got != 45*time.Minute {
		t.Fatalf("EmbedEveryDuration() = %v, want 45m", got)
	}
	if cfg.Embed.URL != "https://embed.example.invalid/v1" {
		t.Fatalf("Embed.URL = %q", cfg.Embed.URL)
	}
	if cfg.Embed.Model != "text-embedding-3-small" {
		t.Fatalf("Embed.Model = %q", cfg.Embed.Model)
	}
	if cfg.Embed.APIKey != "sk-test-token" {
		t.Fatalf("Embed.APIKey = %q", cfg.Embed.APIKey)
	}
	if cfg.Embed.Batch == nil || *cfg.Embed.Batch != 64 {
		t.Fatalf("Embed.Batch = %v, want 64", cfg.Embed.Batch)
	}
	if cfg.Embed.QueryPrefix != "query: " || cfg.Embed.PassagePrefix != "passage: " {
		t.Fatalf("prefixes = (%q, %q)", cfg.Embed.QueryPrefix, cfg.Embed.PassagePrefix)
	}
	if cfg.Embed.Off == nil || *cfg.Embed.Off {
		t.Fatalf("Embed.Off = %v, want false", cfg.Embed.Off)
	}
	if cfg.Hooks.Recall != "aggressive" {
		t.Fatalf("Hooks.Recall = %q", cfg.Hooks.Recall)
	}
	if cfg.Index.Dir != filepath.Join(home, "custom-index") {
		t.Fatalf("Index.Dir = %q", cfg.Index.Dir)
	}
	if !reflect.DeepEqual(cfg.Index.ExcludeProjects, []string{"tmp/*", "scratch-*"}) {
		t.Fatalf("ExcludeProjects = %v", cfg.Index.ExcludeProjects)
	}
	if !reflect.DeepEqual(cfg.Index.Stores, []string{"claude", "codex"}) {
		t.Fatalf("Stores = %v", cfg.Index.Stores)
	}
}

func TestLoadPathSectionEmbedEveryAndDuplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[embed]\nevery = \"30m\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath error: %v", err)
	}
	if cfg.EmbedEveryDuration() != 30*time.Minute {
		t.Fatalf("EmbedEveryDuration() = %v, want 30m", cfg.EmbedEveryDuration())
	}

	dup := filepath.Join(t.TempDir(), "dup.toml")
	if err := os.WriteFile(dup, []byte("embed_every = \"10m\"\n[embed]\nevery = \"20m\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPath(dup); err == nil {
		t.Fatal("expected error when both embed_every and [embed].every are set")
	}
}

func TestValidationAndParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "syntax missing equals",
			content: "[embed]\nurl \"https://example.invalid\"\n",
			wantErr: ":2:",
		},
		{
			name:    "unclosed string",
			content: "[embed]\nmodel = \"abc\n",
			wantErr: ":2:",
		},
		{
			name:    "duplicate key",
			content: "[embed]\nbatch = 10\nbatch = 20\n",
			wantErr: ":3:",
		},
		{
			name:    "duplicate table",
			content: "[embed]\n[embed]\n",
			wantErr: ":2:",
		},
		{
			name:    "invalid embed.every duration",
			content: "[embed]\nevery = \"soon\"\n",
			wantErr: "embed.every",
		},
		{
			name:    "negative embed.every duration",
			content: "embed_every = \"-5m\"\n",
			wantErr: "embed.every",
		},
		{
			name:    "non-positive embed.batch",
			content: "[embed]\nbatch = 0\n",
			wantErr: "embed.batch",
		},
		{
			name:    "wrong type embed.batch",
			content: "[embed]\nbatch = \"fast\"\n",
			wantErr: "embed.batch",
		},
		{
			name:    "invalid hooks.recall",
			content: "[hooks]\nrecall = \"yolo\"\n",
			wantErr: "hooks.recall",
		},
		{
			name:    "invalid include_subagents",
			content: "[index]\ninclude_subagents = \"maybe\"\n",
			wantErr: "index.include_subagents",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadPath(p)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestDiagnosePathUnknownKeysAndAPIKeyPermissions(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	src := `
mystery_top = true

[embed]
api_key = "sk-secret-value"
typo_key = 123

[bogus]
flag = "x"
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}

	diag := DiagnosePath(p)
	if !diag.Exists {
		t.Fatal("expected Exists=true")
	}
	if diag.Err != nil {
		t.Fatalf("expected unknown keys to warn cleanly rather than break parsing, got %v", diag.Err)
	}
	wantUnknown := []string{"bogus.flag", "embed.typo_key", "mystery_top"}
	if !reflect.DeepEqual(diag.Unknown, wantUnknown) {
		t.Fatalf("Unknown = %v, want %v", diag.Unknown, wantUnknown)
	}
	if runtime.GOOS != "windows" {
		if len(diag.Warnings) == 0 {
			t.Fatal("expected permission warning when api_key is readable by group/others")
		}
		for _, w := range diag.Warnings {
			if strings.Contains(w, "sk-secret-value") {
				t.Fatalf("warning leaked api_key secret: %q", w)
			}
		}
	}
}

func unsetTestEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func TestApplyEnvPrecedenceWasAppliedAndRestore(t *testing.T) {
	t.Setenv("DEJA_EMBED_URL", "https://from-env.example/v1")
	t.Setenv("DEJA_RECALL", "")
	unsetTestEnv(t, "DEJA_EMBED_MODEL", "DEJA_EMBED_BATCH", "DEJA_EMBED_EVERY")

	p := filepath.Join(t.TempDir(), "config.toml")
	src := `
[embed]
every = "45m"
url = "https://from-config.example/v1"
model = "from-config-model"
batch = 16

[hooks]
recall = "aggressive"
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	restore := ApplyPath(p)
	if got := os.Getenv("DEJA_EMBED_URL"); got != "https://from-env.example/v1" {
		t.Fatalf("env DEJA_EMBED_URL = %q, want process env to win", got)
	}
	if WasApplied("DEJA_EMBED_URL") {
		t.Fatal("WasApplied(DEJA_EMBED_URL) should be false when process env already set it")
	}
	if WasApplied("DEJA_RECALL") {
		t.Fatal("WasApplied(DEJA_RECALL) should be false when process env explicitly set empty string")
	}
	if got := os.Getenv("DEJA_EMBED_MODEL"); got != "from-config-model" {
		t.Fatalf("DEJA_EMBED_MODEL = %q, want from-config-model", got)
	}
	if !WasApplied("DEJA_EMBED_MODEL") {
		t.Fatal("WasApplied(DEJA_EMBED_MODEL) should be true")
	}
	if got := os.Getenv("DEJA_EMBED_BATCH"); got != "16" {
		t.Fatalf("DEJA_EMBED_BATCH = %q, want 16", got)
	}
	if got := os.Getenv("DEJA_EMBED_EVERY"); got != "45m" {
		t.Fatalf("DEJA_EMBED_EVERY = %q, want 45m", got)
	}

	restore()
	if _, ok := os.LookupEnv("DEJA_EMBED_MODEL"); ok {
		t.Fatal("restore() left DEJA_EMBED_MODEL set")
	}
	if WasApplied("DEJA_EMBED_MODEL") {
		t.Fatal("WasApplied(DEJA_EMBED_MODEL) remained true after restore()")
	}
}

func TestDefaultWrappersAndEffectiveEmbedEvery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	unsetTestEnv(t, "DEJA_CONFIG_FILE", "DEJA_EMBED_EVERY", "DEJA_EMBED_MODEL")
	// Relative XDG_CONFIG_HOME falls back to ~/.config.
	t.Setenv("XDG_CONFIG_HOME", "relative/xdg")

	p := filepath.Join(home, ".config", "deja", "config.toml")
	if got := Path(); got != p {
		t.Fatalf("Path() with relative XDG = %q, want %q", got, p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "[embed]\nevery = \"25m\"\nmodel = \"from-default\"\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := Load().Embed.Model; got != "from-default" {
		t.Fatalf("Load().Embed.Model = %q, want from-default", got)
	}
	if diag := Diagnose(); !diag.Exists || diag.Err != nil || diag.Path != p {
		t.Fatalf("Diagnose() = %+v", diag)
	}
	if got := EffectiveEmbedEvery(); got != 25*time.Minute {
		t.Fatalf("EffectiveEmbedEvery() = %v, want 25m", got)
	}
	t.Setenv("DEJA_EMBED_EVERY", "10m")
	if got := EffectiveEmbedEvery(); got != 10*time.Minute {
		t.Fatalf("EffectiveEmbedEvery() env override = %v, want 10m", got)
	}
	t.Setenv("DEJA_EMBED_EVERY", "not-a-duration")
	if got := EffectiveEmbedEvery(); got != 0 {
		t.Fatalf("EffectiveEmbedEvery() invalid env = %v, want 0", got)
	}

	// Apply() uses Path() and cleans up on restore().
	restore := Apply()
	if got := os.Getenv("DEJA_EMBED_MODEL"); got != "from-default" {
		t.Fatalf("Apply() DEJA_EMBED_MODEL = %q, want from-default", got)
	}
	restore()

	// Invalid config fails open in Load() and ApplyPath().
	if err := os.WriteFile(p, []byte("[embed]\nbad_line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(); !reflect.DeepEqual(got, Config{}) {
		t.Fatalf("Load() on broken config = %#v, want zero Config", got)
	}
	if got := EffectiveEmbedEvery(); got != 0 {
		t.Fatalf("EffectiveEmbedEvery() with invalid env + broken config = %v", got)
	}
	t.Setenv("DEJA_EMBED_EVERY", "off")
	if _, err := ParseEmbedEvery("off"); err != nil {
		t.Fatalf("ParseEmbedEvery(off) err = %v", err)
	}
	if cfg := (Config{Embed: EmbedConfig{Every: "bogus"}}); cfg.EmbedEveryDuration() != 0 {
		t.Fatalf("EmbedEveryDuration() invalid = %v, want 0", cfg.EmbedEveryDuration())
	}

	// Missing file restore is a safe no-op.
	missingRestore := ApplyPath(filepath.Join(t.TempDir(), "missing.toml"))
	missingRestore()

	// Nested ApplyPath decrements refcounts accurately.
	validPath := filepath.Join(t.TempDir(), "nested.toml")
	if err := os.WriteFile(validPath, []byte("[embed]\nmodel = \"nested\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r1 := ApplyPath(validPath)
	r2 := ApplyPath(validPath)
	if !WasApplied("DEJA_EMBED_MODEL") {
		t.Fatal("WasApplied(DEJA_EMBED_MODEL) should be true after first ApplyPath")
	}
	r2()
	r1()
	if WasApplied("DEJA_EMBED_MODEL") {
		t.Fatal("WasApplied(DEJA_EMBED_MODEL) should be false after both restores")
	}

	// DiagnosePath error when target is a directory.
	dirDiag := DiagnosePath(t.TempDir())
	if !dirDiag.Exists || dirDiag.Err == nil {
		t.Fatalf("DiagnosePath(dir) = %+v, want Exists=true and non-nil Err", dirDiag)
	}
}

func TestConfigTypeErrorsStringListsSubagentsAndHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Valid edge forms: string-valued stores/lists, bool/variant include_subagents, and home expansion.
	p := filepath.Join(t.TempDir(), "variants.toml")
	src := "" +
		"[embed]\n" +
		"off = true\n" +
		"async_batch = true\n" +
		"[hooks]\n" +
		"recall_global = false\n" +
		"[index]\n" +
		"dir = \"~\"\n" +
		"policy_file = \"~/policy.json\"\n" +
		"notes_file = '~\\notes.jsonl'\n" +
		"include_subagents = true\n" +
		"stores = \"claude, , codex\"\n" +
		"exclude_projects = []\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadPath(p)
	if err != nil {
		t.Fatalf("LoadPath(variants) = %v", err)
	}
	if cfg.Index.Dir != home {
		t.Fatalf("Index.Dir(~) = %q, want %q", cfg.Index.Dir, home)
	}
	if cfg.Index.NotesFile != filepath.Join(home, "notes.jsonl") {
		t.Fatalf("Index.NotesFile = %q", cfg.Index.NotesFile)
	}
	if cfg.Index.IncludeSubagents != "1" {
		t.Fatalf("IncludeSubagents(true) = %q, want 1", cfg.Index.IncludeSubagents)
	}
	if !reflect.DeepEqual(cfg.Index.Stores, []string{"claude", "codex"}) {
		t.Fatalf("Stores = %v", cfg.Index.Stores)
	}
	bindings := cfg.EnvBindings()
	var gotOff, gotAsyncBatch, gotGlobal string
	for _, b := range bindings {
		switch b.Key {
		case "DEJA_EMBED_OFF":
			gotOff = b.Value
		case "DEJA_EMBED_ASYNC_BATCH":
			gotAsyncBatch = b.Value
		case "DEJA_RECALL_GLOBAL":
			gotGlobal = b.Value
		}
	}
	if gotOff != "1" || gotAsyncBatch != "1" || gotGlobal != "0" {
		t.Fatalf("EnvBindings booleans: off=%q async_batch=%q global=%q", gotOff, gotAsyncBatch, gotGlobal)
	}

	falseBatchPath := filepath.Join(t.TempDir(), "async-false.toml")
	if err := os.WriteFile(falseBatchPath, []byte("[embed]\nasync_batch = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	falseBatchCfg, err := LoadPath(falseBatchPath)
	if err != nil {
		t.Fatalf("LoadPath(async-false) = %v", err)
	}
	var gotFalseBatch string
	for _, b := range falseBatchCfg.EnvBindings() {
		if b.Key == "DEJA_EMBED_ASYNC_BATCH" {
			gotFalseBatch = b.Value
		}
	}
	if gotFalseBatch != "0" {
		t.Fatalf("EnvBindings(async_batch=false) = %q, want 0", gotFalseBatch)
	}

	if got := expandHome("/abs/path"); got != "/abs/path" {
		t.Fatalf("expandHome(/abs/path) = %q", got)
	}
	if got := expandHome(""); got != "" {
		t.Fatalf("expandHome(\"\") = %q", got)
	}

	// parseIncludeSubagents cover bool false, "off", and "summary".
	for _, tc := range []struct {
		toml string
		want string
	}{
		{"[index]\ninclude_subagents = false\n", "0"},
		{"[index]\ninclude_subagents = \"off\"\n", "0"},
		{"[index]\ninclude_subagents = \"summary\"\n", "summary"},
	} {
		vp := filepath.Join(t.TempDir(), "sub.toml")
		if err := os.WriteFile(vp, []byte(tc.toml), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := LoadPath(vp)
		if err != nil || got.Index.IncludeSubagents != tc.want {
			t.Fatalf("%q -> (%q, %v), want %q", tc.toml, got.Index.IncludeSubagents, err, tc.want)
		}
	}

	// Type errors across every field helper.
	errCases := []struct {
		name string
		src  string
	}{
		{"embed_every int", "embed_every = 10\n"},
		{"embed.url bool", "[embed]\nurl = true\n"},
		{"embed.model int", "[embed]\nmodel = 1\n"},
		{"embed.api_key bool", "[embed]\napi_key = false\n"},
		{"embed.query_prefix int", "[embed]\nquery_prefix = 1\n"},
		{"embed.passage_prefix int", "[embed]\npassage_prefix = 1\n"},
		{"embed.off str", "[embed]\noff = \"yes\"\n"},
		{"embed.async_batch str", "[embed]\nasync_batch = \"yes\"\n"},
		{"hooks.recall int", "[hooks]\nrecall = 1\n"},
		{"hooks.recall_global str", "[hooks]\nrecall_global = \"yes\"\n"},
		{"index.dir bool", "[index]\ndir = true\n"},
		{"index.include_subagents int", "[index]\ninclude_subagents = 1\n"},
		{"index.stores bool", "[index]\nstores = true\n"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			ep := filepath.Join(t.TempDir(), "err.toml")
			if err := os.WriteFile(ep, []byte(tc.src), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPath(ep); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestTOMLParserStringsIntsArraysAndSyntaxErrors(t *testing.T) {
	// Valid escapes, unicode escapes, quoted keys, ints with sign/underscores, and CRLF.
	src := "\"embed_every\" = \"15m\"\r\n" +
		"[embed]\r\n" +
		"url = \"https://a\\b\\t\\n\\f\\r\\\"\\\\\\u0041\\u004a\\u004B\\U00000042\"\r\n" +
		"batch = +1_000 # trailing comment\r\n" +
		"'model' = 'single-quoted' \r\n" +
		"[bogus]\r\n" +
		"neg = -42\r\n" +
		"unicode = \"café\"\r\n"

	entries, err := parseTOML("test.toml", src)
	if err != nil {
		t.Fatalf("parseTOML valid = %v", err)
	}
	cfg, unknown, err := populateConfig("test.toml", entries)
	if err != nil {
		t.Fatalf("populateConfig valid = %v", err)
	}
	if cfg.Embed.Batch == nil || *cfg.Embed.Batch != 1000 || cfg.Embed.Model != "single-quoted" {
		t.Fatalf("parsed batch/model = (%v, %q)", cfg.Embed.Batch, cfg.Embed.Model)
	}
	if !strings.HasSuffix(cfg.Embed.URL, "\"\\AJKB") {
		t.Fatalf("escaped URL = %q", cfg.Embed.URL)
	}
	if !reflect.DeepEqual(unknown, []string{"bogus.neg", "bogus.unicode"}) {
		t.Fatalf("unknown = %v", unknown)
	}

	// Invalid TOML syntax and integer edge cases.
	badCases := []string{
		"[[embed]]\n",
		"[embed\n",
		"[]\n",
		"[embed] extra\n",
		"[embed]\n= 1\n",
		"[embed]\nmodel =\n",
		"[embed]\nmodel = \"\"\"multi\"\"\"\n",
		"[embed]\nmodel = '''multi'''\n",
		"[embed]\nmodel = 'bad\nline'\n",
		"[embed]\nmodel = 'eof\n",
		"[embed]\nmodel = \"trailing\\\n",
		"[embed]\nmodel = \"\\x\"\n",
		"[embed]\nmodel = \"\\u12\"\n",
		"[embed]\nmodel = \"\\ud800\"\n",
		"[embed]\nmodel = \"\\u004G\"\n",
		"[embed]\nmodel = \"\\U00110000\"\n",
		"[embed]\nmodel = \"\\UFFFFFFFF\"\n",
		"[embed]\nmodel = \"eof",
		"[embed]\nmodel = \"ok\" extra\n",
		"[embed]\nbatch = +\n",
		"[embed]\nbatch = _1\n",
		"[embed]\nbatch = 1_\n",
		"[embed]\nbatch = 1__0\n",
		"[embed]\nbatch = 12a\n",
		"[index]\nstores = [\"a\", 1]\n",
		"[index]\nstores = [\"unclosed\n",
		"[index]\nstores = [\"a\" \"b\"]\n",
		"[index]\nstores = [\"a\",\n",
		"[",
	}
	for _, bad := range badCases {
		if _, err := parseTOML("bad.toml", bad); err == nil {
			t.Fatalf("expected parseTOML error for %q", bad)
		}
	}
}
