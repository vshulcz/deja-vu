package main

import (
	"os"
	"regexp"
	"testing"
)

// The ssh push settles watermarks only after the remote acknowledges the batch
// (#215), and namespaces them per peer (#982). `index.Export` does neither — it
// advances the unnamed watermark as it writes. Nothing on the ssh path may
// reach for it: `exportBatches` did, kept alive by one test line for a year
// after its caller went away (#1891), and wiring it back would have undone both
// rules with nothing on any surface saying so.
func TestTheSSHPathTakesNoUnnamedWatermark(t *testing.T) {
	src, err := os.ReadFile("sync_ssh.go")
	if err != nil {
		t.Fatal(err)
	}
	// index.Export( exactly — not ExportFull, which ignores watermarks by
	// design, nor ExportDeferred, which is the acknowledged-delivery form.
	unnamed := regexp.MustCompile(`index\.Export\(`)
	if m := unnamed.FindString(string(src)); m != "" {
		t.Errorf("the ssh path exports under the unnamed watermark: %s", m)
	}
}
