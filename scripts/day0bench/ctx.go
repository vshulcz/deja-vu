package main

// The point of a day-0 number is that somebody else can get it too, so the
// neighbouring column runs here rather than being quoted from a blog post.
// Pass -ctx with a path to a ctx binary and it indexes the same corpus, in the
// same sandbox home, answers the same questions, and is scored by the same
// rule: the rank of the first returned session whose source file is one of the
// question's answer sessions.
//
// ctx is asked for session-level results, which is what it returns by default
// and what deja ranks, so neither side is being scored on the other's unit.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ctxResults struct {
	Results []struct {
		Citations []struct {
			SourcePath string `json:"source_path"`
		} `json:"citations"`
	} `json:"results"`
}

var indexedSources = regexp.MustCompile(`indexed_sources:\s*([\d,]+)`)

// runCtx mirrors run() for an external binary: same corpus, same questions,
// same scoring. The control writes nothing, which is the day-0 state of a tool
// that only records forward.
func runCtx(bin string, all, qs []question, control bool) (runResult, error) {
	var out runResult
	tmp, err := os.MkdirTemp("", "day0ctx")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	// On macOS the temp dir sits under /var, a symlink to /private/var. ctx
	// resolves the history it finds to real paths, which then no longer sit
	// under an unresolved HOME, and it indexes nothing at all. Hand it the
	// resolved path so the corpus is discoverable rather than silently empty.
	home, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		return out, err
	}

	// Only the Claude shape is laid down for ctx. The harness rows above differ
	// in on-disk format, not in content — every one carries the same sessions
	// and turns — so this is the same corpus either row was scored on.
	projects := filepath.Join(home, ".claude", "projects")
	seen := map[string]bool{}
	for _, q := range all {
		for i, turns := range q.Sessions {
			id := q.SessionIDs[i]
			if seen[id] {
				continue
			}
			seen[id] = true
			out.written++
			if control {
				continue
			}
			if err := writeClaude(projects, id, parseDate(q.Dates[i]), turns); err != nil {
				return out, err
			}
		}
	}

	ctx := func(quiet bool, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		// The environment is built from nothing rather than inherited: ctx reads
		// CTX_DATA_ROOT, and a machine that has it set would answer these
		// questions out of the real index instead of the corpus under test,
		// which would look like a very good score and mean nothing.
		cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
		if quiet {
			cmd.Env = append(cmd.Env, "CTX_QUIET=1")
		}
		b, err := cmd.Output()
		return string(b), err
	}

	t0 := time.Now()
	if _, err := ctx(true, "setup"); err != nil {
		return out, fmt.Errorf("ctx setup: %w", err)
	}
	out.build = time.Since(t0)

	// CTX_QUIET silences status entirely, so this one call opts out of it.
	status, err := ctx(false, "status")
	if err != nil {
		return out, fmt.Errorf("ctx status: %w", err)
	}
	if m := indexedSources.FindStringSubmatch(status); m != nil {
		if n, err := strconv.Atoi(strings.ReplaceAll(m[1], ",", "")); err == nil {
			out.indexed = n
		}
	}

	for i, q := range qs {
		t1 := time.Now()
		raw, err := ctx(true, "search", q.Question, "--limit", strconv.Itoa(depthK), "--json")
		if err != nil {
			return out, fmt.Errorf("ctx search: %w", err)
		}
		if i == 0 {
			out.first = time.Since(t1)
		}
		var res ctxResults
		if err := json.Unmarshal([]byte(raw), &res); err != nil {
			return out, fmt.Errorf("ctx search output: %w", err)
		}
		want := map[string]bool{}
		for _, id := range q.AnswerSession {
			want[id] = true
		}
		out.n++
		for rank, r := range res.Results {
			hit := false
			for _, c := range r.Citations {
				if want[strings.TrimSuffix(filepath.Base(c.SourcePath), ".jsonl")] {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			if rank == 0 {
				out.hit1++
			}
			if rank < 5 {
				out.hit5++
			}
			out.found++
			break
		}
	}
	return out, nil
}
