package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// Four sessions tie "login" to "jwks": the rescue should let a query that
// says login find the session that only says jwks.
func cooccurCorpus(t *testing.T) string {
	t.Helper()
	texts := []string{
		"login broken again, jwks cache stale after rotation",
		"login failure traced to jwks refresh timing",
		"users report login errors, jwks endpoint returned old keys",
		"jwks rotation deployed, monitoring for regressions",
		"docker build cache oom unrelated filler session",
		"another unrelated session about frontend styling",
	}
	return nlIndex(t, texts...)
}

func TestCooccurMapBuiltOnFullRebuild(t *testing.T) {
	dir := cooccurCorpus(t)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("cooccur map missing after full build")
	}
	found := false
	for _, n := range m["login"] {
		if n == "jwks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("login neighbors = %v, want jwks", m["login"])
	}
}

// The same connection string is pasted into five sessions. Its password is a
// secret redaction strips from the record log; it must not survive into
// cooccur.gob, which the builder reads straight off ss.Messages.
func TestCooccurNeverStoresARepeatedSecret(t *testing.T) {
	const secret = "supersecretpw"
	url := "postgres://admin:" + secret + "@db.example.com/prod"
	dir := nlIndex(t,
		"deploy pipeline uses "+url,
		"deploy pipeline again "+url,
		"deploy pipeline retry "+url,
		"deploy pipeline once more "+url,
		"deploy pipeline final "+url,
	)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("cooccur map missing; corpus did not build pairs")
	}
	if _, ok := m[secret]; ok {
		t.Fatalf("secret is a cooccur key: %v", m[secret])
	}
	for tok, ns := range m {
		for _, n := range ns {
			if n == secret {
				t.Fatalf("secret leaked as a neighbor of %q: %v", tok, ns)
			}
		}
	}
}

func TestCooccurRescueSwapsOneToken(t *testing.T) {
	dir := cooccurCorpus(t)
	// "login" never appears with "monitoring" in one session; the rescue may
	// swap login -> jwks and land on session d.
	result, err := SearchDetailed(dir, search.Options{Query: "login monitoring regressions", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) == 0 {
		t.Fatal("cooccur rescue did not fire")
	}
	if vs := result.Variants["login"]; len(vs) != 1 || vs[0] == "login" {
		t.Fatalf("swap not narrated: %v", result.Variants)
	}
}

func TestCooccurRescueStaysQuietWhenMapMissing(t *testing.T) {
	dir := nlIndex(t, "one lonely session about nothing in particular")
	_ = os.Remove(filepath.Join(dir, cooccurFile))
	result, err := SearchDetailed(dir, search.Options{Query: "absent tokens here", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sessions) != 0 {
		t.Fatalf("rescue invented results: %+v", result.Sessions)
	}
}

func TestCooccurRanksByNPMIOverRawFrequency(t *testing.T) {
	// "oauth" (df=4) co-occurs with specific partner "pkce" (df=3, c=3) and
	// with noisy high-DF token "handler" (df=8, c=4). Under raw count,
	// "handler" (c=4) beat "pkce" (c=3). Under NPMI + turn-window coupling,
	// "pkce" has higher mutual information with "oauth" and must rank first.
	dir := nlIndex(t,
		"oauth handshake pkce verifier challenge handler",
		"oauth flow pkce code challenge handler",
		"oauth token pkce exchange handler",
		"oauth callback state mismatch handler",
		"http router middleware handler logging",
		"grpc interceptor handler panic recovery",
		"webhook signature verification handler",
		"metrics endpoint handler prometheus",
		"database connection pool timeout",
		"background worker queue retry",
		"tls certificate renewal acme",
		"static asset cache compression",
	)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("cooccur map missing")
	}
	ns := m["oauth"]
	if len(ns) == 0 {
		t.Fatalf("expected neighbors for oauth, got none")
	}
	if ns[0] != "pkce" {
		t.Fatalf("expected top NPMI neighbor of oauth to be pkce, got %v", ns)
	}
}

func TestAssociativeActivationBoostsCollocatedSession(t *testing.T) {
	// Session 0 and Sessions 1-3 all mention "login" and "errors" once.
	// Sessions 1-3 also carry "jwks", the corpus-learned NPMI collocation of
	// "login". For a 3-term relevance query ("login errors regressions") where
	// no session holds all 3 terms, 1-hop associative activation should rank a
	// collocated session (s1/s2/s3) above uncollocated s0.
	dir := nlIndex(t,
		"login errors button alignment tweak",
		"login errors broken again, jwks cache stale after rotation",
		"login errors failure traced to jwks refresh timing",
		"users report login errors, jwks endpoint returned old keys",
		"unrelated filler session about database regressions",
		"another filler session about CSS grid layout",
	)
	res, err := SearchDetailed(dir, search.Options{Query: "login errors regressions", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) == 0 {
		t.Fatal("expected search results for login errors regressions")
	}
	if res.Sessions[0].ID == "s0" {
		t.Fatalf("expected session with NPMI collocation (jwks) to outrank uncollocated s0, got %s first", res.Sessions[0].ID)
	}
}

func TestBM25FZonalWeightingPrefersSettledAndTouchedOverPassingMention(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	ss := []model.Session{
		{
			ID: "passing", Harness: "claude", Project: "work/app", Started: at, Updated: at.Add(time.Minute),
			Messages: []model.Message{
				{Role: "user", Text: "check the dashboard logs", Time: at},
				{Role: "assistant", Text: "saw quicksilver mentioned in a comment, ignoring it", Time: at.Add(time.Minute)},
			},
		},
		{
			ID: "settled", Harness: "claude", Project: "work/app", Started: at, Updated: at,
			Messages: []model.Message{
				{Role: "user", Text: "fix quicksilver retry storm", Time: at},
				{Role: "edit", Text: "internal/quicksilver/retry.go\ncap = 4", Time: at.Add(10 * time.Second)},
				{Role: "command", Text: "go test ./internal/quicksilver/... → exit 0", Time: at.Add(20 * time.Second)},
				{Role: "assistant", Text: "Updated `internal/quicksilver/retry.go` to cap quicksilver backoff at 4.", Time: at.Add(30 * time.Second)},
			},
		},
	}
	writeStore(t, dir, ss)
	got, _, _, _, err := ProjectRelevant(dir, []string{"work/app"}, []string{"quicksilver", "backoff"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].ID != "settled" {
		t.Fatalf("expected settled session with Touched+Settled+Title zones to rank first, got %+v", got)
	}
}

// remineCooccur rebuilds cooccur.gob and discourse.gob in dir directly from its
// committed records.bin log without re-parsing raw transcripts on disk.
func remineCooccur(dir string) {
	m, err := readManifest(dir)
	if err != nil {
		return
	}
	by := map[string]*model.Session{}
	var order []string
	if err := eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		meta, ok := m.Sessions[r.Key]
		if !ok || foreignPath(r.SourcePath, meta) {
			return
		}
		s := by[r.Key]
		if s == nil {
			cp := sessionFromMeta(meta)
			s = &cp
			by[r.Key] = s
			order = append(order, r.Key)
		}
		s.Messages = append(s.Messages, model.Message{Role: r.Role, Text: r.Text, Time: r.Time})
	}); err != nil {
		return
	}
	ss := make([]model.Session, 0, len(order))
	for _, k := range order {
		ss = append(ss, *by[k])
	}
	buildCooccur(dir, ss)
	dropCooccurCache(dir)
}

func TestRemineCooccurRebuildsCooccurAndDiscourseFromRecords(t *testing.T) {
	dir := cooccurCorpus(t)
	_ = os.Remove(filepath.Join(dir, cooccurFile))
	remineCooccur(dir)
	m := readCooccur(dir)
	if m == nil {
		t.Fatal("expected remineCooccur to rebuild cooccur.gob from records.bin")
	}
	if _, leaked := m[cooccurDiscourseKey]; leaked {
		t.Fatal("cooccurDiscourseKey leaked into neighbor map")
	}
	found := false
	for _, n := range m["login"] {
		if n == "jwks" {
			found = true
		}
	}
	if !found {
		t.Fatalf("remineCooccur login neighbors = %v, want jwks", m["login"])
	}
}
