package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPromptCachePermissionsEnforcedAndTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes only")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the generated prompt cache module")
	}

	dir := t.TempDir()
	modPath := filepath.Join(dir, "cache.mjs")
	modSrc := promptCacheImportsJS + "\nconst DEJA = \"/usr/local/bin/deja\";\n" + promptCacheStoreJS +
		"\nexport { promptCachePath, writePromptCacheFile, createSessionStore };\n"
	if err = os.WriteFile(modPath, []byte(modSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	driverPath := filepath.Join(dir, "drive.mjs")
	driverSrc := `
import { dirname } from "node:path";
import { createSessionStore, promptCachePath } from "./cache.mjs";

process.umask(0o022);
const store = createSessionStore("test-harness");
store.write("ses_1", 0, (cur) => {
  cur.digest = "DIGEST-1";
});
const file = promptCachePath("test-harness");
console.log(JSON.stringify({ dir: dirname(file), file }));
`
	if err = os.WriteFile(driverPath, []byte(driverSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	runWithXDG := func(xdg string) (cacheDir, cacheFile string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), node, driverPath)
		baseEnv := os.Environ()
		env := make([]string, 0, len(baseEnv)+2)
		for _, kv := range baseEnv {
			if strings.HasPrefix(kv, "XDG_CACHE_HOME=") || strings.HasPrefix(kv, "DEJA_PROMPT_CACHE_FILE=") {
				continue
			}
			env = append(env, kv)
		}
		env = append(env, "XDG_CACHE_HOME="+xdg, "DEJA_PROMPT_CACHE_FILE=")
		cmd.Env = env
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("running prompt cache driver: %v\n%s", runErr, out)
		}
		cacheDir = filepath.Join(xdg, "deja-vu")
		cacheFile = filepath.Join(cacheDir, "test-harness.json")
		return cacheDir, cacheFile
	}

	assertModes := func(label, cacheDir, cacheFile string) {
		t.Helper()
		dirInfo, statErr := os.Stat(cacheDir)
		if statErr != nil {
			t.Fatalf("%s: stat cache dir: %v", label, statErr)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Errorf("%s: cache dir %s mode = %#o, want 0700", label, cacheDir, got)
		}
		fileInfo, fileErr := os.Stat(cacheFile)
		if fileErr != nil {
			t.Fatalf("%s: stat cache file: %v", label, fileErr)
		}
		if got := fileInfo.Mode().Perm(); got != 0o600 {
			t.Errorf("%s: cache file %s mode = %#o, want 0600", label, cacheFile, got)
		}
	}

	freshXDG := filepath.Join(dir, "fresh-xdg")
	freshDir, freshFile := runWithXDG(freshXDG)
	assertModes("fresh", freshDir, freshFile)

	looseXDG := filepath.Join(dir, "loose-xdg")
	looseDir := filepath.Join(looseXDG, "deja-vu")
	looseFile := filepath.Join(looseDir, "test-harness.json")
	if err = os.MkdirAll(looseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(looseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(looseFile, []byte(`{"version":1,"sessions":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(looseFile, 0o644); err != nil {
		t.Fatal(err)
	}
	runWithXDG(looseXDG)
	assertModes("tightened", looseDir, looseFile)
}

func TestPromptCacheTempBinIsolationAndProtoSafety(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the generated prompt cache module")
	}

	dir := t.TempDir()
	tempBinDir := filepath.Join(os.TempDir(), "deja-pc-test-"+filepath.Base(dir))
	if err = os.MkdirAll(tempBinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempBinDir) })
	tempBin := filepath.Join(tempBinDir, "deja")

	modPath := filepath.Join(dir, "cache.mjs")
	modSrc := promptCacheImportsJS + "\nconst DEJA = process.env.TEST_TEMP_BIN;\n" + promptCacheStoreJS +
		"\nexport { promptCachePath, createSessionStore };\n"
	if err = os.WriteFile(modPath, []byte(modSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	xdgDir := filepath.Join(dir, "xdg")
	overrideFile := filepath.Join(dir, "override.json")
	driverPath := filepath.Join(dir, "drive.mjs")
	driverSrc := `
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { createSessionStore, promptCachePath } from "./cache.mjs";

const tempExpected = join(dirname(process.env.TEST_TEMP_BIN), ".deja-cache", "iso-test.json");
assert.equal(promptCachePath("iso-test"), tempExpected);

process.env.DEJA_PROMPT_CACHE_FILE = process.env.TEST_OVERRIDE_FILE;
assert.equal(promptCachePath("iso-test"), process.env.TEST_OVERRIDE_FILE);
delete process.env.DEJA_PROMPT_CACHE_FILE;

const store = createSessionStore("iso-test", 2);
for (const scope of ["constructor", "toString", "valueOf", "hasOwnProperty", "__proto__"]) {
  const empty = store.read(scope);
  assert.equal(empty.epoch, 0);
  assert.equal(empty.session.recalled.toString, undefined);
  assert.equal(empty.session.repairs.constructor, undefined);
  const w = store.write(scope, 0, (cur) => {
    cur.digest = "D-" + scope;
    cur.recalled[scope] = "R-" + scope;
    cur.repairs[scope] = "F-" + scope;
    cur.recalled.marker = "OK";
  });
  assert.equal(w.stale, false);
}
// With maxSessions=2, keeping "__proto__" must work without prototype collision.
assert.equal(store.read("__proto__").session.recalled["__proto__"], "R-__proto__");
assert.equal(store.read("__proto__").session.repairs["__proto__"], "F-__proto__");
assert.equal(store.read("__proto__").session.recalled.constructor, undefined);
assert.equal(existsSync(join(process.env.XDG_CACHE_HOME, "deja-vu", "iso-test.json")), false);
assert.equal(existsSync(tempExpected), true);
`
	if err = os.WriteFile(driverPath, []byte(driverSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), node, driverPath)
	baseEnv := os.Environ()
	env := make([]string, 0, len(baseEnv)+4)
	for _, kv := range baseEnv {
		if strings.HasPrefix(kv, "XDG_CACHE_HOME=") || strings.HasPrefix(kv, "DEJA_PROMPT_CACHE_FILE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"XDG_CACHE_HOME="+xdgDir,
		"DEJA_PROMPT_CACHE_FILE=",
		"TEST_TEMP_BIN="+tempBin,
		"TEST_OVERRIDE_FILE="+overrideFile,
	)
	cmd.Env = env
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("running temp-bin & proto safety test: %v\n%s", runErr, out)
	}
}

func TestAllPromptCacheWritersImportChmodSyncAndEnforceModes(t *testing.T) {
	if !strings.Contains(promptCacheImportsJS, "chmodSync") {
		t.Fatal("promptCacheImportsJS does not import chmodSync")
	}
	checkPromptCacheContracts := func(label, src string) {
		t.Helper()
		for _, token := range []string{"chmodSync", "0o700", "0o600", "Object.create(null)"} {
			if !strings.Contains(src, token) {
				t.Errorf("%s handles prompt cache without %q", label, token)
			}
		}
		fnIdx := strings.Index(src, "function promptCachePath")
		if fnIdx < 0 {
			t.Errorf("%s missing function promptCachePath", label)
			return
		}
		body := src[fnIdx:]
		tempIdx := strings.Index(body, "isTempDejaBin(")
		xdgIdx := strings.Index(body, "XDG_CACHE_HOME")
		if tempIdx < 0 || xdgIdx < 0 || tempIdx >= xdgIdx {
			t.Errorf("%s must check isTempDejaBin before XDG_CACHE_HOME in promptCachePath", label)
		}
	}
	checkPromptCacheContracts("promptCacheStoreJS", promptCacheStoreJS)

	installFiles, globErr := filepath.Glob("install_*.go")
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(installFiles) == 0 {
		t.Fatal("expected install_*.go files in cmd/deja")
	}
	checkedInstallers := 0
	for _, path := range installFiles {
		if path == "install_prompt_cache.go" || strings.HasSuffix(path, "_test.go") {
			continue
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		src := string(raw)
		if !strings.Contains(src, "promptCacheStoreJS") {
			continue
		}
		checkedInstallers++
		if !strings.Contains(src, "promptCacheImportsJS") && !strings.Contains(src, "chmodSync") {
			t.Errorf("%s embeds promptCacheStoreJS without promptCacheImportsJS or chmodSync import", path)
		}
	}
	if checkedInstallers == 0 {
		t.Fatal("expected at least one installer embedding promptCacheStoreJS")
	}

	var extFiles []string
	walkErr := filepath.WalkDir(filepath.Join("..", "..", "extensions"), func(path string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			return wErr
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == "test" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext == ".js" || ext == ".mjs" || ext == ".ts" {
			extFiles = append(extFiles, path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	checkedExts := 0
	for _, path := range extFiles {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		src := string(raw)
		if !strings.Contains(src, "DEJA_PROMPT_CACHE_FILE") {
			continue
		}
		checkedExts++
		checkPromptCacheContracts(path, src)
	}
	if checkedExts == 0 {
		t.Fatal("expected at least one extension handling DEJA_PROMPT_CACHE_FILE")
	}
}

func TestOpencodePluginsIsolateTempDejaBinPromptCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell stub only")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the generated plugin scripts")
	}

	dir := t.TempDir()
	tempBinDir := filepath.Join(os.TempDir(), "deja-opencode-test-"+filepath.Base(dir))
	if err = os.MkdirAll(tempBinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempBinDir) })

	tempBin := filepath.Join(tempBinDir, "deja")
	if err = os.WriteFile(tempBin, []byte("#!/bin/sh\nprintf '{\"hookSpecificOutput\":{\"additionalContext\":\"<deja-recall>temp-digest</deja-recall>\"}}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	v2Path := filepath.Join(dir, "opencode-v2.mjs")
	if err = os.WriteFile(v2Path, []byte(opencodePluginJS(tempBin)), 0o600); err != nil {
		t.Fatal(err)
	}
	v1Path := filepath.Join(dir, "kilocode-v1.mjs")
	if err = os.WriteFile(v1Path, []byte(legacyPluginJSFor("kilocode-auto", tempBin)), 0o600); err != nil {
		t.Fatal(err)
	}

	userCache := filepath.Join(dir, "xdg-cache")
	workDir := filepath.Join(dir, "proj")
	if err = os.MkdirAll(workDir, 0o700); err != nil {
		t.Fatal(err)
	}

	driverPath := filepath.Join(dir, "drive.mjs")
	driverSrc := `
import assert from "node:assert/strict";
import v2Plugin from "./opencode-v2.mjs";
import { DejaRecall } from "./kilocode-v1.mjs";

const workDir = process.env.TEST_WORK_DIR;

const contextHooks = [];
await v2Plugin.setup({
  location: { directory: workDir },
  session: {
    hook(name, fn) {
      if (name === "context") contextHooks.push(fn);
    },
    async get() { return {}; },
  },
  tool: { async hook() {} },
});
assert.ok(contextHooks.length >= 1, "expected context hook");
const ev = { sessionID: "s-v2", system: [{ type: "text", text: "sys" }] };
await contextHooks[0](ev);
assert.ok(ev.system[0].text.includes("temp-digest"), "v2 plugin should receive digest");

const v1Hooks = await DejaRecall({
  directory: workDir,
  client: {
    session: { async get() { return {}; } },
    tui: { async showToast() {} },
  },
  $: () => ({
    text: async () => JSON.stringify({ hookSpecificOutput: { additionalContext: "<deja-recall>temp-v1</deja-recall>" } }),
    quiet: async () => {},
  }),
});
const out = { system: ["sys"] };
await v1Hooks["experimental.chat.system.transform"]({ sessionID: "s-v1" }, out);
assert.ok(out.system[0].includes("temp-v1"), "v1 plugin should receive digest");
`
	if err = os.WriteFile(driverPath, []byte(driverSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), node, driverPath)
	baseEnv := os.Environ()
	env := make([]string, 0, len(baseEnv)+3)
	for _, kv := range baseEnv {
		if strings.HasPrefix(kv, "XDG_CACHE_HOME=") || strings.HasPrefix(kv, "DEJA_PROMPT_CACHE_FILE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"XDG_CACHE_HOME="+userCache,
		"DEJA_PROMPT_CACHE_FILE=",
		"TEST_WORK_DIR="+workDir,
	)
	cmd.Env = env
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("running opencode temp-bin isolation driver: %v\n%s", runErr, out)
	}

	wantV2 := filepath.Join(tempBinDir, ".deja-cache", "opencode-auto.json")
	if _, statErr := os.Stat(wantV2); statErr != nil {
		t.Errorf("expected temp-isolated opencode-auto cache at %s: %v", wantV2, statErr)
	}
	wantV1 := filepath.Join(tempBinDir, ".deja-cache", "kilocode-auto.json")
	if _, statErr := os.Stat(wantV1); statErr != nil {
		t.Errorf("expected temp-isolated kilocode-auto cache at %s: %v", wantV1, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(userCache, "deja-vu")); !os.IsNotExist(statErr) {
		t.Errorf("temp deja bin leaked prompt cache into %s", filepath.Join(userCache, "deja-vu"))
	}
}

func TestPromptCacheBypassedWhenVaultEnabled(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is needed to run the generated prompt cache module")
	}

	dir := t.TempDir()
	modPath := filepath.Join(dir, "cache.mjs")
	modSrc := promptCacheImportsJS + "\nconst DEJA = \"deja\";\n" + promptCacheStoreJS +
		"\nexport { createSessionStore };\n"
	if err = os.WriteFile(modPath, []byte(modSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	vaultFile := filepath.Join(dir, "explicit-vault.json")
	if err = os.WriteFile(vaultFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	xdgCfg := filepath.Join(dir, "xdg-cfg")
	if err = os.MkdirAll(filepath.Join(xdgCfg, "deja"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(xdgCfg, "deja", "vault.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	seededCache := filepath.Join(dir, "seeded.json")
	seededBody := `{"version":1,"sessions":{"s1":{"epoch":3,"updatedAt":1,"digest":"secret-digest","recalled":{"k":"secret-val"},"repairs":{"r":"secret-rep"}}}}`
	if err = os.WriteFile(seededCache, []byte(seededBody), 0o600); err != nil {
		t.Fatal(err)
	}
	freshCache := filepath.Join(dir, "fresh.json")
	xdgFallbackCache := filepath.Join(dir, "xdg-fallback.json")

	driverPath := filepath.Join(dir, "drive.mjs")
	driverSrc := `
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { createSessionStore } from "./cache.mjs";

const store = createSessionStore("bypass-test");
process.env.DEJA_PROMPT_CACHE_FILE = process.env.TEST_SEEDED;
const r1 = store.read("s1");
assert.equal(r1.epoch, 0);
assert.equal(r1.session.digest, undefined);
assert.equal(r1.session.recalled.k, undefined);

process.env.DEJA_PROMPT_CACHE_FILE = process.env.TEST_FRESH;
store.write("s1", 0, (cur) => {
  cur.digest = "should-not-persist";
  cur.recalled.k = "should-not-persist";
});
store.compact("s1", true);
assert.equal(existsSync(process.env.TEST_FRESH), false);

delete process.env.DEJA_VAULT_FILE;
process.env.DEJA_PROMPT_CACHE_FILE = process.env.TEST_XDG_CACHE;
store.write("s2", 0, (cur) => {
  cur.digest = "xdg-vault-should-suppress";
});
assert.equal(existsSync(process.env.TEST_XDG_CACHE), false);
assert.equal(readFileSync(process.env.TEST_SEEDED, "utf8"), ` + "`" + seededBody + "`" + `);
`
	if err = os.WriteFile(driverPath, []byte(driverSrc), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), node, driverPath)
	baseEnv := os.Environ()
	env := make([]string, 0, len(baseEnv)+7)
	for _, kv := range baseEnv {
		if strings.HasPrefix(kv, "XDG_CACHE_HOME=") ||
			strings.HasPrefix(kv, "XDG_CONFIG_HOME=") ||
			strings.HasPrefix(kv, "DEJA_VAULT_FILE=") ||
			strings.HasPrefix(kv, "DEJA_PROMPT_CACHE_FILE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"XDG_CACHE_HOME="+filepath.Join(dir, "xdg-cache"),
		"XDG_CONFIG_HOME="+xdgCfg,
		"DEJA_VAULT_FILE="+vaultFile,
		"TEST_SEEDED="+seededCache,
		"TEST_FRESH="+freshCache,
		"TEST_XDG_CACHE="+xdgFallbackCache,
	)
	cmd.Env = env
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("running prompt cache vault bypass driver: %v\n%s", runErr, out)
	}
}
