package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestProjectExcludePatternsCountsOnlyProjectRules(t *testing.T) {
	for _, tc := range []struct {
		name, file, projects, harnesses string
		want                            int
	}{
		{"empty", "", "", "", 0},
		{"environment harness only", "", "", "codex", 0},
		{"projects with environment harness", "secret-project\nother-project\n", "", "codex", 2},
		{"duplicate harness rules", "harness:codex\nharness:codex\nsecret\n", "", "codex", 1},
		{"mixed sources", "# comment\nsecret\nharness:codex\n", "other", "codex,aider", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_EXCLUDE_PROJECTS", tc.projects)
			t.Setenv("DEJA_EXCLUDE_HARNESSES", tc.harnesses)
			writeExclude(t, tc.file)
			if got := projectExcludePatterns(); got != tc.want {
				t.Errorf("project pattern count = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSourcesNamesTheHarnessExclusionSetting(t *testing.T) {
	for _, tc := range []struct{ name, file, harnesses string }{
		{"environment", "", " CoDeX , AIDER , OpenCode "},
		{"file", "harness:codex\nharness:aider\nharness:opencode\n", ""},
		{"both", "harness:codex\nharness:aider\nharness:opencode\n", "codex,aider,opencode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := hermeticEnv(t)
			t.Setenv("DEJA_STORES", "codex,aider,opencode")
			t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
			t.Setenv("DEJA_EXCLUDE_HARNESSES", tc.harnesses)
			writeExclude(t, tc.file)
			var out bytes.Buffer
			printSourcesTo(&out, filepath.Join(tmp, "index.db"))
			for _, name := range []string{"codex", "aider", "opencode"} {
				var row string
				for _, line := range strings.Split(out.String(), "\n") {
					if strings.HasPrefix(line, name+"\t") {
						row = line
						break
					}
				}
				if row == "" {
					t.Fatalf("missing %s row:\n%s", name, out.String())
				}
				if !strings.Contains(row, "excluded") || strings.Contains(row, "sessions=") {
					t.Errorf("excluded store was not reported correctly: %s", row)
				}
				if tc.harnesses != "" {
					if !strings.Contains(row, "DEJA_EXCLUDE_HARNESSES") || strings.Contains(row, "is in "+sources.ExcludePath()) {
						t.Errorf("environment exclusion names the wrong setting: %s", row)
					}
				} else if !strings.Contains(row, "`harness:"+name+"` is in "+sources.ExcludePath()) {
					t.Errorf("file exclusion lost its setting: %s", row)
				}
			}
		})
	}
}

func TestHiddenByOwnSettingsNamesProjectCountsAndHarnessSettings(t *testing.T) {
	for _, tc := range []struct {
		name, file, harnesses  string
		wantProject, wantStore string
	}{
		{"environment with project", "secret\n", "codex", "1 project pattern excluded", "1 store not read at all (DEJA_EXCLUDE_HARNESSES)"},
		{"environment only", "", " CoDeX , codex ", "", "1 store not read at all (DEJA_EXCLUDE_HARNESSES)"},
		{"file", "secret\nharness:codex\nharness:codex\n", "", "1 project pattern excluded", "1 store not read at all ($file)"},
		{"both for one store", "secret\nharness:codex\n", "codex", "1 project pattern excluded", "1 store not read at all (DEJA_EXCLUDE_HARNESSES)"},
		{"different stores", "secret\nharness:aider\n", "codex", "1 project pattern excluded", "2 stores not read at all ("},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hermeticEnv(t)
			t.Setenv("DEJA_EXCLUDE_PROJECTS", "")
			t.Setenv("DEJA_EXCLUDE_HARNESSES", tc.harnesses)
			writeExclude(t, tc.file)
			got := hiddenByOwnSettings()
			if tc.wantProject == "" {
				if strings.Contains(got, "project pattern") {
					t.Errorf("a harness rule was counted as a project: %s", got)
				}
			} else if !strings.Contains(got, tc.wantProject) {
				t.Errorf("missing %q in %q", tc.wantProject, got)
			}
			wantStore := strings.ReplaceAll(tc.wantStore, "$file", sources.ExcludePath())
			start := strings.Index(got, wantStore)
			if start < 0 {
				t.Fatalf("missing %q in %q", wantStore, got)
			}
			if tc.name == "different stores" {
				storeNote := got[start:]
				for _, setting := range []string{sources.ExcludePath(), "DEJA_EXCLUDE_HARNESSES"} {
					if !strings.Contains(storeNote, setting) {
						t.Errorf("store note does not name %s: %s", setting, storeNote)
					}
				}
			}
		})
	}
}
