package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #2608 fixed the English README's banner. The same claim lives in the places
// someone reads before they install anything — the site's hero stat, the
// Chinese README, a guide page — and the site's own token-cost page states the
// honest pair beside it ("about 0.4 ms in process, around 25 ms on the
// LongMemEval-S haystacks"). Measured in process on a 137 MB index an exact
// answer is 3.4–6.2 ms (#2610).
func TestNothingOutsideTheReadmePromisesSubMillisecond(t *testing.T) {
	root := "../.."
	var offenders []string
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			switch fi.Name() {
			case ".git", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(p) {
		case ".md", ".html", ".json", ".toml", ".yml", ".yaml":
		default:
			return nil
		}
		// The changelog is a record of what was said at the time.
		if strings.Contains(p, "CHANGELOG") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		text := string(b)
		for _, claim := range []string{"sub-millisecond", "亚毫秒", "under a millisecond", "in under a millisecond"} {
			if strings.Contains(text, claim) {
				offenders = append(offenders, strings.TrimPrefix(p, root+"/")+": "+claim)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("a lookup is milliseconds, not sub-millisecond, on a real store:\n  %s", strings.Join(offenders, "\n  "))
	}
}
