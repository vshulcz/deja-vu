package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Every call re-sends the whole context, so a session that runs to Claude
// Code's default compaction point pays for 500k-1M tokens on each call near
// the top. On a long-task stand (opencode, 16-turn sessions, 2026-10-04)
// compacting at about 250k instead of about 900k cut weighted cost by half
// with the same tasks solved. The doctor row below is advice only: it is shown
// when the user's own recent sessions went past compactPeakTokens and nothing
// already lowers the window.

const (
	compactPeakTokens    = 400_000
	compactSuggestWindow = 250_000
	compactLookback      = 30 * 24 * time.Hour
	// A transcript under this size cannot have carried compactPeakTokens of
	// conversation: the text alone would not fit.
	compactMinFileBytes = 1 << 20
	compactTailBytes    = 256 << 10
)

type compactPeaks struct {
	over, total int
}

// claudeCompactPeaks counts the main Claude Code sessions touched within the
// lookback, and those whose context passed compactPeakTokens on some call.
// Without a compaction the context only grows, so the last call's usage is the
// peak; with one, the compact_boundary record keeps the size it compacted at.
func claudeCompactPeaks(root string, now time.Time) compactPeaks {
	var p compactPeaks
	cutoff := now.Add(-compactLookback)
	projects, err := os.ReadDir(root)
	if err != nil {
		return p
	}
	for _, d := range projects {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		// <root>/<project>/<session>.jsonl only: a subagent's transcript sits
		// in the session's own directory and is not a main session.
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			fi, err := f.Info()
			if err != nil || fi.ModTime().Before(cutoff) {
				continue
			}
			p.total++
			if fi.Size() >= compactMinFileBytes && transcriptPassedTokens(filepath.Join(root, d.Name(), f.Name()), fi.Size(), compactPeakTokens) {
				p.over++
			}
		}
	}
	return p
}

// transcriptPassedTokens reports whether any call in a Claude Code transcript
// carried at least limit tokens of context. It reads the tail first and the
// whole file only when the tail does not settle it, stopping at the first
// compaction that did.
func transcriptPassedTokens(path string, size int64, limit int) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	off := size - compactTailBytes
	if off < 0 {
		off = 0
	}
	tail := make([]byte, size-off)
	if _, err := f.ReadAt(tail, off); err != nil && err != io.EOF {
		return false
	}
	if lastUsageContext(tail) >= limit || maxPreTokens(tail) >= limit {
		return true
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	key := []byte(`"preTokens":`)
	buf := make([]byte, 1<<20)
	carry := 0
	for {
		n, err := f.Read(buf[carry:])
		chunk := buf[:carry+n]
		if maxPreTokens(chunk) >= limit {
			return true
		}
		if err != nil {
			return false
		}
		// Keep enough of the end to finish a key and its number split
		// across reads.
		keep := len(key) + 12
		if keep > len(chunk) {
			keep = len(chunk)
		}
		carry = copy(buf, chunk[len(chunk)-keep:])
	}
}

// maxPreTokens is the largest compactMetadata.preTokens in b.
func maxPreTokens(b []byte) int {
	best := 0
	key := []byte(`"preTokens":`)
	for {
		i := bytes.Index(b, key)
		if i < 0 {
			return best
		}
		b = b[i+len(key):]
		if n, ok := leadingInt(b); ok && n > best {
			best = n
		}
	}
}

// lastUsageContext is the context size of the last API call recorded in b:
// fresh input plus what was written to and read from the prompt cache.
func lastUsageContext(b []byte) int {
	i := bytes.LastIndex(b, []byte(`"usage":{`))
	if i < 0 {
		return 0
	}
	u := b[i:]
	if j := bytes.IndexByte(u, '}'); j > 0 {
		u = u[:j]
	}
	total := 0
	for _, k := range []string{`"input_tokens":`, `"cache_creation_input_tokens":`, `"cache_read_input_tokens":`} {
		if j := bytes.Index(u, []byte(k)); j >= 0 {
			if n, ok := leadingInt(u[j+len(k):]); ok {
				total += n
			}
		}
	}
	return total
}

func leadingInt(b []byte) (int, bool) {
	j := 0
	for j < len(b) && b[j] >= '0' && b[j] <= '9' {
		j++
	}
	if j == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(string(b[:j]))
	return n, err == nil
}

// claudeCompactWindowLowered reports whether something the user or an
// administrator set already keeps compaction at or under compactPeakTokens,
// or turns it off or retunes it by percentage — a choice the hint should not
// argue with.
func claudeCompactWindowLowered(userSettings string) bool {
	env := map[string]string{}
	for _, k := range []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "CLAUDE_AUTOCOMPACT_PCT_OVERRIDE", "DISABLE_AUTO_COMPACT", "DISABLE_COMPACT"} {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		}
	}
	var windows []any
	for _, p := range []string{userSettings, claudeManagedSettings()} {
		m, _ := readSettingsJSON(p)
		if m == nil {
			continue
		}
		windows = append(windows, m["autoCompactWindow"])
		if ms, ok := m["modelSettings"].(map[string]any); ok {
			for _, v := range ms {
				if mv, ok := v.(map[string]any); ok {
					windows = append(windows, mv["autoCompactWindow"])
				}
			}
		}
		if e, ok := m["env"].(map[string]any); ok {
			for k, v := range e {
				if _, seen := env[k]; !seen {
					env[k] = fmt.Sprint(v)
				}
			}
		}
	}
	if strings.TrimSpace(env["CLAUDE_AUTOCOMPACT_PCT_OVERRIDE"]) != "" || envTruthy(env["DISABLE_AUTO_COMPACT"]) || envTruthy(env["DISABLE_COMPACT"]) {
		return true
	}
	if n, err := strconv.Atoi(strings.TrimSpace(env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"])); err == nil && n > 0 && n <= compactPeakTokens {
		return true
	}
	for _, w := range windows {
		if f, ok := w.(float64); ok && f > 0 && f <= compactPeakTokens {
			return true
		}
	}
	return false
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// doctorCompactWindow prints the hint, or nothing.
func doctorCompactWindow(w io.Writer, now time.Time) {
	settings := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	if claudeCompactWindowLowered(settings) {
		return
	}
	p := claudeCompactPeaks(sources.ClaudeRoot(), now)
	if p.over == 0 {
		return
	}
	fmt.Fprintf(w, "  %-12s %d of your %d Claude Code sessions in the last 30 days went past 400k tokens of context, and every call re-sends all of it\n", "compaction", p.over, p.total)
	fmt.Fprintf(w, "  %-12s compacting sooner keeps calls small: \"autoCompactWindow\": %d in %s, or CLAUDE_CODE_AUTO_COMPACT_WINDOW=%d\n", "", compactSuggestWindow, reportPath(settings), compactSuggestWindow)
}
