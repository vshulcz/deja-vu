package index

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/query"
)

// manualStore indexes one Claude session and returns the index and the
// transcript, so a test can change the transcript after the build.
func manualStore(t *testing.T) (dir, transcript string) {
	t.Helper()
	tmp := t.TempDir()
	claudeRoot := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "policy.json"))
	t.Setenv(ManualEnv, "")
	proj := filepath.Join(claudeRoot, "-tmp-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	transcript = filepath.Join(proj, "man1.jsonl")
	line := `{"type":"user","sessionId":"man1","timestamp":"2026-01-02T03:04:05Z","message":{"role":"user","content":"the quetzalcoatl migration"}}` + "\n"
	if err := os.WriteFile(transcript, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(tmp, "idx")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	return dir, transcript
}

func growTranscript(t *testing.T, transcript string) {
	t.Helper()
	growTranscriptWith(t, transcript, "zanzibarquux")
}

func growTranscriptWith(t *testing.T, transcript, word string) {
	t.Helper()
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(`{"type":"user","sessionId":"man1","timestamp":"2026-01-02T03:05:05Z","message":{"role":"user","content":"` + word + ` arrived later"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
}

type indexStamp struct {
	manifest []byte
	mtime    int64
}

func stampOf(t *testing.T, dir string) indexStamp {
	t.Helper()
	p := filepath.Join(dir, "manifest.gob")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return indexStamp{b, fi.ModTime().UnixNano()}
}

// With automatic updates off every Ensure answers from the index on disk: a
// transcript that changed after the build stays out of it, and the manifest
// is not rewritten (#5385).
func TestManualModeEnsureLeavesTheIndexAlone(t *testing.T) {
	for _, viaFile := range []bool{false, true} {
		dir, transcript := manualStore(t)
		if viaFile {
			if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"auto_index": false}`), 0o600); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Setenv(ManualEnv, "1")
		}
		if !Manual() {
			t.Fatalf("switch not seen (policy file: %v)", viaFile)
		}
		growTranscript(t, transcript)
		o := query.Options{Query: "zanzibarquux", All: true}
		calls := map[string]func() error{
			"Ensure":          func() error { return Ensure(dir, "", false, io.Discard) },
			"Ensure force":    func() error { return Ensure(dir, "", true, io.Discard) },
			"EnsureForSearch": func() error { return EnsureForSearch(dir, o, false, io.Discard) },
			"EnsureUnlessBusy": func() error {
				_, err := EnsureUnlessBusy(dir, io.Discard)
				return err
			},
			"EnsureForSearchNoWait": func() error {
				_, err := EnsureForSearchNoWait(dir, o, io.Discard)
				return err
			},
			"EnsureForSearchStale": func() error {
				stale, err := EnsureForSearchStale(dir, o, io.Discard)
				if stale {
					t.Errorf("EnsureForSearchStale asked for a background refresh")
				}
				return err
			},
		}
		// Each call gets a change of its own, so one that refreshed cannot
		// hide the next.
		for name, call := range calls {
			growTranscriptWith(t, transcript, "later"+strings.ReplaceAll(name, " ", ""))
			before := stampOf(t, dir)
			if err := call(); err != nil {
				t.Errorf("%s with an index on disk: %v", name, err)
			}
			if after := stampOf(t, dir); !bytes.Equal(after.manifest, before.manifest) || after.mtime != before.mtime {
				t.Errorf("%s rewrote the manifest with automatic updates off (policy file: %v)", name, viaFile)
			}
		}
		if r, err := SearchDetailed(dir, o); err != nil || len(r.Sessions) != 0 {
			t.Errorf("the change reached the index: %d sessions (%v)", len(r.Sessions), err)
		}

		// The explicit build still writes, and is what picks the change up.
		undo := AllowBuild()
		if err := Ensure(dir, "", false, io.Discard); err != nil {
			t.Fatal(err)
		}
		undo()
		if r, err := SearchDetailed(dir, o); err != nil || len(r.Sessions) != 1 {
			t.Errorf("deja index did not pick the change up: %d sessions (%v)", len(r.Sessions), err)
		}
	}
}

// The control: with the switch off the same change is picked up by the next
// Ensure, so the test above is not passing on a store that never refreshes.
func TestDefaultModeEnsureStillRefreshes(t *testing.T) {
	dir, transcript := manualStore(t)
	t.Setenv(ManualEnv, "0")
	if err := os.WriteFile(os.Getenv("DEJA_POLICY_FILE"), []byte(`{"auto_index": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if Manual() {
		t.Fatal("DEJA_NO_AUTO_INDEX=0 did not win over the policy file")
	}
	before := stampOf(t, dir)
	growTranscript(t, transcript)
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if after := stampOf(t, dir); bytes.Equal(after.manifest, before.manifest) {
		t.Error("the default mode did not refresh the index")
	}
}

// With no index at all nothing is built; the error names the command.
func TestManualModeWithNoIndexSaysHowToBuildIt(t *testing.T) {
	manualStore(t)
	t.Setenv(ManualEnv, "1")
	dir := filepath.Join(t.TempDir(), "none")
	err := Ensure(dir, "", false, io.Discard)
	if !IsManualStop(err) || !strings.Contains(err.Error(), "deja index") {
		t.Fatalf("Ensure over no index = %v, want the command that builds it", err)
	}
	if _, err := EnsureForSearchStale(dir, query.Options{All: true}, io.Discard); !IsManualStop(err) {
		t.Errorf("EnsureForSearchStale over no index = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the index directory was created: %v", err)
	}
}

// A corrupt index is not rebuilt behind the reader's back either: the search
// says so and names the rebuild.
func TestManualModeCorruptIndexIsNotRebuilt(t *testing.T) {
	dir, _ := manualStore(t)
	t.Setenv(ManualEnv, "1")
	o := query.Options{Query: "quetzalcoatl"}
	if r, err := SearchWithRecoveryDetailed(dir, o, io.Discard); err != nil || len(r.Sessions) != 1 {
		t.Fatalf("clean search: %d sessions (%v)", len(r.Sessions), err)
	}
	if err := os.WriteFile(filepath.Join(dir, "buckets", bucket("tquetzalcoatl")+".bin"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := stampOf(t, dir)
	_, err := SearchWithRecoveryDetailed(dir, o, io.Discard)
	if !IsManualStop(err) || !strings.Contains(err.Error(), "deja index --rebuild") {
		t.Fatalf("search over a corrupt bucket = %v, want the rebuild named", err)
	}
	if after := stampOf(t, dir); !bytes.Equal(after.manifest, before.manifest) {
		t.Error("the corrupt index was rebuilt with automatic updates off")
	}
}
