package main

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// servedHandler answers recall with a page for its query after the query's
// gate opens, and says it came from the number of sessions in served.
type servedHandler struct {
	slowHandler
	served map[string]int
	pages  map[string]string
	runs   atomic.Int64
	done   chan string
}

func (h *servedHandler) install(t *testing.T) {
	t.Helper()
	h.done = make(chan string, 16)
	saved := mcpHandler
	mcpHandler = func(dir string, req rpcRequest) (any, int, string) {
		if req.Method != "tools/call" {
			return handleMCP(dir, req)
		}
		var p struct {
			Arguments struct {
				Query string `json:"query"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		q := p.Arguments.Query
		h.runs.Add(1)
		<-h.gate(q)
		if req.served != nil {
			*req.served = h.served[q]
		}
		page := h.pages[q]
		if page == "" {
			page = "answer for " + q
		}
		defer func() { h.done <- q }()
		return toolText(page), 0, ""
	}
	t.Cleanup(func() { mcpHandler = saved })
}

// runPastDeadline sends q, takes the wait note, lets the work finish and
// waits until the server has kept the answer.
func (h *servedHandler) runPastDeadline(t *testing.T, s *mcpSession, q string) {
	t.Helper()
	s.call(1, `{"mode":"recall","query":"`+q+`"}`)
	if got := s.next(2 * time.Minute); got == nil || !strings.Contains(replyText(got), "has not finished") {
		t.Fatalf("want the wait note for %s, got %#v", q, got)
	}
	close(h.gate(q))
	select {
	case <-h.done:
	case <-time.After(2 * time.Minute):
		t.Fatalf("%s never finished", q)
	}
	// The answer is kept under the lock the reply loop also takes.
	time.Sleep(100 * time.Millisecond)
}

// Agents asked again with the same arguments 2 times of 47; the rest moved on
// to another call, and the answer that finished was never read. It rides on
// the next reply, once.
func TestALateRecallRidesOnTheNextCall(t *testing.T) {
	h := &servedHandler{served: map[string]int{"wal checkpoint": 3}}
	h.install(t)
	setDeadline(t, 50*time.Millisecond)
	s := openMCPSession(t, t.TempDir())
	h.runPastDeadline(t, s, "wal checkpoint")

	setDeadline(t, 0)
	close(h.gate("tide table"))
	s.call(2, `{"mode":"recall","query":"tide table"}`)
	got := replyText(s.next(2 * time.Minute))
	if !strings.HasPrefix(got, `deja: earlier answer for recall "wal checkpoint"`) {
		t.Fatalf("the next reply should lead with the late answer, got %q", got)
	}
	if !strings.Contains(got, "answer for wal checkpoint") || !strings.HasSuffix(got, "answer for tide table") {
		t.Fatalf("both answers should be in the reply, got %q", got)
	}

	s.call(3, `{"mode":"recall","query":"tide table"}`)
	if got := replyText(s.next(2 * time.Minute)); got != "answer for tide table" {
		t.Fatalf("the late answer goes out once, got %q", got)
	}
	// Handed on, it is gone: the same call asked again runs again.
	s.call(4, `{"mode":"recall","query":"wal checkpoint"}`)
	if got := replyText(s.next(2 * time.Minute)); got != "answer for wal checkpoint" {
		t.Fatalf("the same call asked again = %q", got)
	}
	if n := h.runs.Load(); n != 4 {
		t.Fatalf("handler ran %d times, want 4", n)
	}
	s.close()
}

// A recall that found nothing is not worth reading twice: it stays for the
// same call asked again and never rides on another.
func TestALateMissDoesNotRideOnTheNextCall(t *testing.T) {
	h := &servedHandler{pages: map[string]string{"quokka ledger": "No prior deja sessions matched."}}
	h.install(t)
	setDeadline(t, 50*time.Millisecond)
	s := openMCPSession(t, t.TempDir())
	h.runPastDeadline(t, s, "quokka ledger")

	setDeadline(t, 0)
	close(h.gate("tide table"))
	s.call(2, `{"mode":"recall","query":"tide table"}`)
	if got := replyText(s.next(2 * time.Minute)); got != "answer for tide table" {
		t.Fatalf("a miss rode on another call: %q", got)
	}
	s.call(3, `{"mode":"recall","query":"quokka ledger"}`)
	if got := replyText(s.next(2 * time.Minute)); got != "No prior deja sessions matched." {
		t.Fatalf("the same call asked again should get the kept miss, got %q", got)
	}
	if n := h.runs.Load(); n != 2 {
		t.Fatalf("handler ran %d times, want 2", n)
	}
	s.close()
}

// A reply carrying a late answer stays within the largest page a deja call
// returns, and a page cut to fit still closes its frame.
func TestALateRecallKeepsTheReplyInBudget(t *testing.T) {
	big := frameRecall(strings.Repeat("lantern harbour fog line\n", 600))
	h := &servedHandler{
		served: map[string]int{"lantern": 2},
		pages:  map[string]string{"lantern": big, "tide table": strings.Repeat("t", 3000)},
	}
	h.install(t)
	setDeadline(t, 50*time.Millisecond)
	s := openMCPSession(t, t.TempDir())
	h.runPastDeadline(t, s, "lantern")

	setDeadline(t, 0)
	close(h.gate("tide table"))
	s.call(2, `{"mode":"recall","query":"tide table"}`)
	got := replyText(s.next(2 * time.Minute))
	if len(got) > contextMCPBudget {
		t.Fatalf("reply is %d bytes, over %d", len(got), contextMCPBudget)
	}
	if !strings.Contains(got, `earlier answer for recall "lantern"`) || !strings.Contains(got, recallFrameFooter) {
		t.Fatalf("the late page should be cut inside a closed frame: %q", got[:200])
	}
	if !strings.HasSuffix(got, strings.Repeat("t", 3000)) {
		t.Fatal("the reply's own answer must stay whole")
	}
	s.close()
}

// The wait note itself carries a finished answer too: an agent that moved on
// to another slow call still gets the first one.
func TestALateRecallRidesOnAWaitNote(t *testing.T) {
	h := &servedHandler{served: map[string]int{"wal checkpoint": 1}}
	h.install(t)
	setDeadline(t, 50*time.Millisecond)
	s := openMCPSession(t, t.TempDir())
	h.runPastDeadline(t, s, "wal checkpoint")

	s.call(2, `{"mode":"recall","query":"tide table"}`)
	got := replyText(s.next(2 * time.Minute))
	if !strings.HasPrefix(got, `deja: earlier answer for recall "wal checkpoint"`) || !strings.Contains(got, "has not finished") {
		t.Fatalf("the wait note should carry the late answer, got %q", got)
	}
	close(h.gate("tide table"))
	s.close()
}

func TestMCPLateLabel(t *testing.T) {
	for params, want := range map[string]string{
		`{"name":"deja","arguments":{"mode":"recall","query":"wal"}}`: `recall "wal"`,
		`{"name":"deja","arguments":{"mode":"how","q":"tide"}}`:       `how "tide"`,
		`{"name":"deja","arguments":{"mode":"blame"}}`:                `blame`,
		`{"name":"recall","arguments":{"query":"wal"}}`:               `recall "wal"`,
	} {
		if got := mcpLateLabel(rpcRequest{Params: json.RawMessage(params)}); got != want {
			t.Errorf("label(%s) = %q, want %q", params, got, want)
		}
	}
}
