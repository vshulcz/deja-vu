package sources

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Cline rewrites taskHistory.json on every turn of any task. Only a change to
// the fields the reader takes from a task's own entry moves its fingerprint,
// or each turn would re-read every task the extension ever ran.
func TestClineVSCodeSidecarFollowsTheTasksOwnEntry(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "tasks", "1767300000000", "api_conversation_history.json")
	history := filepath.Join(root, "state", "taskHistory.json")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	write := func(body string) (int64, int64) {
		t.Helper()
		at = at.Add(time.Second)
		if err := os.WriteFile(history, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(history, at, at); err != nil {
			t.Fatal(err)
		}
		return clineVSCodeSidecar(transcript)
	}
	_, base := write(`[{"id":"1767300000000","ts":1767300000000,"task":"fix the retry loop","tokensIn":1},{"id":"2","task":"other"}]`)
	if base == 0 {
		t.Fatal("the task's entry left no fingerprint")
	}
	if _, got := write(`[{"id":"1767300000000","ts":1767300000000,"task":"fix the retry loop","tokensIn":9},{"id":"2","task":"other, renamed"}]`); got != base {
		t.Error("a token count and another task's rename moved this task's fingerprint")
	}
	if _, got := write(`[{"id":"1767300000000","ts":1767300000000,"task":"cap the retry at three","tokensIn":9}]`); got == base {
		t.Error("a rename of this task left its fingerprint as it was")
	}
}

// The reader takes the first entry under a task's id, so the fingerprint has
// to come from that one too, or a change to it is never read.
func TestClineVSCodeSidecarHashesTheEntryTheReaderTakes(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "tasks", "1767300000000", "api_conversation_history.json")
	history := filepath.Join(root, "state", "taskHistory.json")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	write := func(body string) int64 {
		t.Helper()
		at = at.Add(time.Second)
		if err := os.WriteFile(history, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(history, at, at); err != nil {
			t.Fatal(err)
		}
		_, stamp := clineVSCodeSidecar(transcript)
		return stamp
	}
	base := write(`[{"id":"1767300000000","task":"fix the retry loop"},{"id":"1767300000000","task":"stale copy"}]`)
	if write(`[{"id":"1767300000000","task":"cap the retry at three"},{"id":"1767300000000","task":"stale copy"}]`) == base {
		t.Error("a rename of the entry the reader takes left the fingerprint as it was")
	}
}

// Each sidecar is fingerprinted from the file the reader opens beside the
// transcript. A missing one counts as nothing, a directory in its place too,
// an unchanged one keeps the fingerprint and a rewrite moves it (#4446).
func TestSidecarsFingerprintTheFileBesideTheTranscript(t *testing.T) {
	root := t.TempDir()
	kimiState := filepath.Join(root, "kimi", "state.json")
	for _, c := range []struct {
		name       string
		transcript string
		sidecar    string
		stat       func(string) (int64, int64)
	}{
		{"grok summary", filepath.Join(root, "grok", "s1", "updates.jsonl"), filepath.Join(root, "grok", "s1", "summary.json"), besideSidecar("summary.json")},
		{"cline sdk manifest", filepath.Join(root, "cline", "s2", "s2.messages.json"), filepath.Join(root, "cline", "s2", "s2.json"), clineSDKSidecar},
		{"reasonix meta", filepath.Join(root, "reasonix", "s3.jsonl"), filepath.Join(root, "reasonix", "s3.meta.json"), reasonixSidecar},
		{"kimi state", filepath.Join(root, "kimi", "sessions", "s4", "wire.jsonl"), kimiState, kimiSidecar},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Dir(c.sidecar), 0o755); err != nil {
				t.Fatal(err)
			}
			if size, stamp := c.stat(c.transcript); size != 0 || stamp != 0 {
				t.Fatalf("missing sidecar = %d/%d, want nothing", size, stamp)
			}
			if err := os.Mkdir(c.sidecar, 0o755); err != nil {
				t.Fatal(err)
			}
			if size, stamp := c.stat(c.transcript); size != 0 || stamp != 0 {
				t.Fatalf("a directory in the sidecar's place = %d/%d, want nothing", size, stamp)
			}
			if err := os.Remove(c.sidecar); err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Hour)
			write := func(body string) {
				t.Helper()
				at = at.Add(time.Second)
				if err := os.WriteFile(c.sidecar, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(c.sidecar, at, at); err != nil {
					t.Fatal(err)
				}
			}
			write(`{"title":"fix the retry loop"}`)
			size, stamp := c.stat(c.transcript)
			if size == 0 || stamp == 0 {
				t.Fatalf("sidecar = %d/%d, want its size and time", size, stamp)
			}
			if s2, st2 := c.stat(c.transcript); s2 != size || st2 != stamp {
				t.Error("an unchanged sidecar moved the fingerprint")
			}
			write(`{"title":"cap the retry at three"}`)
			if s2, st2 := c.stat(c.transcript); s2 == size && st2 == stamp {
				t.Error("a renamed session left the fingerprint as it was")
			}
		})
	}
}

// No taskHistory.json, or one without this task, fingerprints nothing.
func TestClineVSCodeSidecarWithoutTheTasksEntry(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "tasks", "1767300000000", "api_conversation_history.json")
	if size, stamp := clineVSCodeSidecar(transcript); size != 0 || stamp != 0 {
		t.Errorf("no history = %d/%d, want nothing", size, stamp)
	}
	history := filepath.Join(root, "state", "taskHistory.json")
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte(`[{"id":"other","task":"something else"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if size, stamp := clineVSCodeSidecar(transcript); size != 0 || stamp != 0 {
		t.Errorf("history without the task = %d/%d, want nothing", size, stamp)
	}
}
