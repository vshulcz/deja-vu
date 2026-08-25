package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The line that confirms a note was stored is the one a user reads to check
// they got it right, and it names a value they typed. An escape byte in it
// recoloured and rewound that line, and a 5000-character project printed whole
// — the listing surfaces have bounded both since #1090 (#1792).
func TestRememberEchoDoesNotHandTheTerminalWhateverWasTyped(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	hostile := "red\x1b[31mALERT\x1b[0m\rrewound"
	out := captureStdout(t, func() {
		if err := runRemember(index.DefaultDir(), []string{"--project", hostile, "the shard limit is 64"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("the confirmation carried an escape or a rewind: %q", out)
	}
	if !strings.Contains(out, "ALERT") {
		t.Errorf("the project the note was stored under is gone from the line: %q", out)
	}
}

func TestRememberEchoBoundsALongProject(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	long := strings.Repeat("w", 5000)
	out := captureStdout(t, func() {
		if err := runRemember(index.DefaultDir(), []string{"--project", long, "the shard limit is 64"}); err != nil {
			t.Fatal(err)
		}
	})
	if len(out) > 200 {
		t.Errorf("a 5000-character project printed %d bytes: %q", len(out), out[:120])
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the cut is not marked, so the line reads as the whole name: %q", out)
	}
}

// Over MCP the same value goes back into a model's context, where a frame
// marker matters as much as an escape byte does on a terminal.
func TestMCPRememberReplyBoundsTheProject(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	args, err := json.Marshal(map[string]string{
		"text":    "the shard limit is 64",
		"project": "proj\x1b[31mALERT\x1b[0m\r" + strings.Repeat("w", 3000),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := callMCPTool(index.DefaultDir(), "remember", args)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(result, "\x1b\r") {
		t.Errorf("the reply carried an escape or a rewind: %q", result[:80])
	}
	if len(result) > 200 {
		t.Errorf("the reply is %d bytes of project name: %q", len(result), result[:120])
	}
}

// A project of nothing but control bytes bounds down to nothing, and the
// confirmation used to trail off after "under".
func TestRememberEchoNamesAProjectThatBoundsToNothing(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	out := captureStdout(t, func() {
		if err := runRemember(index.DefaultDir(), []string{"--project", "\x00\x01\x02", "the shard limit is 64"}); err != nil {
			t.Fatal(err)
		}
	})
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "remembered under") {
			line = l
		}
	}
	if strings.HasSuffix(strings.TrimSpace(line), "under") || strings.TrimSpace(line) == "deja: remembered under" {
		t.Errorf("the confirmation names no project: %q", line)
	}
	if !strings.Contains(line, "no printable characters") {
		t.Errorf("the line does not say why the name is missing: %q", line)
	}
}

// The project name has been bounded since #1792; the tags printed beside it
// were not, so a tag with an escape byte in it still rewrote the line (#1810).
func TestRememberEchoBoundsTheTagsToo(t *testing.T) {
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(t.TempDir(), "notes.jsonl"))
	t.Setenv("DEJA_INDEX_DIR", filepath.Join(t.TempDir(), "index.db"))
	out := captureStdout(t, func() {
		err := runRemember(index.DefaultDir(), []string{
			"--project", "proj",
			"--tag", "ok",
			"--tag", "red\x1b[31mALERT\x1b[0m\rrewound",
			"--tag", strings.Repeat("w", 400),
			"the shard limit is 64",
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	if strings.ContainsAny(out, "\x1b\r") {
		t.Errorf("the confirmation carried an escape or a rewind: %q", out)
	}
	if len(out) > 200 {
		t.Errorf("the confirmation is %d bytes of tags: %q", len(out), out[:120])
	}
	if !strings.Contains(out, "#ok") {
		t.Errorf("the tags the note was filed under are gone: %q", out)
	}
	if !strings.Contains(out, "under proj ") {
		t.Errorf("the project and its tags ran together: %q", out)
	}
}
