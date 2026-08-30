package main

import (
	"strings"
	"testing"
)

// 485 gave `check -` a sentence for each way it comes back empty and left one
// case it could not tell apart: an index this build will not read is not a
// missing index, and saying so tells someone who has used deja for months that
// their history was never indexed. doctor has had the right words all along
// (#2680).
func TestCheckTellsAnOlderIndexFromNoIndex(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready bool
		older bool
		want  string
	}{
		{name: "no index at all", want: "no index to check against yet"},
		{name: "an index an older build wrote", older: true, want: "written by an older deja"},
		{name: "a healthy index with nothing to say", ready: true, want: "nothing found for this plan"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			restoreReady := planIndexReady
			restoreOlder := indexFormatDirection
			t.Cleanup(func() {
				planIndexReady = restoreReady
				indexFormatDirection = restoreOlder
			})
			planIndexReady = func(string) bool { return tc.ready }
			indexFormatDirection = func(string) int {
				if tc.older {
					return -1
				}
				return 0
			}
			var out, errOut strings.Builder
			if err := runCheckTo("", []string{"-"}, strings.NewReader("rewrite the widget pipeline"), &out, &errOut); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("want %q, got:\n%s", tc.want, errOut.String())
			}
			if out.String() != "" {
				t.Fatalf("findings keep stdout to themselves (#2566), got:\n%s", out.String())
			}
		})
	}
}
