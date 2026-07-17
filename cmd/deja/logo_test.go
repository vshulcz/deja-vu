package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestPrintLogo(t *testing.T) {
	var b bytes.Buffer
	printLogo(&b, brandInfo())
	out := b.String()
	if !strings.Contains(out, "█") || !strings.Contains(out, "deja-vu") || !strings.Contains(out, "memory for coding agents") {
		t.Fatalf("logo output: %q", out)
	}
	if n := len(strings.Split(strings.TrimSpace(out), "\n")); n != len(loopArt) {
		t.Fatalf("mark should be %d lines, got %d: %q", len(loopArt), n, out)
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
