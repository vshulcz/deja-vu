package sources

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// What an Antigravity store keeps beside the transcript deja reads, and what
// it does not: the row that reports an unread transcript is only as good as
// this list, and a first cut that exempted everything outside a conversation
// swallowed a transcript restored beside one (#3377).
func TestAntigravitySidecarFilesClaimsOnlyWhatItKnows(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_ANTIGRAVITY_ROOT", root)
	brain := filepath.Join(root, "brain", "f017fad5", ".system_generated")
	for _, d := range []string{
		filepath.Join(brain, "logs", "chunks", "transcript"),
		filepath.Join(brain, "messages"),
		filepath.Join(root, "cache"),
		filepath.Join(root, "restored_backup"),
		filepath.Join(root, "brain", "sessB", ".system_generated", "logs"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	claimed := map[string]bool{
		filepath.Join(brain, "logs", "transcript_full.jsonl"):                                       true,
		filepath.Join(brain, "logs", "chunks", "transcript", "0.jsonl"):                             true,
		filepath.Join(brain, "messages", "8f1c2a3e.json"):                                           true,
		filepath.Join(root, "brain", "f017fad5", "task.md.metadata.json"):                           true,
		filepath.Join(root, "cache", "last_conversations.json"):                                     true,
		filepath.Join(root, "settings.json"):                                                        true,
		filepath.Join(brain, "logs", "transcript.jsonl"):                                            false,
		filepath.Join(root, "restored_backup", "transcript.jsonl"):                                  false,
		filepath.Join(root, "brain", "sessB", ".system_generated", "logs", "transcript_full.jsonl"): false,
	}
	for p := range claimed {
		write(p)
	}

	got := AntigravitySidecarFiles()
	for p, want := range claimed {
		has := slices.Contains(got, p)
		if has != want {
			verb := "claimed"
			if want {
				verb = "left out"
			}
			t.Errorf("%s: %s by the sidecar list", filepath.Base(filepath.Dir(p))+"/"+filepath.Base(p), verb)
		}
	}
}
