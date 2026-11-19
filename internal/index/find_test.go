package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// FindByID has no harness to go on — the compact hook's payload does not carry
// one — so when two stores hold the same id it used to answer with whichever
// entry the manifest map handed over first: 340 claude against 60 codex over
// 400 identical calls (#1997). A session asking for its own evidence after a
// compaction got someone else's, and not even the same someone twice.
func TestFindByIDAnswersTheSameWayTwice(t *testing.T) {
	tmp := t.TempDir()
	claude := filepath.Join(tmp, "claude", "-tmp-app")
	codex := filepath.Join(tmp, "codex", "sessions", "2026", "01", "01")
	for _, d := range []string{claude, codex} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "none.db"))

	// The same id in two stores. The claude copy is the newer one.
	if err := os.WriteFile(filepath.Join(claude, "shared.jsonl"), []byte(
		`{"type":"user","sessionId":"shared","cwd":"/tmp/app","timestamp":"2026-03-02T03:04:05Z","message":{"role":"user","content":"the claude copy of pgbouncer"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "rollout-2026-01-01T12-00-00-shared.jsonl"), []byte(
		`{"type":"session_meta","timestamp":"2026-01-01T12:00:00Z","payload":{"session_id":"shared","cwd":"/tmp/app"}}`+"\n"+
			`{"timestamp":"2026-01-01T12:00:01Z","payload":{"role":"user","content":"the codex copy of pgbouncer"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	// The premise: both copies are in the index under their own identity. If
	// one of them failed to index, every answer below agrees for the wrong
	// reason.
	for _, h := range []string{"claude", "codex"} {
		if _, ok, err := FindByIdentity(dir, h, "shared"); err != nil || !ok {
			t.Fatalf("the %s copy is not in the index (%v %v), so the id is not shared", h, ok, err)
		}
	}

	// Enough calls that map order would have shown itself: the observed split
	// was 15% on the losing side, so 400 identical answers by chance is not a
	// number worth worrying about.
	first, ok, err := FindByID(dir, "shared")
	if err != nil || !ok {
		t.Fatalf("the id is in the index but FindByID missed it: %v %v", ok, err)
	}
	for i := range 400 {
		got, ok, err := FindByID(dir, "shared")
		if err != nil || !ok {
			t.Fatalf("lookup %d: %v %v", i, ok, err)
		}
		if got.Harness != first.Harness {
			t.Fatalf("lookup %d answered %s after %s: the answer depends on map order",
				i, got.Harness, first.Harness)
		}
	}
	// Which one it settles on is a choice, not an accident: the freshest
	// session is the one a compacting agent is most likely to be.
	if first.Harness != "claude" {
		t.Errorf("with two copies of an id, the newer one is the better guess; got %s", first.Harness)
	}
	beforeScans := RecordLogScans()
	meta, metaOK, metaErr := FindMetaByID(dir, "shared")
	if metaErr != nil || !metaOK || meta.Harness != first.Harness {
		t.Fatalf("FindMetaByID = (%s, %v, %v), want %s", meta.Harness, metaOK, metaErr, first.Harness)
	}
	if RecordLogScans() != beforeScans {
		t.Fatalf("FindMetaByID scanned records.bin (%d -> %d)", beforeScans, RecordLogScans())
	}
}

// A uuid is case-insensitive by RFC 4122 and harnesses print it either way, so
// an id pasted in the other case is still that id. Every lookup here compared
// bytes, and deja answered "no session matches" about a session it holds and
// prints that id for (#1620).
func TestFindByPrefixResolvesAnIdInTheOtherCase(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "abcd1234-0001-4a1b-9c2d-000000000001"
	body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"the pool work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The precondition: the id as stored resolves.
	if s, ok, err := FindByPrefix(dir, "abcd1234"); err != nil || !ok || s.ID != id {
		t.Fatalf("the id as stored: %q ok=%v err=%v", s.ID, ok, err)
	}
	for _, p := range []string{"ABCD1234", "AbCd1234", "ABCD1234-0001-4A1B-9C2D-000000000001"} {
		s, ok, err := FindByPrefix(dir, p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !ok || s.ID != id {
			t.Errorf("%s found %q (ok=%v); it is the same id in another case", p, s.ID, ok)
		}
	}
	// The control: a prefix that names nothing still names nothing.
	if _, ok, err := FindByPrefix(dir, "ZZZZ9999"); err != nil || ok {
		t.Errorf("ZZZZ9999 resolved (ok=%v, err=%v); case folding must not match everything", ok, err)
	}
}

// FindManyByIdentity is FindByIdentity's batch form; callers that looped over
// the single one walked the whole record log per session (#1069).
func TestFindManyByIdentity(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, tmp)
	t.Setenv("USERPROFILE", tmp)
	idx := filepath.Join(tmp, "index.db")
	t.Setenv("DEJA_INDEX_DIR", idx)
	claudeRoot := filepath.Join(tmp, "claude")
	proj := filepath.Join(claudeRoot, "-tmp-demo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		id := fmt.Sprintf("s%d", i)
		one := fmt.Sprintf(`{"type":"user","sessionId":%q,"timestamp":"2026-01-0%dT03:04:05Z","message":{"role":"user","content":"needle %s asked"}}`, id, i+1, id) + "\n"
		two := fmt.Sprintf(`{"type":"assistant","sessionId":%q,"timestamp":"2026-01-0%dT03:05:05Z","message":{"role":"assistant","content":"answer for %s"}}`, id, i+1, id) + "\n"
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(one+two), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("DEJA_CLAUDE_ROOT", claudeRoot)
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	if err := Ensure(idx, "claude", false, nil); err != nil {
		t.Fatal(err)
	}

	// Asked-for order, one walk of the log, and identities the manifest does
	// not know are dropped rather than returned empty.
	before := RecordLogScans()
	got, err := FindManyByIdentity(idx, []Identity{
		{Harness: "claude", ID: "s3"},
		{Harness: "codex", ID: "nope"},
		{Harness: "claude", ID: "s1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if scans := RecordLogScans() - before; scans != 1 {
		t.Errorf("walked the record log %d times for 2 sessions, want 1", scans)
	}
	if len(got) != 2 || got[0].ID != "s3" || got[1].ID != "s1" {
		t.Fatalf("want s3 then s1, got %#v", got)
	}
	for _, s := range got {
		if len(s.Messages) != 2 {
			t.Errorf("%s came back with %d messages, want 2", s.ID, len(s.Messages))
		}
	}

	if got, err := FindManyByIdentity(idx, nil); err != nil || got != nil {
		t.Errorf("no identities = no work: %#v err=%v", got, err)
	}
	if got, err := FindManyByIdentity(idx, []Identity{{Harness: "codex", ID: "nope"}}); err != nil || got != nil {
		t.Errorf("nothing known = nothing back: %#v err=%v", got, err)
	}
	// An empty dir means the default, the same as FindByIdentity.
	if got, err := FindManyByIdentity("", []Identity{{Harness: "claude", ID: "s0"}}); err != nil || len(got) != 1 || got[0].ID != "s0" {
		t.Errorf("default dir: %#v err=%v", got, err)
	}
}

// Import rewrites a session's id, and OrigID keeps the one it had on the machine
// it came from (#1049). Nothing looked at it, so a reader who saw an id on one
// machine and typed it on another was told no session matches — about a session
// deja holds and prints that very id for under `--json`. resume even has a
// sentence written for the case ("synced from another machine — resume it
// there") that could never fire, because the lookup never resolved the id.
//
// Done as a real round trip rather than a hand-staged manifest: the id is only
// rewritten by import, so staging the row by hand would assert against my own
// idea of what import does.
func TestFindByPrefixResolvesTheIdTheSessionCameWith(t *testing.T) {
	const origID = "faraway1"
	line := `{"type":"user","sessionId":"` + origID + `","cwd":"/w","timestamp":"2026-09-01T09:00:00Z","message":{"role":"user","content":"the thrumbleknot migration"}}` + "\n"

	// Machine A: index it, export it.
	dirA, tmpA := syncPeerIndex(t, line)
	box := filepath.Join(tmpA, "box")
	if _, err := Export(dirA, box); err != nil {
		t.Fatal(err)
	}

	// Machine B: its own session, then A's import on top.
	tmpB := t.TempDir()
	setHome(t, filepath.Join(tmpB, "home"))
	claudeB := filepath.Join(tmpB, "claude", "-tmp-b")
	if err := os.MkdirAll(claudeB, 0o755); err != nil {
		t.Fatal(err)
	}
	local := `{"type":"user","sessionId":"local1","cwd":"/w","timestamp":"2026-09-02T09:00:00Z","message":{"role":"user","content":"the local parser work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(claudeB, "local.jsonl"), []byte(local), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmpB, "claude"))
	dirB := filepath.Join(tmpB, "index.db")
	if err := Ensure(dirB, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dirB, box); err != nil {
		t.Fatal(err)
	}

	// The precondition everything below rests on: import really did rename it.
	m, err := readManifest(dirB)
	if err != nil {
		t.Fatal(err)
	}
	renamed := false
	for _, meta := range m.Sessions {
		if meta.OrigID == origID && meta.ID != origID {
			renamed = true
		}
	}
	if !renamed {
		t.Fatal("import did not rewrite the id, so there is nothing here for this test to resolve")
	}

	got, ok, err := FindByPrefix(dirB, origID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the id the session came with resolves to nothing")
	}
	if got.OrigID != origID {
		t.Errorf("resolved to %q (orig %q), want the session imported as %q", got.ID, got.OrigID, origID)
	}

	// The count has to agree with what the resolver just did, or the reader is
	// told the selector matches nothing and then watches it open a session
	// (#853, with the sign flipped).
	if n := PrefixMatches(dirB, origID); n == 0 {
		t.Errorf("the resolver opened a session for %q while the count says none match", origID)
	}

	// A local id still wins: the fallback runs only when nothing else matched.
	got, ok, err = FindByPrefix(dirB, "local1")
	if err != nil || !ok {
		t.Fatalf("the local id stopped resolving: ok=%v err=%v", ok, err)
	}
	if got.ID != "local1" {
		t.Errorf("a local id resolved to %q", got.ID)
	}

	// And an exact match on the imported id beats a local id that merely
	// contains it: typing the whole thing should not hand over something else.
	near := `{"type":"user","sessionId":"zz-` + origID + `-zz","cwd":"/w","timestamp":"2026-09-03T09:00:00Z","message":{"role":"user","content":"a local session whose id contains the other one"}}` + "\n"
	if err = os.WriteFile(filepath.Join(claudeB, "near.jsonl"), []byte(near), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = Ensure(dirB, "", false, nil); err != nil {
		t.Fatal(err)
	}
	got, ok, err = FindByPrefix(dirB, origID)
	if err != nil || !ok {
		t.Fatalf("the imported id stopped resolving once a local id contained it: ok=%v err=%v", ok, err)
	}
	if got.OrigID != origID {
		t.Errorf("typing %q in full opened %q instead of the session imported under it", origID, got.ID)
	}
}

// Remote control shows a Claude Code session as claude.ai/code/session_01…,
// and that is the id a person copies from the phone or the browser. The file on
// disk is named by the local uuid, so the copied id found nothing. The local
// transcript records the URL in a bridge_status line, which can arrive after
// the session is already indexed, so the second half goes through the append
// path.
func TestFindByPrefixResolvesTheRemoteControlId(t *testing.T) {
	const remote = "session_01RmtCtl7xYzQw3dEfGhJkLm"
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	proj := filepath.Join(tmp, "claude", "-w")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	dir := filepath.Join(tmp, "index.db")

	file := filepath.Join(proj, "4be0c2a1-0000-4000-8000-000000000001.jsonl")
	first := `{"type":"user","sessionId":"4be0c2a1-0000-4000-8000-000000000001","cwd":"/w","timestamp":"2026-09-01T09:00:00Z","message":{"role":"user","content":"the flanged parser rewrite"}}` + "\n"
	if err := os.WriteFile(file, []byte(first), 0o600); err != nil {
		t.Fatal(err)
	}
	other := `{"type":"user","sessionId":"other1","cwd":"/w","timestamp":"2026-09-02T09:00:00Z","message":{"role":"user","content":"unrelated work"}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "other1.jsonl"), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := FindByPrefix(dir, remote); ok {
		t.Fatal("resolved a remote id before the session recorded one")
	}

	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"system","subtype":"bridge_status","content":"/remote-control is active.","url":"https://claude.ai/code/` + remote + `","sessionId":"4be0c2a1-0000-4000-8000-000000000001","timestamp":"2026-09-01T09:05:00Z"}` + "\n" +
		`{"type":"assistant","sessionId":"4be0c2a1-0000-4000-8000-000000000001","timestamp":"2026-09-01T09:06:00Z","message":{"role":"assistant","content":"the parser now reads flanges"}}` + "\n")
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	for _, sel := range []string{remote, remote[:16], "https://claude.ai/code/" + remote, "claude.ai/code/" + remote + "/"} {
		got, ok, err := FindByPrefix(dir, sel)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || got.ID != "4be0c2a1-0000-4000-8000-000000000001" {
			t.Errorf("FindByPrefix(%q) = %q, %v; want the local session", sel, got.ID, ok)
		}
		if n := PrefixMatches(dir, sel); n != 1 {
			t.Errorf("PrefixMatches(%q) = %d, want 1 to agree with the resolver", sel, n)
		}
	}
	// A URL that is not remote control's resolves nothing.
	if _, ok, _ := FindByPrefix(dir, "https://example.com/code/"+remote); ok {
		t.Error("a URL from another site resolved a session")
	}

	// A full rebuild reads the line from the start and keeps the id.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := FindByPrefix(dir, remote); !ok || got.RemoteID != remote {
		t.Errorf("after a rebuild FindByPrefix = %q (remote %q), %v", got.ID, got.RemoteID, ok)
	}
}

// Result lines elide the middle of a long id, so what a reader copies out of a
// search is a head and a tail rather than a prefix (#707).
func TestFindByPrefixFallsBackToASubstring(t *testing.T) {
	tmp := t.TempDir()
	setHome(t, filepath.Join(tmp, "home"))
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude"))
	t.Setenv("DEJA_CODEX_ROOT", filepath.Join(tmp, "codex"))
	t.Setenv("DEJA_OPENCODE_DB", filepath.Join(tmp, "opencode.db"))
	root := filepath.Join(tmp, "claude", "proj-p")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"00000000-0001-4a1b-9c2d-000000000001", "00000000-0002-4a1b-9c2d-000000000002"} {
		body := `{"type":"user","timestamp":"2026-07-21T10:00:00Z","message":{"role":"user","content":"work in ` + id + `"}}` + "\n"
		if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(tmp, "index.db")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}

	// The tail alone, as it appears after the ellipsis in a result line.
	s, ok, err := FindByPrefix(dir, "0000000002")
	if err != nil || !ok {
		t.Fatalf("substring lookup: ok=%v err=%v", ok, err)
	}
	if s.ID != "00000000-0002-4a1b-9c2d-000000000002" {
		t.Errorf("resolved %q", s.ID)
	}
	// A real prefix still wins outright.
	s, ok, err = FindByPrefix(dir, "00000000-0001")
	if err != nil || !ok || s.ID != "00000000-0001-4a1b-9c2d-000000000001" {
		t.Fatalf("prefix lookup: %q ok=%v err=%v", s.ID, ok, err)
	}
	// Nothing matching stays nothing.
	if _, ok, err := FindByPrefix(dir, "not-in-this-store"); err != nil || ok {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}
