package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// PR 1220 raised the sync-vs-ingest divergence question around issue #492:
// the route a message takes into an index must not change its per-session term
// frequencies. Offsets and numeric session ordinals are intentionally excluded
// because independently built indexes are free to assign both differently.
func TestIngestAndSyncProduceSamePostings(t *testing.T) {
	tmp := t.TempDir()

	claudeA := filepath.Join(tmp, "claude-a")
	projectA := filepath.Join(claudeA, "-tmp-parity")
	if err := os.MkdirAll(projectA, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config-a"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeA)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes-a.jsonl"))

	records := []struct {
		name string
		line string
	}{
		{
			name: "cjk.jsonl",
			line: `{"type":"user","message":{"role":"user","content":"關係 and 关系 describe the same relationship"},"timestamp":"2026-01-02T03:04:05Z","sessionId":"cjk-session","cwd":"/tmp/parity"}` + "\n",
		},
		{
			name: "english.jsonl",
			line: `{"type":"user","message":{"role":"user","content":"plain english message about durable indexes"},"timestamp":"2026-02-03T04:05:06Z","sessionId":"english-session","cwd":"/tmp/parity"}` + "\n",
		},
		{
			// Term frequency counts one posting per record, so a tf above one
			// needs the word in two turns of the same session, not twice in
			// one turn.
			name: "repeat.jsonl",
			line: `{"type":"user","message":{"role":"user","content":"repeat this indexed word"},"timestamp":"2026-03-04T05:06:07Z","sessionId":"repeat-session","cwd":"/tmp/parity"}` + "\n" +
				`{"type":"assistant","message":{"role":"assistant","content":"the repeat happened again"},"timestamp":"2026-03-04T05:07:07Z","sessionId":"repeat-session","cwd":"/tmp/parity"}` + "\n",
		},
	}
	for _, record := range records {
		if err := os.WriteFile(filepath.Join(projectA, record.name), []byte(record.line), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A session whose only turn is one long tool result pins the other two
	// halves of the invariant. The text has no signal-marker lines, so
	// signalLines keeps its head and tail and drops the middle: headonlyword
	// sits inside the first 8 KiB, midonlyword between the head and the final
	// 4 KiB, tailonlyword in the tail. Ingest indexes head and tail with the
	// tool bit set and never indexes the middle; the imported copy must do
	// exactly the same.
	pad := strings.Repeat("plain filler that stays clear of every signal marker ", 200)
	longTool := "headonlyword " + pad[:9000] + " midonlyword " + pad[:5000] + " tailonlyword"
	toolJSON, err := json.Marshal(longTool)
	if err != nil {
		t.Fatal(err)
	}
	toolLine := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":` + string(toolJSON) + `}]},"timestamp":"2026-04-05T06:07:08Z","sessionId":"tool-session","cwd":"/tmp/parity"}` + "\n"
	if err := os.WriteFile(filepath.Join(projectA, "tool.jsonl"), []byte(toolLine), 0o644); err != nil {
		t.Fatal(err)
	}

	dirA := filepath.Join(tmp, "index-a.db")
	if err := Ensure(dirA, "", false, nil); err != nil {
		t.Fatal(err)
	}

	sourceManifest, err := readManifest(dirA)
	if err != nil {
		t.Fatal(err)
	}
	wantSourceIDs := map[string]bool{
		"cjk-session":     false,
		"english-session": false,
		"repeat-session":  false,
		"tool-session":    false,
	}
	if len(sourceManifest.Sessions) != len(wantSourceIDs) {
		t.Fatalf("ingest produced %d sessions, want %d", len(sourceManifest.Sessions), len(wantSourceIDs))
	}

	// Local Claude metadata carries the original JSON sessionId in ID, while
	// imported metadata uses ImportedSessionID. Derive both spellings from the
	// source manifest and normalize them to that original sessionId.
	identityAliases := make(map[string]string, len(sourceManifest.Sessions)*2)
	for _, meta := range sourceManifest.Sessions {
		if _, ok := wantSourceIDs[meta.ID]; !ok {
			t.Fatalf("ingest produced unexpected session identity %q", meta.ID)
		}
		wantSourceIDs[meta.ID] = true
		identityAliases[meta.ID] = meta.ID
		identityAliases[ImportedSessionID(meta.Harness, meta.ID)] = meta.ID
	}
	for id, found := range wantSourceIDs {
		if !found {
			t.Fatalf("ingest did not produce session %q", id)
		}
	}

	batchDir := filepath.Join(tmp, "batch")
	if err := os.MkdirAll(batchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exported, err := Export(dirA, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if exported == 0 {
		t.Fatal("export produced no records")
	}

	// Use a separate config root so the destination represents another machine
	// and does not reject the exported batch as its own returning outbox.
	claudeB := filepath.Join(tmp, "claude-b")
	if err := os.MkdirAll(claudeB, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config-b"))
	t.Setenv("DEJA_CLAUDE_ROOT", claudeB)
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "notes-b.jsonl"))

	dirB := filepath.Join(tmp, "index-b.db")
	if err := Ensure(dirB, "", false, nil); err != nil {
		t.Fatal(err)
	}
	imported, err := Import(dirB, batchDir)
	if err != nil {
		t.Fatal(err)
	}
	if imported != exported {
		t.Fatalf("imported %d records from an export of %d", imported, exported)
	}

	collect := func(dir string) map[string]map[string]int {
		t.Helper()

		manifest, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		ordinalIdentity := make(map[int]string, len(manifest.Sessions))
		for _, meta := range manifest.Sessions {
			identity, ok := identityAliases[meta.ID]
			if !ok {
				t.Fatalf("%s contains unexpected session identity %q", dir, meta.ID)
			}
			ordinal := int(meta.Ord)
			if previous, exists := ordinalIdentity[ordinal]; exists {
				t.Fatalf("%s assigns ordinal %d to both %q and %q", dir, ordinal, previous, identity)
			}
			ordinalIdentity[ordinal] = identity
		}

		out := make(map[string]map[string]int)
		bucketsDir := filepath.Join(dir, "buckets")
		entries, err := os.ReadDir(bucketsDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".bin" {
				continue
			}
			bucketPostings, err := readBucket(filepath.Join(bucketsDir, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for token, postings := range bucketPostings {
				bySession := out[token]
				if bySession == nil {
					bySession = make(map[string]int)
					out[token] = bySession
				}
				for _, posting := range postings {
					identity, ok := ordinalIdentity[int(posting.Sid)]
					if !ok {
						t.Fatalf("%s token %q refers to unknown session ordinal %d", dir, token, posting.Sid)
					}
					bySession[identity]++
				}
			}
		}
		return out
	}

	ingested := collect(dirA)
	synced := collect(dirB)

	// The simplified key is emitted several times for the one CJK message —
	// once by the whole-run token of the Simplified spelling and once by the
	// folded bigrams of each spelling — and all of them collapse to a single
	// posting. That collapse is exactly what the sync route has to reproduce
	// for the DeepEqual below to hold.
	if got := ingested["t关系"]["cjk-session"]; got != 1 {
		t.Fatalf("fold-collision fixture produced tf %d for t关系, want 1", got)
	}

	repeatedTF := false
	for _, bySession := range ingested {
		if bySession["repeat-session"] > 1 {
			repeatedTF = true
			break
		}
	}
	if !repeatedTF {
		t.Fatal("repeated-word fixture did not produce a token with tf greater than one")
	}

	// The tokenizedPart half: the middle of the long tool output earns no
	// posting on either route, while its head and tail do.
	if _, ok := ingested["tmidonlyword"]; ok {
		t.Fatal("ingest indexed the middle of a long tool output; the fixture no longer exercises signalLines")
	}
	if _, ok := synced["tmidonlyword"]; ok {
		t.Fatal("import indexed the middle of a long tool output that ingest drops")
	}

	// The tool-bit half: postings for the tool-only session carry Tool on
	// both routes.
	for _, dir := range []string{dirA, dirB} {
		for _, tok := range []string{"theadonlyword", "ttailonlyword"} {
			posts, err := postingsFor(dir, tok)
			if err != nil {
				t.Fatal(err)
			}
			if len(posts) == 0 {
				t.Fatalf("%s holds no postings for %q", dir, tok)
			}
			for _, p := range posts {
				if !p.Tool {
					t.Fatalf("%s posting for %q lost the tool bit", dir, tok)
				}
			}
		}
	}

	tokenSet := make(map[string]struct{}, len(ingested)+len(synced))
	for token := range ingested {
		tokenSet[token] = struct{}{}
	}
	for token := range synced {
		tokenSet[token] = struct{}{}
	}
	tokens := make([]string, 0, len(tokenSet))
	for token := range tokenSet {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)

	for _, token := range tokens {
		if !reflect.DeepEqual(ingested[token], synced[token]) {
			t.Errorf("posting counts differ for token %q: ingest=%v sync=%v", token, ingested[token], synced[token])
		}
	}
}
