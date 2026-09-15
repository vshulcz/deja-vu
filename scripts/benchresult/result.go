// Package benchresult writes what a benchmark run measured to a file, so the
// number in the documentation has a run behind it in the repository rather than
// only a harness a reader has to execute themselves.
//
// The independent review at neoneye/agent-memory-atlas named this gap: the
// harnesses are committed and there is no result file, so the published numbers
// are re-runnable but not checkable. Everything in the record is computed by
// the run — the dataset's own digest included — so nothing here can claim a
// number the run did not produce.
package benchresult

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"time"
)

// Dataset identifies the input by content rather than by name, because the
// LongMemEval and LoCoMo files circulate in more than one shape.
type Dataset struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Row is one slice of a run: a question type for LongMemEval, a category for
// LoCoMo, or the total.
type Row struct {
	Name string  `json:"name"`
	N    int     `json:"n"`
	Hit1 float64 `json:"hit@1"`
	Hit5 float64 `json:"hit@5"`
	// Hit10 and Hit20 stay out of the JSON when a benchmark does not report
	// them rather than appearing as a zero somebody could read as a result.
	Hit10 *float64 `json:"hit@10,omitempty"`
	Hit20 *float64 `json:"hit@20,omitempty"`
	MRR   float64  `json:"mrr"`
}

// Result is the whole record. Flags carry the switches that change what the
// numbers mean — a cleaned-set run and a full-set run are different results.
type Result struct {
	Benchmark      string            `json:"benchmark"`
	Harness        string            `json:"harness"`
	RunAt          string            `json:"run_at"`
	Dataset        Dataset           `json:"dataset"`
	Flags          map[string]any    `json:"flags,omitempty"`
	Questions      int               `json:"questions"`
	WallSeconds    float64           `json:"wall_seconds"`
	MedianSearchMS float64           `json:"median_search_ms"`
	Rows           []Row             `json:"rows"`
	Total          Row               `json:"total"`
	Extra          map[string]any    `json:"extra,omitempty"`
	Notes          map[string]string `json:"notes,omitempty"`
}

// DescribeDataset digests the file the run read.
func DescribeDataset(path string) (Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return Dataset{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return Dataset{}, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return Dataset{}, err
	}
	return Dataset{Name: info.Name(), Bytes: info.Size(),
		SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// Write saves the record, pretty-printed so a diff between two runs is readable.
func Write(path string, r Result) error {
	if r.RunAt == "" {
		r.RunAt = time.Now().UTC().Format("2006-01-02")
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Pct is the percentage the harnesses print, kept here so the file and the
// table cannot round differently.
func Pct(hit, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(hit) / float64(n) * 100
}
