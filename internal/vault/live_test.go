package vault

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

type countingKeyring struct {
	memKeyring
	gets atomic.Int32
}

func (c *countingKeyring) Get(id string) ([]byte, error) {
	c.gets.Add(1)
	return c.memKeyring.Get(id)
}

func TestLiveWholeFileAEADAndHelpers(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}

	dek := make([]byte, 32)
	_, _ = rand.Read(dek)
	wrongDEK := make([]byte, 32)
	_, _ = rand.Read(wrongDEK)

	plain := []byte("LIVE_CANARY_PAYLOAD_ALPHA_42")
	sealed1, err := SealBytes(dek, DomainIndexFile, plain)
	if err != nil {
		t.Fatalf("SealBytes: %v", err)
	}
	sealed2, err := SealBytes(dek, DomainIndexFile, plain)
	if err != nil {
		t.Fatalf("SealBytes 2: %v", err)
	}
	if !IsSealedBytes(sealed1) {
		t.Fatal("IsSealedBytes false for sealed payload")
	}
	if IsSealedBytes(plain) {
		t.Fatal("IsSealedBytes true for plaintext")
	}
	if !IsSealedBytes([]byte("DJVF1")) {
		t.Fatal("IsSealedBytes should flag truncated DJVF1 prefix so readers fail closed")
	}
	if bytes.Equal(sealed1, sealed2) {
		t.Fatal("SealBytes produced identical ciphertexts (reused nonce)")
	}
	if bytes.Contains(sealed1, plain) {
		t.Fatal("sealed payload leaked plaintext")
	}
	got, err := OpenBytes(dek, DomainIndexFile, sealed1)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("OpenBytes = (%q, %v), want %q", got, err, plain)
	}
	if _, oErr := OpenBytes(wrongDEK, DomainIndexFile, sealed1); oErr == nil {
		t.Fatal("OpenBytes accepted wrong DEK")
	}
	if _, oErr := OpenBytes(dek, DomainSidecar, sealed1); oErr == nil {
		t.Fatal("OpenBytes accepted wrong domain AAD")
	}
	tampered := append([]byte(nil), sealed1...)
	tampered[len(tampered)-1] ^= 0xff
	if _, oErr := OpenBytes(dek, DomainIndexFile, tampered); oErr == nil {
		t.Fatal("OpenBytes accepted tampered ciphertext")
	}
	if _, oErr := OpenBytes(dek, DomainIndexFile, []byte("DJVF1short")); oErr == nil {
		t.Fatal("OpenBytes accepted truncated DJVF1 frame")
	}
	if _, sErr := SealBytes([]byte("short"), DomainIndexFile, plain); sErr == nil {
		t.Fatal("SealBytes accepted short DEK")
	}

	// Unencrypted store writes and reads plaintext via helpers.
	plainPath := indexDir + ".dejavu"
	if wErr := WriteSealedFile(indexDir, plainPath, DomainSidecar, plain, 0o600); wErr != nil {
		t.Fatalf("WriteSealedFile unencrypted: %v", wErr)
	}
	rawPlain, err := os.ReadFile(plainPath)
	if err != nil || !bytes.Equal(rawPlain, plain) {
		t.Fatalf("unencrypted raw = (%q, %v)", rawPlain, err)
	}
	gotPlain, err := ReadSealedFile(indexDir, plainPath, DomainSidecar)
	if err != nil || !bytes.Equal(gotPlain, plain) {
		t.Fatalf("ReadSealedFile unencrypted = (%q, %v)", gotPlain, err)
	}

	// Encrypted store seals on disk while unlocked and fails closed while locked.
	if _, iErr := Init(indexDir, "live-passphrase-1", 0); iErr != nil {
		t.Fatalf("Init: %v", iErr)
	}
	livePath := indexDir + ".live"
	if wErr := WriteSealedFile(indexDir, livePath, DomainSidecar, plain, 0o600); wErr != nil {
		t.Fatalf("WriteSealedFile encrypted: %v", wErr)
	}
	diskBytes, err := os.ReadFile(livePath)
	if err != nil || !IsSealedBytes(diskBytes) || bytes.Contains(diskBytes, plain) {
		t.Fatalf("disk sidecar not sealed: %q (%v)", diskBytes, err)
	}
	gotDec, err := ReadSealedFile(indexDir, livePath, DomainSidecar)
	if err != nil || !bytes.Equal(gotDec, plain) {
		t.Fatalf("ReadSealedFile encrypted = (%q, %v)", gotDec, err)
	}

	// Truncated DJVF1 file on disk fails loudly even when unlocked.
	truncPath := indexDir + ".trunc"
	if wErr := os.WriteFile(truncPath, []byte("DJVF1bad"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, rErr := ReadSealedFile(indexDir, truncPath, DomainSidecar); rErr == nil {
		t.Fatal("expected error reading truncated DJVF1 file")
	}

	// Locked store fails closed on both ReadSealedFile and WriteSealedFile.
	if lErr := Lock(indexDir, ""); lErr != nil {
		t.Fatalf("Lock: %v", lErr)
	}
	if _, rErr := ReadSealedFile(indexDir, livePath, DomainSidecar); !errors.Is(rErr, ErrLocked) {
		t.Fatalf("ReadSealedFile while locked = %v, want ErrLocked", rErr)
	}
	if wErr := WriteSealedFile(indexDir, livePath, DomainSidecar, plain, 0o600); !errors.Is(wErr, ErrLocked) {
		t.Fatalf("WriteSealedFile while locked = %v, want ErrLocked", wErr)
	}
}

func TestLiveStreamAEAD(t *testing.T) {
	dek := make([]byte, 32)
	_, _ = rand.Read(dek)
	wrong := make([]byte, 32)
	_, _ = rand.Read(wrong)

	payload := bytes.Repeat([]byte("EMBED_VECTOR_CANARY_9876\n"), 5000)
	var buf bytes.Buffer
	sw, err := NewStreamWriter(&buf, dek, DomainEmbed)
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	if _, wErr := sw.Write(payload); wErr != nil {
		t.Fatalf("StreamWriter.Write: %v", wErr)
	}
	if cErr := sw.Close(); cErr != nil {
		t.Fatalf("StreamWriter.Close: %v", cErr)
	}
	sealed := buf.Bytes()
	if !IsStreamHeader(sealed) {
		t.Fatal("IsStreamHeader false for sealed stream")
	}
	if IsStreamHeader(payload) {
		t.Fatal("IsStreamHeader true for plaintext")
	}
	if bytes.Contains(sealed, []byte("EMBED_VECTOR_CANARY_9876")) {
		t.Fatal("stream leaked plaintext canary")
	}

	sr, err := NewStreamReader(bytes.NewReader(sealed), dek, DomainEmbed)
	if err != nil {
		t.Fatalf("NewStreamReader: %v", err)
	}
	got, err := io.ReadAll(sr)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("StreamReader roundtrip mismatch: err=%v, len=%d want %d", err, len(got), len(payload))
	}

	if _, err := NewStreamWriter(&buf, []byte("short"), DomainEmbed); err == nil {
		t.Fatal("expected error with short DEK")
	}
	if wrongR, err := NewStreamReader(bytes.NewReader(sealed), wrong, DomainEmbed); err == nil {
		if _, err := io.ReadAll(wrongR); err == nil {
			t.Fatal("stream decrypted with wrong DEK")
		}
	}
	if wrongDom, err := NewStreamReader(bytes.NewReader(sealed), dek, DomainIndexFile); err == nil {
		if _, err := io.ReadAll(wrongDom); err == nil {
			t.Fatal("stream decrypted with wrong domain")
		}
	}
	if truncR, err := NewStreamReader(bytes.NewReader(sealed[:len(sealed)-5]), dek, DomainEmbed); err == nil {
		if _, err := io.ReadAll(truncR); err == nil {
			t.Fatal("truncated stream reported EOF cleanly")
		}
	}
	extra := append(append([]byte(nil), sealed...), 0x01)
	if extraR, err := NewStreamReader(bytes.NewReader(extra), dek, DomainEmbed); err == nil {
		if _, err := io.ReadAll(extraR); err == nil {
			t.Fatal("stream with trailing garbage reported EOF cleanly")
		}
	}
}

func TestLiveRecordCipherAndSidecarLines(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if RecordOverhead != 28 {
		t.Fatalf("RecordOverhead = %d, want 28", RecordOverhead)
	}
	if rc, ok, err := ActiveRecordCipher(indexDir); err != nil || ok || rc != nil {
		t.Fatalf("disabled ActiveRecordCipher = (%v, %v, %v), want (nil, false, nil)", rc, ok, err)
	}
	line, err := SealSidecarLine(indexDir, DomainSidecar, []byte("plain line"))
	if err != nil || line != "plain line" {
		t.Fatalf("disabled SealSidecarLine = (%q, %v)", line, err)
	}
	gotLine, wasSealed, err := OpenSidecarLine(indexDir, DomainSidecar, line)
	if err != nil || wasSealed || string(gotLine) != "plain line" {
		t.Fatalf("disabled OpenSidecarLine = (%q, %v, %v)", gotLine, wasSealed, err)
	}

	if _, iErr := Init(indexDir, "record-cipher-pass", 0); iErr != nil {
		t.Fatalf("Init: %v", iErr)
	}
	rc, ok, err := ActiveRecordCipher(indexDir)
	if err != nil || !ok || rc == nil {
		t.Fatalf("ActiveRecordCipher unlocked = (%v, %v, %v)", rc, ok, err)
	}
	recBody := []byte("RECORD_CANARY_SECRET_77")
	sealed, err := rc.Seal(1, recBody)
	if err != nil {
		t.Fatalf("RecordCipher.Seal: %v", err)
	}
	if len(sealed) != len(recBody)+RecordOverhead {
		t.Fatalf("sealed len = %d, want %d", len(sealed), len(recBody)+RecordOverhead)
	}
	if bytes.Contains(sealed, recBody) {
		t.Fatal("sealed record contains plaintext")
	}
	opened, err := rc.Open(1, sealed)
	if err != nil || !bytes.Equal(opened, recBody) {
		t.Fatalf("RecordCipher.Open = (%q, %v)", opened, err)
	}
	if _, oErr := rc.Open(0, sealed); oErr == nil {
		t.Fatal("RecordCipher.Open accepted wrong baseFlag AAD")
	}
	if _, oErr := rc.Open(1, []byte("too-short")); oErr == nil {
		t.Fatal("RecordCipher.Open accepted short slice")
	}

	encLine, err := SealSidecarLine(indexDir, DomainSidecar, []byte(`{"id":"s1","k":"SECRET"}`))
	if err != nil || !strings.HasPrefix(encLine, NoteLinePrefix) || strings.Contains(encLine, "SECRET") {
		t.Fatalf("SealSidecarLine = (%q, %v)", encLine, err)
	}
	decLine, wasSealed, err := OpenSidecarLine(indexDir, DomainSidecar, encLine)
	if err != nil || !wasSealed || string(decLine) != `{"id":"s1","k":"SECRET"}` {
		t.Fatalf("OpenSidecarLine = (%q, %v, %v)", decLine, wasSealed, err)
	}

	if lErr := Lock(indexDir, ""); lErr != nil {
		t.Fatalf("Lock: %v", lErr)
	}
	if _, _, rErr := ActiveRecordCipher(indexDir); !errors.Is(rErr, ErrLocked) {
		t.Fatalf("locked ActiveRecordCipher = %v, want ErrLocked", rErr)
	}
	if _, sErr := SealSidecarLine(indexDir, DomainSidecar, []byte("x")); !errors.Is(sErr, ErrLocked) {
		t.Fatalf("locked SealSidecarLine = %v, want ErrLocked", sErr)
	}
	if _, _, oErr := OpenSidecarLine(indexDir, DomainSidecar, encLine); !errors.Is(oErr, ErrLocked) {
		t.Fatalf("locked OpenSidecarLine = %v, want ErrLocked", oErr)
	}
}

func TestLiveDEKCacheAndInvalidation(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	ck := &countingKeyring{memKeyring: memKeyring{name: "counting-kr", keys: map[string][]byte{}}}
	activeKeyring = ck
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(indexDir, "cache-passphrase-123", 0); err != nil {
		t.Fatalf("Init: %v", err)
	}
	ck.gets.Store(0)
	for range 50 {
		dek, err := CheckUnlocked(indexDir)
		if err != nil || len(dek) != 32 {
			t.Fatalf("CheckUnlocked = (%d, %v)", len(dek), err)
		}
		clear(dek)
	}
	if got := ck.gets.Load(); got != 1 {
		t.Fatalf("keyring Get calls across 50 checks = %d, want 1", got)
	}
	// Changing DEJA_VAULT_KEY must invalidate or bypass cache cleanly.
	cfg, _ := LoadConfig()
	rawKey := ck.keys[cfg.KeyID]
	t.Setenv(EnvVaultKey, hex.EncodeToString(rawKey))
	if dek, err := CheckUnlocked(indexDir); err != nil || len(dek) != 32 {
		t.Fatalf("CheckUnlocked with EnvVaultKey: %v", err)
	}
	t.Setenv(EnvVaultKey, "00")
	if _, err := CheckUnlocked(indexDir); err == nil {
		t.Fatal("expected invalid EnvVaultKey error after cached hit")
	}
	t.Setenv(EnvVaultKey, "")

	// Lock must evict cache immediately.
	if err := Lock(indexDir, ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := CheckUnlocked(indexDir); !errors.Is(err, ErrLocked) {
		t.Fatalf("CheckUnlocked after Lock = %v, want ErrLocked", err)
	}
	if err := Unlock(indexDir, "cache-passphrase-123"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	ck.gets.Store(0)
	for range 10 {
		if _, err := CheckUnlocked(indexDir); err != nil {
			t.Fatalf("CheckUnlocked after Unlock: %v", err)
		}
	}
	if got := ck.gets.Load(); got != 1 {
		t.Fatalf("keyring Get calls after Unlock = %d, want 1", got)
	}
}

func TestInitAndUnlockMigratorsAndPromptCacheAndTUIHistory(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	canary := "MIGRATOR_SECRET_CANARY_314"
	tuiHistoryPath := indexDir + ".tui-history"
	if err := os.WriteFile(tuiHistoryPath, []byte(`["`+canary+`"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	hookSeenPath := indexDir + ".hookseen"
	if err := os.WriteFile(hookSeenPath, []byte(`{"id":"s1","msg":"`+canary+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forksPath := indexDir + ".forks"
	if err := os.WriteFile(forksPath, []byte("s1 fork-"+canary+" 123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pcDir := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "deja-vu")
	if err := os.MkdirAll(pcDir, 0o700); err != nil {
		t.Fatal(err)
	}
	prePrompt := filepath.Join(pcDir, "opencode.json")
	if err := os.WriteFile(prePrompt, []byte(`{"sessions":{"s1":{"digest":"`+canary+`"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var migrated atomic.Int32
	RegisterMigrator("test-store-mig", func(dir string, dek []byte) error {
		if dir != indexDir || len(dek) != 32 {
			t.Fatalf("migrator args = (%q, %d)", dir, len(dek))
		}
		migrated.Add(1)
		return nil
	})
	t.Cleanup(func() { unregisterMigratorForTest("test-store-mig") })

	if _, err := Init(indexDir, "migrate-passphrase", 0); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if migrated.Load() != 1 {
		t.Fatalf("Init ran migrator %d times, want 1", migrated.Load())
	}
	if _, err := os.Stat(prePrompt); !os.IsNotExist(err) {
		t.Fatalf("pre-Init prompt cache %s still exists (err=%v)", prePrompt, err)
	}
	rawTUI, err := os.ReadFile(tuiHistoryPath)
	if err != nil || !IsSealedBytes(rawTUI) || strings.Contains(string(rawTUI), canary) {
		t.Fatalf(".tui-history not sealed after Init: %q (%v)", rawTUI, err)
	}
	rawHookSeen, err := os.ReadFile(hookSeenPath)
	if err != nil || !strings.HasPrefix(string(rawHookSeen), NoteLinePrefix) || strings.Contains(string(rawHookSeen), canary) {
		t.Fatalf(".hookseen not sealed after Init: %q (%v)", rawHookSeen, err)
	}
	rawForks, err := os.ReadFile(forksPath)
	if err != nil || !strings.HasPrefix(string(rawForks), NoteLinePrefix) || strings.Contains(string(rawForks), canary) {
		t.Fatalf(".forks not sealed after Init: %q (%v)", rawForks, err)
	}
	gotTUI, err := ReadSealedFile(indexDir, tuiHistoryPath, DomainSidecar)
	if err != nil || string(gotTUI) != `["`+canary+`"]` {
		t.Fatalf("ReadSealedFile(.tui-history) = (%q, %v)", gotTUI, err)
	}

	if lErr := Lock(indexDir, ""); lErr != nil {
		t.Fatalf("Lock: %v", lErr)
	}
	if _, sErr := os.Stat(tuiHistoryPath); !os.IsNotExist(sErr) {
		t.Fatalf(".tui-history survived Lock (err=%v)", sErr)
	}
	if uErr := Unlock(indexDir, "migrate-passphrase"); uErr != nil {
		t.Fatalf("Unlock: %v", uErr)
	}
	if migrated.Load() != 2 {
		t.Fatalf("Unlock ran migrator %d times, want 2", migrated.Load())
	}
	gotTUI2, err := ReadSealedFile(indexDir, tuiHistoryPath, DomainSidecar)
	if err != nil || string(gotTUI2) != `["`+canary+`"]` {
		t.Fatalf(".tui-history after Unlock = (%q, %v)", gotTUI2, err)
	}
}

func TestDBusFallbackAndStatus(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	sockDir := t.TempDir()
	busSock := filepath.Join(sockDir, "bus")
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", busSock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer func() { _ = ln.Close() }()

	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	t.Setenv("XDG_RUNTIME_DIR", sockDir)

	_, _, _, extraEnv, err := keyringCommand("linux", "get", "0123456789abcdef", "")
	if err != nil {
		t.Fatalf("keyringCommand linux: %v", err)
	}
	wantBus := "DBUS_SESSION_BUS_ADDRESS=unix:path=" + busSock
	if !slices.Contains(extraEnv, wantBus) {
		t.Fatalf("linux keyringCommand extraEnv = %v, want %q", extraEnv, wantBus)
	}

	st0 := Status(indexDir)
	if st0.Enabled || st0.Locked {
		t.Fatalf("uninitialized Status = %+v", st0)
	}
	if mErr := os.MkdirAll(indexDir, 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if _, iErr := Init(indexDir, "status-passphrase", 300*1e9); iErr != nil {
		t.Fatalf("Init: %v", iErr)
	}
	cfg, cErr := LoadConfig()
	if cErr != nil {
		t.Fatalf("LoadConfig: %v", cErr)
	}
	if _, uErr := unwrapDEKCached(cfg, "status-passphrase"); uErr != nil {
		t.Fatalf("unwrapDEKCached valid: %v", uErr)
	}
	if _, badErr := unwrapDEKCached(cfg, "wrong-passphrase"); badErr == nil {
		t.Fatal("unwrapDEKCached accepted wrong passphrase after cache fill")
	}
	st1 := Status(indexDir)
	if !st1.Enabled || st1.Locked || st1.AutoLockSeconds != 300 || st1.KeyBackend == "" {
		t.Fatalf("unlocked Status = %+v", st1)
	}
	if err := Lock(indexDir, ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	st2 := Status(indexDir)
	if !st2.Enabled || !st2.Locked {
		t.Fatalf("locked Status = %+v", st2)
	}
}

func TestKeyringHelpersAndOSBackend(t *testing.T) {
	restoreKR := SetKeyringForTest(&memKeyring{name: "swap-kr", keys: map[string][]byte{}})
	if !KeyringAvailable() {
		t.Fatal("KeyringAvailable with swapped test keyring = false, want true")
	}
	restoreKR()
	if got := keyringIdentity(nil); got != "" {
		t.Fatalf("keyringIdentity(nil) = %q, want empty", got)
	}
	if got := keyringIdentity(osKeyring{}); !strings.Contains(got, osKeyring{}.Name()) {
		t.Fatalf("keyringIdentity(osKeyring{}) = %q", got)
	}

	if (osKeyring{}).Name() == "" {
		t.Fatal("osKeyring.Name returned empty")
	}
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"0123456789abcdef", true},
		{"", false},
		{"0123456789abcdeF", false},
		{"0123456789abcdeg", false},
	} {
		if got := validKeyID(tc.id); got != tc.want {
			t.Errorf("validKeyID(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}

	t.Setenv("LOCALAPPDATA", t.TempDir())
	for _, tc := range []struct {
		goos string
		op   string
	}{
		{"darwin", "set"},
		{"darwin", "get"},
		{"darwin", "delete"},
		{"linux", "set"},
		{"linux", "delete"},
		{"windows", "set"},
		{"windows", "get"},
		{"windows", "delete"},
	} {
		if _, _, _, _, err := keyringCommand(tc.goos, tc.op, "0123456789abcdef", "secret"); err != nil {
			t.Errorf("keyringCommand(%s, %s): %v", tc.goos, tc.op, err)
		}
	}
	if _, _, _, _, err := keyringCommand("darwin", "badop", "0123456789abcdef", ""); err == nil {
		t.Fatal("keyringCommand(darwin, badop) succeeded")
	}
	if _, _, _, _, err := keyringCommand("plan9", "get", "0123456789abcdef", ""); err == nil {
		t.Fatal("keyringCommand(plan9, get) succeeded")
	}

	if runtime.GOOS != "windows" {
		out, execErr := defaultExecKeyring("sh", []string{"-c", "cat"}, "pong", []string{"DEJA_KR_ENV=1"})
		if execErr != nil || strings.TrimSpace(string(out)) != "pong" {
			t.Fatalf("defaultExecKeyring = (%q, %v)", out, execErr)
		}
	}

	origExec := execKeyringFn
	t.Cleanup(func() { execKeyringFn = origExec })
	var kr osKeyring
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	if KeyringAvailable() {
		t.Fatal("KeyringAvailable with DEJA_VAULT_BACKEND=passphrase = true, want false")
	}
	if err := kr.Set("0123456789abcdef", make([]byte, 32)); err == nil {
		t.Fatal("osKeyring.Set under passphrase backend succeeded")
	}
	if _, err := kr.Get("0123456789abcdef"); err == nil {
		t.Fatal("osKeyring.Get under passphrase backend succeeded")
	}
	if err := kr.Delete("0123456789abcdef"); err != nil {
		t.Fatalf("osKeyring.Delete under passphrase backend = %v", err)
	}

	t.Setenv("DEJA_VAULT_BACKEND", "")
	if err := kr.Set("bad-id", make([]byte, 32)); err == nil {
		t.Fatal("osKeyring.Set bad keyID succeeded")
	}
	if _, err := kr.Get("bad-id"); err == nil {
		t.Fatal("osKeyring.Get bad keyID succeeded")
	}
	if err := kr.Delete("bad-id"); err != nil {
		t.Fatalf("osKeyring.Delete bad keyID = %v", err)
	}

	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(i + 10)
	}
	execKeyringFn = func(_ string, _ []string, _ string, _ []string) ([]byte, error) {
		return []byte(hex.EncodeToString(dek) + "\n"), nil
	}
	if err := kr.Set("0123456789abcdef", dek); err != nil {
		t.Fatalf("osKeyring.Set valid: %v", err)
	}
	if got, err := kr.Get("0123456789abcdef"); err != nil || !slices.Equal(got, dek) {
		t.Fatalf("osKeyring.Get valid = (%x, %v)", got, err)
	}
	if err := kr.Delete("0123456789abcdef"); err != nil {
		t.Fatalf("osKeyring.Delete valid: %v", err)
	}

	execKeyringFn = func(_ string, _ []string, _ string, _ []string) ([]byte, error) {
		return []byte("not-hex"), nil
	}
	if _, err := kr.Get("0123456789abcdef"); err == nil {
		t.Fatal("osKeyring.Get corrupt hex succeeded")
	}
	execKeyringFn = func(_ string, _ []string, _ string, _ []string) ([]byte, error) {
		return []byte("boom"), errors.New("backend failure")
	}
	if err := kr.Set("0123456789abcdef", dek); err == nil {
		t.Fatal("osKeyring.Set error returned nil")
	}
	if _, err := kr.Get("0123456789abcdef"); err == nil {
		t.Fatal("osKeyring.Get error returned nil")
	}

	// Parameterized OS backend name, availability, and delete helpers (cover all platforms in CI).
	for _, tc := range []struct {
		goos string
		want string
	}{
		{"darwin", "keychain"},
		{"linux", "secret-service"},
		{"windows", "dpapi"},
		{"plan9", "passphrase"},
	} {
		if got := keyringBackendName(tc.goos); got != tc.want {
			t.Fatalf("keyringBackendName(%q) = %q, want %q", tc.goos, got, tc.want)
		}
	}

	okLook := func(string) (string, error) { return "/bin/true", nil }
	errLook := func(string) (string, error) { return "", errors.New("missing") }
	if !keyringAvailableFor("darwin", okLook) || keyringAvailableFor("darwin", errLook) {
		t.Fatal("keyringAvailableFor(darwin) mismatch")
	}
	if keyringAvailableFor("linux", errLook) {
		t.Fatal("keyringAvailableFor(linux) with missing tool returned true")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/tmp/fake-dbus")
	if !keyringAvailableFor("linux", okLook) {
		t.Fatal("keyringAvailableFor(linux) with DBUS_SESSION_BUS_ADDRESS returned false")
	}
	if !keyringAvailableFor("windows", errLook) || keyringAvailableFor("plan9", okLook) {
		t.Fatal("keyringAvailableFor(windows/default) mismatch")
	}

	dpapiDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dpapiDir)
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	if dErr := deleteOSKeyring("darwin", "0123456789abcdef"); dErr != nil {
		t.Fatalf("deleteOSKeyring passphrase forced: %v", dErr)
	}
	t.Setenv("DEJA_VAULT_BACKEND", "")
	if dErr := deleteOSKeyring("windows", "bad-id"); dErr != nil {
		t.Fatalf("deleteOSKeyring invalid keyID: %v", dErr)
	}
	dpPath, pErr := dpapiPath("0123456789abcdef")
	if pErr != nil {
		t.Fatal(pErr)
	}
	if mkErr := os.MkdirAll(filepath.Dir(dpPath), 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	if wErr := os.WriteFile(dpPath, []byte("dpapi-blob"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if dErr := deleteOSKeyring("windows", "0123456789abcdef"); dErr != nil {
		t.Fatalf("deleteOSKeyring windows remove: %v", dErr)
	}
	if dErr := deleteOSKeyring("windows", "0123456789abcdef"); dErr != nil {
		t.Fatalf("deleteOSKeyring windows missing idempotent: %v", dErr)
	}
	if dErr := deleteOSKeyring("plan9", "0123456789abcdef"); dErr == nil {
		t.Fatal("deleteOSKeyring unsupported OS succeeded")
	}
	execKeyringFn = func(_ string, _ []string, _ string, _ []string) ([]byte, error) { return nil, nil }
	if dErr := deleteOSKeyring("darwin", "0123456789abcdef"); dErr != nil {
		t.Fatalf("deleteOSKeyring darwin stub = %v", dErr)
	}
}

func TestLiveVaultEdgeBranches(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}

	storeConfigCache("ignored", nil, Config{})
	if _, err := aeadFor([]byte("short"), DomainIndexFile); err == nil {
		t.Fatal("aeadFor accepted short DEK")
	}
	if _, err := aeadFor(make([]byte, 32), ""); err == nil {
		t.Fatal("aeadFor accepted empty domain")
	}
	if _, err := OpenBytes(make([]byte, 32), DomainIndexFile, []byte("plain")); err == nil {
		t.Fatal("OpenBytes accepted unsealed prefix")
	}
	if err := encryptExistingSidecars("", make([]byte, 32)); err != nil {
		t.Fatalf("encryptExistingSidecars empty dir = %v", err)
	}

	// Vault disabled: sealed DJVF1 file and sealed sidecar line fail closed.
	sealedBlob, err := SealBytes(make([]byte, 32), DomainSidecar, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	orphanSealed := indexDir + ".orphan"
	if wErr := os.WriteFile(orphanSealed, sealedBlob, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, rErr := ReadSealedFile(indexDir, orphanSealed, DomainSidecar); rErr == nil {
		t.Fatal("ReadSealedFile disabled vault accepted sealed blob")
	}
	if _, rErr := ReadSealedFile(indexDir, filepath.Join(t.TempDir(), "missing"), DomainSidecar); rErr == nil {
		t.Fatal("ReadSealedFile missing succeeded")
	}
	if _, _, oErr := OpenSidecarLine(indexDir, DomainSidecar, NoteLinePrefix+"abc"); oErr == nil {
		t.Fatal("OpenSidecarLine disabled vault accepted NoteLinePrefix")
	}

	// Corrupt vault.json -> Status reports Locked + Error.
	if wErr := os.MkdirAll(filepath.Dir(ConfigPath()), 0o700); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := os.WriteFile(ConfigPath(), []byte("{broken"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	badSt := Status(indexDir)
	if !badSt.Enabled || !badSt.Locked || badSt.Error == "" {
		t.Fatalf("Status corrupt vault.json = %+v", badSt)
	}
	_ = os.Remove(ConfigPath())
	invalidateConfigCache()

	// encryptExistingSidecars handles dir collision, mixed lines, idempotent rerun, and Archive/sealed-manifest Status.
	base := strings.TrimSuffix(indexDir, string(os.PathSeparator))
	if mErr := os.MkdirAll(base+".dejavu", 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if _, iErr := Init(indexDir, "edge-passphrase-1", 0); iErr != nil {
		t.Fatalf("Init: %v", iErr)
	}
	dek, err := CheckUnlocked(indexDir)
	if err != nil {
		t.Fatalf("CheckUnlocked: %v", err)
	}
	defer clear(dek)

	encLine, err := SealLine(dek, DomainSidecar, []byte("already-sealed"))
	if err != nil {
		t.Fatal(err)
	}
	hookSeen := base + ".hookseen"
	if wErr := os.WriteFile(hookSeen, []byte("\nplain-entry 1\n"+encLine+"\n"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if eErr := encryptExistingSidecars(indexDir, dek); eErr != nil {
		t.Fatalf("encryptExistingSidecars first: %v", eErr)
	}
	if eErr := encryptExistingSidecars(indexDir, dek); eErr != nil {
		t.Fatalf("encryptExistingSidecars idempotent: %v", eErr)
	}
	if _, _, oErr := OpenSidecarLine(indexDir, DomainSidecar, NoteLinePrefix+"corrupt!!!"); oErr == nil {
		t.Fatal("OpenSidecarLine unlocked accepted corrupt ciphertext")
	}

	// Sealed manifest.gob sets EncryptedStore=true in Status, as does ArchivePath.
	sealedManifest, err := SealBytes(dek, DomainIndexFile, []byte("manifest"))
	if err != nil {
		t.Fatal(err)
	}
	if wErr := os.WriteFile(filepath.Join(base, "manifest.gob"), sealedManifest, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if st := Status(indexDir); !st.EncryptedStore {
		t.Fatalf("Status with sealed manifest = %+v, want EncryptedStore=true", st)
	}
	_ = os.Remove(filepath.Join(base, "manifest.gob"))
	if wErr := os.WriteFile(ArchivePath(base), []byte("archive"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if st := Status(indexDir); !st.EncryptedStore {
		t.Fatalf("Status with archive = %+v, want EncryptedStore=true", st)
	}

	// Additional edge branches: pbCache hit, keyringIdentity nil/pointer, RecordCipher/WriteSealedFile errors, Unlock migrator failure.
	cfg, cErr := LoadConfig()
	if cErr != nil {
		t.Fatalf("LoadConfig: %v", cErr)
	}
	u1, uErr := unwrapDEKCached(cfg, "edge-passphrase-1")
	if uErr != nil {
		t.Fatalf("unwrapDEKCached first: %v", uErr)
	}
	clear(u1)
	u2, uErr2 := unwrapDEKCached(cfg, "edge-passphrase-1")
	if uErr2 != nil {
		t.Fatalf("unwrapDEKCached cached hit: %v", uErr2)
	}
	clear(u2)
	if _, wErr := unwrapDEKCached(cfg, "wrong-passphrase"); wErr == nil {
		t.Fatal("unwrapDEKCached wrong passphrase succeeded")
	}
	if got := keyringIdentity(nil); got != "" {
		t.Fatalf("keyringIdentity(nil) = %q, want empty", got)
	}
	ptrKR := &countingKeyring{}
	if got := keyringIdentity(ptrKR); !strings.Contains(got, "countingKeyring") {
		t.Fatalf("keyringIdentity(ptr) = %q", got)
	}
	if _, sErr := SealBytes([]byte("short"), DomainSidecar, []byte("x")); sErr == nil {
		t.Fatal("SealBytes short DEK succeeded")
	}
	if _, oErr := OpenBytes(dek, DomainSidecar, []byte(fileMagic+"short")); oErr == nil {
		t.Fatal("OpenBytes truncated succeeded")
	}
	if _, oErr := OpenBytes([]byte("short"), DomainSidecar, sealedManifest); oErr == nil {
		t.Fatal("OpenBytes short DEK succeeded")
	}
	if wErr := WriteSealedFile(indexDir, filepath.Join(t.TempDir(), "x"), "", []byte("a"), 0o600); wErr == nil {
		t.Fatal("WriteSealedFile empty domain succeeded")
	}
	if _, rcErr := NewRecordCipher([]byte("short")); rcErr == nil {
		t.Fatal("NewRecordCipher short DEK succeeded")
	}
	rc, active, rcErr := ActiveRecordCipher(indexDir)
	if rcErr != nil || !active || rc == nil {
		t.Fatalf("ActiveRecordCipher unlocked = (%v, %v, %v)", rc, active, rcErr)
	}
	if _, oErr := rc.Open(0, []byte("short")); oErr == nil {
		t.Fatal("RecordCipher.Open truncated succeeded")
	}

	if lErr := Lock(indexDir, "edge-passphrase-1"); lErr != nil {
		t.Fatalf("Lock: %v", lErr)
	}
	if wErr := WriteSealedFile(indexDir, filepath.Join(t.TempDir(), "locked"), DomainSidecar, []byte("x"), 0o600); !errors.Is(wErr, ErrLocked) {
		t.Fatalf("WriteSealedFile locked = %v, want ErrLocked", wErr)
	}
	if _, _, aErr := ActiveRecordCipher(indexDir); !errors.Is(aErr, ErrLocked) {
		t.Fatalf("ActiveRecordCipher locked = %v, want ErrLocked", aErr)
	}

	_ = os.Remove(ArchivePath(base))
	RegisterMigrator("zzz_edge_fail", func(string, []byte) error {
		return errors.New("migrator boom")
	})
	t.Cleanup(func() { unregisterMigratorForTest("zzz_edge_fail") })
	if uErr := Unlock(indexDir, "edge-passphrase-1"); uErr == nil || !strings.Contains(uErr.Error(), "migrator boom") {
		t.Fatalf("Unlock failing migrator = %v, want migrator boom", uErr)
	}
}

func TestVaultLiveMigrationAndCacheBranches(t *testing.T) {
	tmp := t.TempDir()
	indexDir := filepath.Join(tmp, "index.db")
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	vaultPath := filepath.Join(tmp, "vault.json")
	t.Setenv("DEJA_VAULT_FILE", vaultPath)
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "mig-pass-1")
	oldIter := KDFIterations
	KDFIterations = 1000
	t.Cleanup(func() { KDFIterations = oldIter })
	ClearCache()
	t.Cleanup(ClearCache)

	if _, iErr := Init(indexDir, "mig-pass-1", 0); iErr != nil {
		t.Fatalf("Init: %v", iErr)
	}
	dek, kErr := CheckUnlocked(indexDir)
	if kErr != nil {
		t.Fatalf("CheckUnlocked: %v", kErr)
	}
	defer clear(dek)

	// Invalid DEK / domain checks in aeadFor, SealBytes, and NewStreamReader.
	if _, sErr := SealBytes([]byte("short"), DomainSidecar, []byte("hi")); sErr == nil {
		t.Fatal("SealBytes short DEK succeeded")
	}
	if _, sErr := SealBytes(dek, "", []byte("hi")); sErr == nil {
		t.Fatal("SealBytes empty domain succeeded")
	}
	if _, srErr := NewStreamReader(bytes.NewReader([]byte("BAD!")), dek, DomainEmbed); srErr == nil {
		t.Fatal("NewStreamReader bad magic succeeded")
	}

	// ActiveRecordCipher under corrupt vault config fails with error.
	badVault := filepath.Join(tmp, "bad-vault.json")
	if wErr := os.WriteFile(badVault, []byte("{bad"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	t.Setenv("DEJA_VAULT_FILE", badVault)
	ClearCache()
	if _, _, rcErr := ActiveRecordCipher(indexDir); rcErr == nil {
		t.Fatal("ActiveRecordCipher with bad vault config succeeded")
	}
	st := Status(indexDir)
	if !st.Enabled || !st.Locked || st.Error == "" {
		t.Fatalf("Status corrupt vault = %+v, want enabled+locked+error", st)
	}

	// Wrong passphrase fails unwrap cleanly.
	t.Setenv("DEJA_VAULT_FILE", vaultPath)
	t.Setenv("DEJA_VAULT_PASSPHRASE", "wrong-pass")
	ClearCache()
	if _, ckErr := CheckUnlocked(indexDir); ckErr == nil || !strings.Contains(ckErr.Error(), "passphrase") {
		t.Fatalf("CheckUnlocked wrong passphrase = %v, want invalid passphrase error", ckErr)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", "mig-pass-1")
	ClearCache()

	// encryptExistingSidecars seals plaintext wholeFileSidecars and plaintext lines in lineEncryptedSidecars.
	dejavuSidecar := indexDir + ".dejavu"
	if wErr := os.WriteFile(dejavuSidecar, []byte("plain-dejavu-entry"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	embedBatchSidecar := indexDir + ".embed-batch"
	if wErr := os.WriteFile(embedBatchSidecar, []byte(`{"batch_id":"b1"}`), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	sealedLine, slErr := SealLine(dek, DomainSidecar, []byte("already-sealed"))
	if slErr != nil {
		t.Fatal(slErr)
	}
	hookseenSidecar := indexDir + ".hookseen"
	if wErr := os.WriteFile(hookseenSidecar, []byte("\n"+sealedLine+"\nplain-hookseen-token\n"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := encryptExistingSidecars(indexDir, dek); mErr != nil {
		t.Fatalf("encryptExistingSidecars: %v", mErr)
	}
	gotDV, rErr := os.ReadFile(dejavuSidecar)
	if rErr != nil || !IsSealedBytes(gotDV) {
		t.Fatalf("wholeFileSidecar not sealed: %q (%v)", gotDV, rErr)
	}
	gotEB, rErr := os.ReadFile(embedBatchSidecar)
	if rErr != nil || !IsSealedBytes(gotEB) {
		t.Fatalf("embed-batch sidecar not sealed: %q (%v)", gotEB, rErr)
	}
	gotHS, rErr := os.ReadFile(hookseenSidecar)
	if rErr != nil || strings.Contains(string(gotHS), "plain-hookseen-token") || !strings.Contains(string(gotHS), NoteLinePrefix) {
		t.Fatalf("lineEncryptedSidecar not sealed: %q (%v)", gotHS, rErr)
	}

	// OS keyring availability & deleteOSKeyring cross-platform branches.
	t.Setenv("DEJA_VAULT_BACKEND", "")
	restoreKR := SetKeyringForTest(nil)
	defer restoreKR()
	_ = KeyringAvailable()
	if krErr := deleteOSKeyring("plan9", "0123456789abcdef"); krErr == nil {
		t.Fatal("deleteOSKeyring unsupported OS succeeded")
	}
	t.Setenv("LOCALAPPDATA", tmp)
	if krErr := deleteOSKeyring("windows", "0123456789abcdef"); krErr != nil {
		t.Fatalf("deleteOSKeyring windows missing = %v", krErr)
	}
}
