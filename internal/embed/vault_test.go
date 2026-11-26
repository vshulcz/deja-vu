package embed

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/vault"
)

func TestSidecarLiveEncryptionLockPurgesMemoAndMigrates(t *testing.T) {
	vault.ClearCache()
	readMemo.Lock()
	readMemo.path = ""
	readMemo.car = Sidecar{}
	readMemo.Unlock()
	t.Cleanup(func() {
		vault.ClearCache()
		readMemo.Lock()
		readMemo.path = ""
		readMemo.car = Sidecar{}
		readMemo.Unlock()
	})

	dir, records := threeSessionStore(t)
	tmp := filepath.Dir(dir)
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "embed-vault-pass")
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	canaryKey := "claude:CANARY_EMBED_KEY_8821"
	plainCar := Sidecar{
		Model:      "m",
		Dim:        2,
		Generation: "g1",
		Covered:    1,
		Vectors: []Vector{
			{Offset: records[0].Offset, Key: canaryKey, Values: []float32{0.5, 0.5}},
		},
	}
	if err := write(dir, plainCar); err != nil {
		t.Fatalf("write plaintext sidecar: %v", err)
	}
	rawPlain, err := os.ReadFile(Path(dir))
	if err != nil || !bytes.Contains(rawPlain, []byte(canaryKey)) {
		t.Fatalf("expected plaintext canary in .vectors.bin before vault.Init (err=%v)", err)
	}

	// vault.Init must migrate existing .vectors.bin in place to DJV1 ciphertext.
	if _, iErr := vault.Init(dir, "embed-vault-pass", 0); iErr != nil {
		t.Fatalf("vault.Init: %v", iErr)
	}
	rawEnc, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsStreamHeader(rawEnc) {
		t.Fatalf(".vectors.bin missing DJV1 stream header after Init")
	}
	if bytes.Contains(rawEnc, []byte(canaryKey)) || bytes.Contains(rawEnc, []byte("g1")) {
		t.Fatalf(".vectors.bin still contains plaintext canary after Init")
	}

	// Read while unlocked succeeds, warms readMemo, and second Read hits readMemo.
	got, err := Read(dir)
	if err != nil || len(got.Vectors) != 1 || got.Vectors[0].Key != canaryKey {
		t.Fatalf("Read encrypted sidecar = (%+v, %v)", got, err)
	}
	gotMemo, err := Read(dir)
	if err != nil || len(gotMemo.Vectors) != 1 || gotMemo.Vectors[0].Key != canaryKey {
		t.Fatalf("Read encrypted sidecar memo hit = (%+v, %v)", gotMemo, err)
	}

	// Idempotent migration when .vectors.bin is already encrypted.
	dek, err := vault.CheckUnlocked(dir)
	if err != nil || len(dek) != 32 {
		t.Fatalf("CheckUnlocked = (%d, %v)", len(dek), err)
	}
	defer clear(dek)
	if mErr := migrateVectorSidecar(dir, dek); mErr != nil {
		t.Fatalf("idempotent migrateVectorSidecar: %v", mErr)
	}

	// Lock must purge readMemo and fail closed with vault.ErrLocked.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(dir, "embed-vault-pass"); lErr != nil {
		t.Fatalf("vault.Lock: %v", lErr)
	}
	if _, rErr := Read(dir); !errors.Is(rErr, vault.ErrLocked) {
		t.Fatalf("Read when locked = %v, want ErrLocked", rErr)
	}

	// Unlock restores Read and subsequent live writes stay encrypted.
	if uErr := vault.Unlock(dir, "embed-vault-pass"); uErr != nil {
		t.Fatalf("vault.Unlock: %v", uErr)
	}
	t.Setenv("DEJA_VAULT_PASSPHRASE", "embed-vault-pass")
	canaryKey2 := "claude:CANARY_EMBED_LIVE_9922"
	liveCar := Sidecar{
		Model:      "m",
		Dim:        2,
		Generation: "g2",
		Covered:    1,
		Vectors: []Vector{
			{Offset: records[0].Offset, Key: canaryKey2, Values: []float32{0.25, 0.75}},
		},
	}
	if wErr := write(dir, liveCar); wErr != nil {
		t.Fatalf("live encrypted write: %v", wErr)
	}
	rawLive, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsStreamHeader(rawLive) || bytes.Contains(rawLive, []byte(canaryKey2)) {
		t.Fatalf("live written .vectors.bin not encrypted cleanly")
	}
	got2, err := Read(dir)
	if err != nil || len(got2.Vectors) != 1 || got2.Vectors[0].Key != canaryKey2 {
		t.Fatalf("Read after Unlock + live write = (%+v, %v)", got2, err)
	}

	// Corrupted ciphertext must fail closed once readMemo is invalidated.
	readMemo.Lock()
	readMemo.path = ""
	readMemo.car = Sidecar{}
	readMemo.Unlock()
	if wErr := os.WriteFile(Path(dir), append(rawLive, 0xff), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, rErr := Read(dir); rErr == nil {
		t.Fatal("Read accepted trailing garbage on encrypted sidecar")
	}

	// Edge cases of migrateVectorSidecar: missing file, bad magic, unsupported version, empty DEK.
	edgeDir := filepath.Join(t.TempDir(), "idx")
	if mErr := os.MkdirAll(edgeDir, 0o700); mErr != nil {
		t.Fatal(mErr)
	}
	if mErr := migrateVectorSidecar(edgeDir, nil); mErr != nil {
		t.Fatalf("migrateVectorSidecar nil dek: %v", mErr)
	}
	if mErr := migrateVectorSidecar(edgeDir, dek); mErr != nil {
		t.Fatalf("migrateVectorSidecar missing file: %v", mErr)
	}
	if wErr := os.WriteFile(Path(edgeDir), []byte("BADM"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateVectorSidecar(edgeDir, dek); mErr != nil {
		t.Fatalf("migrateVectorSidecar bad magic: %v", mErr)
	}
	var badVer [6]byte
	copy(badVer[:4], magic[:])
	binary.LittleEndian.PutUint16(badVer[4:6], sidecarVersion+1)
	if wErr := os.WriteFile(Path(edgeDir), badVer[:], 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateVectorSidecar(edgeDir, dek); mErr != nil {
		t.Fatalf("migrateVectorSidecar unsupported version: %v", mErr)
	}
	if wErr := os.WriteFile(Path(edgeDir), []byte("AB"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateVectorSidecar(edgeDir, dek); mErr != nil {
		t.Fatalf("migrateVectorSidecar short header: %v", mErr)
	}
	if wErr := os.WriteFile(Path(edgeDir), magic[:], 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateVectorSidecar(edgeDir, dek); mErr != nil {
		t.Fatalf("migrateVectorSidecar missing version: %v", mErr)
	}
	var zeroVer [6]byte
	copy(zeroVer[:4], magic[:])
	if wErr := os.WriteFile(Path(edgeDir), zeroVer[:], 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateVectorSidecar(edgeDir, dek); mErr != nil {
		t.Fatalf("migrateVectorSidecar zero version: %v", mErr)
	}

	// Locked vault fails write cleanly too.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if lErr := vault.Lock(dir, "embed-vault-pass"); lErr != nil {
		t.Fatalf("vault.Lock: %v", lErr)
	}
	if wErr := write(dir, liveCar); !errors.Is(wErr, vault.ErrLocked) {
		t.Fatalf("write while locked = %v, want ErrLocked", wErr)
	}
	if _, rfErr := readFile(Path(dir)); !errors.Is(rfErr, vault.ErrLocked) {
		t.Fatalf("readFile while locked = %v, want ErrLocked", rfErr)
	}

	// Sidecar migration on directory path, short DEK with valid header, and idempotent already-encrypted rerun.
	if mErr := migrateVectorSidecar(dir, dek); mErr != nil {
		t.Fatalf("migrateVectorSidecar already encrypted = %v", mErr)
	}
	dirSidecar := filepath.Join(t.TempDir(), "idx-dir")
	if mkErr := os.MkdirAll(Path(dirSidecar), 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	if mErr := migrateVectorSidecar(dirSidecar, dek); mErr == nil {
		t.Fatal("migrateVectorSidecar on directory path succeeded")
	}
	plainSidecarDir := filepath.Join(t.TempDir(), "idx-plain-err")
	if mkErr := os.MkdirAll(plainSidecarDir, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	validHdr := make([]byte, 6)
	copy(validHdr[:4], magic[:])
	binary.LittleEndian.PutUint16(validHdr[4:6], sidecarVersion)
	if wErr := os.WriteFile(Path(plainSidecarDir), validHdr, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if mErr := migrateVectorSidecar(plainSidecarDir, []byte("short")); mErr == nil {
		t.Fatal("migrateVectorSidecar short DEK on valid header succeeded")
	}
}

func TestSidecarReadErrorBranches(t *testing.T) {
	vault.ClearCache()
	readMemo.Lock()
	readMemo.path = ""
	readMemo.car = Sidecar{}
	readMemo.enc = false
	readMemo.Unlock()
	t.Cleanup(func() {
		vault.ClearCache()
		readMemo.Lock()
		readMemo.path = ""
		readMemo.car = Sidecar{}
		readMemo.enc = false
		readMemo.Unlock()
	})
	oldIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = oldIter })

	tmp := t.TempDir()
	uDir := filepath.Join(tmp, "u.db")
	if mkErr := os.MkdirAll(uDir, 0o700); mkErr != nil {
		t.Fatal(mkErr)
	}
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "embed-read-pass")
	if _, iErr := vault.Init(uDir, "embed-read-pass", 0); iErr != nil {
		t.Fatal(iErr)
	}
	uDEK, dErr := vault.CheckUnlocked(uDir)
	if dErr != nil {
		t.Fatal(dErr)
	}
	defer clear(uDEK)

	if wErr := os.WriteFile(Path(uDir), []byte("bad-stream"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, rErr := readFile(Path(uDir)); rErr == nil {
		t.Fatal("readFile bad stream succeeded")
	}

	if wErr := write(uDir, Sidecar{Model: "m", Generation: "g", Dim: 2}); wErr != nil {
		t.Fatal(wErr)
	}
	rawEnc, oErr := os.Open(Path(uDir))
	if oErr != nil {
		t.Fatal(oErr)
	}
	sr, srErr := vault.NewStreamReader(rawEnc, uDEK, vault.DomainEmbed)
	if srErr != nil {
		_ = rawEnc.Close()
		t.Fatal(srErr)
	}
	plain, pErr := io.ReadAll(sr)
	_ = rawEnc.Close()
	if pErr != nil {
		t.Fatal(pErr)
	}
	var trailingBuf bytes.Buffer
	sw, swErr := vault.NewStreamWriter(&trailingBuf, uDEK, vault.DomainEmbed)
	if swErr != nil {
		t.Fatal(swErr)
	}
	if _, wErr := sw.Write(append(plain, 'X')); wErr != nil {
		t.Fatal(wErr)
	}
	if cErr := sw.Close(); cErr != nil {
		t.Fatal(cErr)
	}
	if wErr := os.WriteFile(Path(uDir), trailingBuf.Bytes(), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, rErr := readFile(Path(uDir)); rErr == nil || !strings.Contains(rErr.Error(), "unexpected trailing data") {
		t.Fatalf("readFile trailing = %v, want unexpected trailing data", rErr)
	}
}
