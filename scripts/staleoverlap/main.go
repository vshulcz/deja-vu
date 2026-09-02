// Command staleoverlap fails when a pull request would silently revert what
// landed on its base while it was open.
//
// A squash merge applies the branch's own diff. If something merged ahead of
// it rewrote the same region, the diff still applies — GitHub calls the PR
// mergeable, both PRs are green, and the second merge restores the first one's
// old lines along with dropping its tests. That happened here: #2875 made the
// goose filter a deny-list at 09:34 and #2877, branched before it, put the
// allow-list back at 09:44 (#2911).
//
// Requiring every branch to be up to date would prevent it and costs a rebase
// per PR on a busy day. This is the narrow version of the same rule: a rebase
// is demanded only where the base has moved on a file the branch also touches,
// which is the only case that can revert anything.
//
//	go run ./scripts/staleoverlap -base origin/main
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

func main() {
	base := flag.String("base", "origin/main", "the branch this one will be merged into")
	head := flag.String("head", "HEAD", "the branch to check")
	flag.Parse()

	mergeBase, err := git("merge-base", *base, *head)
	if err != nil {
		fmt.Fprintln(os.Stderr, "staleoverlap:", err)
		os.Exit(2)
	}
	mine, err := changedFiles(mergeBase, *head)
	if err != nil {
		fmt.Fprintln(os.Stderr, "staleoverlap:", err)
		os.Exit(2)
	}
	theirs, err := changedFiles(mergeBase, *base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "staleoverlap:", err)
		os.Exit(2)
	}
	var overlap []string
	for f := range mine {
		if theirs[f] {
			overlap = append(overlap, f)
		}
	}
	if len(overlap) == 0 {
		fmt.Printf("nothing on %s has touched the %d file(s) this branch changes\n", *base, len(mine))
		return
	}
	sort.Strings(overlap)
	fmt.Fprintf(os.Stderr, "this branch changes %d file(s) that %s has changed since it was cut:\n", len(overlap), *base)
	for _, f := range overlap {
		fmt.Fprintln(os.Stderr, "  "+f)
	}
	fmt.Fprintf(os.Stderr, "rebase on %s and push again — merging as it stands would apply this branch's\n"+
		"version of those files over the newer one, which is a revert no check can see (#2911)\n", *base)
	os.Exit(1)
}

func changedFiles(from, to string) (map[string]bool, error) {
	out, err := git("diff", "--name-only", from, to)
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.TrimSpace(line); f != "" {
			files[f] = true
		}
	}
	return files, nil
}

func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
