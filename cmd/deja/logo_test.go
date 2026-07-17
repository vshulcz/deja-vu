package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPrintLogo(t *testing.T) {
	var b bytes.Buffer
	printLogo(&b, "memory for coding agents")
	out := b.String()
	if !strings.Contains(out, "●") || !strings.Contains(out, "▲") || !strings.Contains(out, "deja-vu") || !strings.Contains(out, "memory for coding agents") {
		t.Fatalf("logo output: %q", out)
	}
	if len(strings.Split(strings.TrimSpace(out), "\n")) != 5 {
		t.Fatalf("loop mark should be 5 lines: %q", out)
	}
}

func TestLogoWanted(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if logoWanted(f) {
		t.Fatal("regular file must not want a logo")
	}
	t.Setenv("NO_COLOR", "1")
	if logoWanted(os.Stdout) {
		t.Fatal("NO_COLOR must suppress the logo")
	}
}
