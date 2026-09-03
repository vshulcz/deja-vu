package main

import (
	"strings"
	"testing"
)

// The opencode plugin caches what the session-start hook answered so it does
// not shell out on every turn. An empty answer was cached the same way, and its
// reasons are not alike: no history is permanent, while a locked index, a call
// that did not get through, or an upgrade replacing the binary are over by the
// next turn.
//
// Driven against the installed plugin with a deja whose first call failed and
// no build running, turns two and three stayed silent as well — the session
// lost its memory for good over one bad moment.
func TestOpencodePluginAsksAgainAfterAnEmptyAnswer(t *testing.T) {
	js := opencodePluginJS("/bin/deja")
	compact := strings.Join(strings.Fields(js), "")

	if !strings.Contains(compact, "constemptyRetries=") {
		t.Error("no bound on how often the session asks again")
	}
	if !strings.Contains(compact, "if(asks<emptyRetries)cache.delete(key)") {
		t.Error("the cached emptiness is never dropped, so the session cannot recover")
	}
	// Per session: one session's bad moment must not spend another's retries.
	if !strings.Contains(compact, "empties.set(key,asks)") {
		t.Error("the count is not kept per session")
	}
	// The build notice keeps its own path — it is the one empty answer worth
	// saying out loud, and it already dropped the cache before this existed.
	if !strings.Contains(compact, "told.add(key)") || !strings.Contains(compact, "cache.delete(key)") {
		t.Error("the warmup notice lost its own recovery")
	}
}
