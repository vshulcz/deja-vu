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
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	if _, err := OpenBytes(wrongDEK, DomainIndexFile, sealed1); err == nil {
		t.Fatal("OpenBytes accepted wrong DEK")
	}
	if _, err := OpenBytes(dek, DomainSidecar, sealed1); err == nil {
		t.Fatal("OpenBytes accepted wrong domain AAD")
	}
	tampered := append([]byte(nil), sealed1...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := OpenBytes(dek, DomainIndexFile, tampered); err == nil {
		t.Fatal("OpenBytes accepted tampered ciphertext")
	}
	if _, err := OpenBytes(dek, DomainIndexFile, []byte("DJVF1short")); err == nil {
		t.Fatal("OpenBytes accepted truncated DJVF1 frame")
	}
	if _, err := SealBytes([]byte("short"), DomainIndexFile, plain); err == nil {
		t.Fatal("SealBytes accepted short DEK")
	}

	// Unencrypted store writes and reads plaintext via helpers.
	plainPath := indexDir + ".dejavu"
	if err := WriteSealedFile(indexDir, plainPath, DomainSidecar, plain, 0o600); err != nil {
		t.Fatalf("WriteSealedFile unencrypted: %v", err)
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
	if _, err := Init(indexDir, "live-passphrase-1", 0); err != nil {
		t.Fatalf("Init: %v", err)
	}
	livePath := indexDir + ".live"
	if err := WriteSealedFile(indexDir, livePath, DomainSidecar, plain, 0o600); err != nil {
		t.Fatalf("WriteSealedFile encrypted: %v", err)
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
	if err := os.WriteFile(truncPath, []byte("DJVF1bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSealedFile(indexDir, truncPath, DomainSidecar); err == nil {
		t.Fatal("expected error reading truncated DJVF1 file")
	}

	// Locked store fails closed on both ReadSealedFile and WriteSealedFile.
	if err := Lock(indexDir, ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := ReadSealedFile(indexDir, livePath, DomainSidecar); !errors.Is(err, ErrLocked) {
		t.Fatalf("ReadSealedFile while locked = %v, want ErrLocked", err)
	}
	if err := WriteSealedFile(indexDir, livePath, DomainSidecar, plain, 0o600); !errors.Is(err, ErrLocked) {
		t.Fatalf("WriteSealedFile while locked = %v, want ErrLocked", err)
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
	if _, err := sw.Write(payload); err != nil {
		t.Fatalf("StreamWriter.Write: %v", err)
	}
	if err := sw.Close(); err != nil {
		t.Fatalf("StreamWriter.Close: %v", err)
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
	if rc, err := ActiveRecordCipher(indexDir); err != nil || rc != nil {
		t.Fatalf("disabled ActiveRecordCipher = (%v, %v), want (nil, nil)", rc, err)
	}
	line, err := SealSidecarLine(indexDir, DomainSidecar, []byte("plain line"))
	if err != nil || line != "plain line" {
		t.Fatalf("disabled SealSidecarLine = (%q, %v)", line, err)
	}
	gotLine, wasSealed, err := OpenSidecarLine(indexDir, DomainSidecar, line)
	if err != nil || wasSealed || string(gotLine) != "plain line" {
		t.Fatalf("disabled OpenSidecarLine = (%q, %v, %v)", gotLine, wasSealed, err)
	}

	if _, err := Init(indexDir, "record-cipher-pass", 0); err != nil {
		t.Fatalf("Init: %v", err)
	}
	rc, err := ActiveRecordCipher(indexDir)
	if err != nil || rc == nil {
		t.Fatalf("ActiveRecordCipher unlocked = (%v, %v)", rc, err)
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
	if _, err := rc.Open(0, sealed); err == nil {
		t.Fatal("RecordCipher.Open accepted wrong baseFlag AAD")
	}
	if _, err := rc.Open(1, []byte("too-short")); err == nil {
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

	if err := Lock(indexDir, ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := ActiveRecordCipher(indexDir); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked ActiveRecordCipher = %v, want ErrLocked", err)
	}
	if _, err := SealSidecarLine(indexDir, DomainSidecar, []byte("x")); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked SealSidecarLine = %v, want ErrLocked", err)
	}
	if _, _, err := OpenSidecarLine(indexDir, DomainSidecar, encLine); !errors.Is(err, ErrLocked) {
		t.Fatalf("locked OpenSidecarLine = %v, want ErrLocked", err)
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

	if err := Lock(indexDir, ""); err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := os.Stat(tuiHistoryPath); !os.IsNotExist(err) {
		t.Fatalf(".tui-history survived Lock (err=%v)", err)
	}
	if err := Unlock(indexDir, "migrate-passphrase"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if migrated.Load() != 2 {
		t.Fatalf("Unlock ran migrator %d times, want 2", migrated.Load())
	}
	gotTUI2, err := ReadSealedFile(indexDir, tuiHistoryPath, DomainSidecar)
	if err != nil || string(gotTUI2) != `["`+canary+`"]` {
		t.Fatalf(".tui-history after Unlock = (%q, %v)", gotTUI2, err)
	}
}

func TestLinuxDBusFallbackKeyringAndStatus(t *testing.T) {
	indexDir, _ := withTestVaultEnv(t)
	sockDir, err := os.MkdirTemp("", "dbus")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	busSock := filepath.Join(sockDir, "bus")
	ln, err := net.Listen("unix", busSock)
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
	found := false
	for _, kv := range extraEnv {
		if kv == wantBus {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("linux keyringCommand extraEnv = %v, want %q", extraEnv, wantBus)
	}

	st0 := Status(indexDir)
	if st0.Enabled || st0.Locked {
		t.Fatalf("uninitialized Status = %+v", st0)
	}
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(indexDir, "status-passphrase", 5*time.Minute); err != nil {
		t.Fatalf("Init: %v", err)
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
