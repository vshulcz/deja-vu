package index

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A deleted transcript that shares its id with a listed 4.x conversation but
// is not that conversation's conversion keeps its turns, as any deleted
// transcript does (#2970). Only the transcript the converter's marker names
// hands the conversation back to the 4.x index when it goes.
//
// The incremental pass and a rebuild start from copies of the same index, as
// TestDeleteMatchesRebuild does: a rebuild of an empty directory has no
// record of a transcript deleted before it ran, so it is not the oracle here.
func TestCodeBuddyIDE4UnrelatedDeletedTranscriptIsKept(t *testing.T) {
	for _, tc := range []struct {
		name string
		// write puts the unrelated transcript in place and names its file
		// and the session key it is read under.
		write func(t *testing.T, f cb4Fixture) (path, key string)
	}{
		{"codebuddy transcript without a marker", func(t *testing.T, f cb4Fixture) (string, string) {
			t.Helper()
			p := filepath.Join(f.cliRoot, "work-p", cb4SessionID+".jsonl")
			cb4Write(t, p, cb4CLILine(t, cb4SessionID, "c1", "user", "gravelstone cli question", cb4StartedMs)+
				cb4CLILine(t, cb4SessionID, "c2", "assistant", "gravelstone cli answer", cb4StartedMs+60_000))
			cb4Stamp(t, p)
			return p, "codebuddy:" + cb4SessionID
		}},
		{"claude transcript", func(t *testing.T, f cb4Fixture) (string, string) {
			t.Helper()
			root := filepath.Join(filepath.Dir(f.cliRoot), "claude")
			t.Setenv("DEJA_CLAUDE_ROOT", root)
			p := filepath.Join(root, "-work-p", cb4SessionID+".jsonl")
			cb4Write(t, p, claudeLine(cb4SessionID, "2026-03-04T09:01:00Z", "gravelstone cli question")+
				claudeLine(cb4SessionID, "2026-03-04T09:02:00Z", "gravelstone cli answer"))
			cb4Stamp(t, p)
			return p, "claude:" + cb4SessionID
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, inc := cb4Env(t)
			cb4Seed(t, f)
			transcript, key := tc.write(t, f)
			indexPass(t, inc)
			if got := cb4Sessions(t, inc, "gravelstone"); !reflect.DeepEqual(got, []string{key}) {
				t.Fatalf("before the delete, gravelstone = %v, want %s", got, key)
			}
			full := inc + "-full"
			copyDir(t, inc, full)
			if err := os.Remove(transcript); err != nil {
				t.Fatal(err)
			}
			parityPass(t, inc, false)
			parityPass(t, full, true)
			for _, dir := range []string{inc, full} {
				pass := filepath.Base(dir)
				if got := cb4Sessions(t, dir, "gravelstone"); !reflect.DeepEqual(got, []string{key}) {
					t.Errorf("%s: the deleted transcript's turns are gone: gravelstone = %v, want %s", pass, got, key)
				}
				if got := cb4Sessions(t, dir, "quasithorn"); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4SessionID}) {
					t.Errorf("%s: the 4.x conversation = %v", pass, got)
				}
				m, err := readManifest(dir)
				if err != nil {
					t.Fatal(err)
				}
				if fs, ok := m.Files[transcript]; !ok || !fs.Kept {
					t.Errorf("%s: the deleted transcript is not kept: %+v, %v", pass, fs, ok)
				}
			}
			diffSnaps(t, tc.name, takeSnap(t, inc), takeSnap(t, full))
		})
	}
}

// A deleted conversion whose marker is still there hands the conversation back
// to the 4.x index on a rebuild of the same index, as it does on an
// incremental pass: the turns only the transcript held go, and the 4.x turns
// are read again. A conversion deleted together with its marker is a
// conversation deleted, and is kept like any other.
func TestCodeBuddyIDE4DeletedConversionHandsBackOnBothPasses(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dropMarker bool
		// kept is whether the converted turns stay searchable.
		kept bool
	}{
		{"marker left", false, false},
		{"marker deleted with it", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, inc := cb4Env(t)
			cb4Seed(t, f)
			transcript := cb4Convert(t, f)
			indexPass(t, inc)
			if got := cb4Sessions(t, inc, "zephyrubium"); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4SessionID}) {
				t.Fatalf("the converted conversation = %v", got)
			}
			full := inc + "-full"
			copyDir(t, inc, full)
			if tc.dropMarker {
				if err := os.RemoveAll(filepath.Join(filepath.Dir(transcript), cb4SessionID)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(transcript); err != nil {
				t.Fatal(err)
			}
			parityPass(t, inc, false)
			parityPass(t, full, true)
			for _, dir := range []string{inc, full} {
				pass := filepath.Base(dir)
				if got := cb4KeyRows(t, dir, cb4SessionID); got != 1 {
					t.Errorf("%s: rows for codebuddy:%s = %d, want one", pass, cb4SessionID, got)
				}
				text := cb4Text(cb4Session(t, dir))
				if !strings.Contains(text, "vellumglow detail") {
					t.Errorf("%s: the 4.x conversation did not come back:\n%s", pass, text)
				}
				if got := strings.Contains(text, "zephyrubium"); got != tc.kept {
					t.Errorf("%s: converted turns kept = %v, want %v:\n%s", pass, got, tc.kept, text)
				}
				if strings.Count(text, "quasithorn question") != 1 {
					t.Errorf("%s: the shared turns are not read once:\n%s", pass, text)
				}
			}
			diffSnaps(t, tc.name, takeSnap(t, inc), takeSnap(t, full))
		})
	}
}
