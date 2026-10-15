package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A deferred hook's payload is the user's prompt, and it went through
// $TMPDIR on the way in, as did the hook's answer on the way out; a hook the
// harness killed left both there (#5052). With no usable temporary directory
// at all the payload still arrives and the answer is still caught.
func TestDeferredHookPassesItsPayloadWithoutTempFiles(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, blocked)
	}
	// Larger than a pipe's buffer, so the writer has to run beside the reader.
	payload := `{"prompt":"my secret prompt text ` + strings.Repeat("x", 200<<10) + `"}`
	var read []byte
	out, err := captureDeferredStdout(func() error {
		return withStdin([]byte(payload), func() error {
			b, err := io.ReadAll(os.Stdin)
			read = b
			fmt.Print("answer ", len(b))
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(read) != payload {
		t.Fatalf("the hook read %d bytes of a %d-byte payload", len(read), len(payload))
	}
	if want := fmt.Sprint("answer ", len(payload)); string(out) != want {
		t.Fatalf("captured %q, want %q", out, want)
	}
}

// A hook that never reads its stdin still returns: the writer stops when the
// read end closes.
func TestDeferredHookThatIgnoresItsPayloadReturns(t *testing.T) {
	payload := strings.Repeat("y", 1<<20)
	if err := withStdin([]byte(payload), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
