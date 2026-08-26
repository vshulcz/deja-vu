package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A hook killed between a dedup line and its newline leaves half a line, and
// the next hook wrote onto it. The reader takes the first two fields of each
// line, so the glued line keeps the interrupted record and drops the one
// appended after it — leaving a session deja does not know it has already
// shown, which is the whole job of this file (#1967).
func TestTheDedupFileKeepsWhatWasWrittenAfterAKill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	rememberInjected(dir, "ses1", []model.Session{{ID: "sessA"}})

	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".hookseen", b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}

	rememberInjected(dir, "ses2", []model.Session{{ID: "sessB"}})

	if seen := alreadyInjected(dir, "ses2"); !seen["sessB"] {
		raw, _ := os.ReadFile(dir + ".hookseen")
		t.Errorf("the session shown to ses2 is not recorded, so it will be shown again:\n%s", raw)
	}
	// And the interrupted record still counts for the session it belonged to.
	if seen := alreadyInjected(dir, "ses1"); !seen["sessA"] {
		t.Error("the record the kill interrupted was lost as well")
	}
}

// The other writer of the same file: block fingerprints rather than sessions.
func TestTheDedupFileKeepsAFingerprintWrittenAfterAKill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "idx")
	rememberInjectedIDs(dir, "ses1", "fingerprint-a")

	b, err := os.ReadFile(dir + ".hookseen")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+".hookseen", b[:len(b)-1], 0o600); err != nil {
		t.Fatal(err)
	}

	rememberInjectedIDs(dir, "ses2", "fingerprint-b")

	if seen := alreadyInjected(dir, "ses2"); !seen["fingerprint-b"] {
		raw, _ := os.ReadFile(dir + ".hookseen")
		t.Errorf("the block shown to ses2 is not recorded, so the same words go out again:\n%s", raw)
	}
}
