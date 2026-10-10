// Command amlserve serves deja as the Add/Search memory API that the Agent
// Memory Leaderboard calls (agentmemoryleaderboard.ai/api-guide). Add writes
// the messages as a Claude Code transcript under a store per user_id and
// indexes it before answering; Search runs the same ranking the CLI and MCP
// tool use and returns the ranked sessions. No model is called on either path.
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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/search"
)

type message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

type addRequest struct {
	RequestID string    `json:"request_id"`
	Messages  []message `json:"messages"`
	UserID    string    `json:"user_id"`
	SessionID string    `json:"session_id"`
}

type searchRequest struct {
	Query   string   `json:"query"`
	Options []string `json:"options"`
	UserID  string   `json:"user_id"`
	TopK    int      `json:"top_k"`
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
	mu       sync.Mutex
}

func main() {
	addr := flag.String("addr", ":8787", "listen address")
	store := flag.String("store", "", "directory that holds one store per user_id")
	maxItems := flag.Int("items", 10, "most sessions returned per search, below top_k")
	maxChars := flag.Int("chars", 8000, "most characters of one session returned")
	flag.Parse()
	if *store == "" {
		log.Fatal("amlserve: -store is required")
	}
	s := &server{store: *store, token: os.Getenv("AML_TOKEN"), maxItems: *maxItems, maxChars: *maxChars}
	if s.token == "" {
		log.Print("amlserve: AML_TOKEN is empty, requests are not authenticated")
	}
	log.Fatal(http.ListenAndServe(*addr, s.routes()))
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /add", s.authed(s.add))
	mux.HandleFunc("POST /search", s.authed(s.search))
	return mux
}

func (s *server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := r.Header.Get("X-Api-Key")
			if a := r.Header.Get("Authorization"); got == "" && a != "" {
				for _, p := range []string{"Bearer ", "Token "} {
					if strings.HasPrefix(a, p) {
						got = strings.TrimPrefix(a, p)
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
	_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]string{"reason": reason}})
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// hashName is the on-disk name for an id the caller sent: user_id and
// session_id never reach a path as they came.
var hashName = regexp.MustCompile(`^[0-9a-f]{24}$`)

func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:12])
}

// userDir is the store of one user_id; the transcripts sit where the Claude
// parser looks for them, under one project.
func (s *server) userDir(user string) (root, proj, idx string) {
	name := hash(user)
	if !hashName.MatchString(name) {
		panic("amlserve: hash is not hex")
	}
	root = filepath.Join(s.store, name)
	return root, filepath.Join(root, "claude", "-work-aml"), filepath.Join(root, "index.db")
}

// useStore points the index at one user's store. The roots are process-wide,
// which is why every request holds s.mu.
func useStore(root, idx string) {
	_ = os.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "claude"))
	_ = os.Setenv("DEJA_INDEX_DIR", idx)
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
		if strings.TrimSpace(m.Content) == "" || (m.Role != "user" && m.Role != "assistant") {
			fail(w, http.StatusUnprocessableEntity, "every message needs role user or assistant and content")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, proj, idx := s.userDir(req.UserID)
	if err := s.write(proj, req); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	useStore(root, idx)
	if err := index.Ensure(idx, "claude", false, nil); err != nil {
		fail(w, http.StatusServiceUnavailable, "index: "+err.Error())
		return
	}
	reply(w, map[string]any{"success": true, "request_id": req.RequestID, "user_id": req.UserID, "session_id": req.SessionID})
}

// write adds the chunk to its session's transcript. Each line carries the
// request_id it came in, and the file is replaced whole, so a retried
// request_id either finds every line of its chunk on disk or none of them.
func (s *server) write(proj string, req addRequest) error {
	if err := os.MkdirAll(proj, 0o755); err != nil {
		return err
	}
	sid := hash(req.SessionID)
	if !hashName.MatchString(sid) {
		return errors.New("session hash is not hex")
	}
	path := filepath.Join(proj, sid+".jsonl")
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	mark, _ := json.Marshal(req.RequestID)
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
			ts = time.UnixMilli(m.Timestamp)
		}
		var content any = m.Content
		if m.Role == "assistant" {
			content = []any{map[string]any{"type": "text", "text": m.Content}}
		}
		if err := enc.Encode(map[string]any{
			"type": m.Role, "sessionId": sid, "amlRequest": req.RequestID,
			"timestamp": ts.UTC().Format(time.RFC3339),
			"message":   map[string]any{"role": m.Role, "content": content},
		}); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *server) search(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "body is not JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Query) == "" || req.UserID == "" || req.TopK <= 0 {
		fail(w, http.StatusUnprocessableEntity, "query, user_id and a positive top_k are required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, proj, idx := s.userDir(req.UserID)
	if _, err := os.Stat(proj); err != nil {
		reply(w, map[string]any{"data": []item{}})
		return
	}
	useStore(root, idx)
	q := req.Query
	if len(req.Options) > 0 {
		q += "\n" + strings.Join(req.Options, "\n")
	}
	hits, err := rank(idx, q)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "search: "+err.Error())
		return
	}
	limit := min(req.TopK, s.maxItems)
	out := make([]item, 0, s.maxItems)
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		text, sid := s.render(proj, h.Session.ID, h.Snippets)
		if text == "" {
			continue
		}
		out = append(out, item{ID: sid, Content: text, Score: h.Score, CreatedAt: h.Session.Started.UTC().Format(time.RFC3339)})
	}
	reply(w, map[string]any{"data": out})
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
		return search.ErrorHits(result.Sessions), nil
	case search.TierRelevance:
		return search.RelevanceHits(result.Sessions, index.RelevanceTerms(q)), nil
	}
	return search.Run(result.Sessions, o)
}

// render returns the session as the answer model should read it: the whole
// transcript when it fits, otherwise the matched excerpts and then the
// transcript from the top until the budget runs out.
func (s *server) render(proj, sid string, snippets []string) (string, string) {
	if !hashName.MatchString(sid) {
		return "", ""
	}
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
		fmt.Fprintf(&b, "[%s] %s: %s\n", l.Timestamp, l.Type, text(l.Message.Content))
	}
	full := b.String()
	if len(full) <= s.maxChars {
		return full, string(orig)
	}
	var out strings.Builder
	for _, sn := range snippets {
		out.WriteString("... " + sn + " ...\n")
	}
	out.WriteString("\n")
	if rest := s.maxChars - out.Len(); rest > 0 {
		for rest > 0 && !utf8.RuneStart(full[rest]) {
			rest--
		}
		out.WriteString(full[:rest])
	}
	return out.String(), string(orig)
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
