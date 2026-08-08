package embed

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// README sizes the semantic sidecar for a reader deciding whether to turn
// embedding on. The sentence said 4 KB per 1k messages, which is the cost of
// one 1,024-dimension vector, not a thousand of them — off by three orders of
// magnitude in the direction that matters. This measures the file the stated
// model actually writes and holds the sentence to it.
func TestReadmeSidecarSizeMatchesWhatEmbedWrites(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	claim := regexp.MustCompile(`roughly ([\d.]+) (KB|MB) per 1k messages for a ([\d,]+)\s+dimension model`)
	m := claim.FindStringSubmatch(strings.ReplaceAll(string(readme), "\n", " "))
	if m == nil {
		t.Fatal("README no longer sizes the vector sidecar per 1k messages; keep the claim measurable or drop it")
	}
	size, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatal(err)
	}
	unit := 1024.0
	if m[2] == "MB" {
		unit = 1024 * 1024
	}
	claimedPerMessage := size * unit / 1000
	dim, err := strconv.Atoi(strings.ReplaceAll(m[3], ",", ""))
	if err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude", "-tmp-project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&lines, `{"type":"user","sessionId":"s","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"sidecar sizing message %d"}}`+"\n", i)
	}
	if err := os.WriteFile(filepath.Join(root, "session.jsonl"), []byte(lines.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"HOME": tmp, "USERPROFILE": tmp, "DEJA_CLAUDE_ROOT": filepath.Join(tmp, "claude"),
		"DEJA_CODEX_ROOT": filepath.Join(tmp, "codex"), "DEJA_OPENCODE_DB": filepath.Join(tmp, "open.db"),
		"DEJA_AIDER_ROOTS": filepath.Join(tmp, "aider"), "DEJA_GEMINI_ROOT": filepath.Join(tmp, "gemini"),
		"DEJA_CURSOR_ROOT": filepath.Join(tmp, "cursor"), "DEJA_CURSOR_CLI_ROOT": filepath.Join(tmp, "cursor-cli"),
		"DEJA_ANTIGRAVITY_ROOT": filepath.Join(tmp, "antigravity"), "DEJA_GROK_ROOT": filepath.Join(tmp, "grok"),
		"DEJA_QWEN_ROOT": filepath.Join(tmp, "qwen"),
	} {
		t.Setenv(key, value)
	}
	dir := filepath.Join(tmp, "index.db")
	if err := index.Ensure(dir, "", false, nil); err != nil {
		t.Fatal(err)
	}
	records, err := index.ReadRecords(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("fixture indexed nothing, the measurement below would be meaningless")
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		out := make([][]float32, len(request.Input))
		for i := range out {
			out[i] = make([]float32, dim)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	}))
	defer ts.Close()

	if _, err := EmbedIndex(dir, &Client{URL: ts.URL, Model: "test", HTTP: ts.Client()}, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	measuredPerMessage := float64(info.Size()) / float64(len(records))

	// A doc figure may round; three orders of magnitude is not rounding.
	if measuredPerMessage > 2*claimedPerMessage || measuredPerMessage < claimedPerMessage/2 {
		t.Errorf("README says %s %s per 1k messages at %d dimensions (%.0f B/message); %d records wrote %d B (%.0f B/message)",
			m[1], m[2], dim, claimedPerMessage, len(records), info.Size(), measuredPerMessage)
	}
}
