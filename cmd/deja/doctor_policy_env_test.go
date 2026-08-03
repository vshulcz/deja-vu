package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// doctor is the screen someone opens to find out what is allowed, and with no
// policy file it decided on the file's absence: "every origin activates
// everywhere", while DEJA_AUTORECALL_LOCAL_ONLY held the auto path to local
// sessions only (#939).
func TestDoctorPolicySeesTheEnvironmentOverride(t *testing.T) {
	tmp := hermeticEnv(t)
	t.Setenv("DEJA_POLICY_FILE", filepath.Join(tmp, "no-such-policy.json"))

	var out bytes.Buffer
	doctorPolicy(&out)
	if !strings.Contains(out.String(), "every origin activates everywhere") {
		t.Errorf("an unrestricted machine stopped saying so:\n%s", out.String())
	}

	t.Setenv("DEJA_AUTORECALL_LOCAL_ONLY", "1")
	out.Reset()
	doctorPolicy(&out)
	got := out.String()
	if strings.Contains(got, "every origin activates everywhere") {
		t.Errorf("doctor called a restricted machine unrestricted:\n%s", got)
	}
	if !strings.Contains(got, "auto") || !strings.Contains(got, "local-only") {
		t.Errorf("doctor does not name what the environment restricts:\n%s", got)
	}
	if !strings.Contains(got, "DEJA_AUTORECALL_LOCAL_ONLY") {
		t.Errorf("doctor does not say where the rule comes from:\n%s", got)
	}
	// The other two paths are untouched by that variable, and saying otherwise
	// would be its own lie.
	if !strings.Contains(got, "local+imported") {
		t.Errorf("doctor restricted paths the variable does not touch:\n%s", got)
	}
}
