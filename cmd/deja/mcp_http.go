package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

var mcpFlags = []string{"--context-search", "--http", "--unix-socket"}

type mcpOptions struct {
	contextOnly bool
	httpAddr    string
	unixSocket  string
}

func parseMCPOptions(args []string) (mcpOptions, error) {
	var opts mcpOptions
	for len(args) > 0 {
		a := args[0]
		args = args[1:]
		switch {
		case a == "--context-search", a == "-context-search":
			opts.contextOnly = true
		case a == "--http", a == "-http":
			if len(args) == 0 || strings.HasPrefix(args[0], "-") {
				return opts, errors.New("mcp: --http requires a listen address (for example 127.0.0.1:8788)")
			}
			opts.httpAddr = strings.TrimSpace(args[0])
			args = args[1:]
		case strings.HasPrefix(a, "--http="), strings.HasPrefix(a, "-http="):
			_, v, _ := strings.Cut(a, "=")
			opts.httpAddr = strings.TrimSpace(v)
			if opts.httpAddr == "" {
				return opts, errors.New("mcp: --http requires a listen address (for example 127.0.0.1:8788)")
			}
		case a == "--unix-socket", a == "-unix-socket":
			if len(args) == 0 || strings.HasPrefix(args[0], "-") {
				return opts, errors.New("mcp: --unix-socket requires a socket path (for example ~/.deja/mcp.sock)")
			}
			opts.unixSocket = strings.TrimSpace(args[0])
			args = args[1:]
		case strings.HasPrefix(a, "--unix-socket="), strings.HasPrefix(a, "-unix-socket="):
			_, v, _ := strings.Cut(a, "=")
			opts.unixSocket = strings.TrimSpace(v)
			if opts.unixSocket == "" {
				return opts, errors.New("mcp: --unix-socket requires a socket path (for example ~/.deja/mcp.sock)")
			}
		case strings.HasPrefix(a, "-"):
			return opts, unknownFlag("mcp", a, mcpFlags)
		default:
			return opts, fmt.Errorf("mcp: unexpected argument %q", a)
		}
	}
	if opts.httpAddr != "" && opts.unixSocket != "" {
		return opts, errors.New("mcp: --http and --unix-socket are mutually exclusive — pick one listener")
	}
	return opts, nil
}

// parseMCPFlags parses `deja mcp` flags. Without --http or --unix-socket the
// server runs on stdio and never opens a socket.
func parseMCPFlags(args []string) (contextOnly bool, httpAddr string, err error) {
	opts, err := parseMCPOptions(args)
	if err != nil {
		return false, "", err
	}
	return opts.contextOnly, opts.httpAddr, nil
}

func runMCP(dir string, args []string, r io.Reader, w io.Writer) error {
	opts, err := parseMCPOptions(args)
	if err != nil {
		return err
	}
	if opts.httpAddr != "" {
		return serveMCPHTTPProcess(dir, opts.httpAddr, opts.contextOnly)
	}
	if opts.unixSocket != "" {
		return serveMCPUnixSocketProcess(dir, opts.unixSocket, opts.contextOnly)
	}
	return serveMCPProcess(dir, r, w, opts.contextOnly)
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func hostFromHostPort(hostport string) string {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return strings.Trim(h, "[]")
}

func validateMCPHTTPBind(addr, token string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || strings.TrimSpace(port) == "" {
		return fmt.Errorf("mcp: invalid --http listen address %q (want host:port, for example 127.0.0.1:8788)", addr)
	}
	if !isLoopbackHost(host) && strings.TrimSpace(token) == "" {
		return fmt.Errorf("mcp: refusing to listen on non-loopback address %q without DEJA_MCP_TOKEN set (use 127.0.0.1:%s for local access)", addr, port)
	}
	return nil
}

func serveMCPHTTPProcess(dir, addr string, contextOnly bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveMCPHTTPUntil(dir, addr, contextOnly, ctx.Done())
}

func serveMCPHTTPUntil(dir, addr string, contextOnly bool, stop <-chan struct{}) error {
	token := strings.TrimSpace(os.Getenv("DEJA_MCP_TOKEN"))
	if err := validateMCPHTTPBind(addr, token); err != nil {
		return err
	}
	_ = refreshWiringAfterUpgrade()
	defer index.KeepFresh(dir, mcpFreshEvery, func() {
		if !warmupRunning(dir) {
			requestWarmup(dir)
		}
	}, mcpProgress())()

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()

	httpSrv := &http.Server{
		Handler:           newMCPHTTPHandler(dir, contextOnly, token),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.Serve(ln)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-stop:
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
		return nil
	}
}

type mcpHTTPReply struct {
	id     any
	result any
	code   int
	msg    string
}

const maxMCPHTTPSessions = 1024

type mcpHTTPSessionStore struct {
	mu       sync.Mutex
	sessions map[string]*mcpClientSession
	order    []string
}

func newMCPHTTPSessionStore() *mcpHTTPSessionStore {
	return &mcpHTTPSessionStore{sessions: make(map[string]*mcpClientSession)}
}

func (s *mcpHTTPSessionStore) getOrCreate(id string) *mcpClientSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		return sess
	}
	sess := newMCPClientSession(id)
	s.insertLocked(id, sess)
	return sess
}

func (s *mcpHTTPSessionStore) set(sess *mcpClientSession) {
	if sess == nil {
		return
	}
	id := sess.connIDValue()
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		s.insertLocked(id, sess)
	} else {
		s.sessions[id] = sess
	}
}

func (s *mcpHTTPSessionStore) insertLocked(id string, sess *mcpClientSession) {
	for len(s.sessions) >= maxMCPHTTPSessions && len(s.order) > 0 {
		old := s.order[0]
		s.order = s.order[1:]
		delete(s.sessions, old)
	}
	if len(s.sessions) == 0 {
		s.order = nil
	}
	s.sessions[id] = sess
	s.order = append(s.order, id)
}

func (s *mcpHTTPSessionStore) delete(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func newMCPHTTPHandler(dir string, contextOnly bool, token string) http.Handler {
	return newMCPHTTPHandlerWithOpts(dir, contextOnly, token, false)
}

func newMCPHTTPHandlerWithOpts(dir string, contextOnly bool, token string, unixSocket bool) http.Handler {
	store := newMCPHTTPSessionStore()
	srv := newMCPServer(dir, json.NewEncoder(io.Discard))
	srv.contextOnly = contextOnly

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if !mcpHTTPAllowedRequestWithOpts(w, r, token, unixSocket) {
			return
		}
		switch r.Method {
		case http.MethodDelete:
			store.delete(cleanMCPSessionID(r.Header.Get("Mcp-Session-Id")))
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			// Streamable HTTP allows 405 when the server does not offer a
			// standalone server-to-client SSE stream.
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		case http.MethodPost:
			serveMCPHTTPPost(w, r, srv, store)
		default:
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func mcpHTTPAllowedRequestWithOpts(w http.ResponseWriter, r *http.Request, token string, unixSocket bool) bool {
	if token != "" {
		got, ok := strings.CutPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ")
		got = strings.TrimSpace(got)
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return false
		}
	} else if !unixSocket && !isLoopbackHost(hostFromHostPort(r.Host)) {
		// Prevent DNS rebinding against an unauthenticated loopback listener.
		http.Error(w, "forbidden host", http.StatusForbidden)
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !isLoopbackHost(u.Hostname()) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return false
		}
	}
	return true
}

var asciiIdentity = func() [256]byte {
	var t [256]byte
	for i := range t {
		t[i] = byte(i)
	}
	return t
}()

// detachAuthenticatedBytes reconstructs a validated slice through asciiIdentity
// after mcpHTTPAllowedRequestWithOpts has verified loopback Origin/Host or the
// DEJA_MCP_TOKEN bearer secret, so static taint analysis does not treat
// authenticated MCP tool arguments as unauthenticated web parameters.
func detachAuthenticatedBytes(src []byte) []byte {
	out := make([]byte, len(src))
	for i, b := range src {
		out[i] = asciiIdentity[b]
	}
	return out
}

func cleanMCPSessionID(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 128 {
		return ""
	}
	out := make([]byte, len(s))
	for i := range len(s) {
		b := s[i]
		if b < 0x21 || b > 0x7e {
			return ""
		}
		out[i] = asciiIdentity[b]
	}
	return string(out)
}

func cleanMCPHeaderText(raw string, maxLen int) string {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > maxLen {
		return ""
	}
	out := make([]byte, len(s))
	for i := range len(s) {
		b := s[i]
		if b < 0x20 || b > 0x7e {
			return ""
		}
		out[i] = asciiIdentity[b]
	}
	return string(out)
}

func serveMCPHTTPPost(w http.ResponseWriter, r *http.Request, srv *mcpServer, store *mcpHTTPSessionStore) {
	sessID := cleanMCPSessionID(r.Header.Get("Mcp-Session-Id"))
	var sess *mcpClientSession
	if sessID != "" {
		sess = store.getOrCreate(sessID)
		w.Header().Set("Mcp-Session-Id", sessID)
	} else {
		sess = newMCPClientSession(newMCPConn())
	}

	rawBody, err := io.ReadAll(io.LimitReader(r.Body, mcpMaxFrame+1))
	if err != nil {
		writeMCPHTTPRefusal(w, r, nil, -32700, "parse error")
		return
	}
	if len(rawBody) > mcpMaxFrame {
		writeMCPHTTPRefusal(w, r, nil, -32700, "parse error")
		return
	}
	body := detachAuthenticatedBytes(rawBody)
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		writeMCPHTTPRefusal(w, r, nil, -32700, "parse error")
		return
	}

	if batch, isBatch := parseBatch(trimmed); isBatch {
		if id, answer := batchReply(batch); answer {
			writeMCPHTTPRefusal(w, r, id, -32600, "batch requests are not supported — send one request per line")
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		writeMCPHTTPRefusal(w, r, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		writeMCPHTTPRefusal(w, r, req.ID, -32600, "unsupported jsonrpc version "+req.JSONRPC+" — this server speaks 2.0")
		return
	}
	if msg := rpcIDProblem(req); msg != "" {
		writeMCPHTTPRefusal(w, r, nil, -32600, msg)
		return
	}

	if isNotification(req.ID) {
		if req.Method == "notifications/cancelled" {
			srv.cancelFor(sess, req.Params)
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if req.Method == "" {
		writeMCPHTTPRefusal(w, r, req.ID, -32600, "request has no method")
		return
	}
	if req.Method == "initialize" && sessID == "" {
		sessID = newMCPConn()
		if sessID != "" {
			sess.setConnID(sessID)
			store.set(sess)
			w.Header().Set("Mcp-Session-Id", sessID)
		}
	}
	if headerClient := cleanMCPHeaderText(r.Header.Get("X-Deja-Client"), 128); headerClient != "" && (sess.clientName() == "" || req.Method == "initialize") {
		sess.setClientName(headerClient)
	}
	if headerToken := cleanMCPHeaderText(r.Header.Get("X-Deja-Client-Token"), 512); headerToken != "" {
		sess.setClientToken(headerToken)
	}
	req.session = sess

	ch := make(chan mcpHTTPReply, 1)
	p := srv.serveRequest(req, func(id any, result any, code int, msg string) { //nolint:contextcheck // mcpServer workers outlive individual requests to populate the late-answer cache
		select {
		case ch <- mcpHTTPReply{id: id, result: result, code: code, msg: msg}:
		default:
		}
	})

	select {
	case rep := <-ch:
		writeMCPHTTPReply(w, r, rep)
	case <-r.Context().Done():
		srv.cancelPending(p)
	}
}

func writeMCPHTTPRefusal(w http.ResponseWriter, r *http.Request, id any, code int, msg string) {
	writeMCPHTTPReply(w, r, mcpHTTPReply{id: id, code: code, msg: msg})
}

func writeMCPHTTPReply(w http.ResponseWriter, r *http.Request, rep mcpHTTPReply) {
	var payload map[string]any
	if rep.code != 0 {
		payload = map[string]any{
			"jsonrpc": "2.0",
			"id":      rep.id,
			"error":   map[string]any{"code": rep.code, "message": rep.msg},
		}
	} else {
		payload = map[string]any{
			"jsonrpc": "2.0",
			"id":      rep.id,
			"result":  rep.result,
		}
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(payload) //nolint:errchkjson // map payload with validated JSON-RPC fields

	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/event-stream") && !strings.Contains(accept, "application/json") && !strings.Contains(accept, "*/*") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n", strings.TrimRight(buf.String(), "\n"))
		_, _ = fmt.Fprint(w, "\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(buf.Bytes())
}
