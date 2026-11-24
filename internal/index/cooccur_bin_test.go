package index

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func seedCooccurSessions() []model.Session {
	ss := make([]model.Session, 0, 60)
	for cluster := range 12 {
		for n := range 5 {
			ss = append(ss, model.Session{
				Harness: "claude",
				ID:      fmt.Sprintf("cb%02d-%d", cluster, n),
				Project: "app",
				Messages: []model.Message{
					{Role: "user", Text: fmt.Sprintf("quokkabloom%02d keeps losing rows", cluster)},
					{Role: "edit", Text: fmt.Sprintf("internal/mod%02d/handler.go", cluster)},
					{Role: "assistant", Text: fmt.Sprintf("Resolved: snorblewidget%02d rotation fixed quokkabloom%02d cleanly.", cluster, cluster)},
				},
			})
		}
	}
	return ss
}

func TestCooccurBinLookupMatchesGobMap(t *testing.T) {
	dir := t.TempDir()
	ss := seedCooccurSessions()
	buildCooccur(dir, ss)

	raw, err := os.ReadFile(filepath.Join(dir, cooccurBinFile))
	if err != nil {
		t.Fatalf("read %s: %v", cooccurBinFile, err)
	}
	if !strings.HasPrefix(string(raw), cooccurBinMagic) {
		t.Fatalf("magic = %q, want prefix %q", string(raw[:min(len(raw), 8)]), cooccurBinMagic)
	}

	want := readCooccur(dir)
	if len(want) == 0 {
		t.Fatal("buildCooccur mined no neighbors")
	}
	for tok, ns := range want {
		got := lookupCooccurNeighbors(dir, tok)
		if !reflect.DeepEqual(got, ns) {
			t.Fatalf("lookupCooccurNeighbors(%q) = %v, want %v", tok, got, ns)
		}
		if upper := CooccurNeighbors(dir, strings.ToUpper(tok)); !reflect.DeepEqual(upper, ns) {
			t.Fatalf("CooccurNeighbors(%q) = %v, want %v", strings.ToUpper(tok), upper, ns)
		}
	}
	if got := lookupCooccurNeighbors(dir, "nosuchtoken"); got != nil {
		t.Fatalf("absent token returned %v, want nil", got)
	}

	withDiscourse := map[string][]string{
		"quokkabloom00":     {"snorblewidget00"},
		cooccurDiscourseKey: {"resolved"},
	}
	if err := writeGobAtomic(cooccurPath(dir), withDiscourse); err != nil {
		t.Fatal(err)
	}
	if err := writeCooccurBinAtomic(dir, withDiscourse); err != nil {
		t.Fatal(err)
	}
	if got := lookupCooccurNeighbors(dir, cooccurDiscourseKey); got != nil {
		t.Fatalf("discourse key leaked: %v", got)
	}
	if got := CooccurNeighbors(dir, cooccurDiscourseKey); got != nil {
		t.Fatalf("CooccurNeighbors leaked discourse key: %v", got)
	}
	if got := lookupCooccurNeighbors(dir, "quokkabloom00"); !reflect.DeepEqual(got, []string{"snorblewidget00"}) {
		t.Fatalf("quokkabloom00 = %v, want [snorblewidget00]", got)
	}
}

func TestCooccurBinRespectsDeletedAndRewrittenGob(t *testing.T) {
	dir := t.TempDir()
	buildCooccur(dir, seedCooccurSessions())
	dropCooccurCache(dir)

	want := readCooccur(dir)
	var sampleTok string
	for k := range want {
		sampleTok = k
		break
	}
	if sampleTok == "" {
		t.Fatal("empty neighbor map")
	}

	// Rewriting cooccur.gob without updating cooccur.bin must fall back via stamp check.
	rewritten := map[string][]string{sampleTok: {"freshneighbor"}}
	if err := writeGobAtomic(cooccurPath(dir), rewritten); err != nil {
		t.Fatal(err)
	}
	dropCooccurCache(dir)
	if got := lookupCooccurNeighbors(dir, sampleTok); !reflect.DeepEqual(got, []string{"freshneighbor"}) {
		t.Fatalf("stale cooccur.bin returned %v after gob rewrite, want [freshneighbor]", got)
	}

	// Corrupting cooccur.bin falls back cleanly to cooccur.gob.
	if err := os.WriteFile(filepath.Join(dir, cooccurBinFile), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropCooccurCache(dir)
	gotMap, ok := lookupCooccurMany(dir, []string{sampleTok, "missing"})
	if !ok || !reflect.DeepEqual(gotMap[sampleTok], []string{"freshneighbor"}) {
		t.Fatalf("corrupt cooccur.bin fallback = (%v, %v), want freshneighbor", gotMap, ok)
	}

	// dropCooccurWords must keep cooccur.bin updated across removals and empty maps.
	buildCooccur(dir, seedCooccurSessions())
	dropCooccurCache(dir)
	want = readCooccur(dir)
	var dropped string
	for k := range want {
		dropped = k
		break
	}
	if err := dropCooccurWords(dir, func(w string) bool { return w == dropped }); err != nil {
		t.Fatal(err)
	}
	if got := lookupCooccurNeighbors(dir, dropped); got != nil {
		t.Fatalf("dropped word %q still returned %v", dropped, got)
	}
	if err := dropCooccurWords(dir, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if got := lookupCooccurNeighbors(dir, "snorblewidget00"); got != nil {
		t.Fatalf("emptied cooccur.bin returned %v, want nil", got)
	}

	// Removing cooccur.gob makes cooccur.bin invisible even if it still exists on disk.
	buildCooccur(dir, seedCooccurSessions())
	dropCooccurCache(dir)
	if err := os.Remove(cooccurPath(dir)); err != nil {
		t.Fatal(err)
	}
	if got := lookupCooccurNeighbors(dir, "quokkabloom00"); got != nil {
		t.Fatalf("deleted cooccur.gob still returned %v", got)
	}
	if m, ok := lookupCooccurMany(dir, []string{"quokkabloom00"}); ok || m != nil {
		t.Fatalf("deleted cooccur.gob lookupCooccurMany = (%v, %v), want (nil, false)", m, ok)
	}
}

func TestOtherWordFormsAvoidsFullTokenCatalogScan(t *testing.T) {
	dir := seedOneWord(t, "caching cached caches")
	dropBucketDirs(dir)
	invalidateManifestCache(dir)

	before := catalogReads.Load()
	got := OtherWordForms(dir, []string{"caching"})
	if after := catalogReads.Load(); after != before {
		t.Fatalf("OtherWordForms read full catalog %d times, want 0", after-before)
	}
	forms := got["caching"]
	if !slices.Contains(forms, "cached") || !slices.Contains(forms, "caches") {
		t.Fatalf("OtherWordForms(caching) = %v, want cached and caches", forms)
	}
}

func TestBucketReaderHasTokenChecksOverlays(t *testing.T) {
	dir := seedOneWord(t, "caching")
	ovTok := "tcached"
	base, ok := bucketPath(dir, ovTok)
	if !ok {
		t.Fatalf("bucketPath(%q) returned false", ovTok)
	}
	if err := writeBucket(overlayPath(base, 1), map[string][]posting{
		ovTok: {{Off: 1, Sid: 0}},
	}); err != nil {
		t.Fatal(err)
	}
	dropBucketDirs(dir)

	br := newBucketReader(dir)
	defer br.close()
	has, err := br.hasToken(ovTok)
	if err != nil {
		t.Fatalf("hasToken(%q): %v", ovTok, err)
	}
	if !has {
		t.Fatalf("hasToken(%q) = false, want true from overlay", ovTok)
	}

	if emptyHas, emptyErr := br.hasToken(""); emptyErr != nil || emptyHas {
		t.Fatalf("hasToken(\"\") = (%v, %v), want (false, nil)", emptyHas, emptyErr)
	}
	if missHas, missErr := br.hasToken("neverindexedtoken"); missErr != nil || missHas {
		t.Fatalf("hasToken(missing) = (%v, %v), want (false, nil)", missHas, missErr)
	}
	if _, perr := br.postings(ovTok); perr != nil {
		t.Fatalf("postings(%q): %v", ovTok, perr)
	}
	if cachedHas, cachedErr := br.hasToken(ovTok); cachedErr != nil || !cachedHas {
		t.Fatalf("hasToken(%q) via cache = (%v, %v), want (true, nil)", ovTok, cachedHas, cachedErr)
	}
}

func TestCooccurBinRejectsMalformedInputs(t *testing.T) {
	if got := lookupCooccurNeighbors("", "cache"); got != nil {
		t.Fatalf("lookupCooccurNeighbors empty dir = %v, want nil", got)
	}
	dir := t.TempDir()
	if got := lookupCooccurNeighbors(dir, ""); got != nil {
		t.Fatalf("lookupCooccurNeighbors empty token = %v, want nil", got)
	}
	if got := lookupCooccurNeighbors(dir, cooccurDiscourseKey); got != nil {
		t.Fatalf("lookupCooccurNeighbors discourse key = %v, want nil", got)
	}
	if _, ok := lookupCooccurMany(dir, []string{"caching"}); ok {
		t.Fatal("lookupCooccurMany without cooccur.gob should return false")
	}

	// Empty cooccur map produces a 0-bucket sidecar; extra trailing bytes reject.
	if err := writeGobAtomic(cooccurPath(dir), map[string][]string{}); err != nil {
		t.Fatal(err)
	}
	if err := writeCooccurBinAtomic(dir, map[string][]string{}); err != nil {
		t.Fatal(err)
	}
	gobSize, stamp, err := cooccurGobStamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := lookupCooccurFromBin(dir, gobSize, stamp, []string{"caching"}); !ok || len(got) != 0 {
		t.Fatalf("empty sidecar lookup = (%v, %v), want (empty, true)", got, ok)
	}
	validEmpty, err := os.ReadFile(cooccurBinPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if werr := os.WriteFile(cooccurBinPath(dir), append(slices.Clone(validEmpty), 0x01), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if _, ok := lookupCooccurFromBin(dir, gobSize, stamp, []string{"caching"}); ok {
		t.Fatal("0-bucket sidecar with trailing bytes should fail")
	}

	// Populated cooccur map exercises skip/dup term handling and corrupt header guards.
	raw := map[string][]string{
		"alpha": {"beta", "gamma"},
	}
	if werr := writeGobAtomic(cooccurPath(dir), raw); werr != nil {
		t.Fatal(werr)
	}
	if werr := writeCooccurBinAtomic(dir, raw); werr != nil {
		t.Fatal(werr)
	}
	gobSize, stamp, err = cooccurGobStamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	gotMany, ok := lookupCooccurMany(dir, []string{"", cooccurDiscourseKey, "alpha", "alpha"})
	if !ok || !reflect.DeepEqual(gotMany["alpha"], []string{"beta", "gamma"}) {
		t.Fatalf("lookupCooccurMany = (%v, %v), want alpha neighbors", gotMany, ok)
	}

	goodBin, err := os.ReadFile(cooccurBinPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	corruptCases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{
			name:   "short_header",
			mutate: func(b []byte) []byte { return b[:cooccurBinHeaderSize-1] },
		},
		{
			name: "bad_magic",
			mutate: func(b []byte) []byte {
				copy(b[:8], "BADMAGIC")
				return b
			},
		},
		{
			name: "non_power_of_two_buckets",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[48:52], 3)
				return b
			},
		},
		{
			name: "oversized_bucket_count",
			mutate: func(b []byte) []byte {
				binary.LittleEndian.PutUint32(b[48:52], maxCooccurBuckets+1)
				return b
			},
		},
		{
			name: "truncated_data_region",
			mutate: func(b []byte) []byte {
				return b[:cooccurBinHeaderSize+8]
			},
		},
		{
			name: "oob_dir_span",
			mutate: func(b []byte) []byte {
				bc := binary.LittleEndian.Uint32(b[48:52])
				for i := range int(bc) {
					off := cooccurBinHeaderSize + i*8
					binary.LittleEndian.PutUint64(b[off:off+8], 0)
					binary.LittleEndian.PutUint64(b[off+8:off+16], uint64(len(b)+10))
				}
				return b
			},
		},
	}
	for _, tc := range corruptCases {
		data := tc.mutate(bytes.Clone(goodBin))
		if err := os.WriteFile(cooccurBinPath(dir), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, binOK := lookupCooccurFromBin(dir, gobSize, stamp, []string{"alpha"}); binOK {
			t.Fatalf("%s: expected lookupCooccurFromBin to reject corrupt sidecar", tc.name)
		}
		// Fallback via cooccur.gob must still succeed.
		if fb, fbOK := lookupCooccurMany(dir, []string{"alpha"}); !fbOK || !reflect.DeepEqual(fb["alpha"], []string{"beta", "gamma"}) {
			t.Fatalf("%s: fallback = (%v, %v), want alpha neighbors", tc.name, fb, fbOK)
		}
	}

	malformedBuckets := [][]byte{
		{0x80},                  // truncated key len varint
		{10, 'a'},               // key len past buffer
		{1, 'a', 0x80},          // truncated val count varint
		{1, 'a', 5, 1, 'x'},     // val count exceeds buffer on match
		{1, 'a', 1, 0x80},       // truncated val len varint
		{1, 'a', 1, 10, 'x'},    // val len past buffer
		{1, 'b', 1, 1, 'z', 99}, // non-matching followed by bad varint
		{0, 0, 0, 0},            // zero-filled bucket with kLen == 0
		{1, 'b', 0},             // unmatched key with vCount == 0
		{1, 'b', 0x80, 0x80, 0x80, 0x80, 0x04, 1, 'z'}, // unmatched key with huge vCount > len(b)
	}
	for i, mb := range malformedBuckets {
		out := map[string][]string{}
		if decodeCooccurBucket(mb, []string{"a"}, out) {
			t.Fatalf("case %d: decodeCooccurBucket unexpectedly succeeded", i)
		}
	}
}

func TestCooccurBinFallbackAndGuardCases(t *testing.T) {
	emptyDir := t.TempDir()
	if got := lookupCooccurNeighbors("", "alpha"); got != nil {
		t.Fatalf("empty dir returned %v", got)
	}
	if got := lookupCooccurNeighbors(emptyDir, ""); got != nil {
		t.Fatalf("empty tok returned %v", got)
	}
	if got := lookupCooccurNeighbors(emptyDir, cooccurDiscourseKey); got != nil {
		t.Fatalf("discourse key returned %v", got)
	}
	if _, ok := lookupCooccurMany("", []string{"alpha"}); ok {
		t.Fatal("empty dir lookupCooccurMany unexpectedly ok")
	}
	if _, ok := lookupCooccurMany(emptyDir, []string{"alpha"}); ok {
		t.Fatal("missing cooccur.gob lookupCooccurMany unexpectedly ok")
	}
	if err := writeCooccurBinBuild(emptyDir, nil); err == nil {
		t.Fatal("expected writeCooccurBinBuild error on empty dir")
	}
	if err := writeCooccurBinAtomic(emptyDir, nil); err == nil {
		t.Fatal("expected writeCooccurBinAtomic error on empty dir")
	}
	if err := os.WriteFile(cooccurPath(emptyDir), []byte("bad-gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := lookupCooccurMany(emptyDir, []string{"alpha"}); ok {
		t.Fatal("corrupt cooccur.gob unexpectedly ok")
	}
	raw := map[string][]string{
		"alpha":             {"beta"},
		cooccurDiscourseKey: {"ignored"},
	}
	if err := writeGobAtomic(cooccurPath(emptyDir), raw); err != nil {
		t.Fatal(err)
	}
	// No cooccur.bin on disk: lookupCooccurMany falls back directly to gob.
	got, ok := lookupCooccurMany(emptyDir, []string{"", cooccurDiscourseKey, "alpha"})
	if !ok || !reflect.DeepEqual(got["alpha"], []string{"beta"}) || len(got) != 1 {
		t.Fatalf("fallback lookupCooccurMany = (%v, %v), want {alpha:[beta]}", got, ok)
	}
}
