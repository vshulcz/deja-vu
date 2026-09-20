package sources

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTheWrittenSideIsHashedNotStored(t *testing.T) {
	const line = "raised default_pool_size to 40 in pgbouncer.ini"
	rec := WroteRecord("/w/pool.go", line+"\n}\n\treturn nil\n"+line)
	if rec == "" {
		t.Fatal("a write of a line long enough to be evidence produced no record")
	}
	path, rest, ok := strings.Cut(rec, "\n")
	if !ok || path != "/w/pool.go" {
		t.Fatalf("record does not start with its path: %q", rec)
	}
	// None of the text itself: the written side is the one place a secret an
	// agent typed into a file would otherwise land in a second store.
	if strings.Contains(rest, "pool_size") || strings.Contains(rest, "pgbouncer") {
		t.Errorf("the record carries the written text: %q", rest)
	}
	// One hash: the short lines are below the floor and the repeat is a repeat.
	if got := len(strings.Fields(rest)); got != 1 {
		t.Errorf("record holds %d hashes, want 1: %q", got, rest)
	}

	h, ok := HashWrittenLine(line)
	if !ok {
		t.Fatal("the line is not hashable")
	}
	gotPath, has := WroteRecordHas(rec, h)
	if !has || gotPath != "/w/pool.go" {
		t.Errorf("the record does not answer for the line it holds: %q %v", gotPath, has)
	}
	other, _ := HashWrittenLine("something else entirely, long enough to count")
	if _, has := WroteRecordHas(rec, other); has {
		t.Error("the record answers for a line nobody wrote")
	}
}

func TestAShortLineIsNotEvidence(t *testing.T) {
	// `}` and `return nil` are in every commit and in every session, so a match
	// on them attributes a line to whichever session happened to be indexed.
	for _, line := range []string{"}", "\treturn nil", "if err != nil {", "package main"} {
		if _, ok := HashWrittenLine(line); ok {
			t.Errorf("%q counts as evidence; it is %d runes folded", line, len([]rune(WrittenLineKey(line))))
		}
	}
	// And the floor is on the folded form, so indentation cannot lift a line
	// over it.
	long := strings.Repeat(" ", 40) + "short"
	if _, ok := HashWrittenLine(long); ok {
		t.Error("whitespace lifted a short line over the floor")
	}
}

func TestTheWrittenKeyIsTheSameOnBothSides(t *testing.T) {
	// A written line and the git diff line it becomes differ in leading
	// whitespace and nothing else; if the two normalisations drift, attribution
	// silently stops working and looks like a ranking problem.
	written := "\t\tif err := sources.StoresSelectionError(); err != nil {"
	diff := "                if err := sources.StoresSelectionError(); err != nil {"
	a, ok1 := HashWrittenLine(written)
	b, ok2 := HashWrittenLine(diff)
	if !ok1 || !ok2 || a != b {
		t.Fatalf("the same line hashes differently: %v %v %d %d", ok1, ok2, a, b)
	}
}

func TestClaudeWritesAreRecordedForEveryToolShape(t *testing.T) {
	const newLine = "pool, err := pgxpool.NewWithConfig(ctx, cfg)"
	const contentLine = "// Package pool holds the database pool and its configuration."
	const multiLine = "cfg.MaxConns = int32(size) // default_pool_size, from the ini"
	raw := `[
	  {"type":"tool_use","input":{"file_path":"/w/pool.go","old_string":"x","new_string":"` + newLine + `"}},
	  {"type":"tool_use","input":{"file_path":"/w/doc.go","content":"` + contentLine + `"}},
	  {"type":"tool_use","input":{"file_path":"/w/cfg.go","edits":[{"old_string":"y","new_string":"` + multiLine + `"}]}}
	]`
	got := claudeWroteRecords([]byte(raw))
	if len(got) != 3 {
		t.Fatalf("got %d records, want one per call: %q", len(got), got)
	}
	for i, want := range []string{newLine, contentLine, multiLine} {
		h, ok := HashWrittenLine(want)
		if !ok {
			t.Fatalf("%q is not evidence", want)
		}
		if _, has := WroteRecordHas(got[i], h); !has {
			t.Errorf("record %d does not hold the line the call wrote: %q", i, got[i])
		}
	}

	// The reference parser has to agree with the decoder, the way the replaced
	// side already does: a store read through one and not the other attributes
	// on some machines and not others.
	var content []any
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		t.Fatal(err)
	}
	ref := wroteRecordsFromContent(content)
	if strings.Join(ref, "|") != strings.Join(got, "|") {
		t.Errorf("the two parsers disagree:\n decoder %q\n reference %q", got, ref)
	}
}

func TestTheAddedSideOfAPatchIsRecorded(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: internal/pool/pool.go
@@
-	cfg.MaxConns = 10
+	cfg.MaxConns = int32(size) // default_pool_size, from the ini
*** End Patch`
	got := addedLinesOfPatch(patch)
	if len(got) != 1 {
		t.Fatalf("got %d records: %q", len(got), got)
	}
	h, _ := HashWrittenLine("cfg.MaxConns = int32(size) // default_pool_size, from the ini")
	path, has := WroteRecordHas(got[0], h)
	if !has {
		t.Errorf("the added line is not in the record: %q", got[0])
	}
	if path != "internal/pool/pool.go" {
		t.Errorf("record is about %q", path)
	}
	// And the removed side is not: it is the other rule's evidence, and a hash
	// of it would attribute a line that no longer exists.
	old, _ := HashWrittenLine("cfg.MaxConns = 10 and some more text to clear the floor")
	if _, has := WroteRecordHas(got[0], old); has {
		t.Error("a removed line was hashed as written")
	}
}

func TestTheWrittenSideCanBeTurnedOff(t *testing.T) {
	t.Setenv("DEJA_INDEX_WRITES", "0")
	if IndexWrites() {
		t.Error("DEJA_INDEX_WRITES=0 leaves the written side on")
	}
	t.Setenv("DEJA_INDEX_WRITES", "")
	if !IndexWrites() {
		t.Error("the written side is off by default; attribution nobody enables attributes nothing")
	}
}
