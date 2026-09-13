package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
)

// `deja bench ingest` measures the other half of the product. The four benches
// beside it score what comes back; none of them asks what an update costs, and
// that is how a path that rewrote and re-tokenized the whole store on every new
// conversation survived two months and twenty releases before a contributor
// measured it (#3500).
//
// Five classes, because they are the five things that happen to a store:
// nothing changed, a turn was appended to a transcript, a transcript appeared,
// a transcript changed its name, a transcript was rewritten. The first four
// must not rewrite `records.bin` —
// that is the property the cost rests on, and it is reported per class rather
// than assumed, since wall time on a shared runner says little and "the bytes
// that were there are still there" says everything.

// ingestClass is one update and what it cost.
type ingestClass struct {
	Name      string  `json:"name"`
	WallMS    float64 `json:"wall_ms"`
	RecordsMB float64 `json:"records_mb"`
	AddedKB   float64 `json:"added_kb"`
	Rewritten bool    `json:"rewritten"`
}

type ingestReport struct {
	CorpusHash string        `json:"corpus_hash"`
	Seed       int64         `json:"seed"`
	Sessions   int           `json:"sessions"`
	Classes    []ingestClass `json:"classes"`
}

func runBenchIngest(args []string) error {
	jsonOutput, seed, err := parseBenchArgs("ingest", args)
	if err != nil {
		return err
	}
	report, err := measureIngest(seed)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printIngestReport(os.Stdout, report)
	return nil
}

// ingestCopies is how many times the corpus is written. Raising it makes the
// classes easier to tell apart and the build slower, in that order.
const ingestCopies = 4

// ingestBenchStore writes the corpus out n times with distinct session ids, so
// the store is large enough for the cost of a pass to show.
func ingestBenchStore(ss []model.Session, n int) []model.Session {
	out := make([]model.Session, 0, len(ss)*n)
	for copy := 0; copy < n; copy++ {
		for _, s := range ss {
			dup := s
			dup.ID = fmt.Sprintf("%s-c%d", s.ID, copy)
			out = append(out, dup)
		}
	}
	return out
}

func measureIngest(seed int64) (ingestReport, error) {
	corpus := bench.Generate(seed)
	root, err := benchmarkTempDir()
	if err != nil {
		return ingestReport{}, err
	}
	defer func() { releaseBenchTempDir(root) }()
	claudeRoot := filepath.Join(root, "claude")
	indexDir := filepath.Join(root, "index.db")
	// The same corpus, written several times over with distinct ids. What these
	// classes cost is a function of how much is already indexed, so a store the
	// size of the recall corpus — a tenth of a megabyte — cannot tell them
	// apart: a pass that rewrites everything and a pass that appends one line
	// both finish in milliseconds. Four copies is enough for the gap to be
	// visible and still quick to build.
	store := ingestBenchStore(corpus.Sessions, ingestCopies)
	if err := writeBenchCorpus(claudeRoot, store); err != nil {
		return ingestReport{}, err
	}
	restore := isolateBenchEnv(root, claudeRoot, indexDir)
	defer restore()
	if err := index.Ensure(indexDir, "", false, io.Discard); err != nil {
		return ingestReport{}, fmt.Errorf("build the ingest benchmark index: %w", err)
	}

	// The transcript the appended and rewritten classes act on, and a project to
	// put the new one in: whichever session sorts first, so a seed produces the
	// same three files every run.
	sessions := append([]model.Session(nil), store...)
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	if len(sessions) == 0 {
		return ingestReport{}, fmt.Errorf("the ingest benchmark corpus is empty")
	}
	target := sessions[0]
	existing := filepath.Join(claudeRoot, target.Project, target.ID+".jsonl")
	// The rename class needs a transcript of its own: acting on the one the
	// append and rewrite classes use would measure them against a moving file.
	renameTarget := existing
	if len(sessions) > 1 {
		renameTarget = filepath.Join(claudeRoot, sessions[1].Project, sessions[1].ID+".jsonl")
	}

	report := ingestReport{CorpusHash: corpus.Hash, Seed: seed, Sessions: len(store)}
	for _, class := range []struct {
		name    string
		prepare func() error
	}{
		{"unchanged", func() error { return nil }},
		{"appended turn", func() error {
			return appendBenchTurn(existing, target.ID, "and the retry budget went back to five")
		}},
		{"new transcript", func() error {
			return writeBenchCorpus(claudeRoot, []model.Session{{
				Harness: "claude", ID: "bench-ingest-new", Project: target.Project,
				Messages: []model.Message{{
					Role: "user", Time: time.Now().UTC(),
					Text: "a conversation that was not on disk when the index was built",
				}},
			}})
		}},
		// Renaming is its own class: a harness that writes a resumed session
		// under a new name used to cost a second copy of the whole log (#3546),
		// and the only place that shows is the size of the store.
		{"renamed transcript", func() error {
			return os.Rename(renameTarget, renameTarget+".renamed")
		}},
		{"rewritten transcript", func() error {
			return os.WriteFile(existing, []byte(""), 0o600)
		}},
	} {
		if err := class.prepare(); err != nil {
			return ingestReport{}, err
		}
		measured, err := measureIngestPass(indexDir)
		if err != nil {
			return ingestReport{}, err
		}
		measured.Name = class.name
		report.Classes = append(report.Classes, measured)
	}
	return report, nil
}

// measureIngestPass runs one update and reports what it cost and whether the
// records already on file survived it.
func measureIngestPass(indexDir string) (ingestClass, error) {
	records := filepath.Join(indexDir, "records.bin")
	before, err := os.ReadFile(records)
	if err != nil {
		return ingestClass{}, err
	}
	// The file itself, not only its bytes: the replacement path writes the same
	// records in the same order into a new file, so the prefix can come back
	// identical from a pass that rewrote every byte of it. What separates the
	// two is whether this is still the same file.
	wasFile, err := os.Stat(records)
	if err != nil {
		return ingestClass{}, err
	}
	start := time.Now()
	if err := index.Ensure(indexDir, "", false, io.Discard); err != nil {
		return ingestClass{}, err
	}
	wall := time.Since(start)
	after, err := os.ReadFile(records)
	if err != nil {
		return ingestClass{}, err
	}
	grew := len(after) - len(before)
	if grew < 0 {
		grew = 0
	}
	isFile, err := os.Stat(records)
	if err != nil {
		return ingestClass{}, err
	}
	rewritten := !os.SameFile(wasFile, isFile) || len(after) < len(before)
	if !rewritten {
		for i := range before {
			if before[i] != after[i] {
				rewritten = true
				break
			}
		}
	}
	return ingestClass{
		WallMS:    float64(wall.Microseconds()) / 1000,
		RecordsMB: float64(len(after)) / (1 << 20),
		AddedKB:   float64(grew) / 1024,
		Rewritten: rewritten,
	}, nil
}

// appendBenchTurn adds one message to a transcript the index has already read,
// in the shape the corpus writer uses.
func appendBenchTurn(path, id, text string) error {
	line, err := benchTranscriptLine(id, model.Message{Role: "user", Text: text, Time: time.Now().UTC()})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, line); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func printIngestReport(w io.Writer, r ingestReport) {
	fmt.Fprintln(w, "deja bench ingest")
	fmt.Fprintf(w, "corpus: %d sessions, seed %d\n", r.Sessions, r.Seed)
	fmt.Fprintf(w, "%-22s %10s %12s %10s %s\n", "update", "wall", "records", "added", "rewrote the store")
	for _, c := range r.Classes {
		rewrote := "no"
		if c.Rewritten {
			rewrote = "YES"
		}
		fmt.Fprintf(w, "%-22s %9.1fms %10.1fMB %9.1fKB %s\n",
			c.name(), c.WallMS, c.RecordsMB, c.AddedKB, rewrote)
	}
	fmt.Fprintln(w, "Only a rewritten transcript should rewrite the store; the other four cost what they added.")
}

// name keeps the printed column stable when a class name is empty, which only
// happens if a caller builds the report by hand.
func (c ingestClass) name() string {
	if strings.TrimSpace(c.Name) == "" {
		return "(unnamed)"
	}
	return c.Name
}
