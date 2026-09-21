package index

import (
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/redact"
)

// The rule name inside a marker is read back out of text, and text can hold
// something that only looks like one: deja's own documentation of the format, a
// test fixture, an answer quoting an earlier answer. Taking those at their word
// invented rules called `<kind>` and `…` with 17 and 3 hits on a real store
// (#536).
func TestMarkerKindsReadsOnlyRulesTheRedactorCanWrite(t *testing.T) {
	text := "the format is " + redact.Marker + "<kind>] and here is a real one: " +
		redact.Marker + "github-token] plus " + redact.Marker + "jwt] and " +
		redact.Marker + "…]"
	got := markerKinds(text)
	want := []string{"github-token", "jwt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("markerKinds read %v, want %v", got, want)
	}
	if !SecretKindNamed("github-token") {
		t.Error("a provider key is not named")
	}
	if SecretKindNamed("entropy") {
		t.Error("the entropy rule is listed as a credential")
	}
}

// One session pasting the same key into three turns is one finding with a
// count, not three lines.
func TestScanSecretsGroupsASessionsTurnsIntoOneFinding(t *testing.T) {
	dir := t.TempDir() + "/index.db"
	when := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	msg := func(text string) model.Message {
		return model.Message{Role: "user", Text: text, Time: when}
	}
	writeStore(t, dir, []model.Session{{
		ID: "s1", Harness: "claude", Project: "work/app", Path: "/tmp/s1.jsonl",
		Updated: when,
		Messages: []model.Message{
			msg("export GITHUB_TOKEN=" + redact.Marker + "github-token]"),
			msg("again with " + redact.Marker + "github-token]"),
			msg("and a digest " + redact.Marker + "entropy]"),
		},
	}})
	scan, err := ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Findings) != 1 {
		t.Fatalf("want one finding, got %d: %+v", len(scan.Findings), scan.Findings)
	}
	f := scan.Findings[0]
	if f.Kind != "github-token" || f.Count != 2 || f.Harness != "claude" {
		t.Errorf("wrong finding: %+v", f)
	}
	if f.Path != "/tmp/s1.jsonl" {
		t.Errorf("the transcript holding it was not named: %q", f.Path)
	}
	if scan.Sessions != 1 {
		t.Errorf("want one session, got %d", scan.Sessions)
	}
	if scan.Counted["entropy"] != 1 {
		t.Errorf("the entropy tier was not counted: %+v", scan.Counted)
	}
}
