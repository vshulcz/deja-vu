package model

import "testing"

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
