package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// clineForkStore holds a Cline CLI session and a /fork of it written after the
// build: the fork's first turn builds its rules and runs the prompt builder
// before any build has seen it. A fork is a copy of its source's messages,
// ids and timestamps included, under a new session id, and its manifest names
// the source in metadata.fork.forkedFromSessionId (CLI 3.0.73, observed live).
func clineForkStore(t *testing.T) (dir, source, fork string) {
	t.Helper()
	return clineForkStoreWith(t, false)
}

// clineForkStoreWith also indexes a sibling fork of the source that went on
// past what it copied, when sibling is set.
func clineForkStoreWith(t *testing.T, sibling bool) (dir, source, fork string) {
	t.Helper()
	tmp := hermeticEnv(t)
	root := filepath.Join(tmp, "cline-data")
	t.Setenv("CLINE_DATA_DIR", root)
	t.Setenv("CLINE_DIR", "")
	t.Setenv("CLINE_SESSION_DATA_DIR", "")
	t.Setenv("DEJA_CLINE_ROOT", "")
	sessions := filepath.Join(root, "sessions")
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Millisecond)
	write := func(id, from, user, reply string, at time.Time, more ...any) {
		t.Helper()
		d := filepath.Join(sessions, id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		meta := map[string]any{"title": user}
		if from != "" {
			meta["fork"] = map[string]any{"forkedFromSessionId": from, "forkedAt": t0.Format(time.RFC3339Nano), "source": "cli"}
		}
		man := mustMarshal(t, map[string]any{
			"version": 1, "session_id": id, "source": "cli", "status": "idle",
			"started_at": at.Format(time.RFC3339Nano), "cwd": "/w/p", "workspace_root": "/w/p",
			"prompt": user, "metadata": meta,
		})
		msgs := mustMarshal(t, map[string]any{
			"version": 1, "agent": "lead", "sessionId": id,
			"messages": append([]any{
				map[string]any{"id": "u-" + id[:6], "role": "user", "ts": at.UnixMilli(),
					"content": []any{map[string]any{"type": "text", "text": `<user_input mode="act">` + user + `</user_input>`}}},
				map[string]any{"id": "a-" + id[:6], "role": "assistant", "ts": at.Add(5 * time.Second).UnixMilli(),
					"content": []any{map[string]any{"type": "text", "text": reply}}},
			}, more...),
		})
		if err := os.WriteFile(filepath.Join(d, id+".json"), man, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, id+".messages.json"), msgs, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 20 {
		write(fmt.Sprintf("17900000%05d_old%02d", i, i), "", fmt.Sprintf("rename table%d in the billing schema", i), "done", t0.Add(-time.Duration(i+2)*time.Hour))
	}
	write("1790000099999_prior", "", "the retry loop in fetch.go spins on HTTP 500 again", "Capped the retry loop in fetch.go at MaxAttempts five.", t0.Add(-time.Hour))
	source = "1790000100000_srcaa"
	// The fork's copy is written by hand below with the source's own times;
	// the source is indexed first.
	write(source, "", forkQuestion, "Looking at the retry loop in fetch.go now.", t0)

	if sibling {
		write(clineSibling, source, forkQuestion, "Looking at the retry loop in fetch.go now.", t0,
			map[string]any{"id": "u-more", "role": "user", "ts": t0.Add(time.Minute).UnixMilli(),
				"content": []any{map[string]any{"type": "text", "text": "use exponential backoff with jitter there"}}},
			map[string]any{"id": "a-more", "role": "assistant", "ts": t0.Add(time.Minute + 5*time.Second).UnixMilli(),
				"content": []any{map[string]any{"type": "text", "text": "Switched fetch.go to exponential backoff with jitter."}}})
	}
	dir = index.DefaultDir()
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	fork = "1790000200000_forkb"
	write(fork, source, forkQuestion, "Looking at the retry loop in fetch.go now.", t0)
	return dir, source, fork
}

const clineSibling = "1790000150000_sibfk"

// A Cline /fork's first turn got a digest holding the session it was forked
// from, whose turns it already has: Cline's plugin sends no transcript path,
// and the hook had no way to read a Cline session off its store by id.
func TestAClineForksDigestLeavesOutItsSource(t *testing.T) {
	dir, source, fork := clineForkStore(t)
	digest := func(sid string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "cwd": "/w/p"})
		withHookStdin(t, string(payload))
		return captureStdout(t, func() {
			if err := runHookContext(dir, true); err != nil {
				t.Error(err)
			}
		})
	}
	// The fork first: a session once shown sits out the project's cooldown.
	if out := digest(fork); !strings.Contains(out, "<deja-recall>") || strings.Contains(out, source) {
		t.Errorf("a Cline fork was handed its own source as history:\n%s", out)
	}
	if out := digest("1790000300000_ctrlc"); !strings.Contains(out, source) {
		t.Errorf("the control was not shown the source, so this proves nothing:\n%s", out)
	}
}

// The prompt builder's recall on the fork's first prompt reads the same head.
func TestAClineForksPromptRecallLeavesOutItsSource(t *testing.T) {
	dir, source, fork := clineForkStore(t)
	ask := func(sid string) string {
		payload := mustMarshal(t, map[string]string{"session_id": sid, "cwd": "/w/p", "prompt": forkQuestion})
		var out strings.Builder
		if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	if out := ask(fork); strings.Contains(out, source) {
		t.Errorf("a Cline fork's prompt recall named its own source:\n%s", out)
	}
	if out := ask("1790000300000_ctrlc"); !strings.Contains(out, source) {
		t.Errorf("the control was not told about the source, so this proves nothing:\n%s", out)
	}
}

// Once indexed, the fork names its source in the index: the harness said so.
func TestAClineForkIsIndexedAsAForkOfItsSource(t *testing.T) {
	dir, source, fork := clineForkStore(t)
	if err := index.Ensure(dir, "", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	s, ok, err := index.FindByID(dir, fork)
	if err != nil || !ok {
		t.Fatalf("fork not indexed: %v", err)
	}
	if s.Kind != "fork" || s.Parent != source {
		t.Errorf("fork indexed as kind %q parent %q, want fork of %s", s.Kind, s.Parent, source)
	}
	if src, _, err := index.FindByID(dir, source); err != nil || src.Kind != "" || src.Parent != "" {
		t.Errorf("the source itself is not a fork: kind %q parent %q err %v", src.Kind, src.Parent, err)
	}
}

// A sibling fork that went on past the source holds work the new fork never
// saw, so the new fork's first prompt still gets it; the source stays out.
func TestAClineForksFirstPromptKeepsASiblingThatWentOn(t *testing.T) {
	dir, _, fork := clineForkStoreWith(t, true)
	payload := mustMarshal(t, map[string]string{"session_id": fork, "cwd": "/w/p", "prompt": "did we add exponential backoff with jitter to fetch.go"})
	var out strings.Builder
	if err := runHookPrompt(dir, strings.NewReader(string(payload)), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), clineSibling) {
		t.Errorf("a sibling fork that went on was left out of a new fork's recall:\n%s", out.String())
	}
}

// The MCP tool reads the live stamps: a fork stamped before any build has seen
// it leaves out its source and keeps a sibling that went on.
func TestALiveClineForkLeavesOutItsSourceOverMCP(t *testing.T) {
	dir, source, fork := clineForkStoreWith(t, true)
	markSessionLive(dir, fork)
	live := liveLineage(dir)
	if !live[source] {
		t.Errorf("a live Cline fork's MCP lineage misses its source: %v", live)
	}
	if live[clineSibling] {
		t.Errorf("a sibling that went on was hidden from a live fork's MCP recall: %v", live)
	}
}
