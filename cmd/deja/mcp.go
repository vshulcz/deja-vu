package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const mcpProtocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// served, when set, is told how many sessions a recall answered from, so
	// the server knows whether an answer it kept is worth handing on.
	served *int
}

// isNotification reports whether a request omitted id (a JSON-RPC notification,
// which must get no reply). A literal null id counts as absent too.
func isNotification(id json.RawMessage) bool {
	return len(id) == 0 || string(id) == "null"
}

// rpcIDProblem says what is wrong with a request's id, or nothing. MCP ids
// are strings or numbers: an object or a bool echoed back is not one, and a
// null id on a request (not a notification) went unanswered, which a client
// waits on forever.
func rpcIDProblem(req rpcRequest) string {
	id := bytes.TrimSpace(req.ID)
	if len(id) == 0 {
		return ""
	}
	if string(id) == "null" {
		if strings.HasPrefix(req.Method, "notifications/") {
			return ""
		}
		return "id must be a string or a number, not null"
	}
	switch c := id[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return ""
	}
	return "id must be a string or a number"
}

// parseBatch reports whether a frame is an array of requests, and returns them.
// A frame that only starts like one — a truncated `[` — is not a batch and
// keeps its parse error.
func parseBatch(frame string) ([]json.RawMessage, bool) {
	if !strings.HasPrefix(frame, "[") {
		return nil, false
	}
	var elems []json.RawMessage
	if err := json.Unmarshal([]byte(frame), &elems); err != nil {
		return nil, false
	}
	return elems, true
}

// batchReply says whether a batch is owed an answer and which id to put on it.
// The id is the first request in the array that carries one, so the refusal can
// be matched to something the client sent. A batch of nothing but notifications
// is owed no answer — the spec forbids replying to one, inside a batch or out
// of it — while an empty or malformed array is an invalid request like any
// other and gets the refusal with a null id.
func batchReply(elems []json.RawMessage) (json.RawMessage, bool) {
	notificationsOnly := len(elems) > 0
	for _, elem := range elems {
		var req rpcRequest
		if !bytes.HasPrefix(bytes.TrimSpace(elem), []byte("{")) || json.Unmarshal(elem, &req) != nil {
			// Not a request object at all, so it asked for nothing and is not
			// a notification either: the array is malformed and says so.
			notificationsOnly = false
			continue
		}
		if !isNotification(req.ID) {
			return req.ID, true
		}
	}
	return nil, !notificationsOnly
}

const mcpMaxFrame = 10 * 1024 * 1024

func serveMCP(dir string, r io.Reader, w io.Writer) error {
	return serveMCPAs(dir, r, w, false)
}

// serveMCPAs serves either deja's own tool or, with contextOnly, the single
// context_search tool of Qwen Code's External Context MCP Profile v1.
func serveMCPAs(dir string, r io.Reader, w io.Writer, contextOnly bool) error {
	mcpConn.Store(newMCPConn())
	br := bufio.NewReaderSize(r, 64*1024)
	enc := json.NewEncoder(w)
	srv := newMCPServer(dir, enc)
	srv.contextOnly = contextOnly
	for {
		if werr := srv.writeErr(); werr != nil {
			srv.wait()
			return werr
		}
		line, overlong, err := readMCPLine(br, mcpMaxFrame)
		if overlong {
			// One oversized frame is reported as a parse error and skipped; the
			// server keeps serving instead of tearing down the whole session.
			srv.refuse(nil, -32700, "parse error")
		} else if trimmed := strings.TrimSpace(string(line)); trimmed != "" {
			var req rpcRequest
			if batch, isBatch := parseBatch(trimmed); isBatch {
				// A batch is valid JSON, and answering -32700 told a client its
				// bytes were corrupt when they were not — with a null id, so it
				// could not tell which of its requests died either. deja serves
				// one request per frame; the refusal says so (#1795). A batch
				// carrying nothing but notifications gets no reply at all, as a
				// notification does on its own line: the spec forbids answering
				// one, inside a batch or out of it.
				if id, answer := batchReply(batch); answer {
					srv.refuse(id, -32600, "batch requests are not supported — send one request per line")
				}
			} else if uerr := json.Unmarshal([]byte(trimmed), &req); uerr != nil {
				srv.refuse(nil, -32700, "parse error")
			} else if req.JSONRPC != "" && req.JSONRPC != "2.0" {
				// The member that says which protocol the frame speaks. An
				// absent one is still served — clients in the wild omit it and
				// the request is unambiguous — but "1.0" asks for a protocol
				// this server does not speak and used to be answered anyway.
				srv.refuse(req.ID, -32600, "unsupported jsonrpc version "+req.JSONRPC+" — this server speaks 2.0")
			} else if msg := rpcIDProblem(req); msg != "" {
				srv.refuse(nil, -32600, msg)
			} else {
				switch {
				case !isNotification(req.ID) && req.Method == "":
					srv.refuse(req.ID, -32600, "request has no method")
				case !isNotification(req.ID):
					srv.serve(req)
				case req.Method == "notifications/cancelled":
					srv.cancel(req.Params)
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && os.Getenv("DEJA_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "deja mcp read error: %v\n", err)
			}
			// Input is over, but requests already read are still owed their
			// answers.
			srv.wait()
			return srv.writeErr()
		}
	}
}

// readMCPLine reads one newline-delimited frame. A frame longer than most is
// drained and reported via overlong=true rather than buffered whole, so a
// hostile or corrupt client can't exhaust memory or kill the loop.
func readMCPLine(br *bufio.Reader, most int) (line []byte, overlong bool, err error) {
	for {
		chunk, e := br.ReadSlice('\n')
		if errors.Is(e, bufio.ErrBufferFull) {
			if len(line)+len(chunk) > most {
				// Drain the rest of this overlong frame up to the next newline.
				for errors.Is(e, bufio.ErrBufferFull) {
					_, e = br.ReadSlice('\n')
				}
				return nil, true, e
			}
			line = append(line, chunk...)
			continue
		}
		line = append(line, chunk...)
		return line, false, e
	}
}

func handleMCP(dir string, req rpcRequest) (any, int, string) {
	switch req.Method {
	case "initialize":
		var p struct {
			ClientInfo struct {
				Name string `json:"name"`
			} `json:"clientInfo"`
		}
		// A malformed or missing clientInfo only loses the client-specific
		// sentence; initialize itself must still succeed.
		_ = json.Unmarshal(req.Params, &p)
		setMCPClient(p.ClientInfo.Name)
		return map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}, "prompts": map[string]any{}},
			"serverInfo":      map[string]any{"name": "deja", "version": version},
			"instructions":    mcpInstructions(dir, p.ClientInfo.Name),
		}, 0, ""
	case "tools/list":
		return map[string]any{"tools": []map[string]any{dejaTool()}}, 0, ""
	case "ping":
		// Part of the spec at the version we claim, and both sides must
		// answer it with an empty result. A host that pings for keepalive
		// is entitled to read an error as a stale connection and drop the
		// server, so this cannot fall through to -32601.
		return map[string]any{}, 0, ""
	case "resources/templates/list":
		// We declare a resources capability, so clients ask for its
		// templates. deja exposes concrete session resources and no URI
		// templates, and the empty list is the shape that says so —
		// an error here reads as a broken capability.
		return map[string]any{"resourceTemplates": []map[string]any{}}, 0, ""
	case "prompts/list":
		return map[string]any{"prompts": []map[string]any{dejaPrompt()}}, 0, ""
	case "prompts/get":
		return mcpPromptGet(dir, req.Params)
	case "resources/list":
		return mcpResourcesList(dir)
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, -32602, "invalid params"
		}
		return mcpResourceRead(dir, p.URI)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, -32602, "invalid params"
		}
		text, err := callMCPToolServed(dir, p.Name, p.Arguments, req.served)
		// A call the agent got wrong is invalid params. A tool that ran and
		// failed — a notes file it cannot write — is a result marked isError,
		// which the client hands to the model rather than to its own logs.
		var bad *mcpArgError
		if errors.As(err, &bad) {
			return nil, -32602, err.Error()
		}
		if err != nil {
			out := toolText(err.Error())
			out["isError"] = true
			return out, 0, ""
		}
		if stageMCPToolResult(dir, mcpClientName(), p.Name, p.Arguments, text) {
			return map[string]any{"content": []map[string]string{}}, 0, ""
		}
		return toolText(text), 0, ""
	default:
		return nil, -32601, "method not found"
	}
}

// mcpArgError is a tool call the caller can correct: an argument missing, of
// the wrong type, or naming something that is not there.
type mcpArgError struct{ msg string }

func (e *mcpArgError) Error() string { return e.msg }

func argErrorf(format string, args ...any) error {
	return &mcpArgError{fmt.Sprintf(format, args...)}
}

func asArgError(err error) error {
	if err == nil {
		return nil
	}
	return &mcpArgError{err.Error()}
}

func writeRPCError(enc *json.Encoder, id any, code int, msg string) {
	_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}}) //nolint:errchkjson // a failed reply means the client is gone; there is no one left to tell
}

func toolText(text string) map[string]any {
	return map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}
}

// decodeToolArgs reads a tool call's arguments. The field is optional in the
// protocol, so an absent one is an empty object rather than an error, and a
// decode failure is reported in terms of the argument the caller sent — an
// agent can fix `"query" must be a string`, it can do nothing with
// `json: cannot unmarshal number into Go struct field .query` (#1723).
func decodeToolArgs(tool string, raw json.RawMessage, into any) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = json.RawMessage("{}")
	}
	err := json.Unmarshal(raw, into)
	if err == nil {
		return nil
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return argErrorf("%s: %q must be %s", tool, strings.TrimPrefix(typeErr.Field, "."), jsonTypeName(typeErr.Type.Kind()))
	}
	// A field's own decoder failing is not a malformed argument object, and
	// saying "arguments must be an object" about `{"limit":"five"}` sent the
	// agent to rewrite the one part of the call that was right. mcpNumber's
	// message names what it wanted; carry it rather than replacing it.
	if isJSONObject(raw) {
		return argErrorf("%s: %v", tool, err)
	}
	return argErrorf("%s: arguments must be an object", tool)
}

// isJSONObject reports whether the arguments really are a JSON object, so the
// two failures above can be told apart.
func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

// jsonTypeName names a Go kind the way the tool schema does, so the message
// matches what the caller was asked for rather than how it is stored.
func jsonTypeName(k reflect.Kind) string {
	switch k { //nolint:exhaustive // the schema only names these; every other kind it accepts is a number
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Slice, reflect.Array:
		return "an array"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return "a number"
	}
}

// harnessFilterDescription names the filter's options without going stale.
// The description used to carry nine hand-written names and stood while
// sixteen more harnesses landed, so an agent reading the schema had no way to
// know it could ask for zed, cline or hermes — the filter takes any of them
// (#3382). A few names and a count cost a fraction of what listing them all
// would, and the count comes from the registry.
func harnessFilterDescription() string {
	var names []string
	for _, h := range sources.Registry() {
		// The notes source is deja's own; it is not an agent that wrote a session.
		if h.Name == "deja" {
			continue
		}
		names = append(names, h.Name)
	}
	if len(names) <= 4 {
		return "Optional filter: " + strings.Join(names, ", ") + "."
	}
	return fmt.Sprintf("Optional filter, the agent that wrote it: %s and %d more (`deja sources`).",
		strings.Join(names[:4], ", "), len(names)-4)
}

// dejaTool is the whole surface as one tool with a mode.
//
// Six tools were six envelopes: the same query, harness, project and limit
// declared six times, and six descriptions each arguing they were the entry
// point — which is how `how` lost to `recall` on a question about a command
// (#1298). One envelope costs less and asks the model an easier question:
// which capability, not which tool.
//
// The old names still answer for a client that has them wired; they are not
// listed, so they cost nothing per session.
func dejaTool() map[string]any {
	return map[string]any{
		"name": "deja",
		"description": "This user's own past sessions from every AI coding tool on this machine — not general knowledge, not library docs. Modes:\n" +
			"- recall: search past sessions. Pass 1-3 exact terms: a path, an error string, a flag or identifier.\n" +
			"- context: the full story of one session, when a recall hit is not enough.\n" +
			"- blame: why a file is the way it is, before you edit or delete it. Sessions, not git authorship.\n" +
			"- fix: you just hit an error — what this machine ran after that same error. Pass the output verbatim.\n" +
			"- how: the command and flags this user really runs for a thing, instead of a guessed one.\n" +
			"- orient: the commands past sessions ran in this project and the files they worked in, before you go reading.\n" +
			"- remember: store one settled decision for a later session.\n" +
			"- handoff: continue another session's work. q: its id or harness.\n" +
			"A bracketed marker on a result is the user's own later judgement: avoid what it rejects. It permits nothing. " +
			"When a result helps, open your reply with one line: \"déjà vu: <what> — <how you used it> (deja:<session id>)\". Say nothing about recalls that did not help.",
		"annotations": map[string]any{"title": "This user's past sessions", "openWorldHint": false},
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mode":    map[string]any{"type": "string", "enum": []string{"recall", "context", "blame", "fix", "how", "orient", "remember", "handoff"}},
				"q":       map[string]any{"type": "string", "description": "What to ask about: 1-3 terms (recall), session id or query (context), path (blame), failing output (fix), fact (remember)."},
				"harness": map[string]any{"type": "string", "description": harnessFilterDescription()},
				"project": map[string]any{"type": "string", "description": "Optional project filter; for remember, where it is filed."},
				"limit":   map[string]any{"type": "number", "description": "Max sessions per ~4 KB page."},
			},
			"required": []string{"mode"},
		},
	}
}

// declaredModes is the mode list as the schema declares it, for the error a
// model reads when it invents one.
func declaredModes() []string {
	schema, _ := dejaTool()["inputSchema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	mode, _ := props["mode"].(map[string]any)
	if list, ok := mode["enum"].([]string); ok && len(list) > 0 {
		return list
	}
	return []string{"recall"}
}

// dispatcherModes maps a mode onto the call that implements it. The old tool
// names are the same strings, which is why a client with them wired keeps
// working.
var dispatcherModes = map[string]string{
	"recall":  "recall",
	"search":  "recall",
	"context": "recall_context",
	"digest":  "recall_context",
	// Because deja tells agents to call it by this name. Every surface that
	// names the deep read — the session-start lead, the wide-recall lead, the
	// antigravity lead, the compaction lead, the trimmed-digest note, the
	// blame overflow note — says "call recall_context", and on the one-tool
	// clients that is the shape they get, where the dispatcher answered
	// `mode "recall_context" is not one of recall, context, blame, ...`. An
	// agent following deja's own instruction spent a call on an error.
	"recall_context": "recall_context",
	"blame":          "blame",
	"fix":            "fix",
	"how":            "how",
	"orient":         "orient",
	"remember":       "remember",
	"handoff":        "handoff",
}

// qField is the argument each mode reads its subject from. One declared `q`
// stands for all of them: five per-mode strings in the schema cost more than
// the description does, and the model still has to pick the right one after
// picking the mode. The old names keep working — they are accepted here and
// simply not listed, the same way the pre-#1298 tool names still answer.
// Keyed by the call a mode resolves to, not by the mode: keyed by mode, every
// alias needed its own row and two of them never got one. `search` and
// `digest` were accepted by the dispatcher and then answered "query required",
// because the field to copy q into was looked up under a name only the
// canonical modes had.
var qField = map[string]string{
	"recall":         "query",
	"recall_context": "query",
	"blame":          "path",
	"fix":            "error",
	"how":            "what",
	"orient":         "",
	"remember":       "text",
	"handoff":        "session",
}

// qAliases lists the argument names models write when they do not write "q" or
// the mode's older field name. Leads that say "with its session id" or "for
// blame a path" draw "session_id", "id", or "file", and a strict error there
// wastes a call when the intent is unambiguous.
var qAliases = map[string][]string{
	"query":   {"q", "session_id", "session", "id"},
	"path":    {"q", "file_path", "filepath", "file", "query"},
	"error":   {"q", "output", "stderr", "query"},
	"what":    {"q", "command", "cmd", "query"},
	"text":    {"q", "fact", "note", "content", "query"},
	"session": {"q", "session_id", "id", "query"},
}

func nonEmptyJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && string(trimmed) != `""` && string(trimmed) != "null"
}

// spreadQ copies q (or a common alias for the mode's subject) into the field
// the mode reads, unless the caller already named that field itself.
func spreadQ(mode string, raw json.RawMessage) json.RawMessage {
	field, ok := qField[dispatcherModes[mode]]
	if !ok || field == "" {
		return raw
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return raw
	}
	if cur, taken := args[field]; taken && nonEmptyJSONValue(cur) {
		return raw
	}
	var val json.RawMessage
	for _, key := range qAliases[field] {
		if v, exists := args[key]; exists && nonEmptyJSONValue(v) {
			val = v
			break
		}
	}
	if len(val) == 0 {
		return raw
	}
	args[field] = val
	out, err := json.Marshal(args)
	if err != nil {
		return raw
	}
	return out
}

func callMCPTool(dir, name string, raw json.RawMessage) (string, error) {
	return callMCPToolServed(dir, name, raw, nil)
}

// callMCPToolServed is callMCPTool that also sets *served, when served is not
// nil, to the number of sessions a recall answered from.
func callMCPToolServed(dir, name string, raw json.RawMessage, served *int) (string, error) {
	out, err := callMCPToolRaw(dir, name, raw, served)
	// An index the switch will not rebuild is something only the user can
	// act on, so the agent gets it as an answer to pass on (#5385).
	if index.IsManualStop(err) {
		return "deja: " + err.Error() + " — tell the user to run it.", nil
	}
	return out, err
}

func callMCPRecall(dir string, raw json.RawMessage, served *int) (string, error) {
	var a struct {
		Query   string    `json:"query"`
		Harness string    `json:"harness"`
		Project string    `json:"project"`
		Limit   mcpNumber `json:"limit"`
		Offset  mcpNumber `json:"offset"`
	}
	if err := decodeToolArgs("recall", raw, &a); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Query) == "" {
		return "", argErrorf("query required")
	}
	if err := checkHarness(&a.Harness); err != nil {
		return "", asArgError(err)
	}
	if line := buildingNowForAgent(dir); line != "" {
		return frameRecall(line), nil
	}
	env, deliverEnv := environmentOnce(dir)
	dated := ""
	if line := manualIndexLine(dir); line != "" {
		dated = line + "\n\n"
	}
	brief := liveRecallBrief(dir, howCwd(), a.Query)
	budget := recallMCPBudget - recallFrameOverhead - len(env) - len(dated)
	if brief != "" {
		budget -= len(brief) + 2
	}
	text, sessions, rawBytes, ids, projects, err := recallTextResultIn(dir, a.Query, a.Harness, a.Project, int(a.Limit), int(a.Offset), budget)
	if err == nil {
		if brief != "" {
			text = brief + "\n\n" + text
		}
		if strings.TrimSpace(text) == "" {
			env = strings.TrimLeft(env, "\n")
		}
		text = dated + frameRecall(text+env)
		deliverEnv()
		if served != nil {
			*served = sessions
		}
		usage.RecordServedFromInto(dir, usage.KindRecall, text, mcpConnID(), sessions, rawBytes, ids, projects, policy.Load().Describe(policy.ActivationMCP))
	}
	return text, err
}

func callMCPToolRaw(dir, name string, raw json.RawMessage, served *int) (string, error) {
	if name == "deja" {
		var a struct {
			Mode string `json:"mode"`
		}
		if err := decodeToolArgs(name, raw, &a); err != nil {
			return "", err
		}
		mode := strings.ToLower(strings.TrimSpace(a.Mode))
		target, ok := dispatcherModes[mode]
		if !ok {
			// Named, not guessed at: a model that invents a mode gets the list
			// rather than an empty answer it will read as "no history".
			// Named from what the tool declares rather than spelled out here:
			// the seventh mode landed and this sentence still listed six.
			return "", argErrorf("mode %q is not one of %s", a.Mode, strings.Join(declaredModes(), ", "))
		}
		return callMCPToolServed(dir, target, spreadQ(mode, raw), served)
	}
	raw = spreadQ(name, raw)
	switch name {
	case "recall":
		return callMCPRecall(dir, raw, served)
	case "recall_context":
		var a struct {
			Query   string `json:"query"`
			Harness string `json:"harness"`
		}
		if err := decodeToolArgs(name, raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Query) == "" {
			return "", argErrorf("query required")
		}
		if err := checkHarness(&a.Harness); err != nil {
			return "", asArgError(err)
		}
		if line := buildingNowForAgent(dir); line != "" {
			return frameRecall(line), nil
		}
		text, sessions, raw, ids, projects, note, err := recallContextResultFrom(dir, a.Query, a.Harness)
		if err == nil {
			// Above the frame, the way the resource reader puts its own note:
			// inside it, deja's statement of fact would read as recalled text
			// the agent has just been told not to trust. Its room comes out of
			// the budget before the trim, not after — added afterwards it put
			// the reply over the size this tool documents, which is what
			// #1797 fixed for the frame itself.
			lead := ""
			if note != "" {
				lead = "deja: " + note + "\n\n"
			}
			if line := manualIndexLine(dir); line != "" {
				lead = line + "\n\n" + lead
			}
			text = frameRecall(fitContextDigest(text, a.Query, firstID(ids), contextMCPBudget-recallFrameOverhead-len(lead)))
			text = lead + text
			if served != nil {
				*served = sessions
			}
			usage.RecordServedFromInto(dir, usage.KindContext, text, mcpConnID(), sessions, raw, ids, projects, policy.Load().Describe(policy.ActivationMCP))
		}
		return text, err
	case "blame":
		var a struct {
			Path    string    `json:"path"`
			Harness string    `json:"harness"`
			Project string    `json:"project"`
			Since   string    `json:"since"`
			Limit   mcpNumber `json:"limit"`
			All     bool      `json:"all"`
		}
		if err := decodeToolArgs(name, raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Path) == "" {
			return "", argErrorf("path required")
		}
		if err := checkHarness(&a.Harness); err != nil {
			return "", asArgError(err)
		}
		var since time.Duration
		if a.Since != "" {
			var err error
			since, err = parseDur(a.Since)
			if err != nil {
				return "", asArgError(err)
			}
		}
		// The agent-facing blame reads without waiting, the way recall does:
		// it is the tool called before editing a file, so declining for the
		// length of a refresh means the edit happens without the history
		// (#1784). Only a store deja cannot answer from at all still gets the
		// sentence (#1306).
		if line := buildingNowForAgent(dir); line != "" {
			return line, nil
		}
		text, hits, err := blameTextResult(dir, search.BlameOptions{Harness: a.Harness, Project: a.Project, Since: since, All: a.All}, a.Path, int(a.Limit))
		if err == nil {
			// blame answers the agent the way recall does, and hands over more
			// than either — whole sessions rather than budgeted snippets. Not
			// recording it left `deja log` understating what the agent was
			// given (#682).
			usage.RecordServedFromInto(dir, usage.KindBlame, text, mcpConnID(), hits, 0, nil, nil, policy.Load().Describe(policy.ActivationMCP))
		}
		return text, err
	case "fix":
		// Recorded like blame and the resource reader: `deja log` is what deja
		// put in front of an agent, and this is the surface that hands one a
		// command to run (#2858).
		return recordedMCPAnswer(dir, usage.KindFix, func() (string, int, error) { return mcpFix(dir, name, raw) })
	case "how":
		return recordedMCPAnswer(dir, usage.KindHow, func() (string, int, error) { return mcpHow(dir, name, raw) })
	case "orient":
		return recordedMCPAnswer(dir, usage.KindOrient, func() (string, int, error) { return mcpOrient(dir, name, raw) })
	case "handoff":
		return recordedMCPAnswer(dir, usage.KindHandoff, func() (string, int, error) { return mcpHandoff(dir, name, raw) })
	case "remember":
		var a struct {
			Text    string   `json:"text"`
			Project string   `json:"project"`
			Tags    []string `json:"tags"`
		}
		if err := decodeToolArgs(name, raw, &a); err != nil {
			return "", err
		}
		if strings.TrimSpace(a.Text) == "" {
			return "", argErrorf("text required")
		}
		// "repo:" asks for the repository's committed memory instead of this
		// machine's notes. Written only when named, and never committed.
		if kind, repo, err := repoRememberKind(a.Project); repo {
			if err != nil {
				return "", err
			}
			out, err := mcpRememberRepo(kind, a.Text)
			if err == nil {
				usage.RecordResult(dir, usage.KindRemember, len(a.Text), 1, false)
			}
			return out, err
		}
		if strings.TrimSpace(a.Project) == "" {
			// Filed where `deja remember` files it, under the project the
			// server runs in, so the hooks there serve it back.
			a.Project = "notes"
			if cwd, err := os.Getwd(); err == nil {
				if p := sources.ClaudeProjectName(cwd); p != "" {
					a.Project = p
				}
			}
		}
		switch err := sources.AppendNoteTagged(a.Project, a.Text, a.Tags, time.Now()); {
		case errors.Is(err, sources.ErrNoteExists):
			// Not an error to the agent: the fact it wanted stored is stored.
			// Saying so stops it retrying, and stops one fact costing a line of
			// every later recall (#1736).
			return fmt.Sprintf("Already remembered under %s.", projectForEcho(a.Project)), nil
		case err != nil:
			return "", notesWriteError(err)
		}
		// Journalled here, where the write has just happened, rather than after
		// the index work below. `deja log` is where the user sees what an agent
		// did with their store, and the recorder sat on the one path where the
		// index was quiet — so a note stored while a rebuild was running, which
		// is the state this tool asks for itself, reached the disk and never
		// the journal.
		usage.RecordResult(dir, usage.KindRemember, len(a.Text), 1, false)
		// The note is on disk either way; what is left is making it findable.
		if index.Manual() {
			return "Saved. Automatic indexing is off on this machine, so the note becomes searchable after the next `deja index`.", nil
		}
		// On upgrade day that meant rebuilding the whole index inside the call
		// (#1309), so the agent is told instead — the detached warmup picks it
		// up with everything else.
		if line := buildingNowForBlockingTool(dir); line != "" {
			return "Saved. " + line, nil
		}
		// The check above cannot cover a rebuild that starts after it: it and a
		// blocking Ensure ask the same lock a moment apart, and what began in
		// between was waited out inside the client's call (#1804). One attempt
		// at the lock decides both.
		busy, err := index.EnsureForSearchNoWait(dir, search.Options{All: true}, mcpProgress())
		if err != nil {
			return "", err
		}
		if busy {
			requestWarmup(dir)
			if line := buildingNowForBlockingTool(dir); line != "" {
				return "Saved. " + line, nil
			}
			return "Saved. " + rememberSavedNote(dir), nil
		}
		return fmt.Sprintf("Remembered under %s.", projectForEcho(a.Project)), nil
	default:
		return "", argErrorf("unknown tool %q", name)
	}
}

// blameTextResult returns the answer and how many sessions it names.
func blameTextResult(dir string, o search.BlameOptions, path string, limit int) (string, int, error) {
	target, err := search.ResolveBlamePath(path)
	if err != nil {
		return "", 0, asArgError(err)
	}
	hits, _, _, refreshing, err := findBlameHitsStale(dir, target, o, policy.ActivationMCP, mcpProgress())
	if err != nil {
		return "", 0, err
	}
	if limit <= 0 {
		limit = 10
	}
	found := len(hits)
	if !o.All && len(hits) > limit {
		hits = hits[:limit]
	}
	// Strip the embedded transcript. A hit carries the whole session, messages
	// and all, so one blame call returned 495 KB into an agent's context —
	// against the ~4 KB the other tools answer in. The snippets that sit beside
	// it are the part an agent reads; the full session is one recall_context
	// away when it genuinely needs it.
	//
	// The byte budget below is the same cap for every path into here. `all`
	// used to skip the truncation above and hand back 162 KB from a store where
	// 300 sessions touched one file (#1071); a cap that an argument can turn
	// off is not a cap.
	if len(hits) == 0 {
		// "Nobody touched this file" and "deja has nothing indexed at all" are
		// different answers (#2862), and a store full of history where nothing
		// touched it has to say so in a sentence rather than read as a tool
		// that failed (#3570).
		metas, err := index.AllMeta(dir)
		pol := policy.Load()
		active, visible := 0, 0
		for _, m := range metas {
			if sources.HarnessExcluded(m.Harness) {
				continue
			}
			active++
			if metaVisible(pol, policy.ActivationMCP, m) {
				visible++
			}
		}
		if err == nil && active == 0 {
			return emptyStoreSentence("so nothing can be found"), 0, nil
		}
		out := noBlameHistorySentence(target.Base, visible)
		if refreshing {
			c := index.SnapshotCoverage(dir)
			c.Stale = true
			out += " (" + coverageLine(c) + ")"
		}
		return out, 0, nil
	}
	// Trimmed without the notes, then rebuilt with them: a session must not
	// be dropped to make room for a sentence about the index. The answer can
	// end up a note's length over the budget, about a hundred bytes and worth
	// more than the session it would otherwise cost.
	body := renderMCPBlame(path, target.Base, hits, 0, "")
	for len(body) > blameMCPBudget && len(hits) > 1 {
		hits = hits[:max(len(hits)*3/4, 1)]
		body = renderMCPBlame(path, target.Base, hits, 0, "")
	}
	if omitted := found - len(hits); omitted > 0 || refreshing {
		// Silently returning the top slice let an agent conclude it had seen
		// every session that touched the file.
		stale := ""
		if refreshing {
			c := index.SnapshotCoverage(dir)
			c.Stale = true
			stale = coverageLine(c)
		}
		body = renderMCPBlame(path, target.Base, hits, omitted, stale)
	}
	return body, len(hits), nil
}

// recallMCPBudget is the whole recall reply: the framed page and, on the first
// call of a session, the environment block after it.
const recallMCPBudget = 4096

// contextDigestCut is the line that admits a digest was cut. Without it the
// reply ends mid-word and reads as the whole session, which is what an agent
// then tells the user it saw. It names `deja show`, the one read that prints a
// session whole: it pointed at `deja ctx`, which cuts at the same 8 KB, and at
// recall_context, a tool name the one-tool server no longer lists (#5392).
func contextDigestCut(id string) string {
	if id == "" {
		id = "<id>"
	}
	return "\n[digest trimmed to fit the ~8KB budget, the rest of this session left out — `deja show " + search.SafeLine(id) + "` in a shell prints all of it]\n"
}

// fitContextDigest trims a digest to budget, cutting at a line boundary where
// one is near enough and saying that it cut. The marker is reserved before the
// trim, the way recall reserves its paging line (#1726), so the thing that
// explains the cut is not itself the thing that gets cut.
func fitContextDigest(text, query, id string, budget int) string {
	if len(text) <= budget {
		return text
	}
	cutLine := contextDigestCut(id)
	if budget <= len(cutLine) {
		return trimUTF8(text, budget)
	}
	body := trimUTF8(text, budget-len(cutLine))
	// Back up to the last line break if one is close, so the digest does not
	// end in the middle of a sentence it will not finish — but never at the
	// cost of the words the digest was asked for. A match sitting in that last
	// line is the answer; ending mid-word is only untidy.
	if i := strings.LastIndexByte(body, '\n'); i > len(body)-400 && i > 0 {
		if shorter := body[:i]; keepsQuery(shorter, body, query) {
			body = shorter
		}
	}
	return body + cutLine
}

func firstID(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// keepsQuery reports whether the shorter body still carries a query word that
// the longer one did. A query whose words are nowhere in either is no reason to
// keep the ragged ending.
func keepsQuery(shorter, longer, query string) bool {
	for word := range strings.FieldsSeq(strings.ToLower(query)) {
		if len(word) < 3 {
			continue
		}
		if strings.Contains(strings.ToLower(longer), word) && !strings.Contains(strings.ToLower(shorter), word) {
			return false
		}
	}
	return true
}

// contextMCPBudget is the whole framed recall_context reply, matching the
// "~8KB" its tool description promises an agent. recall has been exact since it
// passed 4096-recallFrameOverhead; this path passed no budget at all, so the
// digest header — which carries a project name and a session id, neither of
// them bounded — pushed an ordinary reply to 8221 bytes and a long-named one to
// 8335 (#1797). The frame's header grew by 270 bytes when it started saying
// which recalled lines to follow, and the budget grew with it, or the digest
// lost its own end note to the trim.
const contextMCPBudget = 8192 + 270

// recordedMCPAnswer journals what a tool handed the agent, whatever it was:
// "no session ran a command after that error" is an answer the agent acts on
// as much as a remedy is, and a log that held only the successful calls
// understated what deja said (#2858).
func recordedMCPAnswer(dir, kind string, answer func() (string, int, error)) (string, error) {
	text, found, err := answer()
	if err == nil {
		// The count, not a zero: a row recorded with none is marked as having
		// found nothing, so the log said "(empty result)" over an answer that
		// served a command from two sessions — the opposite of what happened,
		// on the surface this recording exists to make honest (#2858).
		usage.RecordServedFromInto(dir, kind, text, mcpConnID(), found, 0, nil, nil, policy.Load().Describe(policy.ActivationMCP))
	}
	return text, err
}

func mcpFix(dir, name string, raw json.RawMessage) (string, int, error) {
	var a struct {
		Error string    `json:"error"`
		Limit mcpNumber `json:"limit"`
	}
	if err := decodeToolArgs(name, raw, &a); err != nil {
		return "", 0, err
	}
	if strings.TrimSpace(a.Error) == "" {
		return "", 0, argErrorf("error text required")
	}
	// Before the read, not after: the rebuild used to happen first and the
	// guard ran when there was nothing left to report (#1306, #1309).
	if line := buildingNowForAgent(dir); line != "" {
		return line, 0, nil
	}
	if _, err := index.EnsureForSearchStale(dir, search.Options{}, mcpProgress()); err != nil {
		return "", 0, err
	}
	pol := policy.Load()
	pairs := index.FixesFor(dir, a.Error, int(a.Limit), func(project string) bool {
		return pol.Allows(policy.ActivationMCP, project)
	})
	if len(pairs) == 0 {
		if near := nearestRecordedError(dir, a.Error, func(project string) bool {
			return pol.Allows(policy.ActivationMCP, project)
		}); near != "" {
			pairs = index.FixesFor(dir, near, int(a.Limit), func(project string) bool {
				return pol.Allows(policy.ActivationMCP, project)
			})
		}
	}
	if len(pairs) == 0 {
		// Nothing to look in is not nothing found (#5395). What sessions
		// said about the error still helps.
		if note := fixDataNote(dir); note != "" {
			note = "deja: " + note + "."
			if text, n := fixFallsBackToRecall(dir, a.Error); n > 0 {
				return note + "\n\n" + text, n, nil
			}
			return note, 0, nil
		}
		// What the heuristic is for is mining pairs out of a transcript, where
		// a false positive costs a bad pair. Used on the caller it accuses the
		// agent of passing the wrong thing: measured against twenty error lines
		// a tool actually prints, it refused ten — `connection refused`,
		// `permission denied`, `segmentation fault`, `OOMKilled`, `Exit code
		// 137` among them — and the CLI took all twenty (#3580). So the
		// guidance stays and the accusation goes: the sentence says what is
		// true either way, and what to do if it really was a summary.
		if !index.LooksLikeError(a.Error) {
			if text, n := fixFallsBackToRecall(dir, a.Error); n > 0 {
				return text, n, nil
			}
			return "No session on this machine ran a command after that error. If that was a summary rather than the failing output, pass the output itself." + emptyStoreNote(dir), 0, nil
		}
		// Held-but-unconfirmed is not never-seen, and the agent asking is
		// the one that would otherwise re-derive the remedy (#2282).
		if index.FixCandidateSeen(dir, a.Error, func(project string) bool {
			return pol.Allows(policy.ActivationMCP, project)
		}) {
			return "Something was run after that error before, and nothing has confirmed it worked - deja names no remedy until something does.", 0, nil
		}
		// A remedy exists and was held back because it cannot be taken back;
		// saying nothing ran would be false. What sessions said still helps.
		if index.FixWithheldAsIrreversible(dir, a.Error, func(project string) bool {
			return pol.Allows(policy.ActivationMCP, project)
		}) {
			const withheld = "What ran after that error on this machine was a merge, a force push or a deletion - deja does not hand those over as a fix."
			if text, n := fixFallsBackToRecall(dir, a.Error); n > 0 {
				return withheld + "\n\n" + text, n, nil
			}
			return withheld, 0, nil
		}
		if text, n := fixFallsBackToRecall(dir, a.Error); n > 0 {
			return text, n, nil
		}
		return "No session on this machine ran a command after that error." + emptyStoreNote(dir), 0, nil
	}
	var fb strings.Builder
	for _, p := range pairs {
		when := ""
		if !p.When.IsZero() {
			when = " (" + p.When.Local().Format("2006-01-02") + ")"
		}
		if p.Edit != "" {
			// An edit is not something to run, so it is not offered as one:
			// the file, in the words `deja fix` uses for the same pair
			// (#2163). This surface said "ran next:" with nothing after it
			// (#3261).
			changed := "changed next"
			if p.Candidate {
				changed = "changed next, unconfirmed"
			}
			fmt.Fprintf(&fb, "%s%s\n  %s: %s\n", recallListingLine(p.Error), when, changed,
				search.SafePath(p.Edit))
			for _, dl := range fixEditDiffLines(dir, p) {
				fmt.Fprintf(&fb, "    %s\n", dl)
			}
			continue
		}
		ran := "ran next"
		if p.Candidate {
			ran = "ran next, unconfirmed"
		}
		fmt.Fprintf(&fb, "%s%s\n  %s: %s\n", recallListingLine(p.Error), when, ran,
			commandListingLine(p.Command))
		// What makes a repaired remedy the answer is the command before it, so
		// the surface that hands it over shows both — otherwise the agent is
		// asked to trust a long line that names nothing of its error.
		if p.Failed != "" {
			fmt.Fprintf(&fb, "  after this failed: %s\n", commandListingLine(p.Failed))
		}
	}
	// Framed like `how` (#2844) and for a sharper reason: this hands an agent a
	// command at the moment it has just hit an error, which is the moment it is
	// most likely to run it without reading. The command came out of a
	// transcript, which recall_frame.go names as data an attacker may have
	// influenced.
	return frameRecall(strings.TrimRight(fb.String(), "\n")), len(pairs), nil
}

func mcpHow(dir, name string, raw json.RawMessage) (string, int, error) {
	var a struct {
		What    string    `json:"what"`
		Project string    `json:"project"`
		Limit   mcpNumber `json:"limit"`
	}
	if err := decodeToolArgs(name, raw, &a); err != nil {
		return "", 0, err
	}
	if strings.TrimSpace(a.What) == "" {
		return "", 0, argErrorf("what required")
	}
	if line := buildingNowForAgent(dir); line != "" {
		return line, 0, nil
	}
	if _, err := index.EnsureForSearchStale(dir, search.Options{}, mcpProgress()); err != nil {
		return "", 0, err
	}
	// The project of the working directory unless the caller named one: the
	// agent asking is standing in the repository it means, and a machine-wide
	// answer put another project's wrapper at the top (#3705).
	scope := howScope(howCwd(), a.Project, false)
	entries, hidden, ignored, _, err := howEntriesWithFallback(dir, strings.Fields(a.What), scope, len(scope) > 0 && strings.TrimSpace(a.Project) == "", policy.ActivationMCP)
	if err != nil {
		return "", 0, err
	}
	if len(entries) == 0 {
		// The same reasoning one line down, for the other rule: an agent
		// told nothing exists invents one, and the ignore rule is exactly
		// the case where something does exist (#2630).
		if note := ignoredHiddenNoteFor("answer", ignored); note != "" {
			return strings.TrimSpace(note), 0, nil
		}
		// Not a flat negative when the policy is what emptied the answer:
		// an agent told nothing exists invents one, and here something does
		// exist. The CLI has said so since the note was written; this
		// surface was returning the negative regardless.
		if note := policyHiddenNote(policy.ActivationMCP, hidden); note != "" {
			return strings.TrimSpace(note), 0, nil
		}
		if note := howDataNote(); note != "" {
			return "deja: " + note + ".", 0, nil
		}
		return fmt.Sprintf("No command on this machine mentions %q.", a.What) + emptyStoreNote(dir), 0, nil
	}
	limit := int(a.Limit)
	if limit <= 0 {
		limit = 8
	}
	var hb strings.Builder
	// The same lines the CLI prints, from the same writer: this tool used
	// to keep its own copy of the loop, so a note the CLI learned never
	// reached the agent (#1634).
	writeHowEntries(&hb, entries, limit, ", last ")
	out := strings.TrimRight(hb.String(), "\n")
	if note := howCapNote(len(entries), limit, "call again with a higher limit for the rest"); note != "" {
		// The agent cannot ask a follow-up of its own, so the cut has to
		// travel with the answer rather than to a terminal it never sees.
		out += "\n\n" + note
	}
	// Framed like every other agent-facing recall: these are command lines
	// lifted out of transcripts, which recall_frame.go names as data an
	// attacker may have influenced — and they are the most directly actionable
	// thing deja serves, since an agent may run them (#2827's sweep).
	return frameRecall(out), len(entries), nil
}

// blameMCPBudget bounds one blame answer. Higher than recall's ~4 KB because a
// hit is a whole session rather than a snippet, and well under what an agent
// can absorb from one tool call.
const blameMCPBudget = 8192

// blameTouchedCap bounds the files a blame row names beside the one asked about.
//
// The manifest holds up to forty per session, ordered by how often the session
// touched them, and the whole list was served: measured on a real store, that
// was 3186 of an 8044-byte answer — 40% of the second most-called tool's
// payload — about files the question did not ask about. Three is what the
// documented field says it is, "the few files this session worked on most", and
// the head of that list is the most-touched ones. A co-change line was weighed
// instead and dropped: over eight real paths the recurring neighbour appeared
// in one session of one.
const blameTouchedCap = 3

// renderMCPBlame is the blame answer in the shape recall answers in: one
// framed page, a header naming what was asked and how many sessions follow,
// and a numbered row per session in the same [harness] project · id form.
// It answered in JSON until #4634, the one agent-facing mode that did: on a
// real store 65% of an answer was keys, transcript paths and float scores,
// and the decision-taken-back line blame attaches never reached the agent.
func renderMCPBlame(asked, base string, hits []search.BlameHit, omitted int, stale string) string {
	var b strings.Builder
	if stale != "" {
		fmt.Fprintln(&b, "("+stale+")")
	}
	fmt.Fprintf(&b, "deja blame for %q (%d session%s, the one that names it most specifically first)\n", recallListingLine(asked), len(hits), pluralS(len(hits)))
	for i, h := range hits {
		// A note's title carries no bound into the index (#2092), and this
		// page is read by an agent whose context it spends.
		fmt.Fprintf(&b, "\n%d. [%s] %s · %s · %d mention%s", i+1,
			recallListingLine(h.Session.Harness), recallListingLine(h.Session.Project), recallListingLine(h.Session.ID), h.Count, pluralS(h.Count))
		if !h.Session.Updated.IsZero() {
			fmt.Fprintf(&b, " · updated %s (%s)", h.Session.Updated.Local().Format("2006-01-02"), search.RelativeDate(h.Session.Updated))
		}
		fmt.Fprintln(&b)
		if t := search.SafeNoteTitle(h.Session.Title); t != "" {
			fmt.Fprintf(&b, "title: %s\n", recallListingLine(t))
		}
		// blame is asked "who decided this", and a decision taken back has to
		// say so where the agent reads it (#1017).
		if line := lifecycleLine(search.Hit{Lifecycle: h.Lifecycle, LifecycleAt: h.LifecycleAt, LifecycleNote: h.LifecycleNote}); line != "" {
			fmt.Fprintln(&b, line)
		}
		if h.Tier != "" && h.Tier != search.TierExact {
			fmt.Fprintf(&b, "[%s]\n", recallListingLine(h.Tier))
		}
		if also := otherTouched(h.Session.Touched, base); len(also) > 0 {
			fmt.Fprintf(&b, "also worked on: %s\n", strings.Join(also, ", "))
		}
		for _, sn := range h.Snippets {
			fmt.Fprintf(&b, "- %s\n", recallListingLine(sn))
		}
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n%d more session%s touch this path and were left out to stay within one answer — narrow with project, harness or since, or call the deja tool in \"context\" mode (or recall_context) on one of the above.\n",
			omitted, pluralS(omitted))
	}
	return frameRecall(strings.TrimRight(b.String(), "\n"))
}

// otherTouched names the files a session worked on most besides the one asked
// about, by base name: the full paths were 40% of a blame answer (see
// blameTouchedCap), and the name is what tells the agent what else moved.
func otherTouched(paths []string, base string) []string {
	// Either separator, whatever this machine uses: a synced session from
	// Windows names its files with backslashes.
	leafName := func(p string) string { return p[strings.LastIndexAny(p, `/\`)+1:] }
	var out []string
	for _, p := range paths {
		name := leafName(p)
		if name == "" || name == base {
			continue
		}
		out = append(out, recallListingLine(name))
		if len(out) == blameTouchedCap {
			break
		}
	}
	return out
}

func mcpProgress() io.Writer {
	if os.Getenv("DEJA_DEBUG") == "1" {
		return os.Stderr
	}
	return io.Discard
}

// dropsAWord is whether the search answered without one of the query's words.
func dropsAWord(variants map[string][]string) bool {
	return index.DroppedAWord(variants)
}

func fuzzySummary(variants map[string][]string) []string {
	var out []string
	for token, values := range variants {
		for _, value := range values {
			if value == "" {
				out = append(out, token+" (ignored: no session matches it with the rest)")
				continue
			}
			if value != token {
				out = append(out, token+" -> "+value)
			}
		}
	}
	sort.Strings(out)
	return out
}

// mcpNumber accepts a number or a numeric string. Models emit both — the
// schema says number, but a client that stringifies its arguments would
// otherwise get a Go type error back instead of a result, and the error text
// leaks internal field names into a protocol surface.
type mcpNumber float64

func (n *mcpNumber) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*n = mcpNumber(f)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("want a number, got %s", strings.TrimSpace(string(b)))
	}
	if strings.TrimSpace(s) == "" {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return fmt.Errorf("want a number, got %q", s)
	}
	*n = mcpNumber(f)
	return nil
}

// rebuildRefusedForAgent is what an agent is told when this build cannot read
// the index and cannot start a rebuild itself.
//
// Which of the two states it is decides the sentence, the way it does at
// session start: a rebuild writes the new index beside the old directory and
// replaces it, so a read-only index directory inside a writable parent is
// rebuilt by `deja index` — measured in #2502. Saying it "cannot be rebuilt"
// there denied in one half what the other half advised (#2506).
func rebuildRefusedForAgent(dir string) string {
	if dirWritable(filepath.Dir(dir)) {
		return "deja's index was written by another version of deja and this session cannot rebuild it. Tell the user to run `deja index`; recall is quiet until they do."
	}
	return "deja's index was written by another version of deja and " + unwritableIndexDir(dir) + " is not writable, so it cannot be rebuilt. Tell the user to run `deja index`, which says what to change."
}

// buildingNowForAgent explains the one state an agent cannot ask a human about:
// the index is not there yet because it is being built. Without this the tool
// call failed with `manifest: open /…/manifest.gob: no such file or directory`
// — an internal path and an errno, handed to a model as a broken tool (#972).
// Every other surface says the same thing in words.
func buildingNowForAgent(dir string) string {
	// With automatic indexing off the agent cannot start a build, and "ask
	// again then" would never come true (#5385).
	if index.Manual() {
		return manualIndexNoteForAgent(dir)
	}
	// A refresh is not an empty index. The snapshot on disk is published by an
	// atomic swap and stays readable throughout one, which is why readers take
	// tryLockDir rather than waiting — so answer from it and let recall say a
	// refresh is running. Sending the agent away for the length of every
	// refresh cost it the history it has: an agent does not ask again, it
	// concludes there is none (#1733). The sentence below is for the state it
	// was written for — nothing to answer from yet.
	if index.HasManifest(dir) && !indexNeedsRebuild(dir) {
		return ""
	}
	// The same holds for the rebuild a version bump starts: the old snapshot
	// stays readable until the swap, and the search layer already serves it
	// (#3552). Checked after the status file below, this sent 72 of 82
	// no-answer recalls in real transcripts to "ask again", and agents rarely
	// do. Only while a build runs: with none running, the branch further down
	// asks for one first.
	if warmupRunning(dir) && index.HasManifest(dir) && index.AnswersWhileRebuilding(dir) && !index.Damaged(dir) {
		return ""
	}
	if st := readWarmupStatus(dir); st != nil {
		return "deja is indexing this machine's history (" + st.progress() + "). Recall comes online when it finishes; ask again then."
	}
	if index.RebuildInProgress(dir) || warmupJustRequested(dir) {
		return "deja is indexing this machine's history. Recall comes online when it finishes; ask again then."
	}
	// An index this build cannot read is not one it can answer from, and that
	// is the state a version bump leaves behind. HasManifest called it present,
	// so the first agent call after an upgrade rebuilt 16000 sessions inside
	// the tool call — 2.86s against 0.22s steady state, nothing said, and MCP
	// clients have timeouts (#1309). Hand it to the detached warmup, the way
	// every other surface repairs this, and say so.
	if index.HasManifest(dir) && indexNeedsRebuild(dir) {
		// "Ask again then" has to be true. A read-only index directory is the
		// one state that never repairs itself, and telling an agent to come
		// back would loop it forever (the shape #1048 fixed at session start).
		if !indexDirWritable(dir) {
			return rebuildRefusedForAgent(dir)
		}
		requestWarmup(dir)
		// A stale content version is not an unreadable store. When the layout is
		// the one this build writes, answer from the snapshot and let the
		// detached rebuild catch up — the same choice #1733 made for a refresh,
		// for the same reason: measured on this machine, 8 of the 56 recalls an
		// agent actually made landed in this sentence, and an agent does not ask
		// again, it concludes there is no history. Of the last four upgrades,
		// three changed only what deja derives from a transcript.
		damaged := index.Damaged(dir)
		if !damaged && index.ReadableSnapshot(dir) {
			return ""
		}
		// A store that will not read is not an upgrade, and saying "for this
		// version" sent the reader looking at versions.
		if damaged {
			return "deja's index could not be read and is being rebuilt. Recall comes online shortly; ask again then."
		}
		return "deja is rebuilding its index for this version of deja. Recall comes online shortly; ask again then."
	}
	// Nothing indexed yet and nothing building: this is a first run, and
	// building it here is how an install with no hooks ever gets an index.
	return ""
}

// warmupRunning reports a build in flight or just asked for: its status file,
// the index lock, a fresh sentinel, or a claim whose owner is still running.
func warmupRunning(dir string) bool {
	return readWarmupStatus(dir) != nil || index.RebuildInProgress(dir) || warmupJustRequested(dir) || warmupOwnerLive(dir)
}

// buildingNowForBlockingTool is buildingNowForAgent for the two tools that
// still reach a blocking index.EnsureForSearch — blame and remember. Every
// other tool reads through the non-blocking path and answers from the snapshot
// while a refresh runs (#1733); these two would wait out the whole rebuild
// inside the call, so for them a refresh in flight is still a reason to say so
// rather than to hang.
func buildingNowForBlockingTool(dir string) string {
	if st := readWarmupStatus(dir); st != nil {
		return "deja is indexing this machine's history (" + st.progress() + "). Recall comes online when it finishes; ask again then."
	}
	if index.RebuildInProgress(dir) || warmupJustRequested(dir) {
		return "deja is indexing this machine's history. Recall comes online when it finishes; ask again then."
	}
	// Everything else these two have to say — an index written by another
	// version, an index directory nobody can write — is the same sentence the
	// reading tools get, so it is answered in one place rather than copied.
	return buildingNowForAgent(dir)
}

// projectsOf names the project a session belongs to, as a list, for the digest
// record. Empty for a session that carries none.
func projectsOf(s model.Session) []string {
	if s.Project == "" {
		return nil
	}
	return []string{s.Project}
}

// rememberSavedNote is what a remember says when the index is mid-refresh. Not
// "in a few seconds": the same build measured 59 seconds on a 177 MB index, and
// what the writer needs to know is that the note is safe and will be findable,
// not how long that takes (#2598).
func rememberSavedNote(dir string) string {
	if line := buildingNowForBlockingTool(dir); line != "" {
		return line
	}
	return "deja is refreshing its index; this note becomes findable when that finishes."
}
