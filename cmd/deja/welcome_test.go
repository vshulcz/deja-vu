package main

import (
	"strings"
	"testing"
)

// A bare `deja` into a pipe or a script used to print the full usage block,
// which starts with commands only harnesses call. Issue #4621 wants a short
// welcome instead: what is indexed and one thing to try, with the full list
// kept behind `deja help`. This test pins the welcome shape.
func TestBareDejaPrintsWelcomeNotFullUsage(t *testing.T) {
	hermeticEnv(t)
	out, err := captureRun(t)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("welcome fell back to the full usage block:\n%s", out)
	}
	for _, want := range []string{"nothing indexed yet", "deja <query>", "deja help"} {
		if !strings.Contains(out, want) {
			t.Fatalf("welcome is missing %q:\n%s", want, out)
		}
	}

	withTempStores(t)
	if _, err = captureRun(t, "index"); err != nil {
		t.Fatal(err)
	}
	out, err = captureRun(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "indexed across") {
		t.Fatalf("welcome does not say what is indexed:\n%s", out)
	}
}
