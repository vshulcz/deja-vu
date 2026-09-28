package main

import (
	"strings"
	"testing"
)

// warmup threw its arguments away, so `deja warmup --rebuild` ran the ordinary
// incremental build, said nothing and exited 0; the rebuild lives on
// `deja index --rebuild` (#4109).
func TestWarmupRefusesArguments(t *testing.T) {
	withTempStores(t)
	for _, tc := range []struct {
		arg  string
		want string
	}{
		{"--rebuild", "deja index --rebuild"},
		{"-rebuild", "deja index --rebuild"},
		{"--rebuld", "deja index --rebuild"},
		{"--json", "takes no arguments"},
		{"extra", "takes no arguments"},
	} {
		_, err := captureRun(t, "warmup", tc.arg)
		if err == nil {
			t.Errorf("warmup %s was accepted", tc.arg)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.arg) {
			t.Errorf("warmup %s said %v, want it to name %q and %q", tc.arg, err, tc.arg, tc.want)
		}
	}
}

// version ignored its arguments too; harmless, but it is the same trap (#4109).
func TestVersionRefusesArguments(t *testing.T) {
	for _, cmd := range []string{"version", "--version", "-version"} {
		out, err := captureRun(t, cmd)
		if err != nil || !strings.HasPrefix(out, "deja ") {
			t.Fatalf("bare %s: %q err=%v", cmd, out, err)
		}
		if _, err := captureRun(t, cmd, "--json"); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
			t.Errorf("%s --json: err=%v", cmd, err)
		}
	}
}
