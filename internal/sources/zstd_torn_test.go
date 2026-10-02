package sources

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// zstdFrame compresses one chunk as a frame of its own, the way dsh appends
// its log.
func zstdFrame(t *testing.T, chunk string) []byte {
	t.Helper()
	cmd := exec.Command("zstd", "-q", "-c")
	cmd.Stdin = strings.NewReader(chunk)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("zstd: %v", err)
	}
	return out
}

// tornLog is body one frame per line, then one more frame cut short: what a
// log looks like when its writer died mid-frame, or when it is read mid-append.
func tornLog(t *testing.T, body, tail string) []byte {
	t.Helper()
	var b bytes.Buffer
	for _, line := range strings.SplitAfter(body, "\n") {
		if line != "" {
			b.Write(zstdFrame(t, line))
		}
	}
	last := zstdFrame(t, tail)
	b.Write(last[:len(last)-7])
	return b.Bytes()
}

func hasMessage(s model.Session, role, text string) bool {
	for _, m := range s.Messages {
		if m.Role == role && m.Text == text {
			return true
		}
	}
	return false
}

// One torn frame at the end of a dsh log dropped every complete frame before
// it, and doctor called the whole store unreadable (#4294).
func TestParseDeepSeekFileKeepsTheFramesBeforeATornOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	tail := `{"type":"assistant/message","seq":50,"time":1787320264000,"data":{"message":{"role":"assistant","content":[{"type":"text","text":"and 60 on the replica"}]}}}` + "\n"
	dir := filepath.Join(t.TempDir(), "--work-pgbouncer-lab--", "session-torn")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl.zstd")
	if err := os.WriteFile(path, tornLog(t, deepSeekLog, tail), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Errorf("a torn tail failed the whole file: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("the complete frames were not kept: %+v", ss)
	}
	// By role and text, not by position: records other parsers add beside the
	// prose must not move what this checks.
	if !hasMessage(ss[0], "user", "сколько коннектов держим на шард?") ||
		!hasMessage(ss[0], "assistant", "держим 40 на шард") {
		t.Errorf("the turns before the torn frame are missing: %+v", ss[0].Messages)
	}
	if hasMessage(ss[0], "assistant", "and 60 on the replica") {
		t.Errorf("the torn frame's half-written line was read as a turn")
	}
	// The one file is named, not the store.
	if DiagMalformedCounts()[path] == 0 {
		t.Errorf("the torn file is not named in the ingest diagnostics")
	}
}

// The Codex .zst reader goes through the same CLI and had the same rule.
func TestCodexKeepsTheFramesBeforeATornOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rollout-thread-7.jsonl.zst")
	tail := `{"timestamp":"2026-07-17T09:00:03.000Z","type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"and the retry"}]}}` + "\n"
	if err := os.WriteFile(path, tornLog(t, codexRolloutFixture, tail), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseCodexRollout(path)
	if err != nil {
		t.Errorf("a torn tail failed the whole rollout: %v", err)
	}
	if len(ss) != 1 || !hasMessage(ss[0], "assistant", "the advisory lock was never released") {
		t.Fatalf("the complete frames were not kept: %+v", ss)
	}
	if DiagMalformedCounts()[path] == 0 {
		t.Errorf("the torn file is not named in the ingest diagnostics")
	}
}

// A corrupt frame in the middle is not a torn tail: zstd stops at it, and the
// complete frames after it are as good as the ones before. Each frame is
// decoded on its own then, and only the bad one is lost.
func TestParseDeepSeekFileKeepsTheFramesAfterACorruptOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	var b bytes.Buffer
	for _, line := range strings.SplitAfter(deepSeekLog, "\n") {
		if line == "" {
			continue
		}
		frame := zstdFrame(t, line)
		if strings.Contains(line, `"tool/result"`) {
			// A byte flipped inside the block, before the checksum.
			frame[len(frame)-6] ^= 0xff
		}
		b.Write(frame)
	}
	dir := filepath.Join(t.TempDir(), "--work-pgbouncer-lab--", "session-corrupt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl.zstd")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil {
		t.Errorf("one corrupt frame failed the whole file: %v", err)
	}
	if len(ss) != 1 || !hasMessage(ss[0], "assistant", "сейчас посмотрю конфиг") {
		t.Fatalf("the frames before the corrupt one were not kept: %+v", ss)
	}
	if !hasMessage(ss[0], "assistant", "держим 40 на шард") {
		t.Errorf("the frames after the corrupt one were dropped: %+v", ss[0].Messages)
	}
	if hasMessage(ss[0], "tool-output", "pgbouncer pool_size = 40") {
		t.Errorf("the corrupt frame was read")
	}
	if DiagMalformedCounts()[path] != 1 {
		t.Errorf("malformed = %d, want the one corrupt frame", DiagMalformedCounts()[path])
	}
}

// OpenClaw's compressed archives go through the same reader.
func TestOpenClawKeepsTheFramesBeforeATornOne(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	root := t.TempDir()
	sessions := filepath.Join(root, "main", "sessions")
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_OPENCLAW_ROOT", root)
	archive := filepath.Join(sessions, "dddd4444.jsonl.deleted.1788000003.zst")
	tail := `{"type":"message","message":{"role":"user","content":[{"type":"text","text":"and the replica"}]},"timestamp":"2026-08-02T10:02:00Z"}` + "\n"
	body := tornLog(t, openclawArchiveBody("dddd4444", "we settled on one shard"), tail)
	if err := os.WriteFile(archive, body, 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseOpenClawFile(archive)
	if err != nil {
		t.Errorf("a torn tail failed the whole archive: %v", err)
	}
	if len(ss) != 1 || !hasMessage(ss[0], "user", "we settled on one shard") {
		t.Fatalf("the complete frames were not kept: %+v", ss)
	}
	if DiagMalformedCounts()[archive] == 0 {
		t.Errorf("the torn archive is not named in the ingest diagnostics")
	}
}

// A corrupt frame header, not just a corrupt block, desynced the split: the
// frames after it were lost with it, and a raw block whose size grew past the
// next frame made zstd say "premature end" and spill that frame's bytes into
// the output as if the file were torn.
func TestZstdDecodeFileResyncsAfterACorruptHeader(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not installed")
	}
	a, b, c := zstdFrame(t, "aaaa line one\n"), zstdFrame(t, "bbbb line two\n"), zstdFrame(t, "cccc line three\n")
	for name, corrupt := range map[string]func([]byte){
		"magic":      func(f []byte) { f[0] ^= 0xff },
		"block size": func(f []byte) { f[7] ^= 0x40 },
	} {
		bad := append([]byte{}, b...)
		corrupt(bad)
		path := filepath.Join(t.TempDir(), "session.jsonl.zstd")
		got, err := zstdDecodeFile(path, "deepseek", bytes.Join([][]byte{a, bad, c}, nil))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != "aaaa line one\ncccc line three\n" {
			t.Errorf("%s: decoded %q, want the frames on both sides of the bad one", name, got)
		}
		if DiagMalformedCounts()[path] != 1 {
			t.Errorf("%s: malformed = %d, want 1", name, DiagMalformedCounts()[path])
		}
	}
}

// The splitter reads lengths from untrusted headers: none of these may loop,
// panic, or claim more bytes than it was given.
func TestZstdFrameLenOnHostileHeaders(t *testing.T) {
	magic := []byte{0x28, 0xB5, 0x2F, 0xFD}
	frame := func(b ...byte) []byte { return append(append([]byte{}, magic...), b...) }
	cases := map[string][]byte{
		"empty":               nil,
		"magic only":          magic,
		"truncated header":    frame(0xC0, 0x00, 0x01),
		"huge content size":   frame(0xE0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01, 0x00, 0x00),
		"dictionary id":       frame(0x23, 0x01, 0x02, 0x03, 0x04, 0x05, 0x01, 0x00, 0x00),
		"checksum, cut":       frame(0x24, 0x05, 0x01, 0x00, 0x00, 0xAA),
		"zero-length block":   frame(0x20, 0x00, 0x01, 0x00, 0x00),
		"block past the end":  frame(0x20, 0x00, 0xF9, 0xFF, 0x0F, 0x00),
		"reserved block type": frame(0x20, 0x00, 0x07, 0x00, 0x00),
		"never-last blocks":   frame(0x20, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00),
		"skippable, huge":     {0x50, 0x2A, 0x4D, 0x18, 0xFF, 0xFF, 0xFF, 0xFF, 0x00},
		"skippable, empty":    {0x5F, 0x2A, 0x4D, 0x18, 0x00, 0x00, 0x00, 0x00},
	}
	want := map[string]int{"zero-length block": 9, "skippable, empty": 8, "dictionary id": 13, "huge content size": 16}
	for name, raw := range cases {
		if n := zstdFrameLen(raw); n != want[name] {
			t.Errorf("%s: zstdFrameLen = %d, want %d", name, n, want[name])
		}
		frames, rest := zstdSplitFrames(raw)
		n := len(rest)
		for _, f := range frames {
			n += len(f)
		}
		if n != len(raw) {
			t.Errorf("%s: split covers %d of %d bytes", name, n, len(raw))
		}
	}
}
