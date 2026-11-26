package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
)

func serveMCPUnixSocketProcess(dir, sockPath string, contextOnly bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveMCPUnixSocketUntil(dir, sockPath, contextOnly, ctx.Done())
}

func expandMCPSocketPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", errors.New("mcp: --unix-socket requires a socket path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", errors.New("mcp: cannot expand ~ in --unix-socket path without home directory")
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Clean(p), nil
}

func prepareMCPUnixSocket(sockPath string) (net.Listener, os.FileInfo, error) {
	parent := filepath.Dir(sockPath)
	if pInfo, err := os.Lstat(parent); err == nil {
		if pInfo.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("mcp: refusing unix socket parent symlink %q", parent)
		}
		if !pInfo.IsDir() {
			return nil, nil, fmt.Errorf("mcp: unix socket parent %q is not a directory", parent)
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, nil, fmt.Errorf("mcp: create unix socket directory %q: %w", parent, err)
		}
		_ = os.Chmod(parent, 0o700)
	} else {
		return nil, nil, err
	}

	if info, err := os.Lstat(sockPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("mcp: refusing symlink at unix socket path %q", sockPath)
		}
		if !isSocketMode(info.Mode()) {
			return nil, nil, fmt.Errorf("mcp: refusing non-socket file at %q", sockPath)
		}
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		conn, dialErr := (&net.Dialer{}).DialContext(dialCtx, "unix", sockPath)
		dialCancel()
		if dialErr == nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("mcp: unix socket %q is already in use", sockPath)
		}
		if !isStaleSocketDialError(dialErr) {
			return nil, nil, fmt.Errorf("mcp: inspect unix socket %q: %w", sockPath, dialErr)
		}
		if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("mcp: remove stale unix socket %q: %w", sockPath, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, nil, err
	}

	var ln net.Listener
	if err := withPrivateUmask(func() error {
		var lerr error
		ln, lerr = (&net.ListenConfig{}).Listen(context.Background(), "unix", sockPath)
		return lerr
	}); err != nil {
		return nil, nil, err
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		return nil, nil, err
	}
	info, err := os.Lstat(sockPath)
	if err != nil {
		_ = ln.Close()
		_ = os.Remove(sockPath)
		return nil, nil, err
	}
	return ln, info, nil
}

func cleanupMCPUnixSocket(sockPath string, boundInfo os.FileInfo) {
	if sameSocketFile(sockPath, boundInfo) {
		_ = os.Remove(sockPath)
	}
}

func serveMCPUnixSocketUntil(dir, rawPath string, contextOnly bool, stop <-chan struct{}) error {
	sockPath, err := expandMCPSocketPath(rawPath)
	if err != nil {
		return err
	}
	ln, boundInfo, err := prepareMCPUnixSocket(sockPath)
	if err != nil {
		return err
	}
	defer func() {
		_ = ln.Close()
		cleanupMCPUnixSocket(sockPath, boundInfo)
	}()

	_ = refreshWiringAfterUpgrade()
	defer index.KeepFresh(dir, mcpFreshEvery, func() {
		if !warmupRunning(dir) {
			requestWarmup(dir)
		}
	}, mcpProgress())()

	token := strings.TrimSpace(os.Getenv("DEJA_MCP_TOKEN"))
	httpLn := newChannelListener(ln.Addr())
	httpSrv := &http.Server{
		Handler:           newMCPHTTPHandlerWithOpts(dir, contextOnly, token, true),
		ReadHeaderTimeout: 10 * time.Second,
	}

	var (
		connMu    sync.Mutex
		liveConns = make(map[net.Conn]struct{})
		wg        sync.WaitGroup
		closed    bool
	)
	trackConn := func(c net.Conn) bool {
		connMu.Lock()
		defer connMu.Unlock()
		if closed {
			return false
		}
		liveConns[c] = struct{}{}
		return true
	}
	untrackConn := func(c net.Conn) {
		connMu.Lock()
		delete(liveConns, c)
		connMu.Unlock()
	}
	closeAll := func() {
		connMu.Lock()
		closed = true
		conns := make([]net.Conn, 0, len(liveConns))
		for c := range liveConns {
			conns = append(conns, c)
		}
		connMu.Unlock()
		_ = ln.Close()
		_ = httpLn.Close()
		for _, c := range conns {
			_ = c.Close()
		}
	}

	httpErrCh := make(chan error, 1)
	go func() {
		httpErrCh <- httpSrv.Serve(httpLn)
	}()

	acceptErrCh := make(chan error, 1)
	go func() {
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				acceptErrCh <- aerr
				return
			}
			if !trackConn(conn) {
				_ = conn.Close()
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				forwarded := serveMCPUnixConn(dir, contextOnly, token, c, httpLn, stop)
				untrackConn(c)
				if !forwarded {
					_ = c.Close()
				}
			}(conn)
		}
	}()

	select {
	case aerr := <-acceptErrCh:
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
		closeAll()
		wg.Wait()
		if errors.Is(aerr, net.ErrClosed) {
			return nil
		}
		return aerr
	case herr := <-httpErrCh:
		closeAll()
		wg.Wait()
		if errors.Is(herr, http.ErrServerClosed) || errors.Is(herr, net.ErrClosed) {
			return nil
		}
		return herr
	case <-stop:
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
		closeAll()
		wg.Wait()
		return nil
	}
}

// serveMCPUnixConn inspects the first byte/line on conn to dispatch Streamable
// HTTP, LSP Content-Length framing, or raw newline JSON-RPC over the Unix
// socket. Returns true when HTTP takes ownership of closing conn.
func serveMCPUnixConn(dir string, contextOnly bool, token string, conn net.Conn, httpLn *channelListener, stop <-chan struct{}) bool {
	br := bufio.NewReaderSize(conn, 64*1024)
	first, err := br.Peek(1)
	if err != nil {
		return false
	}
	b := first[0]
	sess := newMCPClientSession(newMCPConn())
	if b == '{' || b == '[' || b == ' ' || b == '\t' || b == '\r' || b == '\n' {
		_ = serveMCPBuffered(dir, br, conn, contextOnly, sess, token)
		return false
	}
	firstLine, err := br.ReadSlice('\n')
	if err != nil && len(firstLine) == 0 {
		return false
	}
	if errors.Is(err, bufio.ErrBufferFull) {
		writeRPCError(json.NewEncoder(conn), nil, -32700, "parse error")
		return false
	}
	if looksLikeHTTPRequestLine(firstLine) {
		pconn := &prefixedConn{
			Conn:   conn,
			reader: io.MultiReader(bytes.NewReader(append([]byte(nil), firstLine...)), br),
		}
		if httpLn.enqueue(pconn, stop) {
			return true
		}
		return false
	}
	if looksLikeContentLengthHeader(firstLine) {
		serveMCPContentLength(dir, contextOnly, token, sess, append([]byte(nil), firstLine...), br, conn)
		return false
	}
	combined := bufio.NewReaderSize(io.MultiReader(bytes.NewReader(append([]byte(nil), firstLine...)), br), 64*1024)
	_ = serveMCPBuffered(dir, combined, conn, contextOnly, sess, token)
	return false
}

func looksLikeHTTPRequestLine(line []byte) bool {
	s := strings.TrimSpace(string(line))
	if !strings.Contains(s, " HTTP/") {
		return false
	}
	for _, method := range []string{"POST ", "GET ", "DELETE ", "HEAD ", "OPTIONS ", "PUT "} {
		if strings.HasPrefix(s, method) {
			return true
		}
	}
	return false
}

func looksLikeContentLengthHeader(line []byte) bool {
	s := strings.TrimSpace(string(line))
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "content-length:") || strings.HasPrefix(lower, "content-type:")
}

type contentLengthWriter struct {
	dst io.Writer
}

func (w *contentLengthWriter) Write(p []byte) (int, error) {
	body := bytes.TrimSuffix(p, []byte{'\n'})
	if len(body) == 0 {
		return len(p), nil
	}
	var hdr [64]byte
	h := strconv.AppendInt(append(hdr[:0], "Content-Length: "...), int64(len(body)), 10)
	h = append(h, "\r\n\r\n"...)
	if _, err := w.dst.Write(h); err != nil {
		return 0, err
	}
	if _, err := w.dst.Write(body); err != nil {
		return 0, err
	}
	return len(p), nil
}

func serveMCPContentLength(dir string, contextOnly bool, token string, sess *mcpClientSession, firstHeader []byte, br *bufio.Reader, w io.Writer) {
	cw := &contentLengthWriter{dst: w}
	enc := json.NewEncoder(cw)
	srv := newMCPServer(dir, enc)
	srv.contextOnly = contextOnly

	pendingHeader := firstHeader
	for {
		if werr := srv.writeErr(); werr != nil {
			srv.wait()
			return
		}
		length := -1
		if pendingHeader == nil {
			line, err := br.ReadSlice('\n')
			if err != nil {
				if errors.Is(err, bufio.ErrBufferFull) {
					srv.refuse(nil, -32700, "parse error")
				}
				srv.wait()
				return
			}
			pendingHeader = line
		}
		for {
			h := strings.TrimRight(string(pendingHeader), "\r\n")
			pendingHeader = nil
			if h == "" {
				break
			}
			if key, val, ok := strings.Cut(h, ":"); ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
				n, convErr := strconv.Atoi(strings.TrimSpace(val))
				if convErr != nil || n < 0 || n > mcpMaxFrame {
					srv.refuse(nil, -32700, "parse error")
					srv.wait()
					return
				}
				length = n
			}
			next, err := br.ReadSlice('\n')
			if err != nil {
				if errors.Is(err, bufio.ErrBufferFull) {
					srv.refuse(nil, -32700, "parse error")
				}
				srv.wait()
				return
			}
			pendingHeader = next
		}
		if length <= 0 {
			srv.refuse(nil, -32700, "parse error")
			srv.wait()
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(br, payload); err != nil {
			srv.wait()
			return
		}
		serveMCPFrame(srv, sess, token, strings.TrimSpace(string(payload)))
	}
}

type prefixedConn struct {
	net.Conn
	reader io.Reader
}

func (c *prefixedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

type channelListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChannelListener(addr net.Addr) *channelListener {
	return &channelListener{
		addr: addr,
		ch:   make(chan net.Conn, 16),
		done: make(chan struct{}),
	}
}

func (l *channelListener) enqueue(c net.Conn, stop <-chan struct{}) bool {
	select {
	case l.ch <- c:
		return true
	case <-l.done:
		return false
	case <-stop:
		return false
	}
}

func (l *channelListener) Accept() (net.Conn, error) {
	select {
	case c, ok := <-l.ch:
		if !ok {
			return nil, net.ErrClosed
		}
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *channelListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		for {
			select {
			case c := <-l.ch:
				if c != nil {
					_ = c.Close()
				}
			default:
				return
			}
		}
	})
	return nil
}

func (l *channelListener) Addr() net.Addr {
	return l.addr
}
