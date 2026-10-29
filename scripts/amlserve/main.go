// Command amlserve serves deja as the Add/Search memory API that the Agent
// Memory Leaderboard calls (agentmemoryleaderboard.ai/api-guide). Add writes
// the messages as a Claude Code transcript under a store per user_id; Search
// updates that user's index when new messages have arrived and answers with
// the two best past sessions (each as its task, how it ended, the files it
// touched, the code it changed and its matching messages), the remaining
// matching tasks in brief, and single matching messages with their neighbours.
// Sessions are scored by BM25 over the whole transcript and its ending plus
// deja's own order. -raw answers with whole sessions in deja's order instead.
// No model is called on either path.
//
//	AML_TOKEN=secret go run ./scripts/amlserve -addr :8787 -store /var/lib/amlserve
//
// The index reads its roots from the environment, so requests are served one
// at a time; declare concurrency 1 when registering. Keep -store outside the
// paths deja ignores by default (agent job and scratch directories): a store
// there indexes and then answers every search with nothing.
package main

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

type contentText string

func (c *contentText) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*c = contentText(s)
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &parts); err != nil {
		return err
	}
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p.Text) != "" {
			out = append(out, p.Text)
		}
	}
	*c = contentText(strings.Join(out, "\n"))
	return nil
}

type message struct {
	Role      string      `json:"role"`
	Content   contentText `json:"content"`
	Timestamp int64       `json:"timestamp,omitempty"`
}

type addRequest struct {
	RequestID string    `json:"request_id"`
	Messages  []message `json:"messages"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
}

type searchRequest struct {
	Query   contentText `json:"query"`
	Options []string    `json:"options"`
	UserID  string      `json:"user_id"`
	TopK    int         `json:"top_k"`
}

type item struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	Score     float64 `json:"score,omitempty"`
	CreatedAt string  `json:"created_at,omitempty"`
}

// maxBody is above the largest Add the contract allows: 30 MiB of images.
const maxBody = 64 << 20

type server struct {
	store    string
	token    string
	maxItems int
	maxChars int
	// head is the budget of the first ranks, which get more or less of their
	// session than maxChars when set.
	head []int
	// fields reranks with BM25 over the transcript and how it ended.
	fields bool
	// messages answers with single messages instead of sessions.
	messages bool
	// result shows each session by what it changed rather than from the top.
	result bool
	// lead is how many best sessions open a -messages answer whole, and
	// brief how many after them come as their task, files and ending.
	lead  int
	brief int
	// windows ends a -result session with its messages that match the
	// question instead of its start.
	windows bool
	mu      sync.Mutex
	tables  map[string]map[string]string
	dirty   map[string]bool
}

func main() {
	addr := flag.String("addr", ":8787", "listen address")
	store := flag.String("store", "", "directory that holds one store per user_id")
	maxItems := flag.Int("items", 10, "most sessions returned per search under -raw, below top_k")
	maxChars := flag.Int("chars", 8000, "most characters of one session returned")
	head := flag.String("head", "", "comma-separated character budgets for the first ranks, before -chars applies")
	flag.IntVar(&answerChars, "cut", answerChars, "most characters returned across all items of one search")
	resultView := flag.Bool("result", false, "show each session as its task, how it ended and the code it changed")
	windows := flag.Bool("windows", false, "under -result, follow each session's changes with its messages that match the question")
	brief := flag.Int("brief", 0, "under -messages, after the leading sessions list this many more by their task, files and ending")
	lead := flag.Int("lead", 0, "under -messages, open the answer with this many best sessions rendered whole")
	messages := flag.Bool("messages", false, "answer with single messages and their neighbours, scored with their session")
	flag.IntVar(&msgShowChars, "show", msgShowChars, "most characters of one non-edit message an answer shows")
	flag.IntVar(&msgNeighbours, "nb", msgNeighbours, "messages on each side that come with a matching one")
	fields := flag.Bool("fields", false, "rerank by BM25 over each session's transcript and its ending, with deja's order")
	raw := flag.Bool("raw", false, "answer with whole sessions in deja's order")
	flag.Parse()
	if *store == "" {
		log.Fatal("amlserve: -store is required")
	}
	s := &server{
		tables:   map[string]map[string]string{},
		dirty:    map[string]bool{},
		store:    *store,
		token:    os.Getenv("AML_TOKEN"),
		maxItems: *maxItems,
		maxChars: *maxChars,
		fields:   *fields,
		messages: *messages,
		result:   *resultView,
		lead:     *lead,
		brief:    *brief,
		windows:  *windows,
	}
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	custom := false
	for _, f := range []string{"result", "windows", "lead", "brief", "messages", "fields"} {
		custom = custom || set[f]
	}
	if !*raw && !custom {
		s.defaults()
		if set["head"] {
			s.head = nil
		}
	}
	for f := range strings.SplitSeq(*head, ",") {
		if f = strings.TrimSpace(f); f != "" {
			n, err := strconv.Atoi(f)
			if err != nil || n <= 0 {
				log.Fatalf("amlserve: -head %q: want positive numbers", *head)
			}
			s.head = append(s.head, n)
		}
	}
	if s.token == "" {
		log.Print("amlserve: AML_TOKEN is empty, requests are not authenticated")
	}
	srv := &http.Server{Addr: *addr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// defaults configures the structured answer used when no mode flag is set:
// the two best matching sessions rendered as their task, ending, touched files,
// code changes and matching messages, followed by the remaining matching tasks
// in brief and single matching messages with their neighbours.
func (s *server) defaults() {
	s.messages, s.result, s.windows = true, true, true
	s.lead, s.brief = 2, 27
	s.head = []int{4200, 6500}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /add", s.authed(s.add))
	mux.HandleFunc("POST /search", s.authed(s.search))
	return mux
}

func (s *server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := strings.TrimSpace(r.Header.Get("X-Api-Key"))
			if a := strings.TrimSpace(r.Header.Get("Authorization")); got == "" && a != "" {
				got = a
				for _, p := range []string{"Bearer ", "Token ", "bearer ", "token "} {
					if after, ok := strings.CutPrefix(a, p); ok {
						got = strings.TrimSpace(after)
						break
					}
				}
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				fail(w, http.StatusUnauthorized, "invalid key")
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		h(w, r)
	}
}

func fail(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]any{"detail": map[string]string{"reason": reason}}); err != nil {
		log.Printf("amlserve: write reply: %v", err)
	}
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("amlserve: write reply: %v", err)
	}
}

// nameOf is the on-disk name of an id the caller sent, numbered in the order
// first seen and kept in a table beside the names: user_id and session_id
// never reach a path, not even hashed. ok is false when the id is unknown and
// create is off.
func (s *server) nameOf(table, prefix, id string, create bool) (string, bool, error) {
	t, ok := s.tables[table]
	if !ok {
		t = map[string]string{}
		if b, err := os.ReadFile(table); err == nil {
			if jerr := json.Unmarshal(b, &t); jerr != nil {
				return "", false, jerr
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
		s.tables[table] = t
	}
	if name, ok := t[id]; ok {
		return name, true, nil
	}
	if !create {
		return "", false, nil
	}
	name := fmt.Sprintf("%s%d", prefix, len(t)+1)
	t[id] = name
	b, err := json.Marshal(t)
	if err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(table), 0o755); err != nil {
		return "", false, err
	}
	tmp := table + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", false, err
	}
	return name, true, os.Rename(tmp, table)
}

// userDir is the store of one user_id; the transcripts sit where the Claude
// parser looks for them, under one project.
func (s *server) userDir(user string, create bool) (root, proj, idx string, ok bool, err error) {
	name, ok, err := s.nameOf(filepath.Join(s.store, "users.json"), "u", user, create)
	if !ok || err != nil {
		return "", "", "", ok, err
	}
	root = filepath.Join(s.store, name)
	return root, filepath.Join(root, "claude", "-work-aml"), filepath.Join(root, "index.db"), true, nil
}

// useStore points the index at one user's store. The roots are process-wide,
// which is why every request holds s.mu.
func useStore(root, idx string) {
	_ = os.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	_ = os.Setenv("DEJA_INDEX_DIR", idx)
	_ = os.Setenv("DEJA_POLICY_FILE", filepath.Join(root, "policy.json"))
}

func (s *server) add(w http.ResponseWriter, r *http.Request) {
	var req addRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "body is not JSON: "+err.Error())
		return
	}
	if req.RequestID == "" || req.UserID == "" || req.SessionID == "" || len(req.Messages) == 0 {
		fail(w, http.StatusUnprocessableEntity, "request_id, user_id, session_id and messages are required")
		return
	}
	for _, m := range req.Messages {
		if strings.TrimSpace(string(m.Content)) == "" || (m.Role != "user" && m.Role != "assistant") {
			fail(w, http.StatusUnprocessableEntity, "every message needs role user or assistant and content")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, proj, _, _, err := s.userDir(req.UserID, true)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.write(root, proj, req); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.dirty == nil {
		s.dirty = map[string]bool{}
	}
	s.dirty[root] = true
	reply(w, map[string]any{"success": true, "request_id": req.RequestID, "user_id": req.UserID, "session_id": req.SessionID})
}

// write adds the chunk to its session's transcript. Each line carries the
// request_id it came in, and the file is replaced whole, so a retried
// request_id either finds every line of its chunk on disk or none of them.
func (s *server) write(root, proj string, req addRequest) error {
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return err
	}
	sid, _, err := s.nameOf(filepath.Join(root, "sessions.json"), "s", req.SessionID, true)
	if err != nil {
		return err
	}
	path := filepath.Join(proj, sid+".jsonl")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	mark, err := json.Marshal(req.RequestID)
	if err != nil {
		return err
	}
	if bytes.Contains(old, append([]byte(`"amlRequest":`), mark...)) {
		return nil
	}
	if err := os.WriteFile(filepath.Join(proj, sid+".sid"), []byte(req.SessionID), 0o644); err != nil {
		return err
	}
	n := bytes.Count(old, []byte("\n"))
	var b bytes.Buffer
	b.Write(old)
	enc := json.NewEncoder(&b)
	for i, m := range req.Messages {
		// Messages without a time keep their order: a second apart from the
		// session's first line.
		ts := time.Unix(1700000000+int64(n+i), 0)
		if m.Timestamp > 0 {
			if m.Timestamp < 100_000_000_000 {
				ts = time.Unix(m.Timestamp, 0)
			} else {
				ts = time.UnixMilli(m.Timestamp)
			}
		}
		var content any = string(m.Content)
		if m.Role == "assistant" {
			content = []any{map[string]any{"type": "text", "text": string(m.Content)}}
		}
		if err := enc.Encode(map[string]any{
			"type": m.Role, "sessionId": sid, "amlRequest": req.RequestID,
			"timestamp": ts.UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": m.Role, "content": content},
		}); err != nil {
			return err
		}
	}
	return replace(path, b.Bytes())
}

func replace(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readTurns reads a stored session back as the turns it holds.
func readTurns(data []byte) []turn {
	var out []turn
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		var l struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &l) == nil && l.Type != "" {
			out = append(out, turn{l.Type, text(l.Message.Content)})
		}
	}
	return out
}

func (s *server) ensureIndexed(root, idx string) error {
	_, statErr := os.Stat(filepath.Join(idx, "manifest.gob"))
	if !s.dirty[root] && statErr == nil {
		return nil
	}
	if err := index.Ensure(idx, "claude", false, nil); err != nil {
		return err
	}
	delete(s.dirty, root)
	return nil
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "body is not JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(string(req.Query)) == "" || req.UserID == "" || req.TopK <= 0 {
		fail(w, http.StatusUnprocessableEntity, "query, user_id and a positive top_k are required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, proj, idx, ok, err := s.userDir(req.UserID, false)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		reply(w, map[string]any{"data": []item{}})
		return
	}
	useStore(root, idx)
	if ierr := s.ensureIndexed(root, idx); ierr != nil {
		fail(w, http.StatusServiceUnavailable, "index: "+ierr.Error())
		return
	}
	q := string(req.Query)
	if len(req.Options) > 0 {
		q += "\n" + strings.Join(req.Options, "\n")
	}
	if s.messages {
		reply(w, map[string]any{"data": s.messageAnswer(proj, idx, q, req.TopK)})
		return
	}
	hits, err := rank(idx, q)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "search: "+err.Error())
		return
	}
	if s.fields {
		hits = s.rerank(proj, q, hits)
	}
	limit := min(req.TopK, s.maxItems)
	out := make([]item, 0, s.maxItems)
	used := 0
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		text, sid := s.render(proj, h.Session.ID, h.Snippets, q, s.budget(len(out)))
		if text == "" {
			continue
		}
		if used+len(text) > answerChars {
			if answerChars-used < 2000 {
				break
			}
			text = cut(text, answerChars-used)
		}
		used += len(text)
		out = append(out, item{ID: sid, Content: text, Score: 1.0 / float64(len(out)+1), CreatedAt: h.Session.Started.UTC().Format(time.RFC3339)})
	}
	reply(w, map[string]any{"data": out})
}

// budget is how many characters the item at rank i may hold.
func (s *server) budget(i int) int {
	if i < len(s.head) {
		return s.head[i]
	}
	return s.maxChars
}

// rank is the CLI and MCP search path: exact, stem and fuzzy tiers through
// search.Run, the relevance tier through RelevanceHits in its own order.
func rank(idx, q string) ([]search.Hit, error) {
	o := search.Options{Query: q, All: true}
	result, err := index.SearchWithRecoveryDetailed(idx, o, nil)
	if err != nil {
		return nil, err
	}
	o.Tier = result.Tier
	if result.Stemmed {
		o.Stemmed = true
	}
	if result.Stemmed || result.Fuzzy {
		o.FuzzyVariants = result.Variants
	}
	switch result.Tier {
	case search.TierError:
		return search.ErrorHits(result.Sessions, o.Query), nil
	case search.TierRelevance:
		return search.RelevanceHits(result.Sessions, index.RelevanceTerms(q)), nil
	}
	return search.Run(result.Sessions, o)
}

// render returns the session as a caller reads it: the structured task,
// conclusion and code changes under -result, or the whole transcript when it
// fits, otherwise the matched excerpts followed by the transcript from the top.
func (s *server) render(proj, sid string, snippets []string, q string, limit int) (string, string) {
	orig, err := os.ReadFile(filepath.Join(proj, sid+".sid"))
	if err != nil {
		return "", ""
	}
	f, err := os.Open(filepath.Join(proj, sid+".jsonl"))
	if err != nil {
		return "", ""
	}
	defer f.Close()
	var b strings.Builder
	var turns []turn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		t := text(l.Message.Content)
		turns = append(turns, turn{l.Type, t})
		fmt.Fprintf(&b, "[%s] %s: %s\n", l.Timestamp, l.Type, t)
	}
	full := b.String()
	if s.result {
		var tail func(int) string
		if s.windows {
			c := s.corpus(proj)
			sc := c.msgScores(q)
			tail = func(n int) string { return c.windows(sid, sc, n) }
		}
		return result(string(orig), turns, full, snippets, limit, tail), string(orig)
	}
	return renderText(full, snippets, limit), string(orig)
}

func renderText(full string, snippets []string, limit int) string {
	if len(full) <= limit {
		return full
	}
	var out strings.Builder
	for _, sn := range snippets {
		out.WriteString("... " + sn + " ...\n")
	}
	out.WriteString("\n")
	if rest := limit - out.Len(); rest > 0 {
		for rest > 0 && !utf8.RuneStart(full[rest]) {
			rest--
		}
		out.WriteString(full[:rest])
	}
	return out.String()
}

func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	parts := make([]string, 0, len(blocks))
	for _, bl := range blocks {
		parts = append(parts, bl.Text)
	}
	return strings.Join(parts, "\n")
}
