package vault

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type memKeyring struct {
	name    string
	keys    map[string][]byte
	failSet bool
}

func (m *memKeyring) Name() string { return m.name }
func (m *memKeyring) Set(id string, dek []byte) error {
	if m.failSet {
		return errors.New("keyring unavailable")
	}
	m.keys[id] = append([]byte(nil), dek...)
	return nil
}
func (m *memKeyring) Get(id string) ([]byte, error) {
	v, ok := m.keys[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), v...), nil
}
func (m *memKeyring) Delete(id string) error {
	delete(m.keys, id)
	return nil
}

func withTestVaultEnv(t *testing.T) (dir string, kr *memKeyring) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("AppData", filepath.Join(tmp, "appdata"))
	t.Setenv("DEJA_VAULT_BACKEND", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmp, "data"))
	t.Setenv(EnvVaultFile, filepath.Join(tmp, "config", "deja", "vault.json"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmp, "data", "deja", "notes.jsonl"))
	t.Setenv(EnvVaultKey, "")
	t.Setenv(EnvVaultPassphrase, "")

	oldIter := KDFIterations
	KDFIterations = 1000
	t.Cleanup(func() { KDFIterations = oldIter })

	oldKR := activeKeyring
	kr = &memKeyring{name: "test-keyring", keys: map[string][]byte{}}
	activeKeyring = kr
	t.Cleanup(func() { activeKeyring = oldKR })

	return filepath.Join(tmp, "cache", "index.db"), kr
}

func TestVaultInitLockUnlockNoPlaintextOnDisk(t *testing.T) {
	indexDir, kr := withTestVaultEnv(t)
	if err := os.MkdirAll(filepath.Join(indexDir, "buckets"), 0o700); err != nil {
		t.Fatal(err)
	}
	canary := "TOP_SECRET_CANARY_TOKEN_987654"
	if err := os.WriteFile(filepath.Join(indexDir, "records.bin"), []byte("header "+canary), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexDir+".dejavu", []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(NotesPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	notesSeed := "\n" + `{"ts":"2026-11-17T00:00:00Z","project":"billing","text":"` + canary + `"}` + "\n" + NoteLinePrefix + "ZmFrZQ==\n"
	if err := os.WriteFile(NotesPath(), []byte(notesSeed), 0o600); err != nil {
		t.Fatal(err)
	}
	snapsSeed := "\nnot-json\n" + `{"t":"2026-11-17T00:00:00Z","kind":"hook","bytes":10,"terms":["` + canary + `"],"digest":"` + canary + `"}` + "\n"
	if err := os.WriteFile(indexDir+".injections.jsonl", []byte(snapsSeed), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(indexDir+".old.1", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir+".old.1", "stale.bin"), []byte(canary), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, initErr := Init(indexDir, "short", 0); initErr == nil {
		t.Fatal("Init accepted passphrase shorter than 8 chars")
	}
	backend, initErr := Init(indexDir, "correct-horse-passphrase", 0)
	if initErr != nil {
		t.Fatalf("Init: %v", initErr)
	}
	if backend != "test-keyring" {
		t.Fatalf("backend = %q, want test-keyring", backend)
	}
	if _, secondErr := Init(indexDir, "correct-horse-passphrase", 0); !errors.Is(secondErr, ErrAlreadyInitialized) {
		t.Fatalf("second Init = %v, want ErrAlreadyInitialized", secondErr)
	}

	// Notes and snapshots must already be sealed on disk right after Init.
	notesRaw, notesErr := os.ReadFile(NotesPath())
	if notesErr != nil || strings.Contains(string(notesRaw), canary) || !strings.HasPrefix(string(notesRaw), NoteLinePrefix) {
		t.Fatalf("notes not encrypted after Init: %q (%v)", notesRaw, notesErr)
	}
	snapsRaw, snapsErr := os.ReadFile(indexDir + ".injections.jsonl")
	if snapsErr != nil || strings.Contains(string(snapsRaw), canary) {
		t.Fatalf("snapshots leaked canary after Init: %q (%v)", snapsRaw, snapsErr)
	}

	dek, dekErr := CheckUnlocked(indexDir)
	if dekErr != nil || len(dek) != 32 {
		t.Fatalf("CheckUnlocked after Init = (%d, %v)", len(dek), dekErr)
	}
	firstNoteLine, _, _ := strings.Cut(string(notesRaw), "\n")
	plainNote, plainErr := OpenNoteLine(dek, firstNoteLine)
	if plainErr != nil || !strings.Contains(string(plainNote), canary) {
		t.Fatalf("OpenNoteLine failed: %q (%v)", plainNote, plainErr)
	}

	// Lock seals indexDir + sidecars into .vault, wipes plaintext files, evicts keyring.
	if lockErr := Lock(indexDir, ""); lockErr != nil {
		t.Fatalf("Lock: %v", lockErr)
	}
	if !IsLocked(indexDir) {
		t.Fatal("expected IsLocked true after Lock")
	}
	if _, chkErr := CheckUnlocked(indexDir); !errors.Is(chkErr, ErrLocked) {
		t.Fatalf("CheckUnlocked when locked = %v, want ErrLocked", chkErr)
	}
	if len(kr.keys) != 0 {
		t.Fatalf("keyring not evicted on Lock: %v", kr.keys)
	}
	if _, statErr := os.Stat(indexDir); !os.IsNotExist(statErr) {
		t.Fatalf("plaintext indexDir still exists after Lock")
	}
	if _, statErr := os.Stat(ArchivePath(indexDir)); statErr != nil {
		t.Fatalf("vault archive missing after Lock: %v", statErr)
	}
	walkBase := filepath.Dir(filepath.Dir(indexDir))
	walkRoot, openRootErr := os.OpenRoot(walkBase)
	if openRootErr != nil {
		t.Fatal(openRootErr)
	}
	rootWalkErr := filepath.WalkDir(walkBase, func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		relPath, relErr := filepath.Rel(walkBase, path)
		if relErr != nil {
			return relErr
		}
		b, _ := walkRoot.ReadFile(relPath)
		if strings.Contains(string(b), canary) {
			t.Errorf("canary plaintext found on disk at %s while locked", path)
		}
		return nil
	})
	_ = walkRoot.Close()
	if rootWalkErr != nil {
		t.Fatal(rootWalkErr)
	}

	if wrongErr := Unlock(indexDir, "wrong-passphrase"); wrongErr == nil {
		t.Fatal("Unlock succeeded with wrong passphrase")
	}
	if unlockErr := Unlock(indexDir, "correct-horse-passphrase"); unlockErr != nil {
		t.Fatalf("Unlock: %v", unlockErr)
	}
	if IsLocked(indexDir) {
		t.Fatal("expected IsLocked false after Unlock")
	}
	restored, restErr := os.ReadFile(filepath.Join(indexDir, "records.bin"))
	if restErr != nil || string(restored) != "header "+canary {
		t.Fatalf("restored records.bin = %q, err = %v", restored, restErr)
	}
	sidecar, sideErr := os.ReadFile(indexDir + ".dejavu")
	if sideErr != nil || string(sidecar) != canary {
		t.Fatalf("restored sidecar = %q, err = %v", sidecar, sideErr)
	}
}

func TestVaultAutoLockAndEnvKeyPaths(t *testing.T) {
	indexDir, kr := withTestVaultEnv(t)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "records.bin"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Init(indexDir, "another-passphrase", 1*time.Second); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Simulate keyring miss -> ErrKeyUnavailable, then recover via DEJA_VAULT_KEY and DEJA_VAULT_PASSPHRASE.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	rawDEK := kr.keys[cfg.KeyID]
	delete(kr.keys, cfg.KeyID)
	if _, err := CheckUnlocked(indexDir); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("missing keyring key = %v, want ErrKeyUnavailable", err)
	}
	t.Setenv(EnvVaultKey, "not-hex")
	if _, err := CheckUnlocked(indexDir); err == nil {
		t.Fatal("expected error for malformed DEJA_VAULT_KEY")
	}
	t.Setenv(EnvVaultKey, hex.EncodeToString(rawDEK))
	if got, err := CheckUnlocked(indexDir); err != nil || len(got) != 32 {
		t.Fatalf("CheckUnlocked via DEJA_VAULT_KEY = (%d, %v)", len(got), err)
	}
	t.Setenv(EnvVaultKey, "")
	t.Setenv(EnvVaultPassphrase, "another-passphrase")
	if got, err := CheckUnlocked(indexDir); err != nil || len(got) != 32 {
		t.Fatalf("CheckUnlocked via DEJA_VAULT_PASSPHRASE = (%d, %v)", len(got), err)
	}
	t.Setenv(EnvVaultPassphrase, "")

	// Reinstall key in keyring and age UnlockedAt past AutoLockSeconds.
	kr.keys[cfg.KeyID] = rawDEK
	cfg.UnlockedAt = time.Now().UTC().Add(-10 * time.Second).Format(time.RFC3339Nano)
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckUnlocked(indexDir); !errors.Is(err, ErrLocked) {
		t.Fatalf("idle timeout = %v, want ErrLocked", err)
	}
}

func TestVaultFailClosedAndArchiveTamper(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	// Corrupt vault.json must fail closed instead of silently running plaintext.
	if err := os.WriteFile(ConfigPath(), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckUnlocked(indexDir); err == nil {
		t.Fatal("corrupt vault.json did not fail closed")
	}
	_ = os.Remove(ConfigPath())

	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "records.bin"), make([]byte, archiveChunkSize+128), 0o600); err != nil {
		t.Fatal(err)
	}
	dek := make([]byte, 32)
	_, _ = rand.Read(dek)
	if err := SealTree(indexDir, dek); err != nil {
		t.Fatalf("SealTree multi-chunk: %v", err)
	}
	arch := ArchivePath(indexDir)
	orig, readErr := os.ReadFile(arch)
	if readErr != nil {
		t.Fatal(readErr)
	}
	// Truncating final chunk must fail authentication.
	if err := os.WriteFile(arch, orig[:len(orig)-10], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OpenTree(indexDir, dek); err == nil {
		t.Fatal("truncated vault archive opened without error")
	}
	// Bit-flipping ciphertext must fail authentication.
	tampered := append([]byte(nil), orig...)
	tampered[len(tampered)/2] ^= 0xff
	if err := os.WriteFile(arch, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OpenTree(indexDir, dek); err == nil {
		t.Fatal("tampered vault archive opened without error")
	}
	// Restore and verify multi-chunk OpenTree succeeds cleanly.
	if err := os.WriteFile(arch, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := OpenTree(indexDir, dek); err != nil {
		t.Fatalf("OpenTree valid multi-chunk: %v", err)
	}
	snapEnc, snapErr := SealSnapshot(dek, "hello world")
	if snapErr != nil {
		t.Fatal(snapErr)
	}
	if got, openErr := OpenSnapshot(dek, snapEnc); openErr != nil || got != "hello world" {
		t.Fatalf("OpenSnapshot = (%q, %v)", got, openErr)
	}
}

func TestVaultRealPBKDF2Smoke(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	// Restore production 600,000 PBKDF2 iterations on one init/unlock cycle.
	KDFIterations = 600_000
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(indexDir, "prod-kdf-passphrase", 0); err != nil {
		t.Fatalf("Init with production KDF: %v", err)
	}
	if err := Lock(indexDir, ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := Unlock(indexDir, "prod-kdf-passphrase"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
}

func TestVaultOSKeyringCommandsAndEdgeCases(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)

	// Restore execKeyringFn with a recording stub to cover OS switch arms without invoking OS GUI prompts.
	oldExec := execKeyringFn
	oldKR := activeKeyring
	t.Cleanup(func() {
		execKeyringFn = oldExec
		activeKeyring = oldKR
	})

	store := map[string]string{}
	execKeyringFn = func(bin string, args []string, stdin string, extraEnv []string) ([]byte, error) {
		switch bin {
		case "/usr/bin/security":
			if len(args) > 0 && args[0] == "add-generic-password" {
				store[args[5]] = args[7]
				return nil, nil
			}
			if len(args) > 0 && args[0] == "find-generic-password" {
				if v, ok := store[args[4]]; ok {
					return []byte(v + "\n"), nil
				}
				return nil, errors.New("not found")
			}
			delete(store, args[4])
			return nil, nil
		case "secret-tool":
			if len(args) > 0 && args[0] == "store" {
				store[args[5]] = strings.TrimSpace(stdin)
				return nil, nil
			}
			if len(args) > 0 && args[0] == "lookup" {
				if v, ok := store[args[4]]; ok {
					return []byte(v + "\n"), nil
				}
				return nil, errors.New("not found")
			}
			delete(store, args[4])
			return nil, nil
		case "powershell":
			var dpapiFile string
			for _, kv := range extraEnv {
				if v, ok := strings.CutPrefix(kv, "DEJA_DPAPI_PATH="); ok {
					dpapiFile = v
				}
			}
			if dpapiFile == "" {
				return nil, errors.New("missing DEJA_DPAPI_PATH")
			}
			if stdin != "" {
				if werr := os.WriteFile(dpapiFile, []byte(strings.TrimSpace(stdin)), 0o600); werr != nil {
					return nil, werr
				}
				return nil, nil
			}
			b, rerr := os.ReadFile(dpapiFile)
			if rerr != nil {
				return nil, rerr
			}
			return append(b, '\n'), nil
		default:
			return nil, nil
		}
	}

	okRing := osKeyring{}
	if okRing.Name() == "" {
		t.Fatal("empty osKeyring.Name()")
	}
	dek := make([]byte, 32)
	_, _ = rand.Read(dek)
	keyID := keyIDFor(dek)
	if setErr := okRing.Set("bad", dek); setErr == nil {
		t.Fatal("expected invalid keyID error on Set")
	}
	if _, getErr := okRing.Get("bad"); getErr == nil {
		t.Fatal("expected invalid keyID error on Get")
	}
	if setErr := okRing.Set(keyID, dek); setErr != nil {
		t.Fatalf("osKeyring.Set: %v", setErr)
	}
	got, getErr := okRing.Get(keyID)
	if getErr != nil || hex.EncodeToString(got) != hex.EncodeToString(dek) {
		t.Fatalf("osKeyring.Get = (%x, %v)", got, getErr)
	}
	if delErr := okRing.Delete(keyID); delErr != nil {
		t.Fatalf("osKeyring.Delete: %v", delErr)
	}
	if _, getAfterErr := okRing.Get(keyID); getAfterErr == nil {
		t.Fatal("expected error after Delete")
	}

	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, op := range []string{"set", "get", "delete"} {
			if _, _, _, _, cmdErr := keyringCommand(goos, op, keyID, hex.EncodeToString(dek)); cmdErr != nil {
				t.Fatalf("keyringCommand(%s, %s): %v", goos, op, cmdErr)
			}
		}
	}
	if _, _, _, _, planErr := keyringCommand("plan9", "get", keyID, ""); planErr == nil {
		t.Fatal("expected error on unsupported OS keyring")
	}

	// Headless fallback: Set fails during Init, yet Init succeeds with passphrase backend and
	// Lock with empty dir or passphrase fallback behaves cleanly.
	activeKeyring = &memKeyring{name: "test-kr", keys: map[string][]byte{}, failSet: true}
	backend, headlessErr := Init(indexDir, "headless-passphrase", 0)
	if headlessErr != nil || backend != "passphrase" {
		t.Fatalf("headless Init = (%q, %v)", backend, headlessErr)
	}
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, "records.bin"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Keyring missing and empty passphrase cannot seal plaintext store.
	if err := Lock(indexDir, ""); err == nil {
		t.Fatal("Lock without key or passphrase sealed plaintext store")
	}
	if err := Lock(indexDir, "headless-passphrase"); err != nil {
		t.Fatalf("headless Lock: %v", err)
	}
	if err := Unlock(indexDir, ""); err == nil {
		t.Fatal("Unlock with empty passphrase succeeded")
	}
	t.Setenv(EnvVaultPassphrase, "headless-passphrase")
	if err := Unlock(indexDir, ""); err != nil {
		t.Fatalf("Unlock via EnvVaultPassphrase: %v", err)
	}
	t.Setenv(EnvVaultPassphrase, "")

	// Path derivation (overrides, XDG, and invalid relative HOME).
	t.Setenv(EnvVaultFile, "")
	if !strings.HasSuffix(ConfigPath(), filepath.Join("deja", "vault.json")) {
		t.Fatalf("ConfigPath = %q", ConfigPath())
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if !strings.HasSuffix(ConfigPath(), filepath.Join("deja", "vault.json")) {
		t.Fatalf("ConfigPath default = %q", ConfigPath())
	}
	t.Setenv("DEJA_NOTES_FILE", "")
	if !strings.HasSuffix(NotesPath(), filepath.Join("deja", "notes.jsonl")) {
		t.Fatalf("NotesPath XDG = %q", NotesPath())
	}
	t.Setenv("XDG_DATA_HOME", "")
	if !strings.HasSuffix(NotesPath(), filepath.Join("deja", "notes.jsonl")) {
		t.Fatalf("NotesPath default = %q", NotesPath())
	}

	// Archive / chunk / tar / keyring error paths.
	if err := SealTree(filepath.Join(t.TempDir(), "absent"), dek); err != nil {
		t.Fatalf("SealTree on absent dir = %v", err)
	}
	if err := OpenTree(indexDir, dek); err == nil {
		t.Fatal("OpenTree on missing archive succeeded")
	}
	if _, err := OpenLine(dek, notesDomain, "not-prefixed"); err == nil {
		t.Fatal("OpenLine on unprefixed string succeeded")
	}
	if _, err := OpenLine(dek, notesDomain, NoteLinePrefix+"!badb64!"); err == nil {
		t.Fatal("OpenLine on bad base64 succeeded")
	}
	if _, err := OpenSnapshot(dek, NoteLinePrefix+"!badb64!"); err == nil {
		t.Fatal("OpenSnapshot on bad base64 succeeded")
	}
	if err := okRing.Delete("bad"); err != nil {
		t.Fatalf("Delete invalid keyID = %v", err)
	}
	if runtime.GOOS == "windows" {
		winPath, perr := dpapiPath(keyID)
		if perr != nil {
			t.Fatal(perr)
		}
		if mkErr := os.MkdirAll(filepath.Join(winPath, "sub"), 0o700); mkErr != nil {
			t.Fatal(mkErr)
		}
		if delErr := okRing.Delete(keyID); delErr == nil {
			t.Fatal("expected osKeyring.Delete error")
		}
		_ = os.RemoveAll(winPath)
	} else {
		execKeyringFn = func(string, []string, string, []string) ([]byte, error) {
			return nil, errors.New("delete failed")
		}
		if delErr := okRing.Delete(keyID); delErr == nil {
			t.Fatal("expected osKeyring.Delete error")
		}
	}
	execKeyringFn = oldExec
	if _, execErr := defaultExecKeyring("nonexistent-deja-keyring-bin", []string{"--check"}, "stdin-line\n", []string{"DEJA_TEST=1"}); execErr == nil {
		t.Fatal("expected error from missing keyring binary")
	}
	execKeyringFn = func(string, []string, string, []string) ([]byte, error) {
		return []byte("not-hex"), errors.New("boom")
	}
	if setErr := okRing.Set(keyID, dek); setErr == nil {
		t.Fatal("expected osKeyring.Set error")
	}
	execKeyringFn = func(string, []string, string, []string) ([]byte, error) {
		return []byte("not-hex"), nil
	}
	if _, getErr2 := okRing.Get(keyID); getErr2 == nil {
		t.Fatal("expected osKeyring.Get decode error")
	}
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	if setErr := okRing.Set(keyID, dek); setErr == nil {
		t.Fatal("expected Set error when passphrase backend forced")
	}
	if _, getErr3 := okRing.Get(keyID); getErr3 == nil {
		t.Fatal("expected Get error when passphrase backend forced")
	}
	if delErr := okRing.Delete(keyID); delErr != nil {
		t.Fatalf("expected nil Delete when passphrase backend forced: %v", delErr)
	}
	t.Setenv("DEJA_VAULT_BACKEND", "")

	// chunkReader & OpenTree corruption checks.
	if _, chunkErr := newChunkReader(bytes.NewReader([]byte("BAD112345678")), dek); chunkErr == nil {
		t.Fatal("expected magic error")
	}
	var rawBuf bytes.Buffer
	cw, cwErr := newChunkWriter(&rawBuf, dek)
	if cwErr != nil {
		t.Fatal(cwErr)
	}
	var badTar bytes.Buffer
	tw := tar.NewWriter(&badTar)
	if hdrErr := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "../escape", Size: 1}); hdrErr != nil {
		t.Fatal(hdrErr)
	}
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	_, _ = cw.Write(badTar.Bytes())
	_ = cw.Close()
	archPath := ArchivePath(indexDir)
	if writeErr := os.WriteFile(archPath, rawBuf.Bytes(), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if openErr := OpenTree(indexDir, dek); openErr == nil {
		t.Fatal("expected unsafe path error in OpenTree")
	}
	var badChunk bytes.Buffer
	badChunk.Write(rawBuf.Bytes()[:12])
	var hdr [5]byte
	binary.BigEndian.PutUint32(hdr[:4], 0)
	hdr[4] = 0
	badChunk.Write(hdr[:])
	crBad, badErr := newChunkReader(&badChunk, dek)
	if badErr != nil {
		t.Fatal(badErr)
	}
	if _, readBadErr := crBad.Read(make([]byte, 8)); readBadErr == nil {
		t.Fatal("expected chunk length error")
	}
	// Trailing bytes past final frame must fail VerifyEOF.
	trailing := append(append([]byte(nil), rawBuf.Bytes()...), byte(0xff))
	crTrail, trailErr := newChunkReader(bytes.NewReader(trailing), dek)
	if trailErr != nil {
		t.Fatal(trailErr)
	}
	twDrop := make([]byte, 4096)
	for {
		_, rerr := crTrail.Read(twDrop)
		if rerr != nil {
			break
		}
	}
	if err := crTrail.VerifyEOF(); err == nil {
		t.Fatal("expected trailing bytes error")
	}

	// Config / KDF / AEAD error paths.
	if got, err := deriveKEK("pass-word-1", []byte("salt"), 0); err != nil || len(got) != 32 {
		t.Fatalf("deriveKEK(0) = (%d, %v)", len(got), err)
	}
	if _, err := unwrapDEK(Config{Salt: "!!!", WrappedDEK: "AA==", KeyID: keyID}, "pass"); err == nil {
		t.Fatal("expected salt decode error")
	}
	if _, err := unwrapDEK(Config{Salt: "AA==", WrappedDEK: "!!!", KeyID: keyID}, "pass"); err == nil {
		t.Fatal("expected wrapped DEK decode error")
	}
	if _, err := openAEAD(dek, nil, []byte("short")); err == nil {
		t.Fatal("short ciphertext opened")
	}

	incFile := filepath.Join(t.TempDir(), "vault.json")
	t.Setenv(EnvVaultFile, incFile)
	if err := os.WriteFile(incFile, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
		t.Fatal("incomplete vault.json did not fail")
	}
	t.Setenv(EnvVaultFile, filepath.Join(t.TempDir(), "missing.json"))
	if Enabled() || IsLocked(indexDir) {
		t.Fatal("missing config should report Enabled=false, IsLocked=false")
	}
	if _, err := LoadConfig(); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("LoadConfig on missing = %v", err)
	}
	if err := Lock(indexDir, ""); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("Lock uninitialized = %v", err)
	}
	if err := Unlock(indexDir, "x"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("Unlock uninitialized = %v", err)
	}
	t.Setenv(EnvVaultFile, "")
	t.Setenv("AppData", "")
	t.Setenv("HOME", "")
	_ = homeDir()
	t.Setenv("HOME", "relative-home-not-abs")
	if got := ConfigPath(); got != "" {
		t.Fatalf("ConfigPath with relative HOME = %q", got)
	}
	if _, err := LoadConfig(); err == nil {
		t.Fatal("expected LoadConfig error with empty ConfigPath")
	}
	if err := saveConfig(Config{}); err == nil {
		t.Fatal("expected saveConfig error with empty ConfigPath")
	}

	// Additional archive / idle / AEAD / keyring branches for >=85% coverage.
	tmpRoot := t.TempDir()
	idxBlocked := filepath.Join(tmpRoot, "idx")
	if err := os.MkdirAll(idxBlocked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(idxBlocked, "a.bin"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Make ArchivePath(idxBlocked)+".tmp" a directory so os.OpenFile fails inside SealTree.
	if err := os.MkdirAll(ArchivePath(idxBlocked)+".tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SealTree(idxBlocked, dek); err == nil {
		t.Fatal("expected SealTree OpenFile error when .tmp is directory")
	}
	_ = os.RemoveAll(ArchivePath(idxBlocked) + ".tmp")
	// Make ArchivePath(idxBlocked) a directory so os.Rename fails inside SealTree.
	if err := os.MkdirAll(ArchivePath(idxBlocked), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SealTree(idxBlocked, dek); err == nil {
		t.Fatal("expected SealTree Rename error when archive target is directory")
	}
	_ = os.RemoveAll(ArchivePath(idxBlocked))

	if runtime.GOOS != "windows" {
		_ = os.Symlink(filepath.Join(tmpRoot, "nonexistent"), filepath.Join(idxBlocked, "sym"))
	}
	if err := SealTree(idxBlocked, dek); err != nil {
		t.Fatalf("SealTree with symlink skipped: %v", err)
	}
	if _, err := sealAEAD([]byte("bad"), nil, []byte("x")); err == nil {
		t.Fatal("expected sealAEAD key length error")
	}
	if _, err := openAEAD([]byte("bad"), nil, make([]byte, 32)); err == nil {
		t.Fatal("expected openAEAD key length error")
	}
	if !idleExpired("", Config{AutoLockSeconds: 10}) {
		t.Fatal("expected empty UnlockedAt to report expired")
	}
	idxUsage := filepath.Join(tmpRoot, "u", "index.db")
	_ = os.MkdirAll(filepath.Dir(idxUsage), 0o700)
	_ = os.WriteFile(idxUsage+".usage.jsonl", []byte("{}\n"), 0o600)
	if idleExpired(idxUsage, Config{AutoLockSeconds: 60, UnlockedAt: time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano)}) {
		t.Fatal("expected recent .usage.jsonl modtime to refresh idle timer")
	}
	// Unsupported tar entry type & invalid chunk frame flag.
	var symTar bytes.Buffer
	stw := tar.NewWriter(&symTar)
	_ = stw.WriteHeader(&tar.Header{Typeflag: tar.TypeSymlink, Name: "s", Linkname: "target"})
	_ = stw.Close()
	var symEnc bytes.Buffer
	scw, _ := newChunkWriter(&symEnc, dek)
	_, _ = scw.Write(symTar.Bytes())
	_ = scw.Close()
	if werr := os.WriteFile(ArchivePath(idxBlocked), symEnc.Bytes(), 0o600); werr == nil {
		if openErr := OpenTree(idxBlocked, dek); openErr == nil {
			t.Fatal("expected OpenTree unsupported tar entry error")
		}
	}
	var badFlag bytes.Buffer
	badFlag.Write(rawBuf.Bytes()[:12])
	var fhdr [5]byte
	binary.BigEndian.PutUint32(fhdr[:4], 16)
	fhdr[4] = 9
	badFlag.Write(fhdr[:])
	badFlag.Write(make([]byte, 16))
	crFlag, _ := newChunkReader(&badFlag, dek)
	if _, err := crFlag.Read(make([]byte, 8)); err == nil {
		t.Fatal("expected corrupt frame flag error")
	}
	// Truncated tar header inside valid sealed chunk hits tr.Next() error inside OpenTree.
	var truncTarEnc bytes.Buffer
	tcw, _ := newChunkWriter(&truncTarEnc, dek)
	_, _ = tcw.Write([]byte("not-a-512-byte-tar-header"))
	_ = tcw.Close()
	if err := os.WriteFile(ArchivePath(idxBlocked), truncTarEnc.Bytes(), 0o600); err == nil {
		if err := OpenTree(idxBlocked, dek); err == nil {
			t.Fatal("expected OpenTree corrupt tar error")
		}
	}
	// Unreadable file inside store triggers SealTree writeTreeToTar error cleanup.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		unreadable := filepath.Join(idxBlocked, "unreadable.bin")
		_ = os.MkdirAll(idxBlocked, 0o700)
		_ = os.WriteFile(unreadable, []byte("secret"), 0o000)
		t.Cleanup(func() { _ = os.Chmod(unreadable, 0o600) })
		if err := SealTree(idxBlocked, dek); err == nil {
			t.Fatal("expected SealTree error on unreadable file")
		}
		_ = os.Chmod(unreadable, 0o600)
	}

	// Verify idempotent chunkWriter.Close, failing writer, Init cleanup when saveConfig/notes/snapshots fail.
	_ = cw.Close()
	if _, err := newChunkWriter(errFailWriter{}, dek); err == nil {
		t.Fatal("expected newChunkWriter header write failure")
	}
	var noFinal bytes.Buffer
	cfw, _ := newChunkWriter(&noFinal, dek)
	_, _ = cfw.Write([]byte("half"))
	_ = cfw.flushChunk(false)
	crNoFinal, _ := newChunkReader(&noFinal, dek)
	if err := crNoFinal.VerifyEOF(); err == nil {
		t.Fatal("expected VerifyEOF error when final frame missing")
	}

	t.Setenv("HOME", tmpRoot)
	t.Setenv("USERPROFILE", tmpRoot)
	t.Setenv("AppData", filepath.Join(tmpRoot, "appdata"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpRoot, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmpRoot, "dat"))
	// Make vault config parent a regular file so saveConfig fails inside Init and cleans keyring.
	regFile := filepath.Join(tmpRoot, "not-a-dir")
	_ = os.WriteFile(regFile, []byte("x"), 0o600)
	t.Setenv(EnvVaultFile, filepath.Join(regFile, "vault.json"))
	activeKeyring = &memKeyring{name: "test-kr", keys: map[string][]byte{}}
	if _, err := Init(idxBlocked, "pass-word-123", 0); err == nil {
		t.Fatal("expected Init failure on uncreatable config dir")
	}
	// Make NotesPath a directory so encryptExistingNotes fails inside Init.
	notesDir := filepath.Join(tmpRoot, "notes-dir")
	_ = os.MkdirAll(notesDir, 0o700)
	t.Setenv(EnvVaultFile, filepath.Join(tmpRoot, "v-notes-err.json"))
	t.Setenv("DEJA_NOTES_FILE", notesDir)
	if _, err := Init(idxBlocked, "pass-word-123", 0); err == nil {
		t.Fatal("expected Init failure when NotesPath is directory")
	}
	// Make snapshots sidecar a directory so encryptExistingSnapshots fails inside Init.
	t.Setenv(EnvVaultFile, filepath.Join(tmpRoot, "v-snaps-err.json"))
	t.Setenv("DEJA_NOTES_FILE", filepath.Join(tmpRoot, "notes-ok.jsonl"))
	idxSnapDir := filepath.Join(tmpRoot, "snap-idx")
	_ = os.MkdirAll(idxSnapDir+".injections.jsonl", 0o700)
	if _, err := Init(idxSnapDir, "pass-word-123", 0); err == nil {
		t.Fatal("expected Init failure when snapshots file is directory")
	}
}

type errFailWriter struct{}

func (errFailWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
