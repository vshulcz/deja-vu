package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/userconfig"
)

func unsetTestEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}

func writeTestConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUserConfigRunAppliesBeforeDefaultDirAndRestoresEnv(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	unsetTestEnv(t, "DEJA_CONFIG_FILE", "DEJA_INDEX_DIR", "DEJA_EMBED_MODEL", "DEJA_RECALL")

	cfgPath := filepath.Join(home, ".config", "deja", "config.toml")
	writeTestConfig(t, cfgPath, `
[embed]
model = "from-toml-model"

[hooks]
recall = "aggressive"

[index]
dir = "~/toml-index"
`)

	// Explicit process env must beat config.toml (even an empty value if set in env).
	t.Setenv("DEJA_RECALL", "off")

	out, err := captureRun(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json failed: %v\noutput:\n%s", err, out)
	}

	var report doctorReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("unmarshal doctor --json: %v\n%s", err, out)
	}
	wantIndex := filepath.Join(home, "toml-index")
	if report.Index.Path != wantIndex {
		t.Fatalf("report.Index.Path = %q, want %q from config.toml", report.Index.Path, wantIndex)
	}

	// Process env must remain unpolluted once run() returns.
	if _, ok := os.LookupEnv("DEJA_INDEX_DIR"); ok {
		t.Fatal("DEJA_INDEX_DIR leaked into process environment after run()")
	}
	if _, ok := os.LookupEnv("DEJA_EMBED_MODEL"); ok {
		t.Fatal("DEJA_EMBED_MODEL leaked into process environment after run()")
	}
	if got := os.Getenv("DEJA_RECALL"); got != "off" {
		t.Fatalf("DEJA_RECALL = %q, want explicit env \"off\" preserved", got)
	}
}

func TestUserConfigLauncherEnvSkipsConfigInjectedVars(t *testing.T) {
	hermeticEnv(t)
	unsetTestEnv(t,
		"DEJA_CONFIG_FILE", "XDG_CONFIG_HOME",
		"DEJA_INDEX_DIR", "DEJA_RECALL", "DEJA_NO_AUTO_INDEX", "DEJA_EMBED_KEY",
	)

	xdg := filepath.Join(t.TempDir(), "xdg-custom")
	customCfg := filepath.Join(t.TempDir(), "custom.toml")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("DEJA_CONFIG_FILE", customCfg)
	// Explicit outer env override should still be forwarded by launcherEnv.
	t.Setenv("DEJA_RECALL", "safe")

	writeTestConfig(t, customCfg, `
[embed]
api_key = "sk-do-not-bake"

[hooks]
recall = "aggressive"
no_auto_index = true

[index]
dir = "~/cfg-index"
`)

	restore := userconfig.Apply()
	defer restore()

	pairs := launcherEnv(os.Environ())
	got := map[string]string{}
	for _, p := range pairs {
		got[p[0]] = p[1]
	}

	if got["DEJA_CONFIG_FILE"] != customCfg {
		t.Fatalf("launcherEnv dropped DEJA_CONFIG_FILE: got %q, want %q", got["DEJA_CONFIG_FILE"], customCfg)
	}
	if got["XDG_CONFIG_HOME"] != xdg {
		t.Fatalf("launcherEnv dropped custom XDG_CONFIG_HOME: got %q, want %q", got["XDG_CONFIG_HOME"], xdg)
	}
	if got["DEJA_RECALL"] != "safe" {
		t.Fatalf("launcherEnv dropped explicit outer DEJA_RECALL: got %q, want \"safe\"", got["DEJA_RECALL"])
	}
	for _, omitted := range []string{"DEJA_INDEX_DIR", "DEJA_NO_AUTO_INDEX", "DEJA_EMBED_KEY"} {
		if v, ok := got[omitted]; ok {
			t.Fatalf("launcherEnv baked config-applied %s=%q into wrapper environment", omitted, v)
		}
	}
}

func TestUserConfigSurvivesInstallAndUninstall(t *testing.T) {
	hermeticEnv(t)
	home := os.Getenv("HOME")
	unsetTestEnv(t, "DEJA_CONFIG_FILE")

	// Give install a real harness directory to wire cleanly.
	claudeRoot := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)

	cfgPath := filepath.Join(home, ".config", "deja", "config.toml")
	wantContent := "# user comment must survive upgrade/uninstall\n[hooks]\nrecall = \"safe\"\n\n[embed]\nevery = \"30m\"\n"
	writeTestConfig(t, cfgPath, wantContent)

	indexDir := os.Getenv("DEJA_INDEX_DIR")
	if err := runInstall(indexDir, []string{"claude"}, false); err != nil {
		t.Fatalf("runInstall failed: %v", err)
	}
	gotAfterInstall, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config.toml missing after install: %v", err)
	}
	if string(gotAfterInstall) != wantContent {
		t.Fatalf("config.toml mutated by install:\ngot:\n%s\nwant:\n%s", gotAfterInstall, wantContent)
	}

	if err = runInstall(indexDir, []string{"claude"}, true); err != nil {
		t.Fatalf("runUninstall failed: %v", err)
	}
	gotAfterUninstall, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config.toml removed by uninstall: %v", err)
	}
	if string(gotAfterUninstall) != wantContent {
		t.Fatalf("config.toml mutated by uninstall:\ngot:\n%s\nwant:\n%s", gotAfterUninstall, wantContent)
	}
}

func TestUserConfigWarnBrokenConfig(t *testing.T) {
	hermeticEnv(t)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("DEJA_CONFIG_FILE", cfgPath)

	// Missing config warns nothing.
	var buf bytes.Buffer
	warnBrokenConfig("recall", &buf)
	if buf.Len() != 0 {
		t.Fatalf("missing config warned: %q", buf.String())
	}

	// Valid strict-mode config warns nothing.
	writeTestConfig(t, cfgPath, "[embed]\napi_key = \"sk-quiet\"\n")
	if err := os.Chmod(cfgPath, 0o600); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	warnBrokenConfig("recall", &buf)
	if buf.Len() != 0 {
		t.Fatalf("valid 0600 config warned: %q", buf.String())
	}

	// Unknown keys + loose permissions with api_key must warn without leaking secret.
	secret := "sk-never-expose-secret"
	writeTestConfig(t, cfgPath, "[embed]\napi_key = \""+secret+"\"\nbogus = 1\n")
	if err := os.Chmod(cfgPath, 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	warnBrokenConfig("recall", &buf)
	msg := buf.String()
	if strings.Contains(msg, secret) {
		t.Fatalf("warning leaked secret: %q", msg)
	}
	if !strings.Contains(msg, `"embed.bogus"`) {
		t.Fatalf("expected unknown key warning, got %q", msg)
	}
	if runtime.GOOS != "windows" && !strings.Contains(msg, "chmod 600") {
		t.Fatalf("expected chmod 600 warning, got %q", msg)
	}

	// Broken syntax warns, while doctor/hook-*/mcp/serve stay silent.
	writeTestConfig(t, cfgPath, "[embed]\nurl\n")
	for _, skipped := range []string{"doctor", "mcp", "serve", "hook-context", "hook-session-end", "hook-embed"} {
		buf.Reset()
		warnBrokenConfig(skipped, &buf)
		if buf.Len() != 0 {
			t.Fatalf("command %q should suppress config warning, got %q", skipped, buf.String())
		}
	}
	buf.Reset()
	warnBrokenConfig("recall", &buf)
	if !strings.Contains(buf.String(), cfgPath) {
		t.Fatalf("expected broken config warning mentioning %q, got %q", cfgPath, buf.String())
	}
}

func TestUserConfigDoctorTextAndJSON(t *testing.T) {
	hermeticEnv(t)
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("DEJA_CONFIG_FILE", cfgPath)

	// Absent config: doctor --json omits "config", text prints default path.
	rep := collectDoctorReport(nil, os.Getenv("DEJA_INDEX_DIR"))
	if rep.Config != nil {
		t.Fatalf("expected nil report.Config when config file absent, got %+v", rep.Config)
	}
	var text bytes.Buffer
	doctorConfig(&text)
	if !strings.Contains(text.String(), "User config:") || !strings.Contains(text.String(), cfgPath) {
		t.Fatalf("absent doctorConfig text = %q", text.String())
	}

	// Present valid config with api_key (0644) and an unknown key: reports keys & warnings, never secret value.
	secret := "sk-doctor-secret-value"
	writeTestConfig(t, cfgPath, `
[embed]
api_key = "`+secret+`"
model = "text-embedding-3-small"
async_batch = true
unknown_opt = true

[hooks]
recall = "safe"
`)
	if err := os.Chmod(cfgPath, 0o644); err != nil {
		t.Fatal(err)
	}

	text.Reset()
	doctorConfig(&text)
	outText := text.String()
	if strings.Contains(outText, secret) {
		t.Fatalf("doctorConfig text leaked api_key: %q", outText)
	}
	if !strings.Contains(outText, "embed.unknown_opt") {
		t.Fatalf("doctorConfig text missing unknown key: %q", outText)
	}
	if runtime.GOOS != "windows" && !strings.Contains(outText, "chmod 600") {
		t.Fatalf("doctorConfig text missing chmod 600 warning: %q", outText)
	}

	rep = collectDoctorReport(nil, os.Getenv("DEJA_INDEX_DIR"))
	if rep.Config == nil || !rep.Config.Exists || !rep.Config.Valid {
		t.Fatalf("unexpected rep.Config: %+v", rep.Config)
	}
	if !reflect.DeepEqual(rep.Config.Unknown, []string{"embed.unknown_opt"}) {
		t.Fatalf("rep.Config.Unknown = %v", rep.Config.Unknown)
	}
	for _, wantKey := range []string{"embed.api_key", "embed.async_batch", "embed.model", "hooks.recall"} {
		if !slices.Contains(rep.Config.Keys, wantKey) {
			t.Fatalf("rep.Config.Keys = %v, missing %q", rep.Config.Keys, wantKey)
		}
	}
	rawJSON, err := json.Marshal(rep.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawJSON), secret) {
		t.Fatalf("doctor --json leaked api_key: %s", rawJSON)
	}

	// Broken config reports unreadable/invalid without failing doctor.
	writeTestConfig(t, cfgPath, "[embed]\nevery = \"soon\"\n")
	text.Reset()
	doctorConfig(&text)
	if !strings.Contains(text.String(), "unreadable") && !strings.Contains(text.String(), "invalid") {
		t.Fatalf("expected invalid/unreadable status in doctorConfig text, got %q", text.String())
	}
	rep = collectDoctorReport(nil, os.Getenv("DEJA_INDEX_DIR"))
	if rep.Config == nil || !rep.Config.Exists || rep.Config.Valid || rep.Config.Error == "" {
		t.Fatalf("broken rep.Config = %+v", rep.Config)
	}
}
