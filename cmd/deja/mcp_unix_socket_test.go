package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/vault"
)

func allocShortSockDir() (string, error) {
	var root string
	if st, err := os.Stat("/tmp"); err == nil && st.IsDir() {
		root = "/tmp"
	} else {
		root = os.TempDir()
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(root, "dj-"+hex.EncodeToString(rnd[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func shortUnixSockPath(t *testing.T, rel string) string {
	t.Helper()
	base, err := allocShortSockDir()
	if err != nil {
		t.Fatalf("alloc short socket dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(base)
	})
	return filepath.Join(base, rel)
}

func startTestUnixSocketServer(t *testing.T, dir, sockPath string, contextOnly bool) func() {
	t.Helper()
	stopCh := make(chan struct{})
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- serveMCPUnixSocketUntil(dir, sockPath, contextOnly, stopCh)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		cancel()
		if err == nil {
			_ = conn.Close()
			var once sync.Once
			stopFn := func() {
				once.Do(func() {
					close(stopCh)
					if serr := <-doneCh; serr != nil {
						t.Errorf("serveMCPUnixSocketUntil exit: %v", serr)
					}
				})
			}
			t.Cleanup(stopFn)
			return stopFn
		}
		select {
		case serr := <-doneCh:
			t.Fatalf("serveMCPUnixSocketUntil exited before ready: %v", serr)
		default:
		}
		time.Sleep(15 * time.Millisecond)
	}
	close(stopCh)
	serr := <-doneCh
	t.Fatalf("timed out waiting for unix socket server %s (err: %v)", sockPath, serr)
	return nil
}

func dialUnixConn(t *testing.T, sockPath string) net.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
	if err != nil {
		t.Fatalf("dial unix %s: %v", sockPath, err)
	}
	return conn
}

func sendRawUnixLines(t *testing.T, sockPath string, frames ...string) []map[string]any {
	t.Helper()
	conn := dialUnixConn(t, sockPath)
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	for _, f := range frames {
		if _, err := io.WriteString(conn, f+"\n"); err != nil {
			t.Fatalf("write raw frame: %v", err)
		}
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		if err := uc.CloseWrite(); err != nil {
			t.Fatalf("CloseWrite: %v", err)
		}
	}

	raw, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read raw replies: %v", err)
	}
	var out []map[string]any
	for line := range strings.SplitSeq(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rep map[string]any
		if uerr := json.Unmarshal([]byte(line), &rep); uerr != nil {
			t.Fatalf("decode raw reply %q: %v", line, uerr)
		}
		out = append(out, rep)
	}
	return out
}

func writeContentLengthFrame(w io.Writer, body string) error {
	hdr := fmt.Sprintf("Content-Type: application/vscode-jsonrpc; charset=utf-8\r\nContent-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, hdr); err != nil {
		return err
	}
	_, err := io.WriteString(w, body)
	return err
}

func readContentLengthFrame(br *bufio.Reader) (map[string]any, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		h := strings.TrimRight(line, "\r\n")
		if h == "" {
			break
		}
		if k, v, ok := strings.Cut(h, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			n, convErr := strconv.Atoi(strings.TrimSpace(v))
			if convErr != nil {
				return nil, convErr
			}
			length = n
		}
	}
	if length <= 0 {
		return nil, fmt.Errorf("invalid Content-Length %d", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}
	var rep map[string]any
	if err := json.Unmarshal(buf, &rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func unixHTTPClient(t *testing.T, sockPath string) *http.Client {
	t.Helper()
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
		},
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

func postUnixMCPHTTP(t *testing.T, client *http.Client, body string, headers map[string]string) (int, http.Header, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://unix/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP POST /mcp: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read HTTP body: %v", err)
	}
	var parsed map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}
	return resp.StatusCode, resp.Header, parsed
}

func serveHTTPHandlerPOST(t *testing.T, handler http.Handler, body string, headers map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8788/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var parsed map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &parsed)
	}
	return rec, parsed
}

func TestParseMCPOptionsUnixSocket(t *testing.T) {
	opts, err := parseMCPOptions([]string{"--context-search", "--unix-socket", "/tmp/deja-test.sock"})
	if err != nil {
		t.Fatalf("parseMCPOptions: %v", err)
	}
	if !opts.contextOnly || opts.unixSocket != "/tmp/deja-test.sock" || opts.httpAddr != "" {
		t.Fatalf("unexpected opts: %+v", opts)
	}

	optsEq, err := parseMCPOptions([]string{"--unix-socket=/tmp/deja-eq.sock"})
	if err != nil || optsEq.unixSocket != "/tmp/deja-eq.sock" {
		t.Fatalf("parseMCPOptions --unix-socket= failed: (%+v, %v)", optsEq, err)
	}

	for _, bad := range [][]string{
		{"--unix-socket"},
		{"--unix-socket", "--http"},
		{"--unix-socket="},
		{"--http", "127.0.0.1:8788", "--unix-socket", "/tmp/deja.sock"},
		{"--bogus-flag"},
		{"unexpected-arg"},
	} {
		if _, parseErr := parseMCPOptions(bad); parseErr == nil {
			t.Errorf("parseMCPOptions(%v) unexpectedly succeeded", bad)
		}
	}

	if _, expandErr := expandMCPSocketPath("   "); expandErr == nil {
		t.Error("expandMCPSocketPath blank succeeded")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	gotHome, err := expandMCPSocketPath("~/mcp.sock")
	if err != nil || gotHome != filepath.Join(home, "mcp.sock") {
		t.Fatalf("expandMCPSocketPath(~/mcp.sock) = (%q, %v)", gotHome, err)
	}
	gotJustHome, err := expandMCPSocketPath("~")
	if err != nil || gotJustHome != filepath.Clean(home) {
		t.Fatalf("expandMCPSocketPath(~) = (%q, %v)", gotJustHome, err)
	}
}

func TestMCPUnixSocketFilesystemSafety(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := index.DefaultDir()

	// Missing nested parent directory is created with 0700 and socket with 0600, then unlinked on shutdown.
	nestedSock := shortUnixSockPath(t, filepath.Join("sub", "mcp.sock"))
	stopNested := startTestUnixSocketServer(t, dir, nestedSock, false)
	if runtime.GOOS != "windows" {
		pInfo, err := os.Stat(filepath.Dir(nestedSock))
		if err != nil {
			t.Fatalf("stat nested parent: %v", err)
		}
		if perm := pInfo.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("nested parent permissions %o allow group/other access", perm)
		}
		sInfo, err := os.Lstat(nestedSock)
		if err != nil {
			t.Fatalf("lstat socket: %v", err)
		}
		if perm := sInfo.Mode().Perm(); perm != 0o600 {
			t.Fatalf("socket permissions = %o, want 0600", perm)
		}
	}

	// Refuse second server while socket has an active listener.
	if err := serveMCPUnixSocketUntil(dir, nestedSock, false, make(chan struct{})); err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("second server on active socket got err=%v, want already in use", err)
	}
	stopNested()
	if _, err := os.Lstat(nestedSock); !os.IsNotExist(err) {
		t.Fatalf("socket %s was not unlinked on clean shutdown: %v", nestedSock, err)
	}

	// Refuse regular file at socket path and regular file as parent.
	regFile := shortUnixSockPath(t, "not-a-socket")
	if err := os.WriteFile(regFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := serveMCPUnixSocketUntil(dir, regFile, false, make(chan struct{})); err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("regular file socket got err=%v", err)
	}
	if err := serveMCPUnixSocketUntil(dir, filepath.Join(regFile, "child.sock"), false, make(chan struct{})); err == nil {
		t.Fatal("non-directory parent unexpectedly succeeded")
	}

	// Refuse symlinks at socket path and parent directory on POSIX systems.
	if runtime.GOOS != "windows" {
		symSock := shortUnixSockPath(t, "sym.sock")
		if err := os.Symlink(filepath.Join(tmp, "target.sock"), symSock); err != nil {
			t.Fatal(err)
		}
		if err := serveMCPUnixSocketUntil(dir, symSock, false, make(chan struct{})); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink socket got err=%v", err)
		}
		symParent := shortUnixSockPath(t, "symdir")
		if err := os.Symlink(tmp, symParent); err != nil {
			t.Fatal(err)
		}
		if err := serveMCPUnixSocketUntil(dir, filepath.Join(symParent, "x.sock"), false, make(chan struct{})); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink parent got err=%v", err)
		}
	}

	// Stale socket left by a crashed listener is safely replaced, and cleanup preserves externally replaced paths.
	staleSock := shortUnixSockPath(t, "stale.sock")
	staleLn, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", staleSock)
	if err != nil {
		t.Fatalf("listen stale sock: %v", err)
	}
	if ul, ok := staleLn.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = staleLn.Close()
	stopStale := startTestUnixSocketServer(t, dir, staleSock, false)
	replies := sendRawUnixLines(t, staleSock, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if len(replies) != 1 || mcpToolIsError(replies[0]) {
		t.Fatalf("ping after stale socket recovery failed: %v", replies)
	}
	stopStale()

	// cleanupMCPUnixSocket must not remove a file whose inode no longer matches boundInfo.
	preserved := shortUnixSockPath(t, "preserved.txt")
	if wErr := os.WriteFile(preserved, []byte("keep"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	pInfo, lErr := os.Lstat(preserved)
	if lErr != nil {
		t.Fatal(lErr)
	}
	cleanupMCPUnixSocket(preserved, pInfo)
	if _, stErr := os.Stat(preserved); stErr != nil {
		t.Fatalf("cleanupMCPUnixSocket deleted non-socket path: %v", stErr)
	}
}

func TestMCPUnixSocketProtocolsAndAuth(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	sockPath := shortUnixSockPath(t, "proto.sock")
	startTestUnixSocketServer(t, dir, sockPath, false)

	// 1. Raw newline JSON-RPC (initialize, ping, tools/list, notifications/cancelled, batch & parse errors).
	rawReplies := repliesByID(sendRawUnixLines(t, sockPath,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":999}}`,
		`[{"jsonrpc":"2.0","id":4,"method":"ping"}]`,
		`{not-valid-json`,
	))
	if len(rawReplies) < 4 || mcpToolIsError(rawReplies[1]) || mcpToolIsError(rawReplies[2]) || mcpToolIsError(rawReplies[3]) {
		t.Fatalf("unexpected raw UDS replies: %v", rawReplies)
	}
	if !mcpToolIsError(rawReplies[4]) || !strings.Contains(vaultReplyText(rawReplies[4]), "batch") {
		t.Fatalf("expected batch refusal on raw UDS, got %v", rawReplies[4])
	}

	// 2. Content-Length LSP framing round-trip + malformed header check.
	clConn := dialUnixConn(t, sockPath)
	_ = clConn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := writeContentLengthFrame(clConn, `{"jsonrpc":"2.0","id":11,"method":"initialize","params":{"clientInfo":{"name":"cursor"}}}`); err != nil {
		t.Fatal(err)
	}
	if err := writeContentLengthFrame(clConn, `{"jsonrpc":"2.0","id":12,"method":"ping"}`); err != nil {
		t.Fatal(err)
	}
	clReader := bufio.NewReader(clConn)
	cl1, err := readContentLengthFrame(clReader)
	if err != nil || mcpToolIsError(cl1) {
		t.Fatalf("Content-Length init reply=(%v, %v)", cl1, err)
	}
	cl2, err := readContentLengthFrame(clReader)
	if err != nil || mcpToolIsError(cl2) {
		t.Fatalf("Content-Length ping reply=(%v, %v)", cl2, err)
	}
	_, _ = io.WriteString(clConn, "Content-Length: nope\r\n\r\n")
	clBad, err := readContentLengthFrame(clReader)
	if err != nil || !mcpToolIsError(clBad) || !strings.Contains(vaultReplyText(clBad), "parse error") {
		t.Fatalf("expected Content-Length parse error, got (%v, %v)", clBad, err)
	}
	_ = clConn.Close()

	// 3. Streamable HTTP over the same Unix socket + security header checks.
	client := unixHTTPClient(t, sockPath)
	initStatus, initHdr, initParsed := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":21,"method":"initialize","params":{"clientInfo":{"name":"codex"}}}`, nil)
	if initStatus != http.StatusOK || mcpToolIsError(initParsed) {
		t.Fatalf("HTTP over UDS initialize = (%d, %v)", initStatus, initParsed)
	}
	sessID := initHdr.Get("Mcp-Session-Id")
	if sessID == "" {
		t.Fatal("missing Mcp-Session-Id header over UDS HTTP")
	}

	badOriginStatus, _, _ := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":22,"method":"ping"}`, map[string]string{
		"Origin":         "https://evil.example",
		"Mcp-Session-Id": sessID,
	})
	if badOriginStatus != http.StatusForbidden {
		t.Fatalf("Origin evil.example over UDS HTTP = %d, want 403", badOriginStatus)
	}

	getReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://unix/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp over UDS = %d, want 405", getResp.StatusCode)
	}

	delReq, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, "http://unix/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	delReq.Header.Set("Mcp-Session-Id", sessID)
	delResp, err := client.Do(delReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /mcp over UDS = %d, want 204", delResp.StatusCode)
	}

	// 4. DEJA_MCP_TOKEN enforcement across raw, Content-Length, and HTTP over UDS, plus --context-search mode.
	t.Setenv("DEJA_MCP_TOKEN", "uds-secret-token")
	authSock := shortUnixSockPath(t, "auth.sock")
	startTestUnixSocketServer(t, dir, authSock, true)

	unauthRaw := repliesByID(sendRawUnixLines(t, authSock, `{"jsonrpc":"2.0","id":31,"method":"ping"}`))
	if !mcpToolIsError(unauthRaw[31]) || !strings.Contains(vaultReplyText(unauthRaw[31]), "unauthorized") {
		t.Fatalf("unauthenticated raw UDS ping = %v, want unauthorized", unauthRaw[31])
	}

	// First request supplies Bearer in _meta.authorization; second request on the same connection reuses sess.isAuthed().
	authRaw := repliesByID(sendRawUnixLines(t, authSock,
		`{"jsonrpc":"2.0","id":32,"method":"initialize","params":{"_meta":{"authorization":"Bearer uds-secret-token"},"clientInfo":{"name":"qwen"}}}`,
		`{"jsonrpc":"2.0","id":33,"method":"tools/list"}`,
	))
	if mcpToolIsError(authRaw[32]) || mcpToolIsError(authRaw[33]) {
		t.Fatalf("authenticated --context-search UDS failed: %v", authRaw)
	}
	resMap, ok := authRaw[33]["result"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected tools/list result shape: %v", authRaw[33])
	}
	toolsList, ok := resMap["tools"].([]any)
	if !ok || len(toolsList) != 1 {
		t.Fatalf("--context-search tools/list len=%d, want 1", len(toolsList))
	}
	firstTool, _ := toolsList[0].(map[string]any)
	if firstTool["name"] != "context_search" {
		t.Fatalf("--context-search returned tool %v, want context_search", firstTool["name"])
	}

	// Content-Length auth failure vs success.
	authCL := dialUnixConn(t, authSock)
	_ = authCL.SetDeadline(time.Now().Add(10 * time.Second))
	if wErr := writeContentLengthFrame(authCL, `{"jsonrpc":"2.0","id":34,"method":"ping"}`); wErr != nil {
		t.Fatal(wErr)
	}
	if wErr := writeContentLengthFrame(authCL, `{"jsonrpc":"2.0","id":35,"method":"ping","params":{"token":"uds-secret-token"}}`); wErr != nil {
		t.Fatal(wErr)
	}
	authCLReader := bufio.NewReader(authCL)
	clDenied, err := readContentLengthFrame(authCLReader)
	if err != nil || !mcpToolIsError(clDenied) || !strings.Contains(vaultReplyText(clDenied), "unauthorized") {
		t.Fatalf("unauthenticated Content-Length got (%v, %v), want unauthorized", clDenied, err)
	}
	clOK, err := readContentLengthFrame(authCLReader)
	if err != nil || mcpToolIsError(clOK) {
		t.Fatalf("authenticated Content-Length got (%v, %v)", clOK, err)
	}
	_ = authCL.Close()

	// HTTP auth failure vs success over UDS.
	authClient := unixHTTPClient(t, authSock)
	unauthStatus, _, _ := postUnixMCPHTTP(t, authClient, `{"jsonrpc":"2.0","id":36,"method":"ping"}`, nil)
	if unauthStatus != http.StatusUnauthorized {
		t.Fatalf("unauthenticated HTTP over UDS = %d, want 401", unauthStatus)
	}
	okStatus, _, okParsed := postUnixMCPHTTP(t, authClient, `{"jsonrpc":"2.0","id":37,"method":"ping"}`, map[string]string{
		"Authorization": "Bearer uds-secret-token",
	})
	if okStatus != http.StatusOK || mcpToolIsError(okParsed) {
		t.Fatalf("authenticated HTTP over UDS = (%d, %v)", okStatus, okParsed)
	}
}

func seedBetaFixSession(t *testing.T, claudeRoot string) {
	t.Helper()
	store := filepath.Join(claudeRoot, "-tmp-beta")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"beta-fix-1", "beta-fix-2"} {
		day := fmt.Sprintf("2026-11-1%d", 7+i)
		lines := []string{
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/tmp/beta","timestamp":"` + day + `T10:00:00Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"beta_cmd_canary_5511"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/tmp/beta","timestamp":"` + day + `T10:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"zsh:1: command not found: betaerr4422"}]}}`,
			`{"type":"assistant","sessionId":"` + id + `","cwd":"/tmp/beta","timestamp":"` + day + `T10:00:02Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"brew install betaremedy6633"}}]}}`,
			`{"type":"user","sessionId":"` + id + `","cwd":"/tmp/beta","timestamp":"` + day + `T10:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"==> Installing betaremedy6633"}]}}`,
		}
		if err := os.WriteFile(filepath.Join(store, id+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMCPConcurrentMultiClientACLIsolation(t *testing.T) {
	tmp, dir := withSecurityVaultEnv(t)
	claudeRoot := filepath.Join(tmp, "claude")
	seedVaultSecuritySessions(t, claudeRoot)
	seedBetaFixSession(t, claudeRoot)
	if err := run([]string{"index"}); err != nil {
		t.Fatalf("index: %v", err)
	}

	polPath := filepath.Join(tmp, "policy.json")
	t.Setenv("DEJA_POLICY_FILE", polPath)
	polJSON := `{
	  "require_clients": true,
	  "clients": {
	    "client-alpha":  {"read": true, "write": false, "projects": ["tmp/alpha"]},
	    "client-beta":   {"read": true, "write": true,  "projects": ["tmp/beta"]},
	    "client-denied": {"read": false, "write": false}
	  },
	  "client_tokens": {
	    "client-alpha":  "` + sha256HexToken("token-alpha") + `",
	    "client-beta":   "` + sha256HexToken("token-beta") + `",
	    "client-denied": "` + sha256HexToken("token-denied") + `"
	  }
	}`
	if err := os.WriteFile(polPath, []byte(polJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	// Verify bounded session store eviction above maxMCPHTTPSessions.
	store := newMCPHTTPSessionStore()
	for i := range maxMCPHTTPSessions + 8 {
		_ = store.getOrCreate(fmt.Sprintf("s-%d", i))
	}
	store.mu.Lock()
	storeCount := len(store.sessions)
	store.mu.Unlock()
	if storeCount > maxMCPHTTPSessions {
		t.Fatalf("session store exceeded bound: %d > %d", storeCount, maxMCPHTTPSessions)
	}

	sockPath := shortUnixSockPath(t, "acl.sock")
	stopUDS := startTestUnixSocketServer(t, dir, sockPath, false)

	handler := newMCPHTTPHandler(dir, false, "http-bearer")
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	failf := func(format string, args ...any) {
		errs <- fmt.Sprintf(format, args...)
	}

	// Concurrent UDS raw + UDS HTTP + standalone HTTP handler requests across three distinct client identities.
	for worker := range 4 {
		wg.Add(3)
		go func(id int) {
			defer wg.Done()
			reps := repliesByID(sendRawUnixLines(t, sockPath,
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"client-alpha"},"clientToken":"token-alpha"}}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
				`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_beta_8822"}}}`,
				`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fix","arguments":{"error":"zsh:1: command not found: betaerr4422"}}}`,
				`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"orient","arguments":{"project":"tmp/beta"}}}`,
				`{"jsonrpc":"2.0","id":6,"method":"prompts/get","params":{"name":"deja","arguments":{"query":"canary_beta_8822"}}}`,
				`{"jsonrpc":"2.0","id":7,"method":"resources/list"}`,
				`{"jsonrpc":"2.0","id":8,"method":"resources/read","params":{"uri":"deja://session/claude:sess-beta-1"}}`,
				fmt.Sprintf(`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"remember","arguments":{"text":"alpha_write_%d","project":"tmp/alpha"}}}`, id),
			))
			if mcpToolIsError(reps[2]) || !strings.Contains(vaultReplyText(reps[2]), "canary_alpha_9911") {
				failf("alpha worker %d missed canary_alpha_9911: %v", id, reps[2])
			}
			for _, checkID := range []int{3, 4, 5, 6} {
				txt := vaultReplyText(reps[checkID])
				if strings.Contains(txt, "beta internal secret") || strings.Contains(txt, "betaremedy6633") || strings.Contains(txt, "beta_cmd_canary_5511") {
					failf("alpha worker %d leaked beta data on call %d: %s", id, checkID, txt)
				}
			}
			resBytes, mErr := json.Marshal(reps[7])
			if mErr != nil {
				failf("alpha worker %d marshal resources/list: %v", id, mErr)
			}
			if strings.Contains(string(resBytes), "sess-beta-1") || strings.Contains(string(resBytes), "beta-fix-1") {
				failf("alpha worker %d resources/list leaked beta session: %s", id, resBytes)
			}
			if !mcpToolIsError(reps[8]) || strings.Contains(vaultReplyText(reps[8]), "canary_beta_8822") {
				failf("alpha worker %d resources/read beta succeeded or leaked: %v", id, reps[8])
			}
			if !mcpToolIsError(reps[9]) {
				failf("alpha worker %d readonly remember succeeded: %v", id, reps[9])
			}
		}(worker)

		go func(id int) {
			defer wg.Done()
			client := unixHTTPClient(t, sockPath)
			hdrs := map[string]string{
				"X-Deja-Client":       "client-beta",
				"X-Deja-Client-Token": "token-beta",
			}
			initStatus, initHdr, pInit := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, hdrs)
			if initStatus != http.StatusOK || mcpToolIsError(pInit) {
				failf("beta worker %d init failed: (%d, %v)", id, initStatus, pInit)
				return
			}
			sid := initHdr.Get("Mcp-Session-Id")
			callHdrs := map[string]string{
				"Mcp-Session-Id":      sid,
				"X-Deja-Client-Token": "token-beta",
			}
			_, _, pRecall := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_beta_8822"}}}`, callHdrs)
			if mcpToolIsError(pRecall) || !strings.Contains(vaultReplyText(pRecall), "canary_beta_8822") {
				failf("beta worker %d missed canary_beta_8822: %v", id, pRecall)
			}
			_, _, pFix := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fix","arguments":{"error":"zsh:1: command not found: betaerr4422"}}}`, callHdrs)
			if mcpToolIsError(pFix) || !strings.Contains(vaultReplyText(pFix), "betaremedy6633") {
				failf("beta worker %d missed beta remedy: %v", id, pFix)
			}
			_, _, pLeak := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`, callHdrs)
			if strings.Contains(vaultReplyText(pLeak), "alpha deployment secret") {
				failf("beta worker %d leaked canary_alpha_9911: %v", id, pLeak)
			}
			_, _, pDenyWrite := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"remember","arguments":{"text":"beta_bad_write","project":"tmp/alpha"}}}`, callHdrs)
			if !mcpToolIsError(pDenyWrite) {
				failf("beta worker %d wrote to forbidden project tmp/alpha: %v", id, pDenyWrite)
			}
			_, _, pAllowWrite := postUnixMCPHTTP(t, client, fmt.Sprintf(`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"remember","arguments":{"text":"beta_note_ok_%d","project":"tmp/beta"}}}`, id), callHdrs)
			if mcpToolIsError(pAllowWrite) {
				failf("beta worker %d allowed remember failed: %v", id, pAllowWrite)
			}
		}(worker)

		go func(id int) {
			defer wg.Done()
			hdrs := map[string]string{
				"Authorization":       "Bearer http-bearer",
				"Mcp-Session-Id":      fmt.Sprintf("denied-%d", id),
				"X-Deja-Client":       "client-denied",
				"X-Deja-Client-Token": "token-denied",
			}
			for _, method := range []string{
				`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`,
				`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
				`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"deja","arguments":{"query":"canary_alpha_9911"}}}`,
				`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
			} {
				_, parsed := serveHTTPHandlerPOST(t, handler, method, hdrs)
				if !mcpToolIsError(parsed) {
					failf("denied HTTP worker %d succeeded on %s: %v", id, method, parsed)
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}

	// Explicit denied client check on prompts/list & tools/call + --context-search UDS client isolation.
	deniedReplies := repliesByID(sendRawUnixLines(t, sockPath,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"client-denied"},"clientToken":"token-denied"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"deja","arguments":{"query":"canary_alpha_9911"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"deja://session/claude:sess-alpha-1"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"recall","arguments":{"query":"canary_alpha_9911"}}}`,
	))
	for id := 2; id <= 6; id++ {
		if !mcpToolIsError(deniedReplies[id]) {
			t.Errorf("client-denied call id=%d unexpectedly succeeded: %v", id, deniedReplies[id])
		}
	}
	stopUDS()

	// Verify locked vault fails closed across UDS (standard and --context-search) + hides active project trailer.
	const pass = "acl-vault-lock-pass" //nolint:gosec // G101: synthetic test passphrase
	if _, err := vault.Init(dir, pass, 30*time.Minute); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	if err := vault.Lock(dir, pass); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	vault.ClearCache()
	t.Setenv(vault.EnvVaultPassphrase, "")

	if trailer := activeProjectInstructionTrailer(dir); trailer != "" {
		t.Fatalf("activeProjectInstructionTrailer leaked while locked: %q", trailer)
	}
	lockedCS := shortUnixSockPath(t, "locked-cs.sock")
	startTestUnixSocketServer(t, dir, lockedCS, true)
	csLocked := repliesByID(sendRawUnixLines(t, lockedCS,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"client-alpha"},"clientToken":"token-alpha"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"context_search","arguments":{"query":"canary_alpha_9911"}}}`,
	))
	if !mcpToolIsError(csLocked[2]) {
		t.Fatalf("--context-search succeeded while vault locked: %v", csLocked[2])
	}
}

func TestMCPLateRecallIsolatedAcrossClients(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	h := &servedHandler{served: map[string]int{"alpha-secret-query": 2}}
	h.install(t)
	setDeadline(t, 50*time.Millisecond)

	sockPath := shortUnixSockPath(t, "late-iso.sock")
	startTestUnixSocketServer(t, dir, sockPath, false)

	client := unixHTTPClient(t, sockPath)
	statusA, hdrAInit, parsedA := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"client-alpha"}}}`, map[string]string{
		"X-Deja-Client":       "client-alpha",
		"X-Deja-Client-Token": "token-alpha",
	})
	if statusA != http.StatusOK || mcpToolIsError(parsedA) {
		t.Fatalf("client-alpha initialize = (%d, %v)", statusA, parsedA)
	}
	sidA := hdrAInit.Get("Mcp-Session-Id")
	if sidA == "" {
		t.Fatal("client-alpha missing Mcp-Session-Id")
	}

	statusB, hdrBInit, parsedB := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"client-beta"}}}`, map[string]string{
		"X-Deja-Client":       "client-beta",
		"X-Deja-Client-Token": "token-beta",
	})
	if statusB != http.StatusOK || mcpToolIsError(parsedB) {
		t.Fatalf("client-beta initialize = (%d, %v)", statusB, parsedB)
	}
	sidB := hdrBInit.Get("Mcp-Session-Id")
	if sidB == "" {
		t.Fatal("client-beta missing Mcp-Session-Id")
	}

	hdrA := map[string]string{"Mcp-Session-Id": sidA, "X-Deja-Client-Token": "token-alpha"}
	hdrB := map[string]string{"Mcp-Session-Id": sidB, "X-Deja-Client-Token": "token-beta"}

	_, _, slowRep := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"recall","query":"alpha-secret-query"}}}`, hdrA)
	if slowTxt := replyText(slowRep); !strings.Contains(slowTxt, "has not finished") {
		t.Fatalf("want wait note for overdue recall, got %q (%#v)", slowTxt, slowRep)
	}
	close(h.gate("alpha-secret-query"))
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		t.Fatal("alpha-secret-query handler never finished")
	}
	// Let serveRequest's goroutine store the overdue answer under srv.mu.
	time.Sleep(100 * time.Millisecond)

	setDeadline(t, 0)
	close(h.gate("beta-query"))
	_, _, betaRep := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"recall","query":"beta-query"}}}`, hdrB)
	betaTxt := replyText(betaRep)
	if betaTxt != "answer for beta-query" || strings.Contains(betaTxt, "alpha-secret-query") || strings.Contains(betaTxt, "earlier answer") {
		t.Fatalf("client-beta received client-alpha late recall: %q", betaTxt)
	}

	close(h.gate("alpha-next"))
	_, _, alphaRep := postUnixMCPHTTP(t, client, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"recall","query":"alpha-next"}}}`, hdrA)
	alphaTxt := replyText(alphaRep)
	if !strings.HasPrefix(alphaTxt, `deja: earlier answer for recall "alpha-secret-query"`) || !strings.HasSuffix(alphaTxt, "answer for alpha-next") {
		t.Fatalf("client-alpha did not receive its own late recall: %q", alphaTxt)
	}
}

func TestMigrateCmdSidecars(t *testing.T) {
	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(i + 3)
	}
	if err := migrateCmdSidecars("", dek); err != nil {
		t.Fatalf("empty dir = %v", err)
	}
	if err := migrateCmdSidecars(t.TempDir(), []byte{1, 2}); err != nil {
		t.Fatalf("short dek = %v", err)
	}
	missingDir := filepath.Join(t.TempDir(), "index.db")
	if err := migrateCmdSidecars(missingDir, dek); err != nil {
		t.Fatalf("missing deferred root = %v", err)
	}

	nonDirBase := filepath.Join(t.TempDir(), "index.db")
	if err := os.MkdirAll(nonDirBase, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deferredRoot(nonDirBase), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateCmdSidecars(nonDirBase, dek); err != nil {
		t.Fatalf("non-dir deferred root = %v", err)
	}

	idxDir := filepath.Join(t.TempDir(), "index.db")
	root := deferredRoot(idxDir)
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	hiddenFile := filepath.Join(root, ".hidden", "skip.txt")
	if err := os.WriteFile(hiddenFile, []byte("skip-dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	dotFile := filepath.Join(root, ".dotfile")
	if err := os.WriteFile(dotFile, []byte("skip-dot"), 0o600); err != nil {
		t.Fatal(err)
	}
	plainFile := filepath.Join(root, "sub", "item.txt")
	if err := os.WriteFile(plainFile, []byte("CANARY_DEFERRED_PLAINTEXT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Symlink(plainFile, filepath.Join(root, "link.txt"))
	}

	if err := migrateCmdSidecars(idxDir, dek); err != nil {
		t.Fatalf("migrateCmdSidecars: %v", err)
	}
	sealedRaw, err := os.ReadFile(plainFile)
	if err != nil || !vault.IsSealedBytes(sealedRaw) || bytes.Contains(sealedRaw, []byte("CANARY_DEFERRED_PLAINTEXT")) {
		t.Fatalf("deferred file not sealed: (%v, %v)", vault.IsSealedBytes(sealedRaw), err)
	}
	opened, err := vault.OpenBytes(dek, vault.DomainSidecar, sealedRaw)
	if err != nil || string(opened) != "CANARY_DEFERRED_PLAINTEXT" {
		t.Fatalf("OpenBytes deferred = (%q, %v)", opened, err)
	}
	dotRaw, err := os.ReadFile(dotFile)
	if err != nil || string(dotRaw) != "skip-dot" {
		t.Fatalf("dotfile unexpectedly modified: %q (%v)", dotRaw, err)
	}
	if err := migrateCmdSidecars(idxDir, dek); err != nil {
		t.Fatalf("migrateCmdSidecars idempotent: %v", err)
	}
	regFile := filepath.Join(t.TempDir(), "file-not-dir")
	if wErr := os.WriteFile(regFile, []byte("x"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if runtime.GOOS != "windows" {
		if mErr := migrateCmdSidecars(regFile, dek); mErr == nil {
			t.Fatal("migrateCmdSidecars under regular file succeeded unexpectedly")
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write boom")
}

func TestMCPUnixSocketEdgeCases(t *testing.T) {
	hermeticEnv(t)
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	if _, err := expandMCPSocketPath("   "); err == nil {
		t.Fatal("expandMCPSocketPath blank succeeded")
	}
	if got, err := expandMCPSocketPath("~"); err != nil || got != homeDir {
		t.Fatalf("expandMCPSocketPath(~) = (%q, %v), want %q", got, err, homeDir)
	}
	if got, err := expandMCPSocketPath("~/sub/mcp.sock"); err != nil || got != filepath.Join(homeDir, "sub", "mcp.sock") {
		t.Fatalf("expandMCPSocketPath(~/sub/mcp.sock) = (%q, %v)", got, err)
	}
	if runtime.GOOS != "windows" {
		t.Setenv("HOME", "")
		if _, err := expandMCPSocketPath("~/mcp.sock"); err == nil {
			t.Fatal("expandMCPSocketPath with empty HOME succeeded")
		}
		t.Setenv("HOME", homeDir)
	}

	// prepareMCPUnixSocket directory creation, conflict guards, and stale socket replacement.
	regParent := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(regParent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareMCPUnixSocket(filepath.Join(regParent, "mcp.sock")); err == nil {
		t.Fatal("prepareMCPUnixSocket non-dir parent succeeded")
	}

	shortBase, err := allocShortSockDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortBase) })
	nestedSock := filepath.Join(shortBase, "created", "mcp.sock")
	ln, info, err := prepareMCPUnixSocket(nestedSock)
	if err != nil {
		t.Fatalf("prepareMCPUnixSocket create parent: %v", err)
	}
	// Active socket guard.
	if _, _, inUseErr := prepareMCPUnixSocket(nestedSock); inUseErr == nil || !strings.Contains(inUseErr.Error(), "already in use") {
		_ = ln.Close()
		t.Fatalf("prepareMCPUnixSocket active = %v, want already-in-use", inUseErr)
	}
	_ = ln.Close()
	cleanupMCPUnixSocket(nestedSock, info)

	// Stale socket replacement.
	staleSock := filepath.Join(shortBase, "stale.sock")
	staleLn, sErr := (&net.ListenConfig{}).Listen(t.Context(), "unix", staleSock)
	if sErr != nil {
		t.Fatalf("listen stale: %v", sErr)
	}
	_ = staleLn.Close()
	reboundLn, reboundInfo, rbErr := prepareMCPUnixSocket(staleSock)
	if rbErr != nil {
		t.Fatalf("prepareMCPUnixSocket stale replacement: %v", rbErr)
	}
	_ = reboundLn.Close()
	cleanupMCPUnixSocket(staleSock, reboundInfo)

	// Non-socket file guard & symlink guards.
	nonSock := filepath.Join(shortBase, "plain.sock")
	if wErr := os.WriteFile(nonSock, []byte("plain"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, nsErr := prepareMCPUnixSocket(nonSock); nsErr == nil {
		t.Fatal("prepareMCPUnixSocket non-socket target succeeded")
	}
	if runtime.GOOS != "windows" {
		symParent := filepath.Join(shortBase, "symdir")
		_ = os.Symlink(shortBase, symParent)
		if _, _, spErr := prepareMCPUnixSocket(filepath.Join(symParent, "s.sock")); spErr == nil {
			t.Fatal("prepareMCPUnixSocket symlink parent succeeded")
		}
		symTarget := filepath.Join(shortBase, "symtarget.sock")
		_ = os.Symlink(nonSock, symTarget)
		if _, _, stErr := prepareMCPUnixSocket(symTarget); stErr == nil {
			t.Fatal("prepareMCPUnixSocket symlink target succeeded")
		}
	}

	// contentLengthWriter & channelListener helpers.
	cw := &contentLengthWriter{dst: failWriter{}}
	if n, wErr := cw.Write([]byte("\n")); wErr != nil || n != 1 {
		t.Fatalf("contentLengthWriter empty line = (%d, %v)", n, wErr)
	}
	if _, wErr := cw.Write([]byte("{}")); wErr == nil {
		t.Fatal("contentLengthWriter failWriter succeeded")
	}
	c1, c2 := net.Pipe()
	cl := newChannelListener(staleLn.Addr())
	if cl.Addr() == nil {
		t.Fatal("channelListener.Addr returned nil")
	}
	if !cl.enqueue(c1, t.Context().Done()) {
		t.Fatal("enqueue before close returned false")
	}
	_ = cl.Close()
	_ = c2.Close()
	c3, c4 := net.Pipe()
	defer func() {
		_ = c3.Close()
		_ = c4.Close()
	}()
	if cl.enqueue(c3, t.Context().Done()) {
		t.Fatal("enqueue after close returned true")
	}

	// Live server framing edge cases: EOF, non-JSON fallback line, oversized line, Content-Length errors.
	dir := index.DefaultDir()
	if eErr := index.Ensure(dir, "", true, nil); eErr != nil {
		t.Fatal(eErr)
	}
	edgeSock := shortUnixSockPath(t, "edge.sock")
	startTestUnixSocketServer(t, dir, edgeSock, false)

	eofConn := dialUnixConn(t, edgeSock)
	_ = eofConn.Close()

	badCLReplies := sendRawUnixLines(t, edgeSock, "PING")
	if len(badCLReplies) == 0 {
		t.Fatal("expected parse error response for PING")
	}

	overConn := dialUnixConn(t, edgeSock)
	_ = overConn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = overConn.Write(bytes.Repeat([]byte("A"), 70*1024))
	if uc, ok := overConn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	overResp, _ := io.ReadAll(overConn)
	_ = overConn.Close()
	if !bytes.Contains(overResp, []byte("-32700")) {
		t.Fatalf("oversized line response = %q, want -32700", overResp)
	}

	for _, rawFrame := range []string{
		"Content-Length: -1\r\n\r\n",
		"Content-Length: notanumber\r\n\r\n",
		"Content-Length: 999999999\r\n\r\n",
		"Content-Type: application/json\r\n\r\n",
		"Content-Length: 50\r\n\r\n{\"jsonrpc\":\"2.0\"",
	} {
		clConn := dialUnixConn(t, edgeSock)
		_ = clConn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = io.WriteString(clConn, rawFrame)
		if uc, ok := clConn.(*net.UnixConn); ok {
			_ = uc.CloseWrite()
		}
		_, _ = io.ReadAll(clConn)
		_ = clConn.Close()
	}

	// Direct helper & Content-Length loop / writeErr / cleanup / stop-enqueue coverage.
	closedStop := make(chan struct{})
	close(closedStop)
	if sErr := serveMCPUnixSocketUntil(dir, "   ", false, closedStop); sErr == nil {
		t.Fatal("serveMCPUnixSocketUntil blank path succeeded")
	}
	if !looksLikeHTTPRequestLine([]byte("POST /mcp HTTP/1.1\r\n")) ||
		looksLikeHTTPRequestLine([]byte("BOGUS /mcp HTTP/1.1\r\n")) ||
		looksLikeHTTPRequestLine([]byte("POST /mcp\r\n")) {
		t.Fatal("looksLikeHTTPRequestLine classification mismatch")
	}
	if looksLikeContentLengthHeader([]byte("Host: localhost\r\n")) {
		t.Fatal("looksLikeContentLengthHeader matched Host header")
	}
	// cleanupMCPUnixSocket must not remove a replacement regular file.
	keepFile := filepath.Join(shortBase, "keep-replacement")
	keepLn, keepInfo, keepErr := prepareMCPUnixSocket(keepFile)
	if keepErr != nil {
		t.Fatalf("prepareMCPUnixSocket keep: %v", keepErr)
	}
	_ = keepLn.Close()
	_ = os.Remove(keepFile)
	if wErr := os.WriteFile(keepFile, []byte("keep"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	cleanupMCPUnixSocket(keepFile, keepInfo)
	if _, stErr := os.Stat(keepFile); stErr != nil {
		t.Fatalf("cleanupMCPUnixSocket removed replacement regular file: %v", stErr)
	}

	// Second Write failure in contentLengthWriter.
	secondFail := &nthFailWriter{failAt: 2}
	if _, wErr := (&contentLengthWriter{dst: secondFail}).Write([]byte(`{"ok":true}`)); wErr == nil {
		t.Fatal("contentLengthWriter second write failure returned nil")
	}

	// channelListener enqueue with closed stop + Accept after Close.
	cl2 := newChannelListener(staleLn.Addr())
	pc1, pc2 := net.Pipe()
	if cl2.enqueue(pc1, closedStop) {
		t.Fatal("enqueue on closed stop succeeded")
	}
	_ = pc1.Close()
	_ = pc2.Close()
	_ = cl2.Close()
	if _, aErr := cl2.Accept(); !errors.Is(aErr, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want net.ErrClosed", aErr)
	}

	// serveMCPContentLength: multi-frame pendingHeader == nil path + writeErr exit + header overflow.
	sess := newMCPClientSession("")
	f1 := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"cl-test"}}}`
	f2 := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	f3 := `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	clStream := fmt.Sprintf(
		"Content-Length: %d\r\n\r\n%sContent-Length: %d\r\n\r\n%sContent-Length: %d\r\n\r\n%s",
		len(f1), f1, len(f2), f2, len(f3), f3,
	)
	var outBuf bytes.Buffer
	firstHdr, rest, _ := strings.Cut(clStream, "\n")
	serveMCPContentLength(dir, false, "", sess, []byte(firstHdr+"\n"), bufio.NewReader(strings.NewReader(rest)), &outBuf)
	if !bytes.Contains(outBuf.Bytes(), []byte("Content-Length:")) {
		t.Fatalf("serveMCPContentLength multi-frame wrote no framed output: %q", outBuf.String())
	}

	// Two frames written to failWriter forces srv.writeErr() != nil check on loop re-entry.
	serveMCPContentLength(dir, false, "", sess, []byte(firstHdr+"\n"), bufio.NewReader(strings.NewReader(rest)), failWriter{})

	// Oversized header line (>64KiB) hits bufio.ErrBufferFull both initially and inside header loop.
	bigHdr := bytes.Repeat([]byte("X"), 70*1024)
	var bigOut bytes.Buffer
	serveMCPContentLength(dir, false, "", sess, []byte("X-Ok: 1\r\n"), bufio.NewReaderSize(bytes.NewReader(bigHdr), 64*1024), &bigOut)
	if !bytes.Contains(bigOut.Bytes(), []byte("-32700")) {
		t.Fatalf("serveMCPContentLength oversized secondary header = %q, want -32700", bigOut.String())
	}
	var bigOut2 bytes.Buffer
	serveMCPContentLength(dir, false, "", sess, nil, bufio.NewReaderSize(bytes.NewReader(bigHdr), 64*1024), &bigOut2)
	if !bytes.Contains(bigOut2.Bytes(), []byte("-32700")) {
		t.Fatalf("serveMCPContentLength oversized initial header = %q, want -32700", bigOut2.String())
	}

	// serveMCPUnixConn HTTP dispatch aborted by closed stop & no-newline short line.
	p1, p2 := net.Pipe()
	go func() {
		_, _ = io.WriteString(p2, "POST /mcp HTTP/1.1\r\n")
		_ = p2.Close()
	}()
	if serveMCPUnixConn(dir, false, "", p1, cl2, closedStop) {
		t.Fatal("serveMCPUnixConn with closed stop unexpectedly forwarded HTTP conn")
	}
	_ = p1.Close()

	q1, q2 := net.Pipe()
	go func() {
		_, _ = io.WriteString(q2, "X")
		_ = q2.Close()
	}()
	if serveMCPUnixConn(dir, false, "", q1, cl2, t.Context().Done()) {
		t.Fatal("serveMCPUnixConn short EOF unexpectedly forwarded HTTP conn")
	}
	_ = q1.Close()
}

type nthFailWriter struct {
	calls  int
	failAt int
}

func (w *nthFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.failAt {
		return 0, errors.New("nth write boom")
	}
	return len(p), nil
}

func TestMCPClientSessionAndGoroutineIsolation(t *testing.T) {
	var nilSess *mcpClientSession
	nilSess.setConnID("x")
	nilSess.setClientName("x")
	nilSess.setClientToken("x")
	nilSess.setAuthed(true)
	if nilSess.connIDValue() != "" || nilSess.clientName() != "" || nilSess.tokenValue() != "" || nilSess.isAuthed() {
		t.Fatal("nil mcpClientSession getters returned non-zero values")
	}

	s1 := newMCPClientSession("")
	if s1.connIDValue() == "" {
		t.Fatal("newMCPClientSession(\"\") produced empty connID")
	}
	s1.setConnID("c-1")
	s1.setClientName("openclaw-a")
	s1.setClientToken("tok-a")
	s1.setAuthed(true)
	if s1.connIDValue() != "c-1" || s1.clientName() != "openclaw-a" || s1.tokenValue() != "tok-a" || !s1.isAuthed() {
		t.Fatalf("mcpClientSession state mismatch: id=%q client=%q tok=%q authed=%v", s1.connIDValue(), s1.clientName(), s1.tokenValue(), s1.isAuthed())
	}

	s2 := newMCPClientSession("c-2")
	withMCPClientSession(nil, func() {
		if currentMCPClientSession() != nil {
			t.Fatal("expected nil session outside withMCPClientSession")
		}
	})
	withMCPClientSession(s1, func() {
		setMCPClient("openclaw-nested-1")
		withMCPClientSession(s2, func() {
			if got := currentMCPClientSession(); got != s2 {
				t.Fatalf("inner currentMCPClientSession = %v, want s2", got)
			}
			setMCPClient("openclaw-nested-2")
		})
		if got := currentMCPClientSession(); got != s1 || s1.clientName() != "openclaw-nested-1" || s2.clientName() != "openclaw-nested-2" {
			t.Fatalf("restored session mismatch: cur=%v s1=%q s2=%q", got, s1.clientName(), s2.clientName())
		}
	})
	if currentMCPClientSession() != nil {
		t.Fatal("session leaked after withMCPClientSession exit")
	}
}

func TestMCPUnixSocketCoverageEdges(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := index.Ensure(dir, "", true, nil); err != nil {
		t.Fatalf("index.Ensure: %v", err)
	}

	// Empty path exercises serveMCPUnixSocketProcess immediately without blocking for signals.
	if pErr := serveMCPUnixSocketProcess(dir, "", false); pErr == nil {
		t.Fatal("serveMCPUnixSocketProcess empty path succeeded")
	}

	// Non-existent nested parent directory is created with 0o700.
	baseDir, aErr := allocShortSockDir()
	if aErr != nil {
		t.Fatal(aErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(baseDir) })
	nestedParent := filepath.Join(baseDir, "sub", "dir")
	nestedSock := filepath.Join(nestedParent, "deja.sock")
	stopNested := startTestUnixSocketServer(t, dir, nestedSock, false)
	if st, sErr := os.Stat(nestedParent); sErr != nil || !st.IsDir() {
		t.Fatalf("nested parent dir not created: st=%v err=%v", st, sErr)
	}
	stopNested()

	// Parent path traversing a regular file fails MkdirAll cleanly.
	fileAncestor := filepath.Join(baseDir, "file-parent")
	if wErr := os.WriteFile(fileAncestor, []byte("x"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if _, _, pErr := prepareMCPUnixSocket(filepath.Join(fileAncestor, "child", "deja.sock")); pErr == nil {
		t.Fatal("prepareMCPUnixSocket under file ancestor succeeded")
	}

	// Stale Unix socket left unlinked on disk is dialed, replaced, and serves active connections closed on shutdown.
	staleSock := filepath.Join(baseDir, "stale.sock")
	uln, lErr := (&net.ListenConfig{}).Listen(t.Context(), "unix", staleSock)
	if lErr != nil {
		t.Fatalf("Listen stale unix socket: %v", lErr)
	}
	if ul, ok := uln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	_ = uln.Close()

	stopStale := startTestUnixSocketServer(t, dir, staleSock, false)

	// Open a persistent Content-Length connection, wait for its reply so liveConns
	// tracks it deterministically, then leave it open across shutdown.
	liveConn := dialUnixConn(t, staleSock)
	t.Cleanup(func() { _ = liveConn.Close() })
	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"edge-live"}}}`
	initFrame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(initBody), initBody)
	if _, wErr := io.WriteString(liveConn, initFrame); wErr != nil {
		t.Fatal(wErr)
	}
	liveBR := bufio.NewReader(liveConn)
	_ = liveConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	clHdr, hdrErr := liveBR.ReadString('\n')
	if hdrErr != nil || !strings.Contains(clHdr, "Content-Length:") {
		t.Fatalf("liveConn Content-Length header = %q (%v)", clHdr, hdrErr)
	}
	if blank, bErr := liveBR.ReadString('\n'); bErr != nil || strings.TrimSpace(blank) != "" {
		t.Fatalf("liveConn header separator = %q (%v)", blank, bErr)
	}
	nLen, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(clHdr, "Content-Length:")))
	respBuf := make([]byte, nLen)
	if _, rErr := io.ReadFull(liveBR, respBuf); rErr != nil {
		t.Fatalf("liveConn body read: %v", rErr)
	}
	_ = liveConn.SetReadDeadline(time.Time{})

	// Non-HTTP, non-Content-Length first line falls through to serveMCPBuffered.
	fallbackReplies := sendRawUnixLines(t, staleSock, "XYZ not json\n")
	if len(fallbackReplies) != 1 || !mcpToolIsError(fallbackReplies[0]) {
		t.Fatalf("fallback non-JSON line replies = %#v", fallbackReplies)
	}

	// Oversized first line triggers parse error in serveMCPUnixConn.
	overConn := dialUnixConn(t, staleSock)
	_ = overConn.SetDeadline(time.Now().Add(5 * time.Second))
	bigLine := bytes.Repeat([]byte{'X'}, 70*1024)
	_, _ = overConn.Write(bigLine)
	if uc, ok := overConn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	overResp, _ := io.ReadAll(overConn)
	_ = overConn.Close()
	if !strings.Contains(string(overResp), "-32700") {
		t.Fatalf("oversized line reply = %q, want -32700", overResp)
	}

	// Invalid Content-Length header returns -32700 Content-Length parse error.
	badCLConn := dialUnixConn(t, staleSock)
	_ = badCLConn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(badCLConn, "Content-Length: nope\r\n\r\n")
	if uc, ok := badCLConn.(*net.UnixConn); ok {
		_ = uc.CloseWrite()
	}
	badCLResp, _ := io.ReadAll(badCLConn)
	_ = badCLConn.Close()
	if !strings.Contains(string(badCLResp), "-32700") {
		t.Fatalf("invalid Content-Length reply = %q, want -32700", badCLResp)
	}

	// Stop server while liveConn is still open so closeAll iterates liveConns.
	stopStale()

	// channelListener edge branches: closed stop, full channel unblock, closed Accept.
	cl := newChannelListener(&net.UnixAddr{Name: staleSock, Net: "unix"})
	closedStop := make(chan struct{})
	close(closedStop)
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})
	if cl.enqueue(c1, closedStop) {
		t.Fatal("enqueue succeeded with closed stop")
	}
	openStop := make(chan struct{})
	for range cap(cl.ch) {
		pA, pB := net.Pipe()
		t.Cleanup(func() {
			_ = pA.Close()
			_ = pB.Close()
		})
		if !cl.enqueue(pA, openStop) {
			t.Fatal("enqueue failed before channel full")
		}
	}
	if cl.enqueue(c1, closedStop) {
		t.Fatal("enqueue succeeded on full channel with closed stop")
	}
	go func() { close(openStop) }()
	if cl.enqueue(c1, openStop) {
		t.Fatal("enqueue succeeded as openStop closed")
	}
	_ = cl.Close()
	if cl.enqueue(c1, make(chan struct{})) {
		t.Fatal("enqueue succeeded after Close")
	}
	if _, accErr := cl.Accept(); !errors.Is(accErr, net.ErrClosed) {
		t.Fatalf("Accept after Close = %v, want net.ErrClosed", accErr)
	}

	// mcp_http.go session store & header sanitizer edge cases.
	store := newMCPHTTPSessionStore()
	store.set(nil)
	store.set(&mcpClientSession{})
	sessA := newMCPClientSession("sess-edge-1")
	store.set(sessA)
	store.set(sessA)
	if cleanMCPHeaderText("bad\x01ctrl", 32) != "" {
		t.Fatal("cleanMCPHeaderText accepted control character")
	}
	if cleanMCPSessionID("bad\x01id") != "" {
		t.Fatal("cleanMCPSessionID accepted control character")
	}

	// mcp.go serveMCPBuffered nil session, blank lines, unauthed batch, and empty auth token.
	var mcpBuf bytes.Buffer
	batchIn := "\n[{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}]\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"ping\"}\n"
	if sErr := serveMCPBuffered(dir, bufio.NewReader(strings.NewReader(batchIn)), &mcpBuf, false, nil, "secret-tok"); sErr != nil {
		t.Fatalf("serveMCPBuffered: %v", sErr)
	}
	if !strings.Contains(mcpBuf.String(), "-32001") {
		t.Fatalf("unauthed batch missing -32001: %s", mcpBuf.String())
	}
	if !mcpRequestMatchesAuthToken(nil, "") {
		t.Fatal("mcpRequestMatchesAuthToken empty authToken returned false")
	}

	// sameSocketFile rejects empty/nil and non-socket paths.
	if sameSocketFile("", nil) {
		t.Fatal("sameSocketFile(\"\", nil) = true, want false")
	}
	if ancestorInfo, ancErr := os.Stat(fileAncestor); ancErr != nil || sameSocketFile(fileAncestor, ancestorInfo) {
		t.Fatalf("sameSocketFile on non-socket file = err:%v", ancErr)
	}
}
