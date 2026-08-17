package index

import (
	"path/filepath"
	"testing"
)

// writeGob opens with O_TRUNC and only then encodes, so a failed encode leaves
// the file it was overwriting destroyed — emptied, in the case below, where the
// encoder refuses the value before writing a byte.
//
// That is harmless where the file cannot already exist: the full builds write
// each sidecar into a directory they just created. It is not harmless where a
// good file has just been carried in and is being rewritten, because a reader
// treats an undecodable sidecar as having no content at all — one failed write
// silences the feature until the next full rebuild.
//
// A channel is what makes the encoder refuse. A struct with a channel field
// does not: gob quietly skips fields it cannot handle, which is worth knowing
// before writing a test that relies on one.
func TestAFailedGobWriteKeepsOrLosesWhatWasThere(t *testing.T) {
	dir := t.TempDir()
	const good = "the file that was already there"
	fail := make(chan int)

	t.Run("writeGob loses it", func(t *testing.T) {
		p := filepath.Join(dir, "plain.gob")
		if err := writeGob(p, good); err != nil {
			t.Fatal(err)
		}
		if err := writeGob(p, fail); err == nil {
			t.Fatal("the encode was expected to fail; this test no longer proves anything")
		}
		var back string
		if err := readGob(p, &back); err == nil && back == good {
			t.Skip("writeGob no longer truncates before encoding — the hazard is gone")
		}
	})

	t.Run("writeGobAtomic keeps it", func(t *testing.T) {
		p := filepath.Join(dir, "atomic.gob")
		if err := writeGobAtomic(p, good); err != nil {
			t.Fatal(err)
		}
		if err := writeGobAtomic(p, fail); err == nil {
			t.Fatal("the encode was expected to fail; this test no longer proves anything")
		}
		var back string
		if err := readGob(p, &back); err != nil || back != good {
			t.Errorf("a failed atomic write lost the previous content: %q, %v", back, err)
		}
	})
}
