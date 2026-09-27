package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// `deja reasonix-ext` is the code extension `deja install reasonix-auto` puts
// into Reasonix: a sidecar the host starts once per runtime generation and
// talks to over stdin/stdout, one JSON-RPC 2.0 object per line (Extension
// Protocol v2, docs/EXTENSION_PROTOCOL.md in Reasonix 1.x).
//
// Hooks cannot do this job on 1.x. Measured on 1.39.1: of every hook event
// Reasonix fires, only SessionStart's stdout reaches the model; the
// additionalContext of UserPromptSubmit, PreToolUse and PostToolUse is
// dropped. An extension's input.receive answer is different — the replaced
// text is what the host stores as the user turn, with the typed text kept in
// raw_content, so recall lands in the turn tail and stays byte-stable in the
// prefix of every later request. Nothing here touches the system prompt:
// a per-turn edit there breaks the prefix cache (the reason Reasonix's
// maintainers turned down prefix injection in their #3929).
//
// The transport is written out here rather than taken from Reasonix's Go SDK,
// which deja cannot import. It is the subset deja uses: the handshake,
// intercepts, observation events, host/ui/publish and host/content/read for
// payloads the host moved out of the frame.

const (
	rxProtocolID    = "reasonix.extension.v2"
	rxProtocolMajor = 2
	// rxFrameBytes is the protocol's frozen per-frame cap, both directions.
	rxFrameBytes = 8 << 20
	// rxContentChunkBytes and rxContentObjectBytes bound host/content/read.
	rxContentChunkBytes  = 256 << 10
	rxContentObjectBytes = 8 << 20
	rxDomainErrorCode    = -32000
)

// rxFrame is one JSON-RPC 2.0 object. IDs are integers on this protocol and
// are echoed back exactly as they arrived.
type rxFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rxRPCError     `json:"error,omitempty"`
}

type rxRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rxRPCError) Error() string { return e.Message }

type rxErrorData struct {
	Reason    string `json:"reason"`
	Retryable bool   `json:"retryable"`
}

func rxDomainError(reason, message string) *rxRPCError {
	data, _ := json.Marshal(rxErrorData{Reason: reason})
	return &rxRPCError{Code: rxDomainErrorCode, Message: message, Data: data}
}

type rxExternalized struct {
	JSONPointer string `json:"jsonPointer"`
	ContentRef  string `json:"contentRef"`
	TotalBytes  int64  `json:"totalBytes"`
	SHA256      string `json:"sha256"`
}

type rxInterceptParams struct {
	Event         string           `json:"event"`
	Seq           uint64           `json:"seq"`
	Payload       json.RawMessage  `json:"payload"`
	TimeoutMillis int              `json:"timeoutMillis"`
	Externalized  []rxExternalized `json:"externalized,omitempty"`
}

type rxEventParams struct {
	Event        string           `json:"event"`
	Payload      json.RawMessage  `json:"payload"`
	Externalized []rxExternalized `json:"externalized,omitempty"`
}

// rxInterceptResult is the ruling. Only continue and replace are used: deja
// adds context, it never blocks or vetoes anything the agent does.
type rxInterceptResult struct {
	Decision    string          `json:"decision"`
	Replacement json.RawMessage `json:"replacement,omitempty"`
}

func rxContinue() rxInterceptResult { return rxInterceptResult{Decision: "continue"} }

// rxConn is the sidecar's end of the pipe.
type rxConn struct {
	in  *bufio.Reader
	out io.Writer
	wmu sync.Mutex

	nextID  atomic.Int64
	pmu     sync.Mutex
	pending map[int64]chan rxFrame

	handlers sync.WaitGroup
}

func newRxConn(in io.Reader, out io.Writer) *rxConn {
	return &rxConn{
		in:      bufio.NewReaderSize(in, 64<<10),
		out:     out,
		pending: map[int64]chan rxFrame{},
	}
}

// write sends one frame. A frame over the cap is not sent at all: the host
// treats one as connection-fatal, and an answer deja cannot send is better
// lost than the connection with it.
func (c *rxConn) write(f rxFrame) error {
	f.JSONRPC = "2.0"
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(b)+1 > rxFrameBytes {
		return fmt.Errorf("frame of %d bytes is over the %d byte cap", len(b)+1, rxFrameBytes)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.out.Write(append(b, '\n'))
	return err
}

func (c *rxConn) reply(id json.RawMessage, result any) error {
	b, err := json.Marshal(result)
	if err != nil {
		c.fail(id, &rxRPCError{Code: -32603, Message: err.Error()})
		return err
	}
	if werr := c.write(rxFrame{ID: id, Result: b}); werr != nil {
		fmt.Fprintf(os.Stderr, "deja reasonix-ext: %v\n", werr)
		return werr
	}
	return nil
}

func (c *rxConn) fail(id json.RawMessage, e *rxRPCError) {
	_ = c.write(rxFrame{ID: id, Error: e})
}

// call sends a request to the host and waits for its answer. The read loop
// delivers the response, so call must never run on the read loop itself.
func (c *rxConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan rxFrame, 1)
	c.pmu.Lock()
	c.pending[id] = ch
	c.pmu.Unlock()
	defer func() {
		c.pmu.Lock()
		delete(c.pending, id)
		c.pmu.Unlock()
	}()
	if err := c.write(rxFrame{ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw}); err != nil {
		return nil, err
	}
	select {
	case f := <-ch:
		if f.Error != nil {
			return nil, f.Error
		}
		return f.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify sends a request whose answer nobody waits for. host/ui/publish goes
// this way: a status line is not worth holding a turn for.
func (c *rxConn) notify(method string, params any) {
	id := c.nextID.Add(1)
	raw, err := json.Marshal(params)
	if err != nil {
		return
	}
	_ = c.write(rxFrame{ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw})
}

func (c *rxConn) deliver(f rxFrame) {
	id, err := strconv.ParseInt(string(bytes.TrimSpace(f.ID)), 10, 64)
	if err != nil {
		return
	}
	c.pmu.Lock()
	ch := c.pending[id]
	c.pmu.Unlock()
	if ch != nil {
		ch <- f
	}
}

// readFrame reads one line. A line over the cap ends the connection, as the
// host's own reader does.
func (c *rxConn) readFrame() ([]byte, error) {
	var line []byte
	for {
		chunk, err := c.in.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > rxFrameBytes {
			return nil, fmt.Errorf("incoming frame is over the %d byte cap", rxFrameBytes)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && len(bytes.TrimSpace(line)) == 0 {
			return nil, err
		}
		return line, nil
	}
}

// readContent pages one content ref back from the host and checks it against
// the byte count and hash the host announced for it.
func (c *rxConn) readContent(ctx context.Context, d rxExternalized) ([]byte, error) {
	if d.TotalBytes > rxContentObjectBytes {
		return nil, fmt.Errorf("content ref of %d bytes is over the %d byte cap", d.TotalBytes, rxContentObjectBytes)
	}
	var out []byte
	var offset int64
	for {
		raw, err := c.call(ctx, "host/content/read", map[string]any{"contentRef": d.ContentRef, "offset": offset})
		if err != nil {
			return nil, err
		}
		var page struct {
			DataBase64 string `json:"dataBase64"`
			NextOffset *int64 `json:"nextOffset"`
			Encoding   string `json:"encoding"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, err
		}
		if page.Encoding != "" && page.Encoding != "utf8" {
			return nil, fmt.Errorf("content ref in unknown encoding %q", page.Encoding)
		}
		chunk, err := base64.StdEncoding.DecodeString(page.DataBase64)
		if err != nil || len(chunk) > rxContentChunkBytes {
			return nil, errors.New("content ref page is not valid")
		}
		out = append(out, chunk...)
		if int64(len(out)) > d.TotalBytes {
			return nil, errors.New("content ref is longer than announced")
		}
		if page.NextOffset == nil {
			break
		}
		if *page.NextOffset <= offset {
			return nil, errors.New("content ref read made no progress")
		}
		offset = *page.NextOffset
	}
	sum := sha256.Sum256(out)
	if int64(len(out)) != d.TotalBytes || !strings.EqualFold(hex.EncodeToString(sum[:]), d.SHA256) {
		return nil, errors.New("content ref does not match its descriptor")
	}
	return out, nil
}

// resolvePayload is the payload itself, or the bytes of the one content ref
// standing in for it.
func (c *rxConn) resolvePayload(ctx context.Context, inline json.RawMessage, ext []rxExternalized) (json.RawMessage, error) {
	if len(ext) == 0 {
		return inline, nil
	}
	if len(ext) != 1 || ext[0].JSONPointer != "/payload" {
		return nil, errors.New("externalized envelope does not name the payload")
	}
	return c.readContent(ctx, ext[0])
}

// runReasonixExt serves the sidecar until the host closes stdin or asks it to
// shut down.
func runReasonixExt(dir string, in io.Reader, out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	x := newRxExt(dir, exe, newRxConn(in, out))
	return x.serve()
}

func (x *rxExt) serve() error {
	c := x.conn
	defer c.handlers.Wait()
	for {
		line, err := c.readFrame()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var f rxFrame
		if err := json.Unmarshal(line, &f); err != nil {
			// Nothing to answer: without a parsed id there is no request to
			// fail. The host's frames are its own contract to keep.
			fmt.Fprintf(os.Stderr, "deja reasonix-ext: unreadable frame: %v\n", err)
			continue
		}
		hasID := len(bytes.TrimSpace(f.ID)) > 0 && string(bytes.TrimSpace(f.ID)) != "null"
		switch {
		case f.Method == "" && hasID:
			c.deliver(f)
		case f.Method == "extension/initialize" && hasID:
			if stop := x.initialize(f); stop {
				return nil
			}
		case f.Method == "extension/shutdown" && hasID:
			_ = c.reply(f.ID, map[string]bool{"accepted": true})
			return nil
		case f.Method == "extension/initialized":
		case f.Method == "extension/event" && !hasID:
			// On the read loop, in arrival order: a session event only moves
			// state, and handling it here keeps it ahead of the next intercept.
			var p rxEventParams
			if json.Unmarshal(f.Params, &p) == nil {
				x.observe(p)
			}
		case f.Method == "extension/intercept" && hasID:
			var p rxInterceptParams
			if err := json.Unmarshal(f.Params, &p); err != nil || p.Event == "" {
				c.fail(f.ID, &rxRPCError{Code: -32602, Message: "extension/intercept params are not readable"})
				continue
			}
			c.handlers.Add(1)
			go func(id json.RawMessage) {
				defer c.handlers.Done()
				// A replacement over the frame cap is not sent; the
				// host still gets an answer, and it is the turn untouched.
				if c.reply(id, x.intercept(p)) != nil {
					_ = c.reply(id, rxContinue())
				}
			}(f.ID)
		case hasID:
			c.fail(f.ID, &rxRPCError{Code: -32601, Message: "deja does not serve " + f.Method})
		}
	}
}

// rxInitParams is the part of extension/initialize deja reads. Decoding is
// lenient: a later host adding fields is not a reason to refuse it.
type rxInitParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ProtocolID      string `json:"protocolId"`
	Manifest        struct {
		Intercepts   []string `json:"intercepts"`
		Capabilities []string `json:"capabilities"`
	} `json:"manifest"`
	Session struct {
		SessionID     string `json:"sessionId"`
		WorkspaceRoot string `json:"workspaceRoot"`
		Generation    uint64 `json:"generation"`
	} `json:"session"`
	Capabilities struct {
		UIHost string `json:"uiHost"`
	} `json:"capabilities"`
}

// rxSubscriptions is every point deja acts on. The handshake answers with the
// ones the installed manifest also lists, since anything beyond it fails the
// handshake.
var rxSubscriptions = []string{
	"input.receive", "tool.after", "compaction.prepare",
	"session.start", "session.load", "session.rotate",
}

// initialize answers the handshake and reports whether the connection is
// over: a host speaking another protocol gets the frozen refusal and nothing
// more.
func (x *rxExt) initialize(f rxFrame) bool {
	var p rxInitParams
	if err := json.Unmarshal(f.Params, &p); err != nil {
		x.conn.fail(f.ID, &rxRPCError{Code: -32602, Message: "extension/initialize params are not readable"})
		return true
	}
	major, err := strconv.Atoi(strings.TrimSpace(p.ProtocolVersion))
	if p.ProtocolID != rxProtocolID || err != nil || major != rxProtocolMajor {
		x.conn.fail(f.ID, rxDomainError("unsupported_version",
			fmt.Sprintf("deja speaks %s major %d, the host offered %q major %q", rxProtocolID, rxProtocolMajor, p.ProtocolID, p.ProtocolVersion)))
		return true
	}
	allowed := map[string]bool{}
	for _, point := range p.Manifest.Intercepts {
		allowed[point] = true
	}
	subs := []string{}
	for _, point := range rxSubscriptions {
		if allowed[point] {
			subs = append(subs, point)
		}
	}
	ui := false
	for _, capability := range p.Manifest.Capabilities {
		ui = ui || capability == "ui"
	}
	x.begin(p.Session.SessionID, p.Session.WorkspaceRoot, p.Session.Generation, ui)
	v := version
	if strings.TrimSpace(v) == "" {
		v = "dev"
	}
	_ = x.conn.reply(f.ID, map[string]any{
		"protocolVersion":    strconv.Itoa(rxProtocolMajor),
		"name":               reasonixPluginName,
		"version":            v,
		"subscriptions":      subs,
		"stateSchemaVersion": 0,
	})
	return false
}

// interceptBudget is how long deja may spend on one intercept: under the
// host's own timeout by a margin, so a slow answer passes through as
// "continue" instead of arriving late as an intercept_timeout warning.
func interceptBudget(hostMillis int, ceiling time.Duration) time.Duration {
	budget := ceiling
	if hostMillis > 0 {
		host := time.Duration(hostMillis) * time.Millisecond
		if margin := host - rxHostMargin; margin < budget {
			budget = margin
		}
	}
	if budget < 50*time.Millisecond {
		budget = 50 * time.Millisecond
	}
	return budget
}

// rxHostMargin is what is left of the host's timeout for the answer to travel.
const rxHostMargin = 750 * time.Millisecond
