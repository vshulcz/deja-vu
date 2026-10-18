package model

import "testing"

func TestSpawnedKind(t *testing.T) {
	for kind, want := range map[string]bool{"sidechain": true, "subagent": true, "subagent_fork": true, "fork": false, "": false} {
		if got := SpawnedKind(kind); got != want {
			t.Errorf("SpawnedKind(%q) = %v, want %v", kind, got, want)
		}
	}
}
