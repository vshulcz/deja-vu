package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The one invariant this command cannot get wrong: it reports a credential
// without reprinting it. That holds structurally — redaction runs at ingest, so
// the index never held the value — and this pins it, because a later change
// that reads the source file to add a line number would break it silently.
func TestSecretsNamesTheKindAndNeverTheValue(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	// Minted rather than realistic: a value this test can grep the whole
	// output for. The shape is a provider prefix so the typed rule fires.
	planted := "ghp_" + strings.Repeat("s3cretzz", 5)
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-leak", "s1.jsonl"), "s1", []string{
		`{"type":"user","sessionId":"s1","timestamp":"2026-03-04T10:00:00Z","message":{"role":"user","content":"deploy with GITHUB_TOKEN=` + planted + `"}}`,
		`{"type":"assistant","sessionId":"s1","timestamp":"2026-03-04T10:01:00Z","message":{"role":"assistant","content":"pushed the tag"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSecrets(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "github-token") {
		t.Fatalf("the report does not name the kind:\n%s", got)
	}
	if strings.Contains(got, planted) || strings.Contains(got, "s3cretzz") {
		t.Fatal("the report printed the credential it found")
	}
	if !strings.Contains(got, "claude") || !strings.Contains(got, "2026-03-04") {
		t.Errorf("the report does not say where to look:\n%s", got)
	}
	if !strings.Contains(got, "s1.jsonl") {
		t.Errorf("the report does not name the transcript still holding it:\n%s", got)
	}
	// The sentence that stops a reader concluding deja is the one leaking.
	if !strings.Contains(got, "deja redacted its own") {
		t.Errorf("the report does not say whose copy is redacted:\n%s", got)
	}
}

// The entropy and assignment rules are half the markers on a real store and
// their largest class is digests, so they are a number at the bottom. Naming
// them as credentials is the failure this pins (#536).
func TestSecretsCountsTheEntropyTierWithoutListingIt(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-entropy", "s2.jsonl"), "s2", []string{
		`{"type":"user","sessionId":"s2","timestamp":"2026-03-05T10:00:00Z","message":{"role":"user","content":"integrity = \"sha512-QQhL9x2mkbLsP0X9rPPPPbbccRRvvTTjjaammKKiillMM==\""}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	scan, err := index.ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Findings) != 0 {
		t.Fatalf("an assignment-shaped hit was listed as a credential: %+v", scan.Findings)
	}
	if secretsCountedTotal(scan.Counted) == 0 {
		t.Fatal("the fixture produced no redaction marker at all, so this test proves nothing")
	}
	var out bytes.Buffer
	if err := runSecrets(dir, nil, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "no provider keys") {
		t.Errorf("a store with only entropy hits should read as clean:\n%s", got)
	}
	if !strings.Contains(got, "counted, not listed") {
		t.Errorf("the count is missing, so the number nobody can act on is invisible:\n%s", got)
	}
}

// One session that pasted one database URL into several turns is one thing to
// act on, not several — and the JSON keeps every hit, because a consumer asked
// for the data rather than for the screen.
func TestSecretsGroupsBySessionAndJSONKeepsEveryHit(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-two", "s3.jsonl"), "s3", []string{
		`{"type":"user","sessionId":"s3","timestamp":"2026-03-06T10:00:00Z","message":{"role":"user","content":"psql postgres://svc:hunter2hunter2@db.internal:5432/app"}}`,
		`{"type":"user","sessionId":"s3","timestamp":"2026-03-06T10:02:00Z","message":{"role":"user","content":"still failing: postgres://svc:hunter2hunter2@db.internal:5432/app"}}`,
		`{"type":"user","sessionId":"s3","timestamp":"2026-03-06T10:03:00Z","message":{"role":"user","content":"curl -H \"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345\" https://api.internal/v1/ping"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runSecrets(dir, []string{"--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var env secretsJSON
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if env.Kind != secretsJSONKind || env.Schema == 0 {
		t.Errorf("envelope = %+v, want the kind and a schema version", env)
	}
	byKind := map[string]int{}
	for _, f := range env.Findings {
		byKind[f.Kind] = f.Count
	}
	if byKind["url-credentials"] != 2 {
		t.Errorf("url-credentials count = %d, want both turns: %+v", byKind["url-credentials"], env.Findings)
	}
	if byKind["bearer-token"] != 1 {
		t.Errorf("bearer-token count = %d, want the one curl: %+v", byKind["bearer-token"], env.Findings)
	}
	if env.Sessions != 1 {
		t.Errorf("sessions = %d, want the one session both came from", env.Sessions)
	}
	// Two kinds, one session: the screen groups them under one heading.
	groups := groupSecrets(env.Findings)
	if len(groups) != 1 || len(groups[0].kinds) != 2 {
		t.Fatalf("grouping = %d groups, want one holding both kinds", len(groups))
	}
	var screen bytes.Buffer
	printSecrets(&screen, index.SecretScan{Findings: env.Findings, Sessions: env.Sessions}, 10)
	if got := screen.String(); !strings.Contains(got, "url-credentials ×2") {
		t.Errorf("the screen does not say how many turns held it:\n%s", got)
	}
	if strings.Contains(screen.String(), "hunter2") {
		t.Fatal("the screen printed the password from the connection string")
	}
}

// A flag deja does not know is refused rather than ignored, the way the other
// list commands refuse one (#2253).
func TestSecretsRefusesAnUnknownFlag(t *testing.T) {
	withStatsStores(t)
	var out bytes.Buffer
	if err := runSecrets(index.DefaultDir(), []string{"--limt", "3"}, &out); err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	if err := runSecrets(index.DefaultDir(), []string{"--limit"}, &out); err == nil {
		t.Fatal("--limit with no value was accepted")
	}
}

// A message that merely mentions the marker format is not a redaction. deja's
// own docs and tests quote it, and taking the text at its word invented rules
// called `<kind>` with 17 hits on a real store (#536).
func TestSecretsIgnoresTextThatOnlyLooksLikeAMarker(t *testing.T) {
	withStatsStores(t)
	claudeRoot := os.Getenv("DEJA_CLAUDE_ROOT")
	writeClaudeFixture(t, filepath.Join(claudeRoot, "-tmp-doc", "s4.jsonl"), "s4", []string{
		`{"type":"user","sessionId":"s4","timestamp":"2026-03-07T10:00:00Z","message":{"role":"user","content":"every value is replaced with [redacted:<kind>] and the docs show [redacted:…]"}}`,
	})
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	scan, err := index.ScanSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Findings) != 0 {
		t.Errorf("a mention of the format was read as a credential: %+v", scan.Findings)
	}
	for kind := range scan.Counted {
		if kind == "<kind>" || kind == "…" {
			t.Errorf("counted a rule the redactor never wrote: %q", kind)
		}
	}
}
