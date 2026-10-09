package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// An empty window names the newest session and the --since that reaches it:
// "nothing in the last 1d" alone read like a machine with no history.
func TestAnEmptyRecapWindowNamesTheNewestSession(t *testing.T) {
	withStatsStores(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runRecap(dir, []string{"--since", "1d"}, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "the newest session is from") || !strings.Contains(got, "`deja recap --since ") {
		t.Fatalf("the empty window does not say where the history is:\n%s", got)
	}
}

// On a machine with no history, tests, secrets, recap and rules candidates
// answer in the words every other command uses, and point at `deja sources`.
// "no provider keys … in your agent history" read as an all-clear.
func TestEmptyStatesShareOnePhrasing(t *testing.T) {
	hermeticEnv(t)
	for _, args := range [][]string{{"tests"}, {"secrets"}, {"recap"}, {"rules", "candidates"}} {
		out, err := captureRun(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "`deja sources` shows where deja looked") {
			t.Errorf("%v on an empty machine: %q", args, out)
		}
	}
	out, err := captureRun(t, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "wrapped for sharing") {
		t.Errorf("an empty stats screen still prints its title:\n%s", out)
	}
}

// remember over an existing index used to open with "deja: updated 1 file
// (1 new message)" — the note it had just written, reported as news.
func TestRememberDoesNotNarrateItsOwnNote(t *testing.T) {
	withTempStores(t)
	if _, err := captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	said, err := captureRunStderr(t, "remember", "staging deploys need the VPN", "--project", "p")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(said, "updated ") {
		t.Fatalf("remember narrated the index:\n%s", said)
	}
}

// The word the reader typed is the part they got right.
func TestSyncNamesWhatIsMissing(t *testing.T) {
	hermeticEnv(t)
	for sub, want := range map[string]string{
		"export": "deja sync export <dir>",
		"import": "deja sync import <dir>",
		"ssh":    "deja sync ssh <host>",
	} {
		err := runSync(index.DefaultDir(), []string{sub})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("sync %s: %v", sub, err)
		}
	}
}
