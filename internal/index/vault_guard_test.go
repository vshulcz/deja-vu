package index

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func setupIndexVaultTest(t *testing.T) (tmp, dir, claudeRoot string) {
	t.Helper()
	tmp = hermeticIndexEnv(t)
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
	return tmp, dir, claudeRoot
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
	var files int
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Base(path) == "index.lock" || filepath.Ext(path) == ".lock" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files++
		for _, needle := range needles {
			if bytes.Contains(raw, []byte(needle)) {
				t.Errorf("plaintext leak %q found in encrypted index file %s", needle, path)
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
	_, dir, _ := setupIndexVaultTest(t)
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
	_, dir, root := setupIndexVaultTest(t)
	if _, err := vault.Init(dir, "index-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}

	canary1 := "vaultcanaryalpha99182"
	canary2 := "vaultcanarybeta77291"
	file1 := "/workspace/demo/canaryfile99182.go"
	cmdFail := "go test ./canarytool99182"
	cmdFix := "go mod tidy # canarytool99182"
	p1 := seedVaultClaudeSession(t, root, "s1.jsonl", "s1", canary1, file1, cmdFail, "FATAL: canaryerr99182 failed", cmdFix)
	seedVaultClaudeSession(t, root, "s2.jsonl", "s2", canary1, file1, cmdFail, "FATAL: canaryerr99182 failed", cmdFix)

	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("initial Ensure under vault: %v", err)
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
	if _, err := f.WriteString(appendLine); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("incremental append Ensure: %v", err)
	}
	assertNoPlaintextUnderDir(t, dir, append(needles, canary2)...)
	res2, err := SearchDetailed(dir, query.Options{Query: canary2, All: true})
	if err != nil || len(res2.Sessions) != 1 {
		t.Fatalf("SearchDetailed after append(%s) = (%d, %v), want 1", canary2, len(res2.Sessions), err)
	}

	// File replacement + scrub path while unlocked.
	canary3 := "vaultcanarygamma55409"
	seedVaultClaudeSession(t, root, "s1.jsonl", "s1", canary3, file1, cmdFail, "FATAL: canaryerr99182 failed", cmdFix)
	if err := Ensure(dir, "", false, nil); err != nil {
		t.Fatalf("replace Ensure: %v", err)
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
	_, dir, root := setupIndexVaultTest(t)
	canary := "plaintextbeforevault66123"
	file1 := "/workspace/demo/mig66123.go"
	seedVaultClaudeSession(t, root, "m1.jsonl", "m1", canary, file1, "go test ./migtool66123", "ERR_MIG_66123 boom", "go mod tidy # migtool66123")
	seedVaultClaudeSession(t, root, "m2.jsonl", "m2", canary, file1, "go test ./migtool66123", "ERR_MIG_66123 boom", "go mod tidy # migtool66123")

	// Build unencrypted index first and confirm plaintext bytes exist on disk.
	if err := Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("plaintext Ensure: %v", err)
	}
	rawRecs, err := os.ReadFile(filepath.Join(dir, "records.bin"))
	if err != nil || !bytes.Contains(rawRecs, []byte(canary)) {
		t.Fatalf("expected plaintext canary in unencrypted records.bin before Init (err=%v)", err)
	}

	// Enable vault and migrate in place.
	if _, err := vault.Init(dir, "index-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init migrate: %v", err)
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
	if atRecs, err := ReadRecordsAt(dir, []int64{recs[0].Offset}); err != nil || len(atRecs) != 1 {
		t.Fatalf("ReadRecordsAt after migration = (%d, %v)", len(atRecs), err)
	}
	if rep, err := DeepVerify(dir); err != nil || len(rep.Findings) != 0 {
		t.Fatalf("DeepVerify after migration = (%+v, %v)", rep, err)
	}

	// Lock vault: manifest/snapshot caches must purge and reads must fail closed.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if err := vault.Lock(dir, "index-vault-pass"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	if _, err := readManifestCached(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("readManifestCached when locked = %v, want ErrLocked", err)
	}
	if _, err := SearchDetailed(dir, query.Options{Query: canary, All: true}); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("SearchDetailed when locked = %v, want ErrLocked", err)
	}
	if _, err := ReadRecords(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("ReadRecords when locked = %v, want ErrLocked", err)
	}
	if _, err := ReadRecordsAt(dir, []int64{recs[0].Offset}); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("ReadRecordsAt when locked = %v, want ErrLocked", err)
	}
	if cmds := ReadCommands(dir); len(cmds) != 0 {
		t.Fatalf("ReadCommands when locked = %v, want empty", cmds)
	}
	if fss := FileSessions(dir, file1); len(fss) != 0 {
		t.Fatalf("FileSessions when locked = %v, want empty", fss)
	}
	if _, err := DeepVerify(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("DeepVerify when locked = %v, want ErrLocked", err)
	}
	if rdr := evidence.Open(dir); rdr != nil {
		t.Fatalf("evidence.Open when locked = %v, want nil", rdr)
	}

	// Unlock restores full access.
	if err := vault.Unlock(dir, "index-vault-pass"); err != nil {
		t.Fatalf("vault.Unlock: %v", err)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", "index-vault-pass")
	resAfterUnlock, err := SearchDetailed(dir, query.Options{Query: canary, All: true})
	if err != nil || len(resAfterUnlock.Sessions) != 2 {
		t.Fatalf("SearchDetailed after Unlock = (%d, %v), want 2", len(resAfterUnlock.Sessions), err)
	}
}
