package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

// The server used to answer one request at a time, in the order they came.
// Agents send tool calls in parallel, so a slow call held up every call behind
// it, and a client times each call from its own send: on opencode, whose limit
// is 30s, four blames sent together all died at 30.0s while the first one was
// still catching the index up. A cancelled request kept its place in the queue,
// and nothing the server did put an upper bound on the wait.
//
// Now requests that touch the index run beside each other, a few at a time; a
// cancelled one that has not started is dropped; and a tools/call that is
// still running at mcpDeadline is answered with a note instead of a timeout.
// The work keeps going, and the same call asked again joins it or picks up its
// answer.

// mcpWorkers is how many requests may read the index at once. Search is
// already parallel inside one query, so more than this buys little.
const mcpWorkers = 4

// mcpLateTTL is how long an answer that came in after its deadline waits for
// the agent to ask again.
const mcpLateTTL = 10 * time.Minute

// mcpDeadline is how long a tools/call may run before it is answered with a
// note. Clients give up at 30s (opencode) or 60s (Codex); the reply has to land
// before the first of them. DEJA_MCP_DEADLINE overrides it, in seconds or as a
// Go duration; 0 turns it off.
var mcpDeadline = mcpDeadlineFrom(os.Getenv("DEJA_MCP_DEADLINE"))

func mcpDeadlineFrom(v string) time.Duration {
	const def = 20 * time.Second
	if v == "" {
		return def
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
		return time.Duration(n * float64(time.Second))
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return d
	}
	return def
}

// mcpHandler answers one request. A variable so tests can make one slow.
var mcpHandler = handleMCP

// mcpInline are the methods answered on the read loop: they read no index and
// must not wait behind one that does.
var mcpInline = map[string]bool{
	"initialize": true, "ping": true, "tools/list": true,
	"prompts/list": true, "resources/templates/list": true,
}

type mcpServer struct {
	dir   string
	enc   *json.Encoder
	slots chan struct{}
	open  sync.WaitGroup // requests not answered yet

	mu      sync.Mutex
	werr    error
	pending map[string]*mcpPending // by request id
	running map[string]*mcpWork    // by call key
	late    map[string]mcpLate     // by call key
}

type mcpPending struct {
	id        any
	key       string
	answered  bool
	cancelled bool
	timer     *time.Timer
}

type mcpWork struct {
	waiters []*mcpPending
}

type mcpLate struct {
	result any
	code   int
	msg    string
	at     time.Time
}

func newMCPServer(dir string, enc *json.Encoder) *mcpServer {
	return &mcpServer{
		dir:     dir,
		enc:     enc,
		slots:   make(chan struct{}, mcpWorkers),
		pending: map[string]*mcpPending{},
		running: map[string]*mcpWork{},
		late:    map[string]mcpLate{},
	}
}

// writeErr is the first failed write, which ends the session.
func (s *mcpServer) writeErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.werr
}

// replyLocked writes one answer. s.mu is held, so replies never interleave.
func (s *mcpServer) replyLocked(id any, result any, code int, msg string) {
	if code != 0 {
		writeRPCError(s.enc, id, code, msg)
		return
	}
	if err := s.enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil && s.werr == nil {
		s.werr = err
	}
}

// serve answers one request that is owed a reply.
func (s *mcpServer) serve(req rpcRequest) {
	if mcpInline[req.Method] {
		result, code, msg := mcpHandler(s.dir, req)
		s.mu.Lock()
		s.replyLocked(req.ID, result, code, msg)
		s.mu.Unlock()
		return
	}
	key := mcpCallKey(req)
	p := &mcpPending{id: req.ID, key: key}
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.late[key]; ok {
		delete(s.late, key)
		if time.Since(l.at) < mcpLateTTL {
			s.replyLocked(req.ID, l.result, l.code, l.msg)
			return
		}
	}
	s.open.Add(1)
	s.pending[mcpIDKey(req.ID)] = p
	if req.Method == "tools/call" && mcpDeadline > 0 {
		p.timer = time.AfterFunc(mcpDeadline, func() { s.overdue(p) })
	}
	if w, ok := s.running[key]; ok {
		// The same call is already in flight — an agent asking again after a
		// deadline note, or the same question sent twice. It gets that answer
		// rather than a second search.
		w.waiters = append(w.waiters, p)
		return
	}
	w := &mcpWork{waiters: []*mcpPending{p}}
	s.running[key] = w
	go s.run(req, w)
}

func (s *mcpServer) run(req rpcRequest, w *mcpWork) {
	s.slots <- struct{}{}
	s.mu.Lock()
	wanted := false
	for _, p := range w.waiters {
		wanted = wanted || !p.cancelled
	}
	if !wanted {
		delete(s.running, mcpCallKey(req))
		s.mu.Unlock()
		<-s.slots
		return
	}
	s.mu.Unlock()

	result, code, msg := mcpHandler(s.dir, req)
	<-s.slots

	s.mu.Lock()
	defer s.mu.Unlock()
	key := mcpCallKey(req)
	delete(s.running, key)
	delivered := false
	for _, p := range w.waiters {
		if p.answered {
			continue
		}
		s.answerLocked(p)
		s.replyLocked(p.id, result, code, msg)
		delivered = true
	}
	if !delivered && code == 0 {
		// Everyone who asked was told to come back. Keep the answer for them.
		s.pruneLateLocked()
		s.late[key] = mcpLate{result: result, code: code, msg: msg, at: time.Now()}
	}
}

// answerLocked marks p as done; it gets no further reply.
func (s *mcpServer) answerLocked(p *mcpPending) {
	if p.answered {
		return
	}
	p.answered = true
	if p.timer != nil {
		p.timer.Stop()
	}
	delete(s.pending, mcpIDKey(p.id))
	s.open.Done()
}

// overdue answers a tools/call that is still running at the deadline.
func (s *mcpServer) overdue(p *mcpPending) {
	// Read outside the lock: it looks at the index directory, and the lock is
	// what every reply waits on.
	building := mcpBuildingLine(s.dir)
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.answered {
		return
	}
	s.answerLocked(p)
	s.replyLocked(p.id, toolText(mcpOverdueNote(mcpDeadline, building)), 0, "")
}

// cancel handles notifications/cancelled. A request the client gave up on is
// owed no reply, and if nobody else is waiting on its work, the work is not
// started.
func (s *mcpServer) cancel(params json.RawMessage) {
	var c struct {
		RequestID any `json:"requestId"`
	}
	if json.Unmarshal(params, &c) != nil || c.RequestID == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[mcpIDKey(c.RequestID)]
	if !ok {
		return
	}
	p.cancelled = true
	s.answerLocked(p)
}

// wait returns once every request read so far has been answered.
func (s *mcpServer) wait() { s.open.Wait() }

func (s *mcpServer) pruneLateLocked() {
	for k, l := range s.late {
		if time.Since(l.at) >= mcpLateTTL {
			delete(s.late, k)
		}
	}
}

// mcpOverdueNote is the reply to a call that ran past the deadline.
func mcpOverdueNote(d time.Duration, building string) string {
	var b bytes.Buffer
	b.WriteString("deja has not finished this call after " + d.Round(time.Second).String() + ", so this is not its answer.")
	if building != "" {
		b.WriteString(" " + building)
	} else {
		b.WriteString(" It is still running: call deja again with the same arguments in a minute and the answer comes back at once.")
	}
	return b.String()
}

// mcpBuildingLine is what an overdue call says when the reason is an index
// being built, as every other surface says it.
func mcpBuildingLine(dir string) string {
	if st := readWarmupStatus(dir); st != nil {
		return "deja is indexing this machine's history (" + st.progress() + "). Recall comes online when it finishes; ask again then."
	}
	if !index.HasManifest(dir) && index.RebuildInProgress(dir) {
		// A first run builds inside the call that found no index: 77s on a
		// 460 MB history, measured.
		return "deja is indexing this machine's history for the first time. Recall comes online when it finishes; ask again then."
	}
	return ""
}

func mcpIDKey(id any) string {
	b, _ := json.Marshal(id)
	return string(b)
}

// mcpCallKey names a call by what it asks, so the same question asked twice
// is one piece of work.
func mcpCallKey(req rpcRequest) string {
	var b bytes.Buffer
	b.WriteString(req.Method)
	b.WriteByte(0)
	if json.Compact(&b, req.Params) != nil {
		b.Write(req.Params)
	}
	return b.String()
}

// refuse writes an error reply from the read loop.
func (s *mcpServer) refuse(id any, code int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeRPCError(s.enc, id, code, msg)
}
