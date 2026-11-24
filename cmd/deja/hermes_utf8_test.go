package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHermesPythonSubprocessUTF8UnderASCIIEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell stub required")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}

	catalogBytes, err := os.ReadFile(filepath.Join("..", "..", "extensions", "hermes", "__init__.py"))
	if err != nil {
		t.Fatalf("read extensions/hermes/__init__.py: %v", err)
	}

	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "deja")
	// Emits UTF-8 bytes for "статус: ок\n" regardless of locale.
	stubScript := "#!/bin/sh\nprintf '\\321\\201\\321\\202\\320\\260\\321\\202\\321\\203\\321\\201: \\320\\276\\320\\272\\n'\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	cases := []struct {
		name string
		src  string
	}{
		{name: "memory_provider", src: hermesMemoryPy(stub)},
		{name: "plugin_hook", src: hermesPluginPy(stub)},
		{name: "catalog_provider", src: string(catalogBytes)},
	}

	const want = "статус: ок"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "agent", "__init__.py"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "agent", "memory_provider.py"), []byte("class MemoryProvider:\n    pass\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "mod.py"), []byte(tc.src), 0o644); err != nil {
				t.Fatal(err)
			}

			outPath := filepath.Join(dir, "result.txt")
			driver := "import mod\n" +
				"with open(" + `"` + outPath + `"` + ", 'w', encoding='utf-8') as f:\n" +
				"    f.write(mod._deja(['status']))\n"

			env := make([]string, 0, len(os.Environ())+6)
			for _, kv := range os.Environ() {
				k, _, _ := strings.Cut(kv, "=")
				switch k {
				case "LC_ALL", "LC_CTYPE", "LANG", "PYTHONUTF8", "PYTHONCOERCECLOCALE", "PYTHONIOENCODING", "PATH":
					continue
				}
				env = append(env, kv)
			}
			env = append(env,
				"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"LC_ALL=C",
				"LC_CTYPE=C",
				"LANG=C",
				"PYTHONUTF8=0",
				"PYTHONCOERCECLOCALE=0",
				"PYTHONIOENCODING=",
			)

			cmd := exec.CommandContext(t.Context(), py, "-c", driver)
			cmd.Dir = dir
			cmd.Env = env
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				t.Fatalf("python3 driver: %v\n%s", cerr, out)
			}
			got, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("read result: %v", err)
			}
			if string(got) != want {
				t.Fatalf("_deja() = %q, want %q", string(got), want)
			}
		})
	}
}
