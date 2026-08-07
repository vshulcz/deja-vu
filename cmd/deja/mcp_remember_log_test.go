package main

import (
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/usage"
)

// #682 put the read tools in the journal. remember is the one tool that writes
// to the store and it recorded nothing: an MCP session that added a note left
// `deja log` empty, so the note appeared in recall with nothing saying an agent
// had put it there.
func TestMCPRememberIsLogged(t *testing.T) {
	withStatsStores(t)
	dir := os.Getenv("DEJA_INDEX_DIR")

	text := "grobnix protocol must use widdershins ordering"
	ack, err := callMCPTool(dir, "remember", []byte(`{"text":`+quoteJSON(text)+`,"project":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ack, "Remembered") {
		t.Fatalf("remember ack = %q", ack)
	}

	events := usage.Events(dir, 10)
	var got *usage.Event
	for i := range events {
		if events[i].Kind == usage.KindRemember {
			got = &events[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("remember wrote a note and left no log entry; log has %+v", events)
	}
	if got.Bytes != len(text) {
		t.Errorf("logged %d bytes, note was %d", got.Bytes, len(text))
	}
	if got.Sessions != 1 {
		t.Errorf("logged %d sessions, want 1", got.Sessions)
	}
	if got.Empty {
		t.Error("a stored note logged as an empty result")
	}
}

func quoteJSON(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
