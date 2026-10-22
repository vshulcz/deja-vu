package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// openclaw-auto writes a hook pack that fires only in gateway mode and a plugin
// that carries the digest and per-prompt recall everywhere else. The row read
// the hook pack alone, so with the plugin gone a local session got no recall
// and doctor still said wired (#4580).
func TestDoctorOpenClawRowSeesTheMissingPlugin(t *testing.T) {
	hermeticEnv(t)
	if _, err := installOpenClawAuto("/bin/deja", false); err != nil {
		t.Fatalf("install: %v", err)
	}
	row := func() (string, string) {
		var out bytes.Buffer
		doctorAutoRecall(&out)
		for l := range strings.SplitSeq(out.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "openclaw ") {
				state, _ := autoWiringState(openclawAutoRow(t))
				return l, state
			}
		}
		t.Fatalf("no openclaw row:\n%s", out.String())
		return "", ""
	}
	// The control: both halves there.
	if line, state := row(); state != "wired" || !strings.Contains(line, "wired") {
		t.Fatalf("a full install does not read wired, so this measures nothing: %q (%s)", line, state)
	}
	if err := os.RemoveAll(filepath.Join(sources.OpenClawStateDir(), "extensions", openclawPluginID)); err != nil {
		t.Fatal(err)
	}
	line, state := row()
	if state == "wired" || strings.Contains(line, " wired ") {
		t.Errorf("the plugin is gone and the row still says wired: %q (json %s)", line, state)
	}
	if !strings.Contains(line, "openclaw-auto") {
		t.Errorf("the row does not say how to put the plugin back: %q", line)
	}
}

func openclawAutoRow(t *testing.T) autoWiring {
	t.Helper()
	for _, a := range autoWirings() {
		if a.name == "openclaw" {
			return a
		}
	}
	t.Fatal("no openclaw row in autoWirings")
	return autoWiring{}
}
