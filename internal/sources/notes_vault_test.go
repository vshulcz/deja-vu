package sources

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func TestPersonalNotesEncryptTransparentlyWithVault(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "sources-vault-pass")
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes.jsonl"))
	t.Setenv("DEJA_REPO_DIR", filepath.Join(tmp, "no-repo"))
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	idxDir := filepath.Join(tmp, "index.db")
	if _, err := vault.Init(idxDir, "sources-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}

	now := time.Now().UTC()
	if err := AppendNoteTagged("alpha", "canary personal secret note", []string{"sec"}, now); err != nil {
		t.Fatalf("AppendNoteTagged: %v", err)
	}
	if err := AppendNoteTagged("alpha", "canary personal secret note", []string{"sec"}, now); !errors.Is(err, ErrNoteExists) {
		t.Fatalf("duplicate AppendNoteTagged = %v, want ErrNoteExists", err)
	}
	if err := AppendPromotedSourced("alpha", "canary promo title", "canary decision body", "claude:s1", model.LifecycleAccepted, nil, now, now); err != nil {
		t.Fatalf("AppendPromotedSourced: %v", err)
	}

	raw, err := os.ReadFile(NotesFile())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "canary") || strings.Contains(string(raw), "alpha") {
		t.Fatalf("plaintext canary leaked in encrypted notes.jsonl: %s", raw)
	}

	// Inject a corrupt encrypted line alongside valid sealed lines; ParseNotesFile must still recover valid notes.
	if err := os.WriteFile(NotesFile(), append(raw, []byte(vault.NoteLinePrefix+"!badb64!\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseNotesFile(NotesFile())
	if err != nil {
		t.Fatalf("ParseNotesFile: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("parsed %d sessions, want 2: %+v", len(ss), ss)
	}
	if got := LoadNotes(); len(got) != 2 {
		t.Fatalf("LoadNotes = %d sessions, want 2", len(got))
	}

	if n, err := ForgetPromotedTitles(func(src string) bool { return src == "claude:s1" }); err != nil || n != 1 {
		t.Fatalf("ForgetPromotedTitles = (%d, %v)", n, err)
	}
	if n, err := ForgetPromotedNotes(func(id string) bool { return id == model.PromotedNoteID("claude:s1") }); err != nil || n != 1 {
		t.Fatalf("ForgetPromotedNotes = (%d, %v)", n, err)
	}
	afterRewrite, _ := os.ReadFile(NotesFile())
	if strings.Contains(string(afterRewrite), "canary") {
		t.Fatalf("rewriteNotes wrote plaintext canary: %s", afterRewrite)
	}

	// Lock vault: personal notes read/write/rewrite must fail closed.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if err := vault.Lock(idxDir, "sources-vault-pass"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	if err := AppendNoteTagged("alpha", "blocked", nil, now); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked AppendNoteTagged = %v, want ErrLocked", err)
	}
	if err := AppendPromotedSourced("alpha", "t", "b", "claude:s2", model.LifecycleAccepted, nil, now, now); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked AppendPromotedSourced = %v, want ErrLocked", err)
	}
	if _, err := ParseNotesFile(NotesFile()); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked ParseNotesFile = %v, want ErrLocked", err)
	}
	if _, err := ForgetPromotedNotes(func(string) bool { return true }); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked ForgetPromotedNotes = %v, want ErrLocked", err)
	}
}
