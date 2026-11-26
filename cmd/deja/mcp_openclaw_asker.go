package main

import (
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// mcpClient is the clientInfo.name the host sent in initialize: one host per
// stdio server process. Multi-session transports (Streamable HTTP, Unix socket)
// bind a per-connection/per-session mcpClientSession onto the worker goroutine.
var (
	mcpClient            atomic.Value
	mcpGoroutineSessions sync.Map // uint64 gid -> *mcpClientSession
)

// mcpClientSession isolates per-connection identity, client policy credentials,
// and transport authentication state across concurrent MCP sessions.
type mcpClientSession struct {
	mu          sync.RWMutex
	connID      string
	client      string
	clientToken string
	authed      bool
}

func newMCPClientSession(connID string) *mcpClientSession {
	if connID == "" {
		connID = newMCPConn()
	}
	return &mcpClientSession{connID: connID}
}

func (s *mcpClientSession) connIDValue() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connID
}

func (s *mcpClientSession) setConnID(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.connID = id
	s.mu.Unlock()
}

func (s *mcpClientSession) clientName() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

func (s *mcpClientSession) setClientName(name string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.client = name
	s.mu.Unlock()
}

func (s *mcpClientSession) tokenValue() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clientToken
}

func (s *mcpClientSession) setClientToken(tok string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.clientToken = tok
	s.mu.Unlock()
}

func (s *mcpClientSession) isAuthed() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.authed
}

func (s *mcpClientSession) setAuthed(v bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.authed = v
	s.mu.Unlock()
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	s := strings.TrimPrefix(string(buf[:n]), "goroutine ")
	field, _, _ := strings.Cut(s, " ")
	id, err := strconv.ParseUint(field, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func withMCPClientSession(sess *mcpClientSession, fn func()) {
	gid := currentGoroutineID()
	if gid == 0 || sess == nil {
		fn()
		return
	}
	prev, had := mcpGoroutineSessions.Load(gid)
	mcpGoroutineSessions.Store(gid, sess)
	defer func() {
		if had {
			mcpGoroutineSessions.Store(gid, prev)
		} else {
			mcpGoroutineSessions.Delete(gid)
		}
	}()
	fn()
}

func currentMCPClientSession() *mcpClientSession {
	gid := currentGoroutineID()
	if gid == 0 {
		return nil
	}
	v, ok := mcpGoroutineSessions.Load(gid)
	if !ok {
		return nil
	}
	sess, _ := v.(*mcpClientSession)
	return sess
}

func setMCPClient(name string) {
	if sess := currentMCPClientSession(); sess != nil {
		sess.setClientName(name)
		return
	}
	mcpClient.Store(name)
}

func setMCPClientToken(tok string) {
	if sess := currentMCPClientSession(); sess != nil {
		sess.setClientToken(tok)
	}
}

func mcpClientName() string {
	if sess := currentMCPClientSession(); sess != nil {
		return sess.clientName()
	}
	s, _ := mcpClient.Load().(string)
	return s
}

// unstampedAskerWindow is how recently the asking session has to have been
// written. An agent mid-turn wrote its transcript seconds ago; a session the
// user left a few minutes back is the one most likely to hold the answer, so
// the window stays short.
const unstampedAskerWindow = 5 * time.Minute

var mcpClientExactHarnesses = map[string][]string{
	"claude-code": {"claude"},
	"roo":         {"roo"},
	"zoo-code":    {"roo"},
	"dsh":         {"deepseek"},
	"pi":          {"pi"},
}

var mcpClientPrefixHarnesses = []struct {
	prefix    string
	harnesses []string
}{
	{"openclaw", []string{"openclaw"}},
	{"claude-code-", []string{"claude"}},
	{"codex", []string{"codex"}},
	{"cursor", []string{"cursor"}},
	{"gemini", []string{"gemini"}},
	{"qwen", []string{"qwen"}},
	{"gigacode", []string{"gigacode"}},
	{"opencode", []string{"opencode"}},
	{"kimi", []string{"kimi"}},
	{"goose", []string{"goose"}},
	{"windsurf", []string{"windsurf"}},
	{"roo-", []string{"roo"}},
	{"zoo-code-", []string{"roo"}},
	{"kilo", []string{"kilocode"}},
	{"cline", []string{"cline"}},
	{"continue", []string{"continue"}},
	{"crush", []string{"crush"}},
	{"amp", []string{"amp"}},
	{"zed", []string{"zed"}},
	{"antigravity", []string{"antigravity"}},
	{"kiro", []string{"kiro"}},
	{"hermes", []string{"hermes"}},
	{"grok", []string{"grok"}},
	{"deepseek", []string{"deepseek"}},
	{"devin", []string{"devin"}},
	{"junie", []string{"junie"}},
	{"jetbrains", []string{"jetbrains"}},
	{"trae", []string{"trae"}},
	{"codebuddy", []string{"codebuddy"}},
	{"codewhale", []string{"codewhale"}},
	{"commandcode", []string{"commandcode"}},
	{"command-code", []string{"commandcode"}},
	{"zcode", []string{"zcode"}},
	{"senpi", []string{"senpi"}},
	{"kimchi", []string{"kimchi"}},
	{"gjc", []string{"gjc"}},
	{"mcode", []string{"mcode"}},
	{"muse", []string{"muse"}},
	{"reasonix", []string{"reasonix"}},
	{"cherrystudio", []string{"cherrystudio"}},
	{"cherry-studio", []string{"cherrystudio"}},
	{"pi-", []string{"pi"}},
	{"omp", []string{"omp"}},
	{"oh-my-pi", []string{"omp"}},
	{"prime", []string{"prime"}},
	{"copilot", []string{"copilot", "copilot-chat"}},
	{"github-copilot", []string{"copilot", "copilot-chat"}},
	{"vscode", []string{"copilot", "copilot-chat"}},
	{"visual-studio-code", []string{"copilot", "copilot-chat"}},
}

// mcpClientHarnesses maps an MCP initialize clientInfo.name to the source
// harness name(s) that host writes transcripts under.
func mcpClientHarnesses(name string) []string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || n == "deja-shell-verify" {
		return nil
	}
	norm := strings.NewReplacer(" ", "-", "_", "-").Replace(n)
	if h, ok := mcpClientExactHarnesses[norm]; ok {
		return h
	}
	for _, rule := range mcpClientPrefixHarnesses {
		if strings.HasPrefix(norm, rule.prefix) {
			return rule.harnesses
		}
	}
	names := sources.HarnessNames()
	for _, h := range names {
		if h != "deja" && norm == h {
			return []string{h}
		}
	}
	for _, h := range names {
		if h != "deja" && strings.HasPrefix(norm, h+"-") {
			return []string{h}
		}
	}
	return nil
}

func hooksStampedSince(dir string, at time.Time) bool {
	for _, when := range readLiveSessions(dir) {
		if when.After(at.Add(-time.Minute)) {
			return true
		}
	}
	return false
}

// mcpUnstampedAsker names the session an MCP call comes from when no hook
// stamped it. An MCP-only install (`deja install <harness>` without `-auto`),
// or a surface like `openclaw chat` / `openclaw tui --local` (#5271), runs no
// hook, so nothing marks the session live, and recall answered with the
// conversation asking (#6027). After EnsureForSearch has appended the latest
// turn, the newest session that host wrote in the caller's project inside the
// last few minutes is that conversation.
//
// Unless a hook stamped some session about as recently. Where hooks run they
// name the asker themselves, and the newest write can be something else: a
// heartbeat poll on OpenClaw's main DM (#5270) or a background session.
func mcpUnstampedAsker(dir string) string {
	harnesses := mcpClientHarnesses(mcpClientName())
	if len(harnesses) == 0 {
		return ""
	}
	now := time.Now()
	since := now.Add(-unstampedAskerWindow)
	if harnesses[0] == "openclaw" {
		id, at := sources.OpenClawNewestSession(since)
		if id == "" || liveSessionIDs(dir)[id] || hooksStampedSince(dir, at) {
			return ""
		}
		return id
	}
	metas, err := index.AllMetaShared(dir)
	if err != nil || len(metas) == 0 {
		return ""
	}
	wantHarness := make(map[string]bool, len(harnesses))
	for _, h := range harnesses {
		wantHarness[h] = true
	}
	scope := howScope(howCwd(), "", false)
	var bestID string
	var bestAt time.Time
	for _, m := range metas {
		if !wantHarness[m.Harness] || !m.Updated.After(since) {
			continue
		}
		if len(scope) > 0 && (m.Project == "" || !howProjectMatches(m.Project, scope)) {
			continue
		}
		if m.Updated.After(bestAt) {
			bestID, bestAt = m.ID, m.Updated
		}
	}
	if bestID == "" || liveSessionIDs(dir)[bestID] || hooksStampedSince(dir, bestAt) {
		return ""
	}
	return bestID
}
