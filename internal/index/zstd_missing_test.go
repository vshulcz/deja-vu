package index

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Without zstd a compressed rollout stays out of the manifest, pass after pass,
// so the first pass that has zstd reads it. Once it is held, an unchanged
// rollout does not run the decoder again (#5061).
func TestZstdInstalledReadsRolloutOnce(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
	plain := filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
	zst := filepath.Join(dir, "rollout-2026-07-20T00-00-00-zstd-installed-thread.jsonl.zst")
	write(t, plain, codexRolloutHead)
	write(t, zst, string([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x01, 0x02}))

	idx := filepath.Join(tmp, "idx")
	for pass := 1; pass <= 2; pass++ {
		if err := Ensure(idx, "", false, nil); err != nil {
			t.Fatal(err)
		}
		m, err := readManifest(idx)
		if err != nil {
			t.Fatal(err)
		}
		if _, held := m.Files[zst]; held {
			t.Fatalf("pass %d: compressed rollout held without zstd", pass)
		}
		if _, held := m.Files[plain]; !held {
			t.Fatalf("pass %d: plain rollout missing from manifest", pass)
		}
	}

	counter := filepath.Join(tmp, "zstd-calls.txt")
	rollout := filepath.Join(tmp, "decoded.jsonl")
	write(t, rollout, strings.ReplaceAll(codexRolloutHead, "cx-1", "zstd-installed-thread"))
	t.Setenv("DEJA_TEST_ZSTD_COUNTER", counter)
	t.Setenv("DEJA_TEST_ZSTD_ROLLOUT", rollout)
	if runtime.GOOS == "windows" {
		write(t, filepath.Join(bin, "zstd.bat"), "@echo off\r\n@echo x>>\"%DEJA_TEST_ZSTD_COUNTER%\"\r\n@type \"%DEJA_TEST_ZSTD_ROLLOUT%\"\r\n@exit /b 0\r\n")
	} else {
		write(t, filepath.Join(bin, "zstd"), "#!/bin/sh\nprintf 'x\\n' >>\"$DEJA_TEST_ZSTD_COUNTER\"\nwhile IFS= read -r line; do\n  printf '%s\\n' \"$line\"\ndone <\"$DEJA_TEST_ZSTD_ROLLOUT\"\n")
		if err := os.Chmod(filepath.Join(bin, "zstd"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	calls := func() int {
		b, err := os.ReadFile(counter)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Count(b, []byte("\n"))
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := m.Files[zst]; !held {
		t.Fatal("first pass with zstd did not hold the compressed rollout")
	}
	if _, held := m.Sessions["codex:zstd-installed-thread"]; !held {
		t.Fatal("decoded rollout session missing from manifest")
	}
	before := calls()
	if before == 0 {
		t.Fatal("first pass with zstd did not run the decoder")
	}
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	if got := calls(); got != before {
		t.Errorf("unchanged pass ran zstd %d more times", got-before)
	}
}

// zstdStubRollouts writes a plain rollout and a compressed one, and returns
// the bin directory a later installZstdStub puts zstd in, already on PATH.
func zstdStubRollouts(t *testing.T, tmp, thread string) (bin, plain, zst string) {
	t.Helper()
	bin = filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "31")
	plain = filepath.Join(dir, "rollout-2026-07-31T00-00-00-cx-1.jsonl")
	zst = filepath.Join(dir, "rollout-2026-07-20T00-00-00-"+thread+".jsonl.zst")
	write(t, plain, codexRolloutHead)
	write(t, zst, string([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00, 0x01, 0x02}))
	return bin, plain, zst
}

// installZstdStub puts a zstd on PATH that prints a rollout for thread.
func installZstdStub(t *testing.T, tmp, bin, thread string) {
	t.Helper()
	rollout := filepath.Join(tmp, "decoded.jsonl")
	write(t, rollout, strings.ReplaceAll(codexRolloutHead, "cx-1", thread))
	t.Setenv("DEJA_TEST_ZSTD_ROLLOUT", rollout)
	if runtime.GOOS == "windows" {
		write(t, filepath.Join(bin, "zstd.bat"), "@echo off\r\n@type \"%DEJA_TEST_ZSTD_ROLLOUT%\"\r\n@exit /b 0\r\n")
		return
	}
	write(t, filepath.Join(bin, "zstd"), "#!/bin/sh\nwhile IFS= read -r line; do\n  printf '%s\n' \"$line\"\ndone <\"$DEJA_TEST_ZSTD_ROLLOUT\"\n")
	if err := os.Chmod(filepath.Join(bin, "zstd"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A rollout waiting for zstd is not a change: with nothing else changed, the
// next pass leaves manifest.gob and sessions.gob as they are. It read as
// changed on every pass, so every search rewrote both (#5105).
func TestZstdMissingLeavesIndexAlone(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	_, plain, zst := zstdStubRollouts(t, tmp, "zstd-waiting-thread")

	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := m.Files[zst]; held {
		t.Fatal("compressed rollout held as read without zstd")
	}
	if _, waiting := m.Deferred[zst]; !waiting {
		t.Fatal("compressed rollout not held as waiting for zstd")
	}
	if _, held := m.Files[plain]; !held {
		t.Fatal("plain rollout missing from manifest")
	}
	if fresh, _ := UpToDate(idx, ""); !fresh {
		t.Fatal("index with only a rollout waiting for zstd reads as stale")
	}

	read := func() (manifest, sessions []byte) {
		var err error
		if manifest, err = os.ReadFile(filepath.Join(idx, "manifest.gob")); err != nil {
			t.Fatal(err)
		}
		if sessions, err = os.ReadFile(filepath.Join(idx, "sessions.gob")); err != nil {
			t.Fatal(err)
		}
		return manifest, sessions
	}
	manifestBefore, sessionsBefore := read()
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	manifestAfter, sessionsAfter := read()
	if !bytes.Equal(manifestBefore, manifestAfter) {
		t.Error("unchanged pass rewrote manifest.gob")
	}
	if !bytes.Equal(sessionsBefore, sessionsAfter) {
		t.Error("unchanged pass rewrote sessions.gob")
	}
}

// The tool fingerprint keeps zstd=true once any deja saw zstd, so a rollout a
// hook deferred with a short PATH is read by the next pass that has zstd even
// though the fingerprint never changes (#5105).
func TestDeferredRolloutReadWhenFingerprintHadZstd(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	bin, _, zst := zstdStubRollouts(t, tmp, "zstd-hook-thread")

	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, waiting := m.Deferred[zst]; !waiting {
		t.Fatal("compressed rollout not held as waiting for zstd")
	}
	// As a terminal pass with zstd would have left it.
	hadSQLite, _ := parseToolFingerprint(m.ToolFingerprint)
	m.ToolFingerprint = toolFingerprint(hadSQLite, true)
	if err = writeManifestOnly(idx, m); err != nil {
		t.Fatal(err)
	}

	installZstdStub(t, tmp, bin, "zstd-hook-thread")
	if err = Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err = readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, held := m.Files[zst]; !held {
		t.Fatal("pass with zstd did not read the deferred rollout")
	}
	if _, held := m.Sessions["codex:zstd-hook-thread"]; !held {
		t.Fatal("deferred rollout's session missing after zstd arrived")
	}
	if _, waiting := m.Deferred[zst]; waiting {
		t.Fatal("rollout still held as waiting after it was read")
	}
}

// On a first build big enough to publish its newest slice early, the rollouts
// waiting for zstd are still held as waiting once the build ends; the early
// publish must not drain the list the full build commits (#5110).
func TestFirstBuildKeepsDeferredAcrossNewestFirstPublish(t *testing.T) {
	tmp := hermeticIndexEnv(t)
	_, _, zst := zstdStubRollouts(t, tmp, "zstd-big-thread")
	dir := filepath.Join(os.Getenv("DEJA_CODEX_ROOT"), "sessions", "2026", "07", "30")
	for i := range partialPublishFrom + 10 {
		id := fmt.Sprintf("big-%04d", i)
		write(t, filepath.Join(dir, "rollout-2026-07-30T00-00-00-"+id+".jsonl"),
			strings.ReplaceAll(codexRolloutHead, "cx-1", id))
	}

	idx := filepath.Join(tmp, "idx")
	if err := Ensure(idx, "", false, nil); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(idx)
	if err != nil {
		t.Fatal(err)
	}
	if _, waiting := m.Deferred[zst]; !waiting {
		t.Fatalf("first build left Deferred empty (%d entries), want the compressed rollout", len(m.Deferred))
	}
}
