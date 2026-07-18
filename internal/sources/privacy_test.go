package sources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestPrivacyExclusions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("DEJA_EXCLUDE_PROJECTS", "*secret*, API")
	if err := os.MkdirAll(filepath.Dir(ExcludePath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExcludePath(), []byte("# comment\n  private  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	patterns := ExclusionPatterns()
	if len(patterns) != 3 || !ExcludedProject("my-private-app") || !ExcludedProject("secret-service") || !ExcludedProject("api-client") || ExcludedProject("public") {
		t.Fatalf("patterns=%v", patterns)
	}
	ss := []model.Session{{Project: "private"}, {Project: "public"}}
	filtered := FilterSessions(ss)
	if len(filtered) != 1 || filtered[0].Project != "public" {
		t.Fatalf("filtered=%#v", filtered)
	}
}
