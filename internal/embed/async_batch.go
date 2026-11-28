package embed

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/vault"
)

const asyncBatchStateVersion = 1

var (
	asyncBatchMaxLines = 50000
	asyncBatchMaxBytes = 100 << 20
	asyncBatchMinPoll  = 2 * time.Second
	asyncBatchMaxPoll  = 30 * time.Second
)

type asyncBatchState struct {
	Version     int               `json:"version"`
	URL         string            `json:"url"`
	StoredModel string            `json:"stored_model"`
	CreatedAt   time.Time         `json:"created_at"`
	Shards      []asyncBatchShard `json:"shards"`
}

type asyncBatchShard struct {
	BatchID      string `json:"batch_id"`
	InputFileID  string `json:"input_file_id"`
	OutputFileID string `json:"output_file_id,omitempty"`
	ErrorFileID  string `json:"error_file_id,omitempty"`
	Status       string `json:"status"`
	Done         bool   `json:"done,omitempty"`
}

type batchRequestBody struct {
	Model     string `json:"model"`
	Input     string `json:"input"`
	InputType string `json:"input_type,omitempty"`
}

type batchRequestLine struct {
	CustomID string           `json:"custom_id"`
	Method   string           `json:"method"`
	URL      string           `json:"url"`
	Body     batchRequestBody `json:"body"`
}

type batchResponse struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	InputFileID  string `json:"input_file_id"`
	OutputFileID string `json:"output_file_id"`
	ErrorFileID  string `json:"error_file_id"`
	Errors       *struct {
		Data []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"data"`
	} `json:"errors"`
}

// BatchStatePath returns the path to the resumable async batch state sidecar
// stored beside dir so index rebuilds do not drop queued jobs.
func BatchStatePath(dir string) string {
	return strings.TrimSuffix(filepath.Clean(dir), string(filepath.Separator)) + ".embed-batch"
}

// EmbedIndexContext embeds index records and polls async batches until done
// or ctx is canceled. When interrupted in async batch mode, .embed-batch stays
// on disk so the next run resumes without re-uploading.
func EmbedIndexContext(ctx context.Context, dir string, client *Client, keep func(index.Record) bool) (Sidecar, error) {
	if !client.AsyncBatch() {
		return embedIndexSync(ctx, dir, client, keep)
	}
	poll := asyncBatchMinPoll
	for {
		sidecar, pending, err := EmbedIndexStep(ctx, dir, client, keep)
		if err != nil || !pending {
			return sidecar, err
		}
		if err := sleepContext(ctx, poll); err != nil {
			return Sidecar{}, err
		}
		poll = min(poll*2, asyncBatchMaxPoll)
	}
}

// EmbedIndexStep executes one non-blocking step of indexing: submitting a new
// async batch if none is active, or checking active batches and absorbing any
// completed outputs. Returns pending=true when remote batches are still running.
func EmbedIndexStep(ctx context.Context, dir string, client *Client, keep func(index.Record) bool) (Sidecar, bool, error) {
	if !client.AsyncBatch() {
		s, err := embedIndexSync(ctx, dir, client, keep)
		return s, false, err
	}
	if err := ctx.Err(); err != nil {
		return Sidecar{}, false, err
	}
	baseURL, err := batchBaseURL(client.URL)
	if err != nil {
		return Sidecar{}, false, err
	}

	recs, gen, old, covered, err := prepareSidecar(dir, client, keep)
	if err != nil {
		return Sidecar{}, false, err
	}

	st, err := readBatchState(dir)
	if err != nil {
		return Sidecar{}, false, err
	}
	if st != nil && (st.URL != client.URL || st.StoredModel != client.storedModel()) {
		cleanupRemoteShards(ctx, client, st.URL, st.Shards, true)
		removeBatchState(dir)
		st = nil
	}

	justCreated := false
	if st == nil {
		// Copy covered so toEmbed deduplication does not mark records as already
		// covered before vectors are persisted.
		queuedSeen := make(map[uint64]bool, len(covered))
		for k, v := range covered {
			queuedSeen[k] = v
		}
		pending := toEmbed(recs, queuedSeen)
		if len(pending) == 0 {
			old.Model, old.Generation, old.Covered = client.storedModel(), gen, len(recs)
			if wErr := write(dir, old); wErr != nil {
				return Sidecar{}, false, wErr
			}
			return old, false, nil
		}
		st, err = submitBatchShards(ctx, dir, client, baseURL, pending)
		if err != nil {
			return Sidecar{}, false, err
		}
		justCreated = true
	}

	resolved := make(map[uint64][]float32)
	anyPending := false
	for i := range st.Shards {
		shard := &st.Shards[i]
		if shard.Done {
			continue
		}
		var b batchResponse
		if justCreated {
			b = batchResponse{
				ID:           shard.BatchID,
				Status:       shard.Status,
				InputFileID:  shard.InputFileID,
				OutputFileID: shard.OutputFileID,
				ErrorFileID:  shard.ErrorFileID,
			}
		} else {
			b, err = getBatch(ctx, client, baseURL, shard.BatchID)
			if err != nil {
				if isUnsupportedOrMissingBatch(err) {
					flushPartialVectors(dir, client.storedModel(), gen, &old, recs, covered, resolved)
					cleanupRemoteShards(ctx, client, st.URL, st.Shards, true)
					removeBatchState(dir)
				}
				return Sidecar{}, false, err
			}
			shard.Status = b.Status
			shard.OutputFileID = b.OutputFileID
			shard.ErrorFileID = b.ErrorFileID
		}

		switch strings.ToLower(strings.TrimSpace(b.Status)) {
		case "validating", "in_progress", "finalizing", "cancelling", "queued":
			anyPending = true
		case "completed":
			if err := absorbCompletedShard(ctx, client, baseURL, shard, resolved); err != nil {
				flushPartialVectors(dir, client.storedModel(), gen, &old, recs, covered, resolved)
				cleanupRemoteShards(ctx, client, st.URL, st.Shards, true)
				removeBatchState(dir)
				return Sidecar{}, false, err
			}
		case "failed", "expired", "cancelled":
			detail := terminalBatchError(ctx, client, baseURL, b)
			flushPartialVectors(dir, client.storedModel(), gen, &old, recs, covered, resolved)
			cleanupRemoteShards(ctx, client, st.URL, st.Shards, true)
			removeBatchState(dir)
			return Sidecar{}, false, fmt.Errorf("async embed batch %s entered terminal state %q%s", shard.BatchID, b.Status, detail)
		default:
			return Sidecar{}, false, fmt.Errorf("async embed batch %s returned unexpected status %q", shard.BatchID, b.Status)
		}
	}

	if err := mergeResolvedVectors(&old, recs, covered, resolved); err != nil {
		return Sidecar{}, false, err
	}
	if anyPending {
		if len(resolved) > 0 && len(old.Vectors) > 0 {
			old.Model, old.Generation, old.Covered = client.storedModel(), gen, len(old.Vectors)
			if wErr := write(dir, old); wErr != nil {
				return Sidecar{}, false, wErr
			}
		}
		if wErr := writeBatchState(dir, *st); wErr != nil {
			return Sidecar{}, false, wErr
		}
		return old, true, nil
	}

	coveredSet := make(map[uint64]bool, len(covered))
	for k, v := range covered {
		coveredSet[k] = v
	}
	stillMissing := len(toEmbed(recs, coveredSet))
	old.Model, old.Generation, old.Covered = client.storedModel(), gen, len(recs)-stillMissing
	if wErr := write(dir, old); wErr != nil {
		return Sidecar{}, false, wErr
	}
	removeBatchState(dir)
	return old, false, nil
}

func absorbCompletedShard(ctx context.Context, client *Client, baseURL string, shard *asyncBatchShard, resolved map[uint64][]float32) error {
	if shard.OutputFileID == "" {
		detail := ""
		if shard.ErrorFileID != "" {
			detail = readErrorFileSummary(ctx, client, baseURL, shard.ErrorFileID)
		}
		if detail == "" {
			detail = "no output_file_id returned"
		}
		return fmt.Errorf("async embed batch %s completed with no output vectors (%s)", shard.BatchID, detail)
	}
	vecs, err := fetchBatchVectors(ctx, client, baseURL, shard.OutputFileID)
	if err != nil {
		return err
	}
	for id, vec := range vecs {
		resolved[id] = vec
	}
	if shard.ErrorFileID != "" {
		// Drain errors only to keep error diagnostics clean if output was empty;
		// partial failures still delete remote artifacts below.
		if len(vecs) == 0 {
			summary := readErrorFileSummary(ctx, client, baseURL, shard.ErrorFileID)
			if summary != "" {
				return fmt.Errorf("async embed batch %s produced zero vectors (%s)", shard.BatchID, summary)
			}
		}
	}
	deleteShardFiles(ctx, client, baseURL, *shard)
	shard.Done = true
	return nil
}

func flushPartialVectors(dir, storedModel, gen string, old *Sidecar, recs []index.OffsetRecord, covered map[uint64]bool, resolved map[uint64][]float32) {
	if len(resolved) == 0 {
		return
	}
	if err := mergeResolvedVectors(old, recs, covered, resolved); err != nil {
		return
	}
	if len(old.Vectors) > 0 {
		old.Model, old.Generation, old.Covered = storedModel, gen, len(old.Vectors)
		_ = write(dir, *old)
	}
}

func mergeResolvedVectors(old *Sidecar, recs []index.OffsetRecord, covered map[uint64]bool, resolved map[uint64][]float32) error {
	if len(resolved) == 0 {
		return nil
	}
	for _, r := range recs {
		id := RecordIdent(r.Record)
		if covered[id] {
			continue
		}
		vec, ok := resolved[id]
		if !ok || len(vec) == 0 {
			continue
		}
		if old.Dim == 0 {
			old.Dim = len(vec)
		}
		if len(vec) != old.Dim {
			return fmt.Errorf("embedding dimension changed from %d to %d", old.Dim, len(vec))
		}
		covered[id] = true
		old.Vectors = append(old.Vectors, Vector{Offset: r.Offset, Ident: id, Key: r.Record.Key, Values: vec})
	}
	return nil
}

func submitBatchShards(ctx context.Context, dir string, client *Client, baseURL string, pending []index.OffsetRecord) (*asyncBatchState, error) {
	payloads, err := buildBatchShards(client, pending)
	if err != nil {
		return nil, err
	}
	st := &asyncBatchState{
		Version:     asyncBatchStateVersion,
		URL:         client.URL,
		StoredModel: client.storedModel(),
		CreatedAt:   time.Now().UTC(),
		Shards:      make([]asyncBatchShard, 0, len(payloads)),
	}
	for i, payload := range payloads {
		fileID, err := uploadBatchFile(ctx, client, baseURL, fmt.Sprintf("deja-batch-%d.jsonl", i), payload)
		if err != nil {
			cleanupRemoteShards(ctx, client, client.URL, st.Shards, true)
			removeBatchState(dir)
			return nil, err
		}
		b, err := createBatch(ctx, client, baseURL, fileID)
		if err != nil {
			_ = deleteRemoteFile(ctx, client, baseURL, fileID)
			cleanupRemoteShards(ctx, client, client.URL, st.Shards, true)
			removeBatchState(dir)
			return nil, err
		}
		status := b.Status
		if status == "" {
			status = "validating"
		}
		st.Shards = append(st.Shards, asyncBatchShard{
			BatchID:      b.ID,
			InputFileID:  fileID,
			OutputFileID: b.OutputFileID,
			ErrorFileID:  b.ErrorFileID,
			Status:       status,
		})
		if wErr := writeBatchState(dir, *st); wErr != nil {
			return nil, wErr
		}
	}
	return st, nil
}

func buildBatchShards(client *Client, pending []index.OffsetRecord) ([][]byte, error) {
	withInputType := client.sendInputType || wantsInputType(client.URL, client.Model)
	var (
		shards [][]byte
		buf    bytes.Buffer
		lines  int
	)
	for _, r := range pending {
		req := batchRequestLine{
			CustomID: fmt.Sprintf("%016x", RecordIdent(r.Record)),
			Method:   http.MethodPost,
			URL:      "/v1/embeddings",
			Body: batchRequestBody{
				Model: client.Model,
				Input: client.PassagePrefix + truncate(r.Record.Text),
			},
		}
		if withInputType {
			req.Body.InputType = "passage"
		}
		raw, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("encode batch request: %w", err)
		}
		lineLen := len(raw) + 1
		if lines > 0 && (lines >= asyncBatchMaxLines || buf.Len()+lineLen > asyncBatchMaxBytes) {
			shards = append(shards, append([]byte(nil), buf.Bytes()...))
			buf.Reset()
			lines = 0
		}
		buf.Write(raw)
		buf.WriteByte('\n')
		lines++
	}
	if lines > 0 {
		shards = append(shards, append([]byte(nil), buf.Bytes()...))
	}
	return shards, nil
}

func batchBaseURL(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("async batch requires a valid HTTP(S) embed.url (got %q)", endpoint)
	}
	p := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(p, "/embeddings"):
		p = strings.TrimSuffix(p, "/embeddings")
	case p == "":
		p = "/v1"
	}
	u.Path = p
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func readBatchState(dir string) (*asyncBatchState, error) {
	raw, err := vault.ReadSealedFile(dir, BatchStatePath(dir), vault.DomainSidecar)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var st asyncBatchState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("decode %s: %w", BatchStatePath(dir), err)
	}
	if len(st.Shards) == 0 {
		return nil, nil
	}
	return &st, nil
}

func writeBatchState(dir string, st asyncBatchState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encode batch state: %w", err)
	}
	return vault.WriteSealedFile(dir, BatchStatePath(dir), vault.DomainSidecar, raw, 0o600)
}

func removeBatchState(dir string) {
	_ = os.Remove(BatchStatePath(dir))
}

func (c *Client) batchHTTPClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 120 * time.Second}
}

func (c *Client) setBatchAuth(req *http.Request) {
	if c != nil && c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func redactKey(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "<redacted>")
}

type batchHTTPError struct {
	op     string
	status int
	detail string
}

func (e *batchHTTPError) Error() string {
	switch e.status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		msg := fmt.Sprintf("async batch %s returned HTTP %d: endpoint does not support OpenAI Batch API (set embed.async_batch = false to use synchronous /embeddings)", e.op, e.status)
		if e.detail != "" {
			return msg + ": " + e.detail
		}
		return msg
	}
	if e.detail != "" {
		return fmt.Sprintf("async batch %s returned HTTP %d: %s", e.op, e.status, e.detail)
	}
	return fmt.Sprintf("async batch %s returned HTTP %d", e.op, e.status)
}

func isUnsupportedOrMissingBatch(err error) bool {
	var he *batchHTTPError
	if !errors.As(err, &he) {
		return false
	}
	return he.status == http.StatusNotFound || he.status == http.StatusMethodNotAllowed || he.status == http.StatusNotImplemented
}

func doBatchRequest(ctx context.Context, client *Client, op, method, targetURL, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return fmt.Errorf("create async batch %s request: %w", op, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client.setBatchAuth(req)
	resp, err := client.batchHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("async batch %s request: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &batchHTTPError{
			op:     op,
			status: resp.StatusCode,
			detail: redactKey(strings.TrimSpace(string(detail)), client.apiKey),
		}
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode async batch %s response: %w", op, err)
		}
	}
	return nil
}

func uploadBatchFile(ctx context.Context, client *Client, baseURL, filename string, payload []byte) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("purpose", "batch"); err != nil {
		return "", err
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(payload); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := doBatchRequest(ctx, client, "file upload", http.MethodPost, baseURL+"/files", mw.FormDataContentType(), &body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("async batch file upload returned empty file id")
	}
	return out.ID, nil
}

func createBatch(ctx context.Context, client *Client, baseURL, inputFileID string) (batchResponse, error) {
	raw, err := json.Marshal(map[string]string{
		"input_file_id":     inputFileID,
		"endpoint":          "/v1/embeddings",
		"completion_window": "24h",
	})
	if err != nil {
		return batchResponse{}, err
	}
	var out batchResponse
	if err := doBatchRequest(ctx, client, "create", http.MethodPost, baseURL+"/batches", "application/json", bytes.NewReader(raw), &out); err != nil {
		return batchResponse{}, err
	}
	if out.ID == "" {
		return batchResponse{}, errors.New("async batch create returned empty batch id")
	}
	return out, nil
}

func getBatch(ctx context.Context, client *Client, baseURL, batchID string) (batchResponse, error) {
	var out batchResponse
	err := doBatchRequest(ctx, client, "status", http.MethodGet, baseURL+"/batches/"+url.PathEscape(batchID), "", nil, &out)
	return out, err
}

func cancelBatch(ctx context.Context, client *Client, baseURL, batchID string) error {
	if batchID == "" || baseURL == "" {
		return nil
	}
	return doBatchRequest(ctx, client, "cancel", http.MethodPost, baseURL+"/batches/"+url.PathEscape(batchID)+"/cancel", "", nil, nil)
}

func deleteRemoteFile(ctx context.Context, client *Client, baseURL, fileID string) error {
	if fileID == "" || baseURL == "" {
		return nil
	}
	return doBatchRequest(ctx, client, "delete file", http.MethodDelete, baseURL+"/files/"+url.PathEscape(fileID), "", nil, nil)
}

func deleteShardFiles(ctx context.Context, client *Client, baseURL string, shard asyncBatchShard) {
	_ = deleteRemoteFile(ctx, client, baseURL, shard.InputFileID)
	_ = deleteRemoteFile(ctx, client, baseURL, shard.OutputFileID)
	_ = deleteRemoteFile(ctx, client, baseURL, shard.ErrorFileID)
}

func cleanupRemoteShards(ctx context.Context, client *Client, endpointURL string, shards []asyncBatchShard, cancelActive bool) {
	baseURL, err := batchBaseURL(endpointURL)
	if err != nil {
		return
	}
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	for _, shard := range shards {
		if cancelActive && !shard.Done && shard.BatchID != "" {
			_ = cancelBatch(cleanCtx, client, baseURL, shard.BatchID)
		}
		deleteShardFiles(cleanCtx, client, baseURL, shard)
	}
}

func fetchBatchVectors(ctx context.Context, client *Client, baseURL, fileID string) (map[uint64][]float32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/files/"+url.PathEscape(fileID)+"/content", nil)
	if err != nil {
		return nil, fmt.Errorf("create async batch output request: %w", err)
	}
	client.setBatchAuth(req)
	resp, err := client.batchHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("async batch output download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &batchHTTPError{
			op:     "output download",
			status: resp.StatusCode,
			detail: redactKey(strings.TrimSpace(string(detail)), client.apiKey),
		}
	}

	out := make(map[uint64][]float32)
	reader := bufio.NewReader(resp.Body)
	for {
		line, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var row struct {
				CustomID string `json:"custom_id"`
				Response *struct {
					StatusCode int `json:"status_code"`
					Body       struct {
						Embeddings [][]float32 `json:"embeddings"`
						Data       []struct {
							Embedding []float32 `json:"embedding"`
						} `json:"data"`
					} `json:"body"`
				} `json:"response"`
			}
			if err := json.Unmarshal(trimmed, &row); err != nil {
				return nil, fmt.Errorf("decode batch output line: %w", err)
			}
			if row.CustomID != "" && row.Response != nil && row.Response.StatusCode >= 200 && row.Response.StatusCode < 300 {
				if id, pErr := strconv.ParseUint(row.CustomID, 16, 64); pErr == nil {
					var vec []float32
					if len(row.Response.Body.Data) > 0 {
						vec = row.Response.Body.Data[0].Embedding
					} else if len(row.Response.Body.Embeddings) > 0 {
						vec = row.Response.Body.Embeddings[0]
					}
					if len(vec) > 0 {
						out[id] = vec
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read batch output: %w", readErr)
		}
	}
	return out, nil
}

func terminalBatchError(ctx context.Context, client *Client, baseURL string, b batchResponse) string {
	if b.Errors != nil && len(b.Errors.Data) > 0 {
		msg := strings.TrimSpace(b.Errors.Data[0].Message)
		if msg != "" {
			return ": " + redactKey(msg, client.apiKey)
		}
	}
	if b.ErrorFileID != "" {
		if msg := readErrorFileSummary(ctx, client, baseURL, b.ErrorFileID); msg != "" {
			return ": " + msg
		}
	}
	return ""
}

func readErrorFileSummary(ctx context.Context, client *Client, baseURL, fileID string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/files/"+url.PathEscape(fileID)+"/content", nil)
	if err != nil {
		return ""
	}
	client.setBatchAuth(req)
	resp, err := client.batchHTTPClient().Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	line := strings.TrimSpace(strings.SplitN(string(snippet), "\n", 2)[0])
	if line == "" {
		return ""
	}
	var parsed struct {
		Response *struct {
			Body *struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"body"`
		} `json:"response"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(line), &parsed) == nil {
		if parsed.Response != nil && parsed.Response.Body != nil && parsed.Response.Body.Error != nil && parsed.Response.Body.Error.Message != "" {
			return redactKey(parsed.Response.Body.Error.Message, client.apiKey)
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return redactKey(parsed.Error.Message, client.apiKey)
		}
	}
	if len(line) > 200 {
		line = line[:200]
	}
	return redactKey(line, client.apiKey)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
