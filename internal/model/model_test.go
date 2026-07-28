package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionSetSource(t *testing.T) {
	local := Session{Project: "agent-fabric"}
	local.SetSource("workstation")
	if local.Source.Origin != "local" || local.Source.Instance != "workstation" {
		t.Fatalf("local source = %#v", local.Source)
	}
	imported := Session{Project: "imported:agent-fabric"}
	imported.SetSource("must-not-leak")
	if imported.Source.Origin != "imported" || imported.Source.Instance != "" {
		t.Fatalf("imported source = %#v", imported.Source)
	}
}

func TestUnsetSourceIsOmittedInsteadOfEmittedEmpty(t *testing.T) {
	b, err := json.Marshal(Session{ID: "x", Harness: "claude"})
	if err != nil || strings.Contains(string(b), `"source"`) {
		t.Fatalf("session JSON = %s, err = %v", b, err)
	}
}
