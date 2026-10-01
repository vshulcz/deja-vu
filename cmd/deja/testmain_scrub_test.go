package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// harmless are variables the code reads that cannot point it at a store.
var harmless = map[string]bool{"COLUMNS": true, "NO_COLOR": true, "TERM": true}

// TestScrubHelperProcess runs only as the child of the test below: it reports
// every variable that reached it with the value the parent planted.
func TestScrubHelperProcess(t *testing.T) {
	if os.Getenv("SCRUB_PROBE") == "" {
		return
	}
	for _, kv := range os.Environ() {
		if name, value, _ := strings.Cut(kv, "="); strings.HasPrefix(value, "/leak/") {
			fmt.Println("LEAK " + name)
		}
	}
}

// Every variable deja reads is planted with a fake path in a copy of this
// binary; whatever TestMain lets through is a way for the suite to read or
// write a real store (#4178). A new variable fails here until it is scrubbed
// or named harmless.
func TestTestMainScrubsEveryVariableTheCodeReads(t *testing.T) {
	read := envNamesReadByCode(t)
	if len(read) < 50 {
		t.Fatalf("found only %d variables, the scan is broken", len(read))
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestScrubHelperProcess$")
	cmd.Env = append(os.Environ(), "SCRUB_PROBE=1")
	for _, name := range read {
		if name == "PATH" || name == "GOMAXPROCS" {
			continue
		}
		cmd.Env = append(cmd.Env, name+"=/leak/"+name)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	var leaked []string
	for _, line := range strings.Split(string(out), "\n") {
		if name, ok := strings.CutPrefix(line, "LEAK "); ok && !harmless[name] {
			leaked = append(leaked, name)
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("TestMain lets these through to the tests: %s", strings.Join(leaked, ", "))
	}
}

func envNamesReadByCode(t *testing.T) []string {
	t.Helper()
	re := regexp.MustCompile(`(?:Getenv|LookupEnv|EnvPath)\("([A-Z0-9_]+)"`)
	seen := map[string]bool{}
	for _, dir := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range re.FindAllSubmatch(src, -1) {
				seen[string(m[1])] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
