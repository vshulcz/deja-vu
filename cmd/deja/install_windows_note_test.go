package main

import (
	"runtime"
	"testing"
)

func TestMCPCommandArgsPerOS(t *testing.T) {
	command, args := mcpCommandArgs("/usr/local/bin/deja")
	if runtime.GOOS == "windows" {
		if command != "cmd" || len(args) != 3 || args[0] != "/c" || args[2] != "mcp" {
			t.Fatalf("windows form = %q %v", command, args)
		}
	} else {
		if command != "/usr/local/bin/deja" || len(args) != 1 || args[0] != "mcp" {
			t.Fatalf("unix form = %q %v", command, args)
		}
	}
	entry := mcpServerEntry("/usr/local/bin/deja")
	if entry["type"] != "stdio" || entry["command"] != command {
		t.Fatalf("entry = %#v", entry)
	}
}
