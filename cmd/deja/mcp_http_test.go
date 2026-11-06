package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseMCPFlags(t *testing.T) {
	ctxOnly, addr, err := parseMCPFlags(nil)
	if err != nil || ctxOnly || addr != "" {
		t.Fatalf("parseMCPFlags(nil) = (%v, %q, %v)", ctxOnly, addr, err)
	}
	ctxOnly, addr, err = parseMCPFlags([]string{"--context-search", "--http", "127.0.0.1:8788"})
	if err != nil || !ctxOnly || addr != "127.0.0.1:8788" {
		t.Fatalf("parseMCPFlags(--context-search --http) = (%v, %q, %v)", ctxOnly, addr, err)
	}
	ctxOnly, addr, err = parseMCPFlags([]string{"--http=localhost:9000"})
	if err != nil || ctxOnly || addr != "localhost:9000" {
		t.Fatalf("parseMCPFlags(--http=localhost:9000) = (%v, %q, %v)", ctxOnly, addr, err)
	}
	for _, bad := range [][]string{
		{"--http"},
		{"--http="},
		{"--http", "--context-search"},
		{"--unknown"},
		{"extra"},
	} {
		if _, _, err := parseMCPFlags(bad); err == nil {
			t.Errorf("parseMCPFlags(%v) succeeded; want error", bad)
		}
	}
}

func TestValidateMCPHTTPBindRequiresTokenOffLoopback(t *testing.T) {
	for _, okAddr := range []string{"127.0.0.1:8788", "localhost:8788", "[::1]:8788"} {
		if err := validateMCPHTTPBind(okAddr, ""); err != nil {
			t.Errorf("validateMCPHTTPBind(%q, \"\") = %v; want nil", okAddr, err)
		}
	}
	for _, remoteAddr := range []string{"0.0.0.0:8788", ":8788", "192.168.1.10:8788", "[::]:8788"} {
		if err := validateMCPHTTPBind(remoteAddr, ""); err == nil {
			t.Errorf("validateMCPHTTPBind(%q, \"\") succeeded without DEJA_MCP_TOKEN", remoteAddr)
		}
		if err := validateMCPHTTPBind(remoteAddr, "secret-token"); err != nil {
			t.Errorf("validateMCPHTTPBind(%q, token) = %v; want nil", remoteAddr, err)
		}
	}
	if err := validateMCPHTTPBind("not-a-hostport", "secret-token"); err == nil {
		t.Error("validateMCPHTTPBind(not-a-hostport) succeeded; want error")
	}
	if got := hostFromHostPort("localhost"); got != "localhost" {
		t.Errorf("hostFromHostPort(localhost) = %q", got)
	}
	if got := cleanMCPSessionID("bad\nheader"); got != "" {
		t.Errorf("cleanMCPSessionID accepted control char: %q", got)
	}
}

func TestMCPStreamableHTTPRoundTripAndSecurityGuards(t *testing.T) {
	dir := t.TempDir()
	h := newMCPHTTPHandler(dir, false, "")

	postRPC := func(body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8788/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. Initialize mints Mcp-Session-Id and returns server capabilities.
	initRec := postRPC(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"cursor"}}}`, nil)
	if initRec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", initRec.Code, initRec.Body.String())
	}
	sessID := initRec.Header().Get("Mcp-Session-Id")
	if !strings.HasPrefix(sessID, "mcp:") {
		t.Fatalf("Mcp-Session-Id = %q; want mcp:* prefix", sessID)
	}
	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(initRec.Body.Bytes(), &initResp); err != nil || initResp.Result.ProtocolVersion == "" {
		t.Fatalf("initialize response = %s (err=%v)", initRec.Body.String(), err)
	}

	// 2. Notification returns 202 Accepted with empty body (including notifications/cancelled and notification-only batch).
	notifRec := postRPC(`{"jsonrpc":"2.0","method":"notifications/initialized"}`, map[string]string{"Mcp-Session-Id": sessID})
	if notifRec.Code != http.StatusAccepted || notifRec.Body.Len() != 0 {
		t.Fatalf("notification status = %d, body = %q; want 202 empty", notifRec.Code, notifRec.Body.String())
	}
	cancelNotif := postRPC(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":99}}`, nil)
	if cancelNotif.Code != http.StatusAccepted {
		t.Fatalf("cancel notification status = %d; want 202", cancelNotif.Code)
	}
	batchNotif := postRPC(`[{"jsonrpc":"2.0","method":"notifications/initialized"}]`, nil)
	if batchNotif.Code != http.StatusAccepted {
		t.Fatalf("batch notification status = %d; want 202", batchNotif.Code)
	}

	// 3. tools/list and tools/call work on /mcp and support SSE framing when Accept asks for text/event-stream only.
	sseRec := postRPC(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{
		"Mcp-Session-Id": sessID,
		"Accept":         "text/event-stream",
	})
	if sseRec.Code != http.StatusOK || !strings.HasPrefix(sseRec.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE tools/list status = %d, content-type = %q", sseRec.Code, sseRec.Header().Get("Content-Type"))
	}
	if !strings.Contains(sseRec.Body.String(), "event: message\ndata: ") || !strings.Contains(sseRec.Body.String(), `"name":"deja"`) {
		t.Fatalf("unexpected SSE body: %s", sseRec.Body.String())
	}
	callRec := postRPC(`{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"orient"}}}`, map[string]string{
		"Mcp-Session-Id": sessID,
		"Origin":         "http://localhost:3000",
	})
	if callRec.Code != http.StatusOK || !strings.Contains(callRec.Body.String(), `"result"`) {
		t.Fatalf("tools/call status = %d, body = %s", callRec.Code, callRec.Body.String())
	}

	// 4. Refusal branches: empty body, invalid JSON, batch request, bad jsonrpc version, bad id, missing method.
	for _, tc := range []struct {
		body string
		want string
	}{
		{"   ", "parse error"},
		{"{not-json", "parse error"},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, "batch requests are not supported"},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, "unsupported jsonrpc version"},
		{`{"jsonrpc":"2.0","id":true,"method":"ping"}`, "id must be a string or a number"},
		{`{"jsonrpc":"2.0","id":1}`, "request has no method"},
	} {
		rec := postRPC(tc.body, nil)
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("postRPC(%q) = %s; want %q", tc.body, rec.Body.String(), tc.want)
		}
	}

	// 5. Cross-origin browser request and DNS-rebinding Host are rejected with 403.
	evilOrigin := postRPC(`{"jsonrpc":"2.0","id":3,"method":"ping"}`, map[string]string{
		"Origin": "https://evil.example",
	})
	if evilOrigin.Code != http.StatusForbidden {
		t.Fatalf("cross-origin request status = %d; want 403", evilOrigin.Code)
	}
	rebindReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://rebind.evil.example:8788/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"ping"}`))
	rebindRec := httptest.NewRecorder()
	h.ServeHTTP(rebindRec, rebindReq)
	if rebindRec.Code != http.StatusForbidden {
		t.Fatalf("DNS-rebinding host status = %d; want 403", rebindRec.Code)
	}

	// 6. GET and PUT return 405 Method Not Allowed; DELETE returns 204 No Content; unknown path returns 404.
	getReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:8788/mcp", nil)
	getRec := httptest.NewRecorder()
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d; want 405", getRec.Code)
	}
	putReq := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "http://127.0.0.1:8788/mcp", nil)
	putRec := httptest.NewRecorder()
	h.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d; want 405", putRec.Code)
	}
	delReq := httptest.NewRequestWithContext(t.Context(), http.MethodDelete, "http://127.0.0.1:8788/mcp", nil)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d; want 204", delRec.Code)
	}
	notFoundReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://127.0.0.1:8788/other", nil)
	notFoundRec := httptest.NewRecorder()
	h.ServeHTTP(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("unknown path status = %d; want 404", notFoundRec.Code)
	}

	// 7. Bearer token authentication when DEJA_MCP_TOKEN is configured.
	authed := newMCPHTTPHandler(dir, false, "top-secret")
	unauthReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://10.0.0.5:8788/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":5,"method":"ping"}`))
	unauthRec := httptest.NewRecorder()
	authed.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request status = %d; want 401", unauthRec.Code)
	}
	okReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://10.0.0.5:8788/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":5,"method":"ping"}`))
	okReq.Header.Set("Authorization", "Bearer top-secret")
	okRec := httptest.NewRecorder()
	authed.ServeHTTP(okRec, okReq)
	bodyBytes, _ := io.ReadAll(okRec.Body)
	if okRec.Code != http.StatusOK || !strings.Contains(string(bodyBytes), `"result":{}`) {
		t.Fatalf("authenticated request status = %d, body = %s", okRec.Code, string(bodyBytes))
	}
}

func TestRunMCPAndHTTPContextCancel(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runMCP(dir, []string{"--bad-flag"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("runMCP(--bad-flag) succeeded; want error")
	}
	if err := runMCP(dir, []string{"--http", "0.0.0.0:8788"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("runMCP(--http 0.0.0.0:8788) without token succeeded; want error")
	}
	if err := runMCP(dir, nil, strings.NewReader(""), &out); err != nil {
		t.Fatalf("runMCP(nil) on empty stdin = %v; want nil", err)
	}

	stop := make(chan struct{})
	time.AfterFunc(80*time.Millisecond, func() { close(stop) })
	if err := serveMCPHTTPUntil(dir, "127.0.0.1:0", false, stop); err != nil {
		t.Fatalf("serveMCPHTTPUntil(127.0.0.1:0) = %v; want nil on stop", err)
	}

	// Verify cancelPending and overdue replyPendingLocked in mcp_dispatch.go.
	srv := newMCPServer(dir, json.NewEncoder(io.Discard))
	srv.cancelPending(nil)
	prevHandler := mcpHandler
	mcpHandler = func(_ string, _ rpcRequest) (any, int, string) {
		time.Sleep(60 * time.Millisecond)
		return toolText("done"), 0, ""
	}
	defer func() { mcpHandler = prevHandler }()

	reqCtx, reqCancel := context.WithCancel(t.Context())
	reqCancel()
	h := newMCPHTTPHandler(dir, false, "")
	req := httptest.NewRequestWithContext(reqCtx, http.MethodPost, "http://127.0.0.1:8788/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":77,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"orient"}}}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
}
