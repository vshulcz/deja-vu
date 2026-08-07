package sources

import (
	"strings"
	"testing"
)

// fakePG stands in for psql: it answers the fingerprint query from counts and
// the json_agg query from a canned array, and records the SQL it saw.
type fakePG struct {
	fingerprint string
	rows        string
	lastSQL     string
	err         error
}

func (f *fakePG) run(dsn, sql string) ([]byte, error) {
	f.lastSQL = sql
	if f.err != nil {
		return nil, f.err
	}
	if strings.HasPrefix(sql, "select count(*)") {
		return []byte(f.fingerprint + "\n"), nil
	}
	return []byte(f.rows), nil
}

func TestParseHermesPGReadsRowsLikeSQLite(t *testing.T) {
	f := &fakePG{rows: `[
		{"session_id":"s1","role":"user","content":"switch the pool to transaction mode","timestamp":1785000000},
		{"session_id":"s1","role":"assistant","content":"pgx was pinned instead","timestamp":1785000060},
		{"session_id":"s2","role":"user","content":"","timestamp":1785000120}
	]`}
	defer SetHermesPGRunner(f.run)()

	ss, err := ParseHermesPG("postgres://x", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("want 1 session (s2 has only empty content), got %d", len(ss))
	}
	if ss[0].ID != "s1" || len(ss[0].Messages) != 2 {
		t.Fatalf("session = %+v", ss[0])
	}
	if ss[0].Project != "hermes" || ss[0].Harness != "hermes" {
		t.Fatalf("project/harness = %q/%q", ss[0].Project, ss[0].Harness)
	}
	if ss[0].Title != "switch the pool to transaction mode" {
		t.Fatalf("title = %q", ss[0].Title)
	}
}

func TestParseHermesPGSinceFiltersByTimestamp(t *testing.T) {
	f := &fakePG{rows: `[]`}
	defer SetHermesPGRunner(f.run)()

	if _, err := ParseHermesPG("postgres://x", 0); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.lastSQL, "timestamp >") {
		t.Errorf("a full read carried a since clause: %s", f.lastSQL)
	}
	// 1785000000 unix is the cutoff; the query must ask only for newer rows.
	if _, err := ParseHermesPG("postgres://x", 1785000000*1_000_000_000); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.lastSQL, "timestamp > 1785000000") {
		t.Errorf("since clause missing: %s", f.lastSQL)
	}
}

func TestHermesPGFingerprintParsesCountAndNewest(t *testing.T) {
	f := &fakePG{fingerprint: "42|1785000000"}
	defer SetHermesPGRunner(f.run)()

	rows, newest, err := HermesPGFingerprint("postgres://x")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 42 {
		t.Errorf("rows = %d", rows)
	}
	if newest != 1785000000*1_000_000_000 {
		t.Errorf("newest nano = %d", newest)
	}
}

// deja reads Hermes' epoch-seconds timestamp. A store whose column is some
// other type (a timestamptz text like `2026-07-21 10:00:00+00`) must fail
// loudly: indexing with a zero watermark would silently miss every later row.
func TestHermesPGFingerprintRejectsAnUnreadableTimestamp(t *testing.T) {
	f := &fakePG{fingerprint: "7|2026-07-21 10:00:00+00"}
	defer SetHermesPGRunner(f.run)()

	if _, _, err := HermesPGFingerprint("postgres://x"); err == nil {
		t.Fatal("an unreadable max(timestamp) was accepted")
	} else if !strings.Contains(err.Error(), "epoch seconds") {
		t.Errorf("error does not explain the format: %v", err)
	}
}

// A fractional epoch second is the shape Hermes actually writes, and it must
// parse.
func TestHermesPGFingerprintReadsFractionalEpoch(t *testing.T) {
	f := &fakePG{fingerprint: "3|1785000000.5"}
	defer SetHermesPGRunner(f.run)()

	rows, newest, err := HermesPGFingerprint("postgres://x")
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || newest <= 0 {
		t.Errorf("rows=%d newest=%d", rows, newest)
	}
}

// The DSN can carry a password; the token that stands in for the store must not
// leak it, and it must be stable so the index recognises the same store twice.
func TestHermesPGStorePathHidesTheDSN(t *testing.T) {
	dsn := "postgres://user:s3cret@host:55432/hermes"
	tok := HermesPGStorePath(dsn)
	if strings.Contains(tok, "s3cret") || strings.Contains(tok, "host") {
		t.Fatalf("token leaked the DSN: %q", tok)
	}
	if tok != HermesPGStorePath(dsn) {
		t.Fatal("token is not stable")
	}
	if !IsHermesPGStore(tok) {
		t.Fatalf("%q not recognised as a pg store", tok)
	}
}
