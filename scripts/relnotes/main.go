// Command relnotes prints the CHANGELOG section for one version, so a release
// page opens with what changed rather than with an install command.
//
// The section is already written by hand for every release — a lede paragraph
// and the Added/Changed/Fixed lists with their PR numbers. The release notes
// used to carry none of it: goreleaser's header was the install block and the
// body was a machine-generated list of commit subjects. Whoever landed on the
// page from Homebrew or npm read `curl … | sh` first and never learned what the
// release was.
//
//	go run ./scripts/relnotes 0.18.0        # section body, empty if absent
//	go run ./scripts/relnotes -check        # every released tag has a section
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const changelogPath = "CHANGELOG.md"

func main() {
	check := flag.Bool("check", false, "verify every version heading has a body")
	path := flag.String("changelog", changelogPath, "path to CHANGELOG.md")
	flag.Parse()

	body, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "relnotes: %v\n", err)
		os.Exit(1)
	}

	if *check {
		if err := checkSections(string(body)); err != nil {
			fmt.Fprintf(os.Stderr, "relnotes: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: relnotes <version>")
		os.Exit(2)
	}
	// A missing section is not an error: a release can be cut before the
	// changelog entry lands, and a release page with only the generated list is
	// worse than a failed release.
	fmt.Print(Section(string(body), flag.Arg(0)))
}

var versionHeading = regexp.MustCompile(`^## \[([^\]]+)\]`)

// Section returns the body under `## [version]`, without the heading, trimmed.
func Section(changelog, version string) string {
	version = strings.TrimPrefix(version, "v")
	var out []string
	inside := false
	s := bufio.NewScanner(strings.NewReader(changelog))
	s.Buffer(make([]byte, 4096), 1<<20)
	for s.Scan() {
		line := s.Text()
		if m := versionHeading.FindStringSubmatch(line); m != nil {
			if inside {
				break
			}
			inside = strings.TrimPrefix(m[1], "v") == version
			continue
		}
		if inside {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// checkSections reports a version heading with nothing under it. An empty
// section reads on the release page as a release that did nothing.
func checkSections(changelog string) error {
	var empty []string
	for _, m := range versionHeading.FindAllStringSubmatch(changelog, -1) {
		v := m[1]
		if strings.EqualFold(v, "Unreleased") {
			continue
		}
		if Section(changelog, v) == "" {
			empty = append(empty, v)
		}
	}
	if len(empty) > 0 {
		return fmt.Errorf("changelog sections with no body: %s", strings.Join(empty, ", "))
	}
	return nil
}
