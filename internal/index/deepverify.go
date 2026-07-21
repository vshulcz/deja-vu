package index

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Deep verification proves the index against the sources instead of trusting
// its own bookkeeping. The category's worst failure class is a green health
// check over silently lost memory; this is the antidote: recount, re-parse a
// deterministic sample, and resolve a sample of postings to live records.
// It never mutates anything — it reports drift and the command that fixes it.

type DeepFinding struct {
	Kind   string `json:"kind"` // shrunk-file, orphan-file, parse-drift, dead-posting, torn-log
	Detail string `json:"detail"`
}

type DeepReport struct {
	FilesChecked    int `json:"files_checked"`
	SessionsIndexed int `json:"sessions_indexed"`
	SampledFiles    int `json:"sampled_files"`
	SampledPostings int `json:"sampled_postings"`
	// Stale lists sources that changed or appeared since the last index pass.
	// That is ordinary life between passes — `deja index` absorbs it. Only
	// Findings mean the index disagrees with what it already claimed to hold.
	Stale    []string      `json:"stale,omitempty"`
	Findings []DeepFinding `json:"findings,omitempty"`
}

func (r DeepReport) Clean() bool { return len(r.Findings) == 0 }

const (
	deepParseSample   = 5
	deepPostingSample = 32
)

// DeepVerify compares the live index with the source stores. Sampling is
// deterministic (path/token hashes), so repeated runs agree with each other.
func DeepVerify(dir string) (DeepReport, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return DeepReport{}, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return DeepReport{}, fmt.Errorf("manifest: %w", err)
	}
	report := DeepReport{SessionsIndexed: len(m.Sessions)}

	// 1. File inventory. New or grown files are ordinary staleness — sessions
	// keep growing between passes. A file that shrank, or one the manifest
	// knows but the disk lost, means the index claims memory it cannot back.
	current := currentFiles("")
	report.FilesChecked = len(current)
	inSync := map[string]FileState{}
	for p, st := range current {
		old, ok := m.Files[p]
		switch {
		case !ok:
			report.Stale = append(report.Stale, p)
		case st.Size < old.Size:
			report.Findings = append(report.Findings, DeepFinding{Kind: "shrunk-file", Detail: fmt.Sprintf("%s shrank from %d to %d bytes since indexing", p, old.Size, st.Size)})
		case old.Size != st.Size || old.MTime != st.MTime:
			report.Stale = append(report.Stale, p)
		default:
			inSync[p] = st
		}
	}
	for p := range m.Files {
		if _, ok := current[p]; !ok {
			report.Findings = append(report.Findings, DeepFinding{Kind: "orphan-file", Detail: p + " is in the manifest but no longer on disk"})
		}
	}
	sort.Strings(report.Stale)

	// 2. Parse drift: deterministically sample in-sync source files (stale
	// ones legitimately hold more than the index), full-parse them fresh, and
	// compare per-session message counts with the index. A record log that
	// cannot be walked is itself the worst finding, not an abort.
	indexedCounts, cerr := indexedMessageCounts(dir)
	if cerr != nil {
		report.Findings = append(report.Findings, DeepFinding{Kind: "torn-log", Detail: "records.bin unreadable: " + cerr.Error()})
	}
	for _, p := range samplePaths(inSync, deepParseSample) {
		if cerr != nil {
			break
		}
		kind, ok := sources.KindForPathKind(p)
		if !ok {
			continue
		}
		ss, perr := kind.Parse(p, 0)
		if perr != nil {
			report.Findings = append(report.Findings, DeepFinding{Kind: "parse-drift", Detail: p + ": " + perr.Error()})
			continue
		}
		report.SampledFiles++
		seen := msgSeen{}
		for _, s := range ss {
			key := s.Harness + ":" + s.ID
			if _, known := m.Sessions[key]; !known {
				report.Findings = append(report.Findings, DeepFinding{Kind: "parse-drift", Detail: key + " parses from " + p + " but is absent from the index"})
				continue
			}
			// Count with the same dedup ingestion applies, so duplicate
			// messages in the source do not read as lost memory.
			want := 0
			for _, msg := range s.Messages {
				if !seen.dup(key, msg.Role, msg.Time, msg.Text) {
					want++
				}
			}
			if got := indexedCounts[key]; got < want {
				report.Findings = append(report.Findings, DeepFinding{Kind: "parse-drift", Detail: fmt.Sprintf("%s: source parses %d messages, index holds %d", key, want, got)})
			}
		}
	}

	// 3. Dead postings: a sample of tokens must resolve to readable records.
	catalog, err := tokenCatalog(dir)
	if err == nil {
		f, ferr := os.Open(recordsPath(dir))
		if ferr == nil {
			defer func() { _ = f.Close() }()
			for _, tok := range sampleTokens(catalog, deepPostingSample) {
				posts, perr := postingsFor(dir, "t"+tok)
				if perr != nil || len(posts) == 0 {
					continue
				}
				report.SampledPostings++
				if _, rerr := readRecordAt(f, posts[0].Off); rerr != nil {
					report.Findings = append(report.Findings, DeepFinding{Kind: "dead-posting", Detail: fmt.Sprintf("token %q points at unreadable record offset %d", tok, posts[0].Off)})
				}
			}
		}
	}
	return report, nil
}

func recordsPath(dir string) string { return filepath.Join(dir, "records.bin") }

// indexedMessageCounts counts records per session key straight from the log.
func indexedMessageCounts(dir string) (map[string]int, error) {
	counts := map[string]int{}
	err := eachRecord(recordsPath(dir), func(r Record) {
		counts[r.Key]++
	})
	return counts, err
}

func hash64(s string) uint64 {
	h := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(h[:8])
}

func samplePaths(files map[string]FileState, n int) []string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool { return hash64(paths[i]) < hash64(paths[j]) })
	if len(paths) > n {
		paths = paths[:n]
	}
	sort.Strings(paths)
	return paths
}

func sampleTokens(catalog map[string]bool, n int) []string {
	toks := make([]string, 0, len(catalog))
	for t := range catalog {
		toks = append(toks, t)
	}
	sort.Slice(toks, func(i, j int) bool { return hash64(toks[i]) < hash64(toks[j]) })
	if len(toks) > n {
		toks = toks[:n]
	}
	return toks
}
