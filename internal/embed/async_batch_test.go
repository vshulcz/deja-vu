package embed

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/vault"
)

type mockBatchState struct {
	id          string
	inputFileID string
	endpoint    string
	status      string
	polls       int
	completeAt  int
	failWith    string
	terminalErr string
	outputID    string
	errorID     string
	canceled    bool
	items       []mockBatchReqLine
}

type mockBatchReqLine struct {
	CustomID string `json:"custom_id"`
	Method   string `json:"method"`
	URL      string `json:"url"`
	Body     struct {
		Model string `json:"model"`
		Input string `json:"input"`
	} `json:"body"`
}

type mockBatchServer struct {
	t           *testing.T
	srv         *httptest.Server
	mu          sync.Mutex
	seq         int
	files       map[string]string
	deleted     []string
	batches     map[string]*mockBatchState
	batchOrder  []string
	canceled    []string
	syncCalls   atomic.Int64
	uploadCalls atomic.Int64
	createCalls atomic.Int64
	postStatus  int // override status for POST /files or POST /batches
	batchPost   int // override status specifically for POST /batches
	completeAt  int // poll count on GET /batches/{id} before switching to completed
	terminalTo  string
	failAllErr  bool
	wantAuth    string
}

func newMockOpenAIBatchServer(t *testing.T) *mockBatchServer {
	t.Helper()
	m := &mockBatchServer{
		t:          t,
		files:      make(map[string]string),
		batches:    make(map[string]*mockBatchState),
		completeAt: 1,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/embeddings", func(w http.ResponseWriter, r *http.Request) {
		m.syncCalls.Add(1)
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := make([][]float32, len(req.Input))
		for i := range out {
			out[i] = []float32{0.6, 0.8}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": func() []map[string]any {
			rows := make([]map[string]any, len(out))
			for i, v := range out {
				rows[i] = map[string]any{"embedding": v}
			}
			return rows
		}()})
	})
	mux.HandleFunc("/v1/files", func(w http.ResponseWriter, r *http.Request) {
		if m.wantAuth != "" && r.Header.Get("Authorization") != m.wantAuth {
			http.Error(w, "unauthorized "+m.wantAuth, http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		m.uploadCalls.Add(1)
		if m.postStatus != 0 {
			http.Error(w, "upload unsupported", m.postStatus)
			return
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("purpose") != "batch" {
			http.Error(w, "bad purpose", http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer func() { _ = f.Close() }()
		raw, _ := io.ReadAll(f)
		m.mu.Lock()
		m.seq++
		id := fmt.Sprintf("file-in-%d", m.seq)
		m.files[id] = string(raw)
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id})
	})
	mux.HandleFunc("/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if m.wantAuth != "" && r.Header.Get("Authorization") != m.wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/v1/files/")
		if strings.HasSuffix(rest, "/content") && r.Method == http.MethodGet {
			id := strings.TrimSuffix(rest, "/content")
			m.mu.Lock()
			content, ok := m.files[id]
			m.mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, content)
			return
		}
		if r.Method == http.MethodDelete && !strings.Contains(rest, "/") {
			m.mu.Lock()
			delete(m.files, rest)
			m.deleted = append(m.deleted, rest)
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": rest, "deleted": true})
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/v1/batches", func(w http.ResponseWriter, r *http.Request) {
		if m.wantAuth != "" && r.Header.Get("Authorization") != m.wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		m.createCalls.Add(1)
		if m.batchPost != 0 {
			http.Error(w, "batches unsupported", m.batchPost)
			return
		}
		var req struct {
			InputFileID      string `json:"input_file_id"`
			Endpoint         string `json:"endpoint"`
			CompletionWindow string `json:"completion_window"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Endpoint != "/v1/embeddings" || req.CompletionWindow != "24h" {
			http.Error(w, "invalid batch endpoint or window", http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		raw, ok := m.files[req.InputFileID]
		if !ok {
			m.mu.Unlock()
			http.Error(w, "missing input file", http.StatusBadRequest)
			return
		}
		var items []mockBatchReqLine
		sc := bufio.NewScanner(strings.NewReader(raw))
		for sc.Scan() {
			var line mockBatchReqLine
			if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
				m.mu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			items = append(items, line)
		}
		m.seq++
		id := fmt.Sprintf("batch_%d", m.seq)
		st := &mockBatchState{
			id:          id,
			inputFileID: req.InputFileID,
			endpoint:    req.Endpoint,
			status:      "validating",
			completeAt:  m.completeAt,
			items:       items,
			terminalErr: m.terminalTo,
		}
		m.batches[id] = st
		m.batchOrder = append(m.batchOrder, id)
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":            id,
			"status":        st.status,
			"input_file_id": st.inputFileID,
		})
	})
	mux.HandleFunc("/v1/batches/", func(w http.ResponseWriter, r *http.Request) {
		if m.wantAuth != "" && r.Header.Get("Authorization") != m.wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/v1/batches/")
		if strings.HasSuffix(rest, "/cancel") && r.Method == http.MethodPost {
			id := strings.TrimSuffix(rest, "/cancel")
			m.mu.Lock()
			if b, ok := m.batches[id]; ok {
				b.canceled = true
				b.status = "cancelled"
				m.canceled = append(m.canceled, id)
			}
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "status": "cancelling"})
			return
		}
		if r.Method == http.MethodGet && !strings.Contains(rest, "/") {
			m.mu.Lock()
			b, ok := m.batches[rest]
			if !ok {
				m.mu.Unlock()
				http.NotFound(w, r)
				return
			}
			b.polls++
			if !b.canceled && b.polls >= b.completeAt {
				if b.terminalErr != "" {
					b.status = b.terminalErr
					m.seq++
					errID := fmt.Sprintf("file-err-%d", m.seq)
					m.files[errID] = `{"custom_id":"x","response":{"status_code":429},"error":{"code":"batch_failed","message":"quota exceeded"}}` + "\n"
					b.errorID = errID
				} else if m.failAllErr {
					b.status = "completed"
					m.seq++
					errID := fmt.Sprintf("file-err-%d", m.seq)
					m.files[errID] = `{"custom_id":"x","response":{"status_code":400,"body":{"error":{"message":"invalid model"}}}}` + "\n"
					b.errorID = errID
				} else {
					b.status = "completed"
					m.seq++
					outID := fmt.Sprintf("file-out-%d", m.seq)
					var buf strings.Builder
					for idx, item := range b.items {
						vec := []float32{float32(idx + 1), float32(len(item.Body.Input))}
						row, _ := json.Marshal(map[string]any{
							"custom_id": item.CustomID,
							"response": map[string]any{
								"status_code": 200,
								"body": map[string]any{
									"data": []map[string]any{{"embedding": vec}},
								},
							},
						})
						buf.Write(row)
						buf.WriteByte('\n')
					}
					m.files[outID] = buf.String()
					b.outputID = outID
				}
			} else if !b.canceled && b.status == "validating" {
				b.status = "in_progress"
			}
			resp := map[string]any{
				"id":             b.id,
				"status":         b.status,
				"input_file_id":  b.inputFileID,
				"output_file_id": b.outputID,
				"error_file_id":  b.errorID,
			}
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

func withFastBatchPoll(t *testing.T) {
	t.Helper()
	origMin, origMax := asyncBatchMinPoll, asyncBatchMaxPoll
	asyncBatchMinPoll, asyncBatchMaxPoll = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() {
		asyncBatchMinPoll, asyncBatchMaxPoll = origMin, origMax
	})
}

func TestAsyncBatchNonBlockingStepLifecycle(t *testing.T) {
	dir, _ := embedStore(t)
	srv := newMockOpenAIBatchServer(t)
	srv.wantAuth = "Bearer sk-batch-secret"
	srv.completeAt = 2

	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")
	t.Setenv("DEJA_EMBED_URL", srv.srv.URL+"/v1/embeddings")
	t.Setenv("DEJA_EMBED_KEY", "sk-batch-secret")
	t.Setenv("DEJA_EMBED_MODEL", "text-embedding-3-small")

	client, err := New()
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if !client.AsyncBatch() {
		t.Fatal("client.AsyncBatch() = false, want true")
	}

	// Step 1: uploads JSONL and creates batch; returns pending=true without blocking.
	_, pending, err := EmbedIndexStep(context.Background(), dir, client, nil)
	if err != nil || !pending {
		t.Fatalf("step 1 EmbedIndexStep = (pending=%v, err=%v), want (true, nil)", pending, err)
	}
	if _, statErr := os.Stat(BatchStatePath(dir)); statErr != nil {
		t.Fatalf("expected %s to exist while pending: %v", BatchStatePath(dir), statErr)
	}
	if srv.uploadCalls.Load() != 1 || srv.createCalls.Load() != 1 || srv.syncCalls.Load() != 0 {
		t.Fatalf("step 1 calls = upload:%d create:%d sync:%d", srv.uploadCalls.Load(), srv.createCalls.Load(), srv.syncCalls.Load())
	}

	// Step 2: poll sees in_progress (poll 1 < completeAt 2) -> still pending, no re-upload.
	_, pending, err = EmbedIndexStep(context.Background(), dir, client, nil)
	if err != nil || !pending {
		t.Fatalf("step 2 EmbedIndexStep = (pending=%v, err=%v), want (true, nil)", pending, err)
	}
	if srv.uploadCalls.Load() != 1 || srv.createCalls.Load() != 1 {
		t.Fatalf("step 2 re-uploaded/created batch: upload=%d create=%d", srv.uploadCalls.Load(), srv.createCalls.Load())
	}

	// Step 3: poll reaches completeAt -> downloads output, deletes remote files, writes .vectors.bin, removes .embed-batch.
	sidecar, pending, err := EmbedIndexStep(context.Background(), dir, client, nil)
	if err != nil || pending {
		t.Fatalf("step 3 EmbedIndexStep = (pending=%v, err=%v), want (false, nil)", pending, err)
	}
	if len(sidecar.Vectors) != 5 || sidecar.Covered != 5 {
		t.Fatalf("sidecar vectors=%d covered=%d, want 5/5", len(sidecar.Vectors), sidecar.Covered)
	}
	if _, statErr := os.Stat(BatchStatePath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("expected %s removed after completion, statErr=%v", BatchStatePath(dir), statErr)
	}
	readBack, err := Read(dir)
	if err != nil || len(readBack.Vectors) != 5 {
		t.Fatalf("Read(dir) = (%d vectors, %v), want 5", len(readBack.Vectors), err)
	}
	if srv.syncCalls.Load() != 0 {
		t.Fatalf("sync /v1/embeddings called %d times, want 0", srv.syncCalls.Load())
	}
	srv.mu.Lock()
	deletedCount := len(srv.deleted)
	srv.mu.Unlock()
	if deletedCount < 2 {
		t.Fatalf("expected input and output remote files deleted, got %d deletions", deletedCount)
	}
}

func TestAsyncBatchContextCancelPreservesStateAndResumes(t *testing.T) {
	withFastBatchPoll(t)
	dir, _ := embedStore(t)
	srv := newMockOpenAIBatchServer(t)
	srv.completeAt = 4

	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")
	client := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "text-embedding-3-small"}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := EmbedIndexContext(ctx, dir, client, nil); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("EmbedIndexContext interrupted = %v, want context cancel/deadline", err)
	}
	if _, err := os.Stat(BatchStatePath(dir)); err != nil {
		t.Fatalf("expected .embed-batch preserved on cancel: %v", err)
	}

	// Resuming finishes using the same uploaded batch without creating a second batch.
	srv.mu.Lock()
	srv.batches[srv.batchOrder[0]].completeAt = 1
	srv.mu.Unlock()
	done, err := EmbedIndexContext(context.Background(), dir, client, nil)
	if err != nil {
		t.Fatalf("resumed EmbedIndexContext = %v", err)
	}
	if len(done.Vectors) != 5 {
		t.Fatalf("resumed vectors = %d, want 5", len(done.Vectors))
	}
	if srv.uploadCalls.Load() != 1 || srv.createCalls.Load() != 1 || srv.syncCalls.Load() != 0 {
		t.Fatalf("resume calls = upload:%d create:%d sync:%d", srv.uploadCalls.Load(), srv.createCalls.Load(), srv.syncCalls.Load())
	}
}

func TestAsyncBatchSplitsShardsByLinesAndBytes(t *testing.T) {
	withFastBatchPoll(t)

	for _, tc := range []struct {
		name     string
		maxLines int
		maxBytes int
		minShard int
	}{
		{"by_lines", 2, 100 << 20, 3},
		{"by_bytes", 50000, 350, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origLines, origBytes := asyncBatchMaxLines, asyncBatchMaxBytes
			asyncBatchMaxLines, asyncBatchMaxBytes = tc.maxLines, tc.maxBytes
			t.Cleanup(func() {
				asyncBatchMaxLines, asyncBatchMaxBytes = origLines, origBytes
			})

			dir, _ := embedStore(t)
			srv := newMockOpenAIBatchServer(t)
			t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")
			client := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "text-embedding-3-small"}

			done, err := EmbedIndexContext(context.Background(), dir, client, nil)
			if err != nil {
				t.Fatalf("EmbedIndexContext = %v", err)
			}
			if len(done.Vectors) != 5 {
				t.Fatalf("vectors = %d, want 5", len(done.Vectors))
			}
			if int(srv.createCalls.Load()) < tc.minShard {
				t.Fatalf("batches created = %d, want >= %d", srv.createCalls.Load(), tc.minShard)
			}
		})
	}
}

func TestAsyncBatchErrorsNeverFallbackToSync(t *testing.T) {
	withFastBatchPoll(t)
	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")

	for _, code := range []int{http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented} {
		t.Run(fmt.Sprintf("http_%d_files", code), func(t *testing.T) {
			dir, _ := embedStore(t)
			srv := newMockOpenAIBatchServer(t)
			srv.postStatus = code
			client := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "m"}
			if _, _, err := EmbedIndexStep(context.Background(), dir, client, nil); err == nil {
				t.Fatal("expected error when /files unsupported")
			}
			if srv.syncCalls.Load() != 0 {
				t.Fatalf("fell back to sync /v1/embeddings (%d calls)", srv.syncCalls.Load())
			}
		})
	}

	for _, status := range []string{"failed", "expired", "cancelled"} {
		t.Run("terminal_"+status, func(t *testing.T) {
			dir, _ := embedStore(t)
			srv := newMockOpenAIBatchServer(t)
			srv.terminalTo = status
			client := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "m"}
			_, err := EmbedIndexContext(context.Background(), dir, client, nil)
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("err = %v, want terminal status %q", err, status)
			}
			if srv.syncCalls.Load() != 0 {
				t.Fatalf("fell back to sync /v1/embeddings (%d calls)", srv.syncCalls.Load())
			}
			if _, statErr := os.Stat(BatchStatePath(dir)); !os.IsNotExist(statErr) {
				t.Fatalf("expected .embed-batch cleaned up on terminal %s, err=%v", status, statErr)
			}
		})
	}

	t.Run("completed_with_only_error_file", func(t *testing.T) {
		dir, _ := embedStore(t)
		srv := newMockOpenAIBatchServer(t)
		srv.failAllErr = true
		client := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "m"}
		_, err := EmbedIndexContext(context.Background(), dir, client, nil)
		if err == nil || !strings.Contains(err.Error(), "invalid model") {
			t.Fatalf("err = %v, want error detail from error_file_id", err)
		}
		if srv.syncCalls.Load() != 0 {
			t.Fatalf("fell back to sync /v1/embeddings")
		}
	})
}

func TestAsyncBatchConfigChangeCancelsOldBatchAndStartsFresh(t *testing.T) {
	withFastBatchPoll(t)
	dir, _ := embedStore(t)
	srv := newMockOpenAIBatchServer(t)
	srv.completeAt = 5

	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")
	oldClient := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "model-v1", PassagePrefix: "old: "}
	_, pending, err := EmbedIndexStep(context.Background(), dir, oldClient, nil)
	if err != nil || !pending {
		t.Fatalf("initial step = (%v, %v)", pending, err)
	}
	firstBatch := srv.batchOrder[0]

	// Switching model/prefix cancels the queued batch and submits a fresh one.
	srv.completeAt = 1
	newClient := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "model-v2", PassagePrefix: "new: "}
	done, err := EmbedIndexContext(context.Background(), dir, newClient, nil)
	if err != nil {
		t.Fatalf("EmbedIndexContext after config change = %v", err)
	}
	if len(done.Vectors) != 5 {
		t.Fatalf("vectors = %d, want 5", len(done.Vectors))
	}
	srv.mu.Lock()
	canceled := append([]string(nil), srv.canceled...)
	batches := len(srv.batchOrder)
	srv.mu.Unlock()
	if len(canceled) != 1 || canceled[0] != firstBatch || batches != 2 {
		t.Fatalf("canceled=%v batches=%d, want [%s] and 2 batches", canceled, batches, firstBatch)
	}
}

func TestEmbedQueryRemainsSynchronousWhenAsyncBatchEnabled(t *testing.T) {
	srv := newMockOpenAIBatchServer(t)
	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")
	client := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "text-embedding-3-small"}

	hits := []search.Hit{{Session: session("a"), Score: 10}, {Session: session("b"), Score: 0}}
	sidecar := Sidecar{Vectors: []Vector{{Key: "claude:a", Values: []float32{0.6, 0.8}}, {Key: "claude:b", Values: []float32{0, 1}}}}
	out, err := Rerank(context.Background(), hits, "unique sync query", sidecar, client)
	if err != nil || len(out) != 2 {
		t.Fatalf("Rerank = (%d, %v)", len(out), err)
	}
	if srv.syncCalls.Load() != 1 || srv.uploadCalls.Load() != 0 || srv.createCalls.Load() != 0 {
		t.Fatalf("calls = sync:%d upload:%d create:%d, want 1/0/0", srv.syncCalls.Load(), srv.uploadCalls.Load(), srv.createCalls.Load())
	}
}

func TestAsyncBatchVaultSealsStateAndFailsClosedWhenLocked(t *testing.T) {
	vault.ClearCache()
	t.Cleanup(vault.ClearCache)
	origIter := vault.KDFIterations
	vault.KDFIterations = 1000
	t.Cleanup(func() { vault.KDFIterations = origIter })

	dir, _ := threeSessionStore(t)
	tmp := filepath.Dir(dir)
	t.Setenv("DEJA_VAULT_FILE", filepath.Join(tmp, "vault.json"))
	t.Setenv("DEJA_VAULT_BACKEND", "passphrase")
	t.Setenv("DEJA_VAULT_PASSPHRASE", "batch-vault-pass")
	if _, err := vault.Init(dir, "batch-vault-pass", 0); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}

	srv := newMockOpenAIBatchServer(t)
	srv.completeAt = 3
	t.Setenv("DEJA_EMBED_ASYNC_BATCH", "1")
	client := &Client{URL: srv.srv.URL + "/v1/embeddings", Model: "text-embedding-3-small"}

	_, pending, err := EmbedIndexStep(context.Background(), dir, client, nil)
	if err != nil || !pending {
		t.Fatalf("EmbedIndexStep = (%v, %v)", pending, err)
	}
	raw, err := os.ReadFile(BatchStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !vault.IsSealedBytes(raw) {
		t.Fatalf("expected %s to be vault-sealed on disk", BatchStatePath(dir))
	}
	if bytes.Contains(raw, []byte("batch_")) || bytes.Contains(raw, []byte("file-in-")) {
		t.Fatalf("sealed .embed-batch leaked plaintext batch/file IDs")
	}

	// Lock vault: reading/resuming must fail closed with vault.ErrLocked without calling remote endpoints.
	t.Setenv("DEJA_VAULT_PASSPHRASE", "")
	if err := vault.Lock(dir, "batch-vault-pass"); err != nil {
		t.Fatalf("vault.Lock: %v", err)
	}
	if _, _, err := EmbedIndexStep(context.Background(), dir, client, nil); !errors.Is(err, vault.ErrLocked) {
		t.Fatalf("locked EmbedIndexStep = %v, want vault.ErrLocked", err)
	}
}
