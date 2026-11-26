package index

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func setupIndexVaultTest(t *testing.T) (dir, claudeRoot string) {
	t.Helper()
	tmp := hermeticIndexEnv(t)
	dir = filepath.Join(tmp, "index.db")
	vault.ClearCache()
	invalidateSnapshot(dir)
	t.Cleanup(func() {
		vault.ClearCache()
		invalidateSnapshot(dir)
	})
	claudeRoot = filepath.Join(tmp, "claude", "projects", "-work-canary")
	if err := os.MkdirAll(claudeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(tmp, "claude", "projects"))
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "index-vault-pass")
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })
	return dir, claudeRoot
}

func seedVaultClaudeSession(t *testing.T, claudeRoot, name, sid, canary, filePath, cmd, errLine, fixCmd string) string {
	t.Helper()
	lines := []string{
		`{"type":"user","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:00Z","message":{"role":"user","content":"investigate ` + canary + ` outage"}}`,
		`{"type":"assistant","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"e1","name":"Edit","input":{"file_path":"` + filePath + `","old_string":"canaryOld = 1","new_string":"canaryNew = 2"}}]}}`,
		`{"type":"user","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"e1","content":"ok","is_error":false}]}}`,
		`{"type":"assistant","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:03Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b1","name":"Bash","input":{"command":"` + cmd + `"}}]}}`,
		`{"type":"user","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b1","content":"` + errLine + `","is_error":true}]}}`,
		`{"type":"assistant","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:05Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"b2","name":"Bash","input":{"command":"` + fixCmd + `"}}]}}`,
		`{"type":"user","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:06Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"b2","content":"ok exit 0","is_error":false}]}}`,
		`{"type":"assistant","sessionId":"` + sid + `","cwd":"/workspace/demo","timestamp":"2026-09-01T10:00:07Z","message":{"role":"assistant","content":[{"type":"text","text":"fixed ` + canary + ` in ` + filePath + ` root cause via ` + fixCmd + `"}]}}`,
	}
	p := filepath.Join(claudeRoot, name)
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertNoPlaintextUnderDir(t *testing.T, dir string, needles ...string) {
	t.Helper()
	dirRoot, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root %s: %v", dir, err)
	}
	defer func() { _ = dirRoot.Close() }()
	var files int
	err = fs.WalkDir(dirRoot.FS(), ".", func(rel string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if d.Name() == "index.lock" || filepath.Ext(d.Name()) == ".lock" {
			return nil
		}
		raw, rErr := dirRoot.ReadFile(rel)
		if rErr != nil {
			return rErr
		}
		files++
		for _, needle := range needles {
			if bytes.Contains(raw, []byte(needle)) {
				t.Errorf("plaintext leak %q found in encrypted index file %s", needle, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if files == 0 {
		t.Fatalf("no index files found under %s", dir)
	}
}

func TestVaultGuardFailsClosed(t *testing.T) {
	dir, _ := setupIndexVaultTest(t)
	if _, err := vault.Init(dir, "index-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if err := vault.Lock(dir, "index-vault-pass"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}

	if err := EnsureChecked(&FreshCheck{dir: dir}, false, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureChecked locked = %v, want ErrLocked", err)
	}
	if err := Ensure(dir, "", false, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("Ensure locked = %v, want ErrLocked", err)
	}
	if _, err := EnsureUnlessBusy(dir, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureUnlessBusy locked = %v, want ErrLocked", err)
	}
	if err := EnsureForSearch(dir, query.Options{}, false, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureForSearch locked = %v, want ErrLocked", err)
	}
	if _, err := EnsureForSearchNoWait(dir, query.Options{}, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("EnsureForSearchNoWait locked = %v, want ErrLocked", err)
	}
	if _, _, err := openRecordLog(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("openRecordLog locked = %v, want ErrLocked", err)
	}
	if _, err := readManifest(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("readManifest locked = %v, want ErrLocked", err)
	}
}

func TestVaultLiveEncryptionCoversIndexFilesAndOperations(t *testing.T) {
	dir, root := setupIndexVaultTest(t)
	if _, iErr := vault.Init(dir, "index-vault-pass", 0); iErr != nil {
		t.Fatalf("vault.Init: %v", iErr)
	}

	canary1 := "vaultcanaryalpha99182"
	canary2 := "vaultcanarybeta77291"
	file1 := "/workspace/demo/canaryfile99182.go"
	cmdFail := "go test ./canarytool99182"
	cmdFix := "go mod tidy # canarytool99182"
	p1 := seedVaultClaudeSession(t, root, "s1.jsonl", "s1", canary1, file1, cmdFail, "FATAL: canaryerr99182 failed", cmdFix)
	seedVaultClaudeSession(t, root, "s2.jsonl", "s2", canary1, file1, cmdFail, "FATAL: canaryerr99182 failed", cmdFix)

	if eErr := Ensure(dir, "", true, nil); eErr != nil {
		t.Fatalf("initial Ensure under vault: %v", eErr)
	}
	needles := []string{canary1, "canaryfile99182", "canarytool99182", "canaryerr99182"}
	assertNoPlaintextUnderDir(t, dir, needles...)

	res, err := SearchDetailed(dir, query.Options{Query: canary1, All: true})
	if err != nil || len(res.Sessions) != 2 {
		t.Fatalf("SearchDetailed(%s) = (%d, %v), want 2", canary1, len(res.Sessions), err)
	}
	recs, err := ReadRecords(dir)
	if err != nil || len(recs) == 0 {
		t.Fatalf("ReadRecords = (%d, %v)", len(recs), err)
	}
	cmds := ReadCommands(dir)
	if len(cmds) == 0 {
		t.Fatalf("ReadCommands empty on encrypted index")
	}
	if fss := FileSessions(dir, file1); len(fss) != 2 {
		t.Fatalf("FileSessions(%s) = %d, want 2", file1, len(fss))
	}
	if fixes := FixesFor(dir, "FATAL: canaryerr99182 failed", 5, nil); len(fixes) == 0 {
		t.Fatalf("FixesFor returned empty on encrypted index")
	}
	if rdr := evidence.Open(dir); rdr == nil || len(rdr.Lookup("claude", "s1", "fixed "+canary1+" in "+file1+" root cause via "+cmdFix)) == 0 {
		t.Fatalf("evidence.Open/Lookup empty on encrypted index")
	}
	rep, err := DeepVerify(dir)
	if err != nil || len(rep.Findings) != 0 {
		t.Fatalf("DeepVerify under vault = (%+v, %v)", rep, err)
	}

	// Incremental append path while unlocked.
	appendLine := `{"type":"user","sessionId":"s1","cwd":"/workspace/demo","timestamp":"2026-09-01T10:05:00Z","message":{"role":"user","content":"follow-up ` + canary2 + ` note"}}` + "\n"
	f, err := os.OpenFile(p1, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, wErr := f.WriteString(appendLine); wErr != nil {
		t.Fatal(wErr)
	}
	_ = f.Close()
	if uErr := Ensure(dir, "", false, nil); uErr != nil {
		t.Fatalf("incremental append Ensure: %v", uErr)
	}
	assertNoPlaintextUnderDir(t, dir, append(needles, canary2)...)
	res2, err := SearchDetailed(dir, query.Options{Query: canary2, All: true})
	if err != nil || len(res2.Sessions) != 1 {
		t.Fatalf("SearchDetailed after append(%s) = (%d, %v), want 1", canary2, len(res2.Sessions), err)
	}

	// File replacement + scrub path while unlocked.
	canary3 := "vaultcanarygamma55409"
	seedVaultClaudeSession(t, root, "s1.jsonl", "s1", canary3, file1, cmdFail, "FATAL: canaryerr99182 failed", cmdFix)
	if rErr := Ensure(dir, "", false, nil); rErr != nil {
		t.Fatalf("replace Ensure: %v", rErr)
	}
	assertNoPlaintextUnderDir(t, dir, append(needles, canary2, canary3)...)
	resReplaced, err := SearchDetailed(dir, query.Options{Query: canary2, All: true})
	if err != nil || len(resReplaced.Sessions) != 0 {
		t.Fatalf("SearchDetailed old appended term after replace = (%d, %v), want 0", len(resReplaced.Sessions), err)
	}
	resNew, err := SearchDetailed(dir, query.Options{Query: canary3, All: true})
	if err != nil || len(resNew.Sessions) != 1 {
		t.Fatalf("SearchDetailed new term after replace = (%d, %v), want 1", len(resNew.Sessions), err)
	}
}

func TestVaultInitMigratesPlaintextIndexAndLockPurgesCache(t *testing.T) {
	dir, root := setupIndexVaultTest(t)
	canary := "plaintextbeforevault66123"
	file1 := "/workspace/demo/mig66123.go"
	seedVaultClaudeSession(t, root, "m1.jsonl", "m1", canary, file1, "go test ./migtool66123", "ERR_MIG_66123 boom", "go mod tidy # migtool66123")
	seedVaultClaudeSession(t, root, "m2.jsonl", "m2", canary, file1, "go test ./migtool66123", "ERR_MIG_66123 boom", "go mod tidy # migtool66123")

	// Build unencrypted index first and confirm plaintext bytes exist on disk.
	if eErr := Ensure(dir, "", true, nil); eErr != nil {
		t.Fatalf("plaintext Ensure: %v", eErr)
	}
	rawRecs, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil || !bytes.Contains(rawRecs, []byte(canary)) {
		t.Fatalf("expected plaintext canary in unencrypted records.bin before Init (err=%v)", err)
	}

	// Enable vault and migrate in place.
	if _, iErr := vault.Init(dir, "index-vault-pass", 0); iErr != nil {
		t.Fatalf("vault.Init migrate: %v", iErr)
	}
	assertNoPlaintextUnderDir(t, dir, canary, "mig66123", "migtool66123", "ERR_MIG_66123")

	// Verify reads & search still succeed while unlocked (and warm snapshot/manifest cache).
	res, err := SearchDetailed(dir, query.Options{Query: canary, All: true})
	if err != nil || len(res.Sessions) != 2 {
		t.Fatalf("SearchDetailed after migration = (%d, %v), want 2", len(res.Sessions), err)
	}
	recs, err := ReadRecords(dir)
	if err != nil || len(recs) == 0 {
		t.Fatalf("ReadRecords after migration = (%d, %v)", len(recs), err)
	}
	if atRecs, aErr := ReadRecordsAt(dir, []int64{recs[0].Offset}); aErr != nil || len(atRecs) != 1 {
		t.Fatalf("ReadRecordsAt after migration = (%d, %v)", len(atRecs), aErr)
	}
	if rep, dErr := DeepVerify(dir); dErr != nil || len(rep.Findings) != 0 {
		t.Fatalf("DeepVerify after migration = (%+v, %v)", rep, dErr)
	}

	// Lock vault: manifest/snapshot caches must purge and reads must fail closed.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(dir, "index-vault-pass"); lErr != nil {
		t.Fatalf("vault.Lock: %v", lErr)
	}
	if _, mErr := readManifestCached(dir); !errors.Is(mErr, vault.ErrLocked) {
		t.Fatalf("readManifestCached when locked = %v, want ErrLocked", mErr)
	}
	if _, sErr := SearchDetailed(dir, query.Options{Query: canary, All: true}); !errors.Is(sErr, vault.ErrLocked) {
		t.Fatalf("SearchDetailed when locked = %v, want ErrLocked", sErr)
	}
	if _, rErr := ReadRecords(dir); !errors.Is(rErr, vault.ErrLocked) {
		t.Fatalf("ReadRecords when locked = %v, want ErrLocked", rErr)
	}
	if _, aErr := ReadRecordsAt(dir, []int64{recs[0].Offset}); !errors.Is(aErr, vault.ErrLocked) {
		t.Fatalf("ReadRecordsAt when locked = %v, want ErrLocked", aErr)
	}
	if cmds := ReadCommands(dir); len(cmds) != 0 {
		t.Fatalf("ReadCommands when locked = %v, want empty", cmds)
	}
	if fss := FileSessions(dir, file1); len(fss) != 0 {
		t.Fatalf("FileSessions when locked = %v, want empty", fss)
	}
	if _, dErr := DeepVerify(dir); !errors.Is(dErr, vault.ErrLocked) {
		t.Fatalf("DeepVerify when locked = %v, want ErrLocked", dErr)
	}
	if rdr := evidence.Open(dir); rdr != nil {
		t.Fatalf("evidence.Open when locked = %v, want nil", rdr)
	}

	// Unlock restores full access.
	if uErr := vault.Unlock(dir, "index-vault-pass"); uErr != nil {
		t.Fatalf("vault.Unlock: %v", uErr)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", "index-vault-pass")
	resAfterUnlock, err := SearchDetailed(dir, query.Options{Query: canary, All: true})
	if err != nil || len(resAfterUnlock.Sessions) != 2 {
		t.Fatalf("SearchDetailed after Unlock = (%d, %v), want 2", len(resAfterUnlock.Sessions), err)
	}
}

func TestVaultMigrationCoversTreesSpillsAndCanonicalPaths(t *testing.T) {
	dir, root := setupIndexVaultTest(t)

	if got := canonicalVaultDir(""); got != "" {
		t.Fatalf("canonicalVaultDir empty = %q", got)
	}
	wantCanon := filepath.Clean(dir)
	for _, raw := range []string{
		dir + ".vaultmig",
		dir + ".part",
		dir + ".tmp",
		dir + ".old",
		dir + ".old.123",
		dir + ".vaultmig.part.old.42",
		compactionSidecarPath(dir) + ".tmp",
	} {
		if got := canonicalVaultDir(raw); got != wantCanon {
			t.Fatalf("canonicalVaultDir(%q) = %q, want %q", raw, got, wantCanon)
		}
	}
	for _, tc := range []struct {
		in   string
		base string
		ok   bool
	}{
		{"plain", "", false},
		{".old.123", "", false},
		{"dir.old.", "", false},
		{"dir.old.12a", "", false},
		{"dir.old.42", "dir", true},
	} {
		b, ok := trimParkedOldSuffix(tc.in)
		if b != tc.base || ok != tc.ok {
			t.Fatalf("trimParkedOldSuffix(%q) = (%q, %v), want (%q, %v)", tc.in, b, ok, tc.base, tc.ok)
		}
	}
	if got := vaultDirForFile(""); got != "" {
		t.Fatalf("vaultDirForFile empty = %q", got)
	}
	for _, p := range []string{
		compactionSidecarPath(dir) + ".tmp",
		filepath.Join(dir, "buckets", "sub", "b0.bin"),
		filepath.Join(dir, "spill", "000.spill"),
		filepath.Join(dir, "deferred", "nested", "item.bin"),
	} {
		if got := vaultDirForFile(p); got != wantCanon {
			t.Fatalf("vaultDirForFile(%q) = %q, want %q", p, got, wantCanon)
		}
	}

	seedVaultClaudeSession(t, root, "f1.jsonl", "f1", "premigalpha881", "/workspace/demo/f1.go", "go test ./a", "ERR_F1 boom", "go mod tidy")
	seedVaultClaudeSession(t, root, "f2.jsonl", "f2", "premigbeta882", "/workspace/demo/f2.go", "go test ./b", "ERR_F2 boom", "go mod tidy")
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, fErr := Forget(dir, ForgetOptions{Session: "f1"}); fErr != nil {
		t.Fatalf("Forget: %v", fErr)
	}

	compactionData := []byte("compaction-sidecar-premig881")
	if wErr := os.WriteFile(compactionSidecarPath(dir), compactionData, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	defItem := filepath.Join(dir, "deferred", "nested", "item.bin")
	if mErr := os.MkdirAll(filepath.Dir(defItem), 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(defItem, []byte("defsecret881"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(filepath.Join(dir, "deferred", "stale.tmp"), []byte("tmp"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	extraItem := filepath.Join(dir, "extra", "custom.dat")
	if mErr := os.MkdirAll(filepath.Dir(extraItem), 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(extraItem, []byte("extrasecret881"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	miscItem := filepath.Join(dir, "misc.dat")
	if wErr := os.WriteFile(miscItem, []byte("miscsecret881"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}

	sp, err := newSpiller(dir)
	if err != nil {
		t.Fatal(err)
	}
	sg := newSpillGroups()
	sg.add([]byte("tspilltok881"), 128, 1, false)
	seen := map[string]bool{}
	var spillShard string
	for sh, payload := range sg.encode(seen) {
		if len(payload) == 0 {
			continue
		}
		if aErr := sp.append(sh, payload); aErr != nil {
			t.Fatal(aErr)
		}
		spillShard = filepath.Join(dir, "spill", shardName(sh))
	}
	if fErr := sp.flushWriters(); fErr != nil {
		t.Fatal(fErr)
	}
	if wErr := os.WriteFile(filepath.Join(dir, "spill", "000.tmp"), []byte("tmp"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}

	if _, iErr := vault.Init(dir, "index-vault-pass", 0); iErr != nil {
		t.Fatalf("vault.Init: %v", iErr)
	}
	assertNoPlaintextUnderDir(t, dir, "premigbeta882", "defsecret881", "extrasecret881", "miscsecret881", "tspilltok881")

	scRaw, err := os.ReadFile(compactionSidecarPath(dir))
	if err != nil || !vault.IsSealedBytes(scRaw) {
		t.Fatalf("compaction sidecar not sealed: err=%v sealed=%v", err, vault.IsSealedBytes(scRaw))
	}
	for _, sealedPath := range []string{defItem, extraItem, miscItem} {
		raw, rErr := os.ReadFile(sealedPath)
		if rErr != nil || !vault.IsSealedBytes(raw) {
			t.Fatalf("%s not sealed: err=%v sealed=%v", sealedPath, rErr, vault.IsSealedBytes(raw))
		}
	}
	if _, stErr := os.Stat(filepath.Join(dir, "deferred", "stale.tmp")); !errors.Is(stErr, fs.ErrNotExist) {
		t.Fatalf("expected stale.tmp to be excluded from migration, got %v", stErr)
	}

	spillRaw, err := os.ReadFile(spillShard)
	if err != nil || len(spillRaw) < 4 || !vault.IsStreamHeader(spillRaw[:4]) {
		t.Fatalf("migrated spill shard missing stream header: err=%v len=%d", err, len(spillRaw))
	}
	sf, err := os.Open(spillShard)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := readSpill(sf)
	_ = sf.Close()
	if err != nil || len(posts["tspilltok881"]) != 1 {
		t.Fatalf("readSpill after migration = (%d, %v)", len(posts["tspilltok881"]), err)
	}

	// Second idempotent migration sweeps unsealed files left inside an already-sealed store.
	if wErr := os.WriteFile(filepath.Join(dir, "buckets", "sweep.dat"), []byte("sweepbkt881"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(defItem, []byte("defsecret-sweep"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(extraItem, []byte("extra-sweep"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(miscItem, []byte("misc-sweep"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	dek, err := vault.CheckUnlocked(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mErr := migrateIndexStore(dir, dek); mErr != nil {
		t.Fatalf("second migrateIndexStore sweep: %v", mErr)
	}
	assertNoPlaintextUnderDir(t, dir, "sweepbkt881", "defsecret-sweep", "extra-sweep", "misc-sweep")

	// Live spill writer under unlocked vault produces stream-encrypted shards.
	liveSp, err := newSpiller(dir)
	if err != nil {
		t.Fatal(err)
	}
	liveSG := newSpillGroups()
	liveSG.add([]byte("tlivespill881"), 256, 2, true)
	var liveShard string
	for sh, payload := range liveSG.encode(map[string]bool{}) {
		if len(payload) == 0 {
			continue
		}
		if aErr := liveSp.append(sh, payload); aErr != nil {
			t.Fatal(aErr)
		}
		liveShard = filepath.Join(dir, "spill", shardName(sh))
	}
	if fErr := liveSp.flushWriters(); fErr != nil {
		t.Fatal(fErr)
	}
	lsf, err := os.Open(liveShard)
	if err != nil {
		t.Fatal(err)
	}
	livePosts, err := readSpill(lsf)
	_ = lsf.Close()
	if err != nil || len(livePosts["tlivespill881"]) != 1 {
		t.Fatalf("readSpill live = (%d, %v)", len(livePosts["tlivespill881"]), err)
	}

	// Detached encrypted spill without an enabled vault fails closed with ErrKeyUnavailable.
	origVaultFile := os.Getenv("DEJA_VAULT_FILE")
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(t.TempDir(), "missing-vault.json"))
	vault.ClearCache()
	detachedSpill := filepath.Join(t.TempDir(), "000.spill")
	sealedBytes, err := os.ReadFile(liveShard)
	if err != nil {
		t.Fatal(err)
	}
	if wErr := os.WriteFile(detachedSpill, sealedBytes, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	df, err := os.Open(detachedSpill)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readSpill(df)
	_ = df.Close()
	t.Setenv("DEJA_VAULT_FILE", origVaultFile)
	vault.ClearCache()
	if !errors.Is(err, vault.ErrKeyUnavailable) {
		t.Fatalf("readSpill detached sealed shard = %v, want ErrKeyUnavailable", err)
	}

	// Locked vault store fails readSpill with ErrLocked.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(dir, "index-vault-pass"); lErr != nil {
		t.Fatal(lErr)
	}
	lf, err := os.Open(detachedSpill)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readSpill(lf)
	_ = lf.Close()
	if !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("readSpill locked = %v, want ErrLocked", err)
	}
}

func makeRawLogRecord(rec []byte) []byte {
	var hdr [4]byte
	if len(rec) > 1<<30 {
		panic("record too large")
	}
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(rec)))
	out := make([]byte, 4+len(rec))
	copy(out[:4], hdr[:])
	copy(out[4:], rec)
	return out
}

func TestVaultIOHelpersAndMigrationEdgeCases(t *testing.T) {
	dir, _ := setupIndexVaultTest(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Unsealed indexBlobFile covers Seek, PlainSize, Stat, Read, ReadAt, and Close.
	unsealedPath := filepath.Join(dir, "plain.dat")
	if err := writeSealedFileAtomic(unsealedPath, []byte("abcdef"), vault.DomainIndexFile); err != nil {
		t.Fatalf("writeSealedFileAtomic unsealed: %v", err)
	}
	ub, err := openIndexBlobFile(unsealedPath)
	if err != nil {
		t.Fatalf("openIndexBlobFile unsealed: %v", err)
	}
	if _, sErr := ub.Seek(2, io.SeekStart); sErr != nil {
		t.Fatalf("Seek unsealed: %v", sErr)
	}
	var two [2]byte
	if _, rErr := io.ReadFull(ub, two[:]); rErr != nil || string(two[:]) != "cd" {
		t.Fatalf("Read after Seek unsealed = (%q, %v)", string(two[:]), rErr)
	}
	if sz, pErr := ub.PlainSize(); pErr != nil || sz != 6 {
		t.Fatalf("PlainSize unsealed = (%d, %v)", sz, pErr)
	}
	if cErr := ub.Close(); cErr != nil {
		t.Fatalf("Close unsealed: %v", cErr)
	}

	// Enable vault and verify sealed indexBlobFile + readIndexBytes/openIndexBlobFile guards.
	if _, iErr := vault.Init(dir, "index-vault-pass", 0); iErr != nil {
		t.Fatalf("vault.Init: %v", iErr)
	}
	dek, err := vault.CheckUnlocked(dir)
	if err != nil {
		t.Fatalf("CheckUnlocked: %v", err)
	}
	unsealedAfterPath := filepath.Join(dir, "plain-after.dat")
	if wErr := os.WriteFile(unsealedAfterPath, []byte("plain"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, rErr := readIndexBytes(unsealedAfterPath); rErr == nil || !strings.Contains(rErr.Error(), "unsealed index file") {
		t.Fatalf("readIndexBytes unsealed = %v, want unsealed error", rErr)
	}
	if _, bErr := openIndexBlobFile(unsealedAfterPath); bErr == nil || !strings.Contains(bErr.Error(), "unsealed index file") {
		t.Fatalf("openIndexBlobFile unsealed with active vault = %v, want unsealed error", bErr)
	}

	sealedPath := filepath.Join(dir, "sealed.dat")
	if wErr := writeSealedIndexFile(sealedPath, []byte("hello-blob"), vault.DomainIndexFile); wErr != nil {
		t.Fatalf("writeSealedIndexFile: %v", wErr)
	}
	sb, err := openIndexBlobFile(sealedPath)
	if err != nil {
		t.Fatalf("openIndexBlobFile sealed: %v", err)
	}
	if _, sErr := sb.Seek(6, io.SeekStart); sErr != nil {
		t.Fatalf("Seek sealed: %v", sErr)
	}
	var tail [4]byte
	if _, rErr := io.ReadFull(sb, tail[:]); rErr != nil || string(tail[:]) != "blob" {
		t.Fatalf("Read after Seek sealed = (%q, %v)", string(tail[:]), rErr)
	}
	if _, atErr := sb.ReadAt(two[:], 0); atErr != nil || string(two[:]) != "he" {
		t.Fatalf("ReadAt sealed = (%q, %v)", string(two[:]), atErr)
	}
	if _, stErr := sb.Stat(); stErr != nil {
		t.Fatalf("Stat sealed: %v", stErr)
	}
	if sz, pErr := sb.PlainSize(); pErr != nil || sz != 10 {
		t.Fatalf("PlainSize sealed = (%d, %v)", sz, pErr)
	}
	if cErr := sb.Close(); cErr != nil {
		t.Fatalf("Close sealed: %v", cErr)
	}

	corruptSealed := filepath.Join(dir, "corrupt.dat")
	if wErr := writeRawFileAtomic(corruptSealed, []byte("DJVF1-short")); wErr != nil {
		t.Fatal(wErr)
	}
	if _, oErr := openIndexBlobFile(corruptSealed); oErr == nil {
		t.Fatal("openIndexBlobFile truncated DJVF1 succeeded unexpectedly")
	}
	if _, oErr := openIndexBlobFile(filepath.Join(dir, "missing.dat")); oErr == nil {
		t.Fatal("openIndexBlobFile missing succeeded unexpectedly")
	}

	// Detached sealed blob without vault fails with ErrKeyUnavailable.
	origVaultFile := os.Getenv("DEJA_VAULT_FILE")
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(t.TempDir(), "missing-vault.json"))
	vault.ClearCache()
	if _, oErr := openIndexBlobFile(sealedPath); !errors.Is(oErr, vault.ErrKeyUnavailable) {
		t.Fatalf("openIndexBlobFile without vault = %v, want ErrKeyUnavailable", oErr)
	}
	t.Setenv("DEJA_VAULT_FILE", origVaultFile)
	vault.ClearCache()

	// remapMigratedManifest covers DeadRecords, DeadBytes, and RecordsSize < oldLogSize.
	core := manifestCore{
		RecordsSize: 100,
		DeadRecords: []int64{10, 999},
		DeadBytes:   10,
	}
	remapMigratedManifest(&core, map[int64]int64{10: 20, 100: 150}, 200, 300, 28)
	if core.RecordsSize != 150 || core.DeadBytes != 38 || len(core.DeadRecords) != 1 || core.DeadRecords[0] != 20 {
		t.Fatalf("remapMigratedManifest = %+v", core)
	}
	remapMigratedManifest(&core, map[int64]int64{}, 200, 300, 0)
	if core.RecordsSize != 300 {
		t.Fatalf("remapMigratedManifest fallback RecordsSize = %d, want 300", core.RecordsSize)
	}

	// migrateRecordsLog covers missing path, sealed pass-through, scrubbed raw record, and corrupt cases.
	rc, err := vault.NewRecordCipher(dek)
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	if om, _, _, _, mErr := migrateRecordsLog(filepath.Join(workDir, "absent.bin"), filepath.Join(workDir, "out.bin"), rc, nil); mErr != nil || len(om) != 0 {
		t.Fatalf("migrateRecordsLog missing = (%v, %v)", om, mErr)
	}
	prefix := []byte{1, 2, 3}
	zeroBody := make([]byte, 8)
	scrubbedRec := append(append([]byte(nil), prefix...), recordRaw)
	scrubbedRec = append(scrubbedRec, zeroBody...)
	alreadySealed := append(append([]byte(nil), prefix...), recordRaw|recordSealed)
	alreadySealed = append(alreadySealed, []byte("opaque")...)
	validRec := append(makeRawLogRecord(scrubbedRec), makeRawLogRecord(alreadySealed)...)
	srcLog := filepath.Join(workDir, "valid.records.bin")
	dstLog := filepath.Join(workDir, "valid.records.out")
	if wErr := os.WriteFile(srcLog, validRec, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if om, _, _, _, mErr := migrateRecordsLog(srcLog, dstLog, rc, []int64{0}); mErr != nil || len(om) != 2 {
		t.Fatalf("migrateRecordsLog valid = (%d, %v)", len(om), mErr)
	}

	badRec := append(append([]byte(nil), prefix...), 0x7f, 'x')
	badSrc := filepath.Join(workDir, "badflag.bin")
	if wErr := os.WriteFile(badSrc, makeRawLogRecord(badRec), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, _, _, mErr := migrateRecordsLog(badSrc, filepath.Join(workDir, "badflag.out"), rc, nil); mErr == nil {
		t.Fatal("migrateRecordsLog unknown flag succeeded unexpectedly")
	}

	shortSrc := filepath.Join(workDir, "short.bin")
	if wErr := os.WriteFile(shortSrc, makeRawLogRecord([]byte{1, 2, 3}), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, _, _, mErr := migrateRecordsLog(shortSrc, filepath.Join(workDir, "short.out"), rc, nil); !errors.Is(mErr, errShortRecord) {
		t.Fatalf("migrateRecordsLog short = %v, want errShortRecord", mErr)
	}

	var bigHdr [4]byte
	binary.LittleEndian.PutUint32(bigHdr[:], maxRecordSize+1)
	bigSrc := filepath.Join(workDir, "big.bin")
	if wErr := os.WriteFile(bigSrc, bigHdr[:], 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, _, _, mErr := migrateRecordsLog(bigSrc, filepath.Join(workDir, "big.out"), rc, nil); !errors.Is(mErr, errCorruptIndex) {
		t.Fatalf("migrateRecordsLog oversized = %v, want errCorruptIndex", mErr)
	}

	// migrateBucketsDir handles non-postings files, .tmp skips, subdirs, and already-sealed buckets.
	bSrc := filepath.Join(workDir, "bsrc")
	bDst := filepath.Join(workDir, "bdst")
	if mErr := os.MkdirAll(filepath.Join(bSrc, "nested"), 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(filepath.Join(bSrc, "meta.dat"), []byte("meta"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(filepath.Join(bSrc, "skip.tmp"), []byte("tmp"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	rawBucket := encodeBucketBytes(map[string][]posting{"alpha": {{Off: 10, Sid: 1}}})
	sealedBucket, err := vault.SealBytes(dek, vault.DomainIndexFile, rawBucket)
	if err != nil {
		t.Fatal(err)
	}
	if wErr := os.WriteFile(filepath.Join(bSrc, "al.bin"), sealedBucket, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateBucketsDir(bSrc, bDst, dek, map[int64]int64{10: 55}); mErr != nil {
		t.Fatalf("migrateBucketsDir: %v", mErr)
	}
	if mErr := migrateBucketsDir(filepath.Join(workDir, "missing-bsrc"), bDst, dek, nil); mErr != nil {
		t.Fatalf("migrateBucketsDir missing: %v", mErr)
	}

	// sealUnsealedSpillsInDir + idempotent sealSpillFileAtomic.
	spDir := filepath.Join(workDir, "spills")
	if mErr := os.MkdirAll(spDir, 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if sErr := sealUnsealedSpillsInDir(filepath.Join(workDir, "no-spills"), dek); sErr != nil {
		t.Fatalf("sealUnsealedSpillsInDir missing: %v", sErr)
	}
	if wErr := os.WriteFile(filepath.Join(spDir, "001.spill"), []byte("spill-plain"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(filepath.Join(spDir, "ignore.tmp"), []byte("tmp"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if sErr := sealUnsealedSpillsInDir(spDir, dek); sErr != nil {
		t.Fatalf("sealUnsealedSpillsInDir plain: %v", sErr)
	}
	if sErr := sealUnsealedSpillsInDir(spDir, dek); sErr != nil {
		t.Fatalf("sealUnsealedSpillsInDir idempotent: %v", sErr)
	}

	// migrateIndexStore error branches: regular file as index dir, records without manifest, corrupt manifest.
	regFile := filepath.Join(workDir, "regular-file")
	if wErr := os.WriteFile(regFile, []byte("x"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateIndexStore(regFile, dek); mErr == nil || !strings.Contains(mErr.Error(), "not a directory") {
		t.Fatalf("migrateIndexStore file = %v, want not-a-directory", mErr)
	}
	orphanDir := filepath.Join(workDir, "orphan-records")
	if mErr := os.MkdirAll(orphanDir, 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(recordsPath(orphanDir), []byte("r"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateIndexStore(orphanDir, dek); mErr == nil || !strings.Contains(mErr.Error(), "without manifest.gob") {
		t.Fatalf("migrateIndexStore missing manifest = %v", mErr)
	}
	badManifestDir := filepath.Join(workDir, "bad-manifest")
	if mErr := os.MkdirAll(badManifestDir, 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(filepath.Join(badManifestDir, "manifest.gob"), []byte("not-gob"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateIndexStore(badManifestDir, dek); mErr == nil || !strings.Contains(mErr.Error(), "decode manifest") {
		t.Fatalf("migrateIndexStore corrupt manifest = %v", mErr)
	}
	if mErr := migrateIndexStore("", dek); mErr != nil {
		t.Fatalf("migrateIndexStore empty = %v", mErr)
	}
	remapMigratedManifest(&manifestCore{}, nil, 0, 0, 0)

	// Truncated/corrupt records and buckets in migrateRecordsLog / migrateBucketsDir.
	truncHdr := filepath.Join(workDir, "trunc-hdr.bin")
	if wErr := os.WriteFile(truncHdr, []byte{1, 2}, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, _, _, mErr := migrateRecordsLog(truncHdr, filepath.Join(workDir, "trunc-hdr.out"), rc, nil); mErr == nil {
		t.Fatal("migrateRecordsLog truncated header succeeded unexpectedly")
	}
	truncBody := filepath.Join(workDir, "trunc-body.bin")
	if wErr := os.WriteFile(truncBody, []byte{10, 0, 0, 0, 1, 2}, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, _, _, mErr := migrateRecordsLog(truncBody, filepath.Join(workDir, "trunc-body.out"), rc, nil); mErr == nil {
		t.Fatal("migrateRecordsLog truncated body succeeded unexpectedly")
	}
	badVarint := filepath.Join(workDir, "bad-varint.bin")
	if wErr := os.WriteFile(badVarint, makeRawLogRecord([]byte{0x80, 0x80, 0x80}), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, _, _, mErr := migrateRecordsLog(badVarint, filepath.Join(workDir, "bad-varint.out"), rc, nil); !errors.Is(mErr, errShortRecord) {
		t.Fatalf("migrateRecordsLog bad uvarint = %v, want errShortRecord", mErr)
	}

	corruptBktDir := filepath.Join(workDir, "corrupt-bkt-sealed")
	if mErr := os.MkdirAll(corruptBktDir, 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(filepath.Join(corruptBktDir, "ba.bin"), []byte("DJVF1-bad"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateBucketsDir(corruptBktDir, filepath.Join(workDir, "corrupt-bkt.out"), dek, nil); mErr == nil {
		t.Fatal("migrateBucketsDir corrupt sealed bucket succeeded unexpectedly")
	}
	corruptPlainBkt := filepath.Join(workDir, "corrupt-bkt-plain")
	if mErr := os.MkdirAll(corruptPlainBkt, 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(filepath.Join(corruptPlainBkt, "bb.bin"), []byte{0xff}, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateBucketsDir(corruptPlainBkt, filepath.Join(workDir, "corrupt-bkt-p.out"), dek, nil); mErr == nil {
		t.Fatal("migrateBucketsDir corrupt plain bucket succeeded unexpectedly")
	}

	// readIndexBytes fails with ErrKeyUnavailable when vault is disabled.
	origVF := os.Getenv("DEJA_VAULT_FILE")
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(t.TempDir(), "missing-vault-2.json"))
	vault.ClearCache()
	if _, rErr := readIndexBytes(sealedPath); !errors.Is(rErr, vault.ErrKeyUnavailable) {
		t.Fatalf("readIndexBytes disabled vault = %v, want ErrKeyUnavailable", rErr)
	}
	// readSpill on corrupt plain spill binary fails decode when vault is disabled.
	corruptPlainSpill := filepath.Join(t.TempDir(), "bad.spill")
	if wErr := os.WriteFile(corruptPlainSpill, []byte{0x80}, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if sf, oErr := os.Open(corruptPlainSpill); oErr == nil {
		_, rsErr := readSpill(sf)
		_ = sf.Close()
		if rsErr == nil {
			t.Fatal("readSpill corrupt varint succeeded unexpectedly")
		}
	}
	t.Setenv("DEJA_VAULT_FILE", origVF)
	vault.ClearCache()

	// Locked vault rejects readIndexBytes, openIndexBlobFile, writeSealedAtomicStat, writeSealedIndexFile, and spiller.append.
	if rErr := writeRawFileAtomic(filepath.Join(dir, "stale.lock"), []byte("1")); rErr != nil {
		t.Fatal(rErr)
	}
	_ = isVaultInfraFile("stale.lock")
	if wErr := os.WriteFile(filepath.Join(dir, "corrupt-spill-in-vault.spill"), []byte("not-a-stream"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if cf, oErr := os.Open(filepath.Join(dir, "corrupt-spill-in-vault.spill")); oErr == nil {
		_, rsErr := readSpill(cf)
		_ = cf.Close()
		if rsErr == nil {
			t.Fatal("readSpill unsealed stream under vault succeeded unexpectedly")
		}
	}

	// Sweep removes stale unsealed derived files and validates invalid DEKs across migration paths.
	sweepDir := filepath.Join(workDir, "sweep-derived")
	if mErr := os.MkdirAll(filepath.Join(sweepDir, "buckets"), 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	for _, dfName := range []string{recordOffsetsFile, frictionTextsFile} {
		if wErr := os.WriteFile(filepath.Join(sweepDir, dfName), []byte("unsealed-derived"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
	}
	if sErr := sweepUnsealedDirFiles(sweepDir, dek); sErr != nil {
		t.Fatalf("sweepUnsealedDirFiles: %v", sErr)
	}
	if sErr := sweepUnsealedDirFiles(filepath.Join(workDir, "missing-sweep"), dek); sErr == nil {
		t.Fatal("sweepUnsealedDirFiles missing dir succeeded unexpectedly")
	}

	shortDEK := []byte{1}
	badSubRoot := filepath.Join(workDir, "short-dek-sweep")
	for _, sub := range []string{"buckets", "deferred", "spill", "custom"} {
		subDir := filepath.Join(badSubRoot, sub)
		if mErr := os.MkdirAll(subDir, 0o700); mErr != nil {
			t.Fatal(mErr)
		}
		if wErr := os.WriteFile(filepath.Join(subDir, "item.dat"), []byte("plain"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		if wErr := os.WriteFile(filepath.Join(subDir, "skip.tmp"), []byte("tmp"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
	}
	if wErr := os.WriteFile(compactionSidecarPath(badSubRoot), []byte("c"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if sErr := sealCompactionSidecar(badSubRoot, shortDEK); sErr == nil {
		t.Fatal("sealCompactionSidecar short DEK succeeded unexpectedly")
	}
	for _, name := range []string{"buckets", "deferred", "spill", "custom"} {
		if sErr := migrateIndexSubdir(name, filepath.Join(badSubRoot, name), filepath.Join(workDir, "out-"+name), shortDEK); sErr == nil {
			t.Fatalf("migrateIndexSubdir(%s) short DEK succeeded unexpectedly", name)
		}
	}
	if sErr := sweepUnsealedDirFiles(badSubRoot, shortDEK); sErr == nil {
		t.Fatal("sweepUnsealedDirFiles short DEK succeeded unexpectedly")
	}
	if sErr := sealSpillFileAtomic(filepath.Join(workDir, "missing-spill.spill"), filepath.Join(workDir, "out.spill"), dek); sErr == nil {
		t.Fatal("sealSpillFileAtomic missing src succeeded unexpectedly")
	}

	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(dir, "index-vault-pass"); lErr != nil {
		t.Fatal(lErr)
	}
	lockedFile := filepath.Join(dir, "locked.dat")
	if _, rErr := readIndexBytes(lockedFile); !errors.Is(rErr, vault.ErrLocked) {
		t.Fatalf("readIndexBytes locked = %v, want ErrLocked", rErr)
	}
	if _, oErr := openIndexBlobFile(lockedFile); !errors.Is(oErr, vault.ErrLocked) {
		t.Fatalf("openIndexBlobFile locked = %v, want ErrLocked", oErr)
	}
	if wErr := writeSealedFileAtomic(lockedFile, []byte("x"), vault.DomainIndexFile); !errors.Is(wErr, vault.ErrLocked) {
		t.Fatalf("writeSealedFileAtomic locked = %v, want ErrLocked", wErr)
	}
	if wErr := writeSealedIndexFile(lockedFile, []byte("x"), vault.DomainIndexFile); !errors.Is(wErr, vault.ErrLocked) {
		t.Fatalf("writeSealedIndexFile locked = %v, want ErrLocked", wErr)
	}
	if sp, spErr := newSpiller(dir); spErr == nil {
		if aErr := sp.append(0, []byte("x")); !errors.Is(aErr, vault.ErrLocked) {
			t.Fatalf("spiller.append locked = %v, want ErrLocked", aErr)
		}
	}
}
