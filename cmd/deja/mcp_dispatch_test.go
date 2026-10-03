package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// mcpSession is a live serveMCP over pipes: requests go in one at a time and
// replies are read as the server writes them.
type mcpSession struct {
	t    *testing.T
	in   *io.PipeWriter
	out  chan map[string]any
	done chan error
}

func openMCPSession(t *testing.T, dir string) *mcpSession {
	t.Helper()
	pr, pw := io.Pipe()
	outR, outW := io.Pipe()
	s := &mcpSession{t: t, in: pw, out: make(chan map[string]any, 64), done: make(chan error, 1)}
	go func() {
		err := serveMCP(dir, pr, outW)
		_ = outW.Close()
		s.done <- err
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), mcpMaxFrame)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("bad reply %q: %v", sc.Text(), err)
				continue
			}
			s.out <- m
		}
		close(s.out)
	}()
	return s
}

func (s *mcpSession) send(frame string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, frame+"\n"); err != nil {
		s.t.Fatalf("send: %v", err)
	}
}

func (s *mcpSession) call(id int, args string) {
	s.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"deja","arguments":%s}}`, id, args))
}

// next is the next reply, or nil if none comes within d.
func (s *mcpSession) next(d time.Duration) map[string]any {
	select {
	case m := <-s.out:
		return m
	case <-time.After(d):
		return nil
	}
}

func (s *mcpSession) close() {
	s.t.Helper()
	_ = s.in.Close()
	if err := <-s.done; err != nil {
		s.t.Fatalf("serveMCP: %v", err)
	}
}

func replyID(m map[string]any) int {
	f, _ := m["id"].(float64)
	return int(f)
}

func replyText(m map[string]any) string {
	res, _ := m["result"].(map[string]any)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		return ""
	}
	c, _ := content[0].(map[string]any)
	s, _ := c["text"].(string)
	return s
}

// slowHandler answers tools/call with the query it was asked, after waiting
// for the gate named by the query; other methods go to the real handler.
type slowHandler struct {
	mu    sync.Mutex
	gates map[string]chan struct{}
	calls atomic.Int64
}

func (h *slowHandler) gate(q string) chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gates == nil {
		h.gates = map[string]chan struct{}{}
	}
	if h.gates[q] == nil {
		h.gates[q] = make(chan struct{})
	}
	return h.gates[q]
}

func (h *slowHandler) install(t *testing.T) {
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
		h.calls.Add(1)
		<-h.gate(p.Arguments.Query)
		return toolText("answer for " + p.Arguments.Query), 0, ""
	}
	t.Cleanup(func() { mcpHandler = saved })
}

func setDeadline(t *testing.T, d time.Duration) {
	saved := mcpDeadline
	mcpDeadline = d
	t.Cleanup(func() { mcpDeadline = saved })
}

// Four blames sent together on opencode all died at 30.0s: the server took
// them one at a time and the first was slow. A slow call must not hold up the
// ones sent beside it.
func TestASlowCallDoesNotHoldUpTheOnesBesideIt(t *testing.T) {
	h := &slowHandler{}
	h.install(t)
	setDeadline(t, 0)
	close(h.gate("fast"))
	s := openMCPSession(t, t.TempDir())
	s.call(1, `{"mode":"recall","query":"slow"}`)
	s.call(2, `{"mode":"recall","query":"fast"}`)
	got := s.next(5 * time.Second)
	if got == nil || replyID(got) != 2 || replyText(got) != "answer for fast" {
		t.Fatalf("the fast call should answer first, got %#v", got)
	}
	close(h.gate("slow"))
	if got := s.next(5 * time.Second); got == nil || replyID(got) != 1 {
		t.Fatalf("the slow call should still answer, got %#v", got)
	}
	s.close()
}

// A call still running at the deadline is answered with a note before the
// client's own timeout; the work goes on and the same call asked again gets
// the answer.
func TestACallPastTheDeadlineIsAnsweredWithANoteAndKeepsItsAnswer(t *testing.T) {
	h := &slowHandler{}
	h.install(t)
	setDeadline(t, 50*time.Millisecond)
	s := openMCPSession(t, t.TempDir())
	s.call(1, `{"mode":"recall","query":"slow"}`)
	got := s.next(5 * time.Second)
	if got == nil || replyID(got) != 1 {
		t.Fatalf("no reply at the deadline: %#v", got)
	}
	if txt := replyText(got); !strings.Contains(txt, "has not finished") || !strings.Contains(txt, "same arguments") {
		t.Fatalf("deadline note = %q", txt)
	}
	// Asked again while it still runs: joins the work, does not start more.
	s.call(2, `{"mode":"recall","query":"slow"}`)
	if got := s.next(5 * time.Second); got == nil || replyID(got) != 2 || !strings.Contains(replyText(got), "has not finished") {
		t.Fatalf("second ask should get the note too, got %#v", got)
	}
	close(h.gate("slow"))
	if got := s.next(200 * time.Millisecond); got != nil {
		t.Fatalf("both asks were answered; the late answer must wait, not be sent: %#v", got)
	}
	// Asked again after it finished: the kept answer, at once.
	setDeadline(t, 0)
	s.call(3, `{"mode":"recall","query":"slow"}`)
	if got := s.next(5 * time.Second); got == nil || replyID(got) != 3 || replyText(got) != "answer for slow" {
		t.Fatalf("third ask should get the kept answer, got %#v", got)
	}
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("the call ran %d times, want 1", n)
	}
	s.close()
}

// A request the client cancelled before it started is dropped: no reply, and
// no work.
func TestACancelledCallIsNotRunAndNotAnswered(t *testing.T) {
	h := &slowHandler{}
	h.install(t)
	setDeadline(t, 0)
	s := openMCPSession(t, t.TempDir())
	// Fill every worker so the next call queues.
	for i := 0; i < mcpWorkers; i++ {
		s.call(10+i, fmt.Sprintf(`{"mode":"recall","query":"busy%d"}`, i))
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.calls.Load() < mcpWorkers && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s.call(1, `{"mode":"recall","query":"queued"}`)
	s.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"timeout"}}`)
	// A ping is answered on the read loop, so its reply means the cancel
	// before it has been read.
	s.send(`{"jsonrpc":"2.0","id":99,"method":"ping"}`)
	if got := s.next(5 * time.Second); got == nil || replyID(got) != 99 {
		t.Fatalf("ping reply = %#v", got)
	}
	close(h.gate("queued"))
	for i := 0; i < mcpWorkers; i++ {
		close(h.gate(fmt.Sprintf("busy%d", i)))
	}
	seen := map[int]bool{}
	for i := 0; i < mcpWorkers; i++ {
		got := s.next(5 * time.Second)
		if got == nil {
			t.Fatalf("missing reply after %v", seen)
		}
		seen[replyID(got)] = true
	}
	if got := s.next(200 * time.Millisecond); got != nil {
		t.Fatalf("the cancelled call was answered: %#v", got)
	}
	if seen[1] {
		t.Fatal("the cancelled call was answered")
	}
	if n := h.calls.Load(); n != mcpWorkers {
		t.Fatalf("handler ran %d times, want %d — the cancelled call ran", n, mcpWorkers)
	}
	s.close()
}

// Input ending is not the end of the answers owed: a client that writes its
// requests and closes stdin still gets every reply.
func TestEndOfInputWaitsForCallsInFlight(t *testing.T) {
	h := &slowHandler{}
	h.install(t)
	setDeadline(t, 0)
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"deja","arguments":{"query":"a"}}}` + "\n"
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(h.gate("a"))
	}()
	var out strings.Builder
	if err := serveMCP(t.TempDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "answer for a") {
		t.Fatalf("reply lost at end of input: %q", out.String())
	}
}

func TestMCPDeadlineFromEnv(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":     20 * time.Second,
		"25":   25 * time.Second,
		"1.5":  1500 * time.Millisecond,
		"40s":  40 * time.Second,
		"0":    0,
		"junk": 20 * time.Second,
		"-3":   20 * time.Second,
	} {
		if got := mcpDeadlineFrom(in); got != want {
			t.Errorf("mcpDeadlineFrom(%q) = %v, want %v", in, got, want)
		}
	}
}

// On a first run the call that found no index builds it, and that took 77s
// on a real history. Past the deadline the note says so instead of asking for
// the same call again.
func TestTheDeadlineNoteNamesAFirstBuild(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "index.db")
	if got := mcpBuildingLine(dir); got != "" {
		t.Fatalf("nothing is building, yet: %q", got)
	}
	if got := mcpOverdueNote(20*time.Second, ""); !strings.Contains(got, "20s") || !strings.Contains(got, "same arguments") {
		t.Fatalf("plain note = %q", got)
	}
	line := "deja is indexing this machine's history for the first time. Recall comes online when it finishes; ask again then."
	if got := mcpOverdueNote(20*time.Second, line); !strings.Contains(got, "first time") || strings.Contains(got, "same arguments") {
		t.Fatalf("building note = %q", got)
	}
}

// The real tools, all at once, over one index: what a client sending parallel
// calls does. Run under -race this is the check that they may share a process.
func TestRealToolsAnswerInParallel(t *testing.T) {
	tmp := hermeticEnv(t)
	claude := filepath.Join(tmp, "claude")
	t.Setenv("DEJA_CLAUDE_ROOT", claude)
	seedClaude(t, claude, "app", "sess-alpha", "the frobnicator crash in parser.go", "fixed the frobnicator in parser.go")
	seedClaude(t, claude, "app", "sess-beta", "another frobnicator regression today", "frobnicator again, go test failed")
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatal(err)
	}
	setDeadline(t, 0)
	calls := []string{
		`{"mode":"recall","query":"frobnicator"}`,
		`{"mode":"context","query":"frobnicator"}`,
		`{"mode":"blame","path":"parser.go"}`,
		`{"mode":"fix","error":"go test failed"}`,
		`{"mode":"how","what":"fix the frobnicator"}`,
		`{"mode":"recall","query":"regression","limit":1}`,
		`{"mode":"remember","text":"frobnicator lives in parser.go","project":"notes"}`,
		`{"mode":"recall","query":"parser"}`,
	}
	s := openMCPSession(t, dir)
	for i, c := range calls {
		s.call(i+1, c)
	}
	s.send(`{"jsonrpc":"2.0","id":100,"method":"resources/list","params":{}}`)
	seen := map[int]bool{}
	for range len(calls) + 1 {
		got := s.next(30 * time.Second)
		if got == nil {
			t.Fatalf("replies so far %v", seen)
		}
		if e, ok := got["error"]; ok {
			t.Fatalf("call %d failed: %v", replyID(got), e)
		}
		seen[replyID(got)] = true
	}
	s.close()
	if len(seen) != len(calls)+1 {
		t.Fatalf("replies %v", seen)
	}
}
