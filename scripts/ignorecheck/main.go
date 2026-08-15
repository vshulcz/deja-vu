// Command ignorecheck verifies that .gitignore has a line for every package
// under scripts/.
//
// `go build ./scripts/<name>` drops a binary called <name> in the repository
// root. .gitignore is what stops it riding along in a commit — a 3.7 MB Mach-O
// reached main that way once. The list said "one line per scripts/ package" and
// was seven short, with three entries for packages that no longer exist, so the
// comment was doing the reassuring while the list did nothing.
//
//	go run ./scripts/ignorecheck
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	entries, err := os.ReadDir("scripts")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	packages := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// A directory is only a command if it holds Go source; scripts/demo is
		// python and never produces a binary here.
		matches, _ := filepath.Glob(filepath.Join("scripts", e.Name(), "*.go"))
		if len(matches) > 0 {
			packages[e.Name()] = true
		}
	}

	b, err := os.ReadFile(".gitignore")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ignored := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "/") && !strings.HasSuffix(line, "/") {
			ignored[strings.TrimPrefix(line, "/")] = true
		}
	}

	var missing []string
	for name := range packages {
		if !ignored[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "no .gitignore line for: %s\n", strings.Join(missing, ", "))
		fmt.Fprintln(os.Stderr, "add /<name> for each, or a stray binary will ride along in a commit")
		os.Exit(1)
	}
	fmt.Printf("every one of the %d scripts/ commands is ignored\n", len(packages))
}
