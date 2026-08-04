package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// A rule that activates nothing turns recall off everywhere, and the line
// people always have on screen read like an ordinary quiet day — true today,
// and still true tomorrow for a reason nothing named (#1012).
func TestStatuslineSaysWhenAPolicyTurnsMemoryOff(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "off here") {
		t.Errorf("an unrestricted machine was called off: %q", out.String())
	}

	policyFile := filepath.Join(tmp, "policy.json")
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":false,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEJA_POLICY_FILE", policyFile)

	out.Reset()
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "no recalls yet today") {
		t.Errorf("a machine with recall switched off reads as a quiet day: %q", got)
	}
	if !strings.Contains(got, "trust policy") {
		t.Errorf("the line does not name what switched it off: %q", got)
	}

	// A rule that narrows the auto path is not the same as switching it off:
	// local sessions still reach the agent.
	if err := os.WriteFile(policyFile, []byte(`{"activations":{"auto":{"local":true,"imported":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runStatusline(dir, strings.NewReader("{}"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "off here") {
		t.Errorf("a narrowed search path was reported as memory off: %q", out.String())
	}
}
