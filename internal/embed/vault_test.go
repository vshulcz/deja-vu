package embed

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
	if _, err := vault.Init(dir, "embed-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
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

	// Read while unlocked succeeds and warms readMemo.
	got, err := Read(dir)
	if err != nil || len(got.Vectors) != 1 || got.Vectors[0].Key != canaryKey {
		t.Fatalf("Read encrypted sidecar = (%+v, %v)", got, err)
	}

	// Lock must purge readMemo and fail closed with vault.ErrLocked.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if err := vault.Lock(dir, "embed-vault-pass"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	if _, err := Read(dir); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("Read when locked = %v, want ErrLocked", err)
	}

	// Unlock restores Read and subsequent live writes stay encrypted.
	if err := vault.Unlock(dir, "embed-vault-pass"); err != nil {
		t.Fatalf("vault.Unlock: %v", err)
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
	if err := write(dir, liveCar); err != nil {
		t.Fatalf("live encrypted write: %v", err)
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
}
