package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// TestEveryCLICommandRejectsBlankInputsBeforeIndexing enforces the CLI
// boundary contract: blank, whitespace-only, empty-selector, or unexpected
// positional arguments must be rejected at parse time before opening or
// building the session index.
func TestEveryCLICommandRejectsBlankInputsBeforeIndexing(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "untouched-index")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	cases := [][]string{
		{"origin", ""},
		{"origin", "   "},
		{"origin", "--", ""},
		{"origin", "--", "   "},
		{"origin", "", "HEAD"},
		{"review", ""},
		{"review", "   "},
		{"review", "--", ""},
		{"review", "--", "   "},
		{"review", "", "HEAD"},
		{"repo", "off", "extra"},
		{"repo", "on", "extra"},
		{"repo", "disable", "--bogus"},
		{"repo", "enable", "--bogus"},
		{"repo", "add", ""},
		{"repo", "add", "   "},
		{"repo", "add", "--kind", "", "note"},
		{"repo", "add", "--kind=", "note"},
		{"repo", "add", "--kind=   ", "note"},
		{"repo", "add", "--kind=bogus", "note"},
		{"repo", "rm", ""},
		{"repo", "rm", "   "},
		{"sync", "forget", ""},
		{"sync", "forget", "   "},
		{"sync", "forget", "host1", "host2"},
		{"sync", "export", ""},
		{"sync", "export", "   "},
		{"sync", "export", "--peer", "", "out"},
		{"sync", "export", "--peer", "   ", "out"},
		{"sync", "export", "dir1", "dir2"},
		{"sync", "import", ""},
		{"sync", "import", "   "},
		{"sync", "ssh", ""},
		{"sync", "ssh", "   "},
		{"sync", "ssh", "", "host2"},
		{"share", ""},
		{"share", "   "},
		{"share", `""`},
		{"share", "''"},
		{"promote", ""},
		{"promote", "   "},
		{"promote", `""`},
		{"promote", "''"},
		{"handoff", ""},
		{"handoff", "   "},
		{"handoff", `""`},
		{"handoff", "''"},
		{"remember", ""},
		{"remember", "   "},
		{"remember", "", "second"},
		{"shell", ""},
		{"shell", "   "},
		{"shell", "-c", ""},
		{"shell", "-c", "   "},
		{"warmup-status", "extra"},
		{"warmup-status", "--bogus"},
		{"show", ""},
		{"show", "   "},
		{"ctx", ""},
		{"ctx", "   "},
		{"blame", ""},
		{"blame", "   "},
		{"recall", ""},
		{"recall", "   "},
		{"fix", ""},
		{"fix", "   "},
		{"how", ""},
		{"how", "   "},
		{"files", ""},
		{"files", "   "},
		{"restore", ""},
		{"restore", "   "},
		{"resume", ""},
		{"resume", "   "},
	}

	for _, args := range cases {
		if err := run(args); err == nil {
			t.Errorf("run(%q) succeeded; want parse error before indexing", args)
		}
		if _, err := os.Stat(idxDir); !os.IsNotExist(err) {
			t.Fatalf("run(%q) touched index dir %s (stat err = %v)", args, idxDir, err)
		}
	}
}

// TestDoctorAndRuntimeShareEnvResolvers verifies that doctor and runtime
// commands consult the same environment helpers for DEJA_EMBED / DEJA_EMBED_OFF
// and DEJA_AIDER_ROOTS.
func TestDoctorAndRuntimeShareEnvResolvers(t *testing.T) {
	tmp := hermeticEnv(t)
	idxDir := filepath.Join(tmp, "idx")
	t.Setenv("DEJA_INDEX_DIR", idxDir)

	t.Setenv("DEJA_EMBED_URL", "")
	t.Setenv("DEJA_EMBED_OFF", "")
	t.Setenv("DEJA_EMBED", " OFF ")
	if !embed.Off() || !embedOff() {
		t.Fatal("DEJA_EMBED=' OFF ' must switch off both embed.Off() and embedOff()")
	}
	if rep := collectDoctorEmbed(idxDir); rep != nil {
		t.Fatalf("collectDoctorEmbed = %#v with DEJA_EMBED=' OFF ', want nil", rep)
	}

	r1 := filepath.Join(tmp, "aider-one")
	r2 := filepath.Join(tmp, "aider-two")
	sep := string(os.PathListSeparator)
	t.Setenv("DEJA_AIDER_ROOTS", sep+" "+r1+" "+sep+sep+r2+sep)

	gotRoots := sources.AiderRoots()
	if len(gotRoots) != 2 || gotRoots[0] != r1 || gotRoots[1] != r2 {
		t.Fatalf("sources.AiderRoots() = %#v, want [%q, %q]", gotRoots, r1, r2)
	}
	loc := doctorAiderLocation()
	wantSuffix := sep + r1 + sep + r2
	if !strings.HasSuffix(loc, wantSuffix) {
		t.Fatalf("doctorAiderLocation() = %q, want suffix %q", loc, wantSuffix)
	}
}
