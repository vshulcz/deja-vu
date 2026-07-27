package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Clients that stringify their arguments are common enough that a Go type
// error is the wrong answer: it fails the call and leaks internal field names
// into the protocol.
func TestMCPAcceptsNumericStrings(t *testing.T) {
	var a struct {
		Limit  mcpNumber `json:"limit"`
		Offset mcpNumber `json:"offset"`
	}
	if err := json.Unmarshal([]byte(`{"limit":"3","offset":"7"}`), &a); err != nil {
		t.Fatalf("numeric strings rejected: %v", err)
	}
	if int(a.Limit) != 3 || int(a.Offset) != 7 {
		t.Fatalf("limit=%v offset=%v", a.Limit, a.Offset)
	}
	if err := json.Unmarshal([]byte(`{"limit":4}`), &a); err != nil || int(a.Limit) != 4 {
		t.Fatalf("plain number broke: %v (%v)", err, a.Limit)
	}
	// An empty string means the client sent no value, not a zero it chose.
	if err := json.Unmarshal([]byte(`{"limit":""}`), &a); err != nil {
		t.Fatalf("empty string rejected: %v", err)
	}
	err := json.Unmarshal([]byte(`{"limit":"abc"}`), &a)
	if err == nil {
		t.Fatal("garbage accepted")
	}
	if strings.Contains(err.Error(), "float64") || strings.Contains(err.Error(), "Go struct") {
		t.Fatalf("error leaks internals: %v", err)
	}
}
