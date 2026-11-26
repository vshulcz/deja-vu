package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A case found on a real Linux 4.x store: overlapping legacy roots.

// A Data directory and the history directory inside it can both be
// configured. Doctor walked each for files it did not place, so a stray file
// under the history was reported twice, and a workspace snapshot the IDE
// keeps under file-tree — a copy of a JSONL file the agent wrote — was
// reported as a transcript deja could not read.
func TestDoctorCodeBuddyIDE4OverlappingRootsCountOnce(t *testing.T) {
	legacy := cb4CmdEnv(t)
	index := cb4CmdConversation(t, legacy)
	history := filepath.Dir(filepath.Dir(filepath.Dir(index)))
	snapshot := filepath.Join(legacy, "u1", "CodeBuddyIDE", "u1", "file-tree", cb4CmdWS, cb4CmdID, "files", "call_1_predictions.jsonl")
	cb4CmdWrite(t, snapshot, []byte("{\"x\":1}\n"))
	cb4CmdWrite(t, filepath.Join(filepath.Dir(filepath.Dir(index)), "stray.json"), map[string]any{})
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", legacy+string(os.PathListSeparator)+history)
	row := cb4CmdDoctorRow(t)
	if !strings.Contains(row, "1 IDE 4.x conversation") {
		t.Errorf("row does not count the one conversation: %s", row)
	}
	if !strings.Contains(row, ", 1 not recognised here") {
		t.Errorf("row should report the one stray file once: %s", row)
	}
}

func TestOutermostRoots(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "Data")
	history := filepath.Join(data, "u1", "CodeBuddyIDE", "u1", "history")
	other := filepath.Join(base, "copy")
	for _, tc := range []struct {
		name  string
		roots []string
		want  []string
	}{
		{"one root", []string{data}, []string{data}},
		{"a root inside another", []string{history, data}, []string{data}},
		{"the same root twice", []string{data, data + string(filepath.Separator)}, []string{data}},
		{"unrelated roots", []string{data, other}, []string{data, other}},
		{"a sibling with a shared prefix", []string{data, data + "2"}, []string{data, data + "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := outermostRoots(tc.roots); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("outermostRoots(%v) = %v, want %v", tc.roots, got, tc.want)
			}
		})
	}
}
