package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Some harnesses delete their own transcripts on a timer. deja keeps what it
// already indexed after the file is gone ("no longer on disk — still
// searchable"), but nobody finds that out until the session is missing from
// the harness. These lines say it a week ahead, for the harnesses where the
// date can be computed from their own setting. Everything else gets no line:
// Codex only compresses, OpenClaw archives, opencode, Qwen, Goose and Roo keep
// everything, and the closed-source ones document nothing (RETENTION notes,
// 2026-10-02).

// retentionWindow is how far ahead the warning looks.
const retentionWindow = 7 * 24 * time.Hour

type retentionDue struct {
	harness string // deja's name for it
	client  string // the harness's own name
	days    int    // its retention period
	n       int    // sessions it deletes inside the window
}

func (d retentionDue) line() string {
	one := d.n == 1
	return fmt.Sprintf("%s: %d session%s %s %s's %d-day cleanup in the next 7 days; deja keeps %s searchable",
		d.harness, d.n, pluralS(d.n), map[bool]string{true: "reaches", false: "reach"}[one], d.client, d.days,
		map[bool]string{true: "it", false: "them"}[one])
}

// retentionDueAll is every harness whose own cleanup deletes something within
// the window, in a fixed order. A harness with nothing due is left out.
func retentionDueAll(now time.Time) []retentionDue {
	var out []retentionDue
	for _, f := range []func(time.Time) (retentionDue, bool){claudeRetentionDue, geminiRetentionDue, hermesRetentionDue} {
		if d, ok := f(now); ok && d.n > 0 {
			out = append(out, d)
		}
	}
	return out
}

// printRetentionDue writes one "deja: ..." line per harness with sessions due.
func printRetentionDue(w io.Writer, now time.Time) {
	for _, d := range retentionDueAll(now) {
		fmt.Fprintln(w, "deja: "+d.line())
	}
}

// doctorRetentionDue is the same, as doctor rows.
func doctorRetentionDue(w io.Writer, now time.Time) {
	for _, d := range retentionDueAll(now) {
		fmt.Fprintln(w, "  expiring "+d.line())
	}
}

// readSettingsJSON reads a harness's JSON settings. missing is true when the
// file is not there, so the harness's default applies; m is nil when the file
// is there and cannot be read or parsed, and then nothing is known.
func readSettingsJSON(p string) (m map[string]any, missing bool) {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, true
	}
	if err != nil {
		return nil, false
	}
	m, _ = parseJSONValue(b).(map[string]any)
	return m, false
}

// claudeManagedSettings is where an administrator's settings live; they win
// over the user's. DEJA_CLAUDE_MANAGED_SETTINGS points elsewhere for a stand.
func claudeManagedSettings() string {
	if p := os.Getenv("DEJA_CLAUDE_MANAGED_SETTINGS"); p != "" {
		return p
	}
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "windows":
		return `C:\Program Files\ClaudeCode\managed-settings.json`
	default:
		return "/etc/claude-code/managed-settings.json"
	}
}

// claudeCleanupDays is cleanupPeriodDays as Claude Code applies it: managed
// settings over the user's, 30 when neither sets it. ok is false when a
// settings file is unreadable or holds a value Claude Code would reject (it
// requires at least 1), because then the period is not known.
func claudeCleanupDays(userSettings string) (days int, ok bool) {
	days = 30
	for _, p := range []string{userSettings, claudeManagedSettings()} {
		m, missing := readSettingsJSON(p)
		if missing {
			continue
		}
		if m == nil {
			return 0, false
		}
		v, set := m["cleanupPeriodDays"]
		if !set {
			continue
		}
		f, isNum := v.(float64)
		if !isNum || f < 1 || f != float64(int(f)) {
			return 0, false
		}
		days = int(f)
	}
	return days, true
}

// claudeRetentionDue counts the transcripts Claude Code's cleanup sweep
// deletes within the window. The docs do not say whether a transcript's age
// is its file's mtime or its last message; mtime is the conservative reading,
// since Claude Code appends to the file on every turn and anything else that
// touches the file only makes it look younger.
func claudeRetentionDue(now time.Time) (retentionDue, bool) {
	d := retentionDue{harness: "claude", client: "Claude Code"}
	for _, root := range sources.ClaudeRoots() {
		days, ok := claudeCleanupDays(filepath.Join(filepath.Dir(root), "settings.json"))
		if !ok {
			continue
		}
		if d.days == 0 || days < d.days {
			d.days = days
		}
		cutoff := now.Add(retentionWindow).Add(-time.Duration(days) * 24 * time.Hour)
		// <root>/<project>/<session>.jsonl: a subagent's file sits under
		// the session's own directory and goes with it, so it is not counted.
		projects, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, p := range projects {
			if !p.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(root, p.Name()))
			if err != nil {
				continue
			}
			for _, f := range files {
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
					continue
				}
				if fi, err := f.Info(); err == nil && fi.ModTime().Before(cutoff) {
					d.n++
				}
			}
		}
	}
	return d, d.days > 0
}

// geminiMaxAge parses sessionRetention.maxAge: a number and one of h, d, w.
// Anything else is a format deja does not know, and then it says nothing.
func geminiMaxAge(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 1 {
		return 0, false
	}
	switch s[len(s)-1] {
	case 'h':
		return time.Duration(n) * time.Hour, true
	case 'd':
		return time.Duration(n) * 24 * time.Hour, true
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, true
	}
	return 0, false
}

// geminiRetention is general.sessionRetention as Gemini CLI applies it: on,
// 30 days, unless the user's settings.json says otherwise.
func geminiRetention() (time.Duration, bool) {
	age := 30 * 24 * time.Hour
	m, missing := readSettingsJSON(filepath.Join(sources.GeminiHome(), "settings.json"))
	if missing {
		return age, true
	}
	if m == nil {
		return 0, false
	}
	r, set := jsonAt(m, "general", "sessionRetention").(map[string]any)
	if !set {
		return age, true
	}
	if on, isBool := r["enabled"].(bool); isBool && !on {
		return 0, false
	}
	if v, has := r["maxAge"]; has {
		s, isStr := v.(string)
		if !isStr {
			return 0, false
		}
		var ok bool
		if age, ok = geminiMaxAge(s); !ok {
			return 0, false
		}
	}
	return age, true
}

// geminiRetentionDue counts the chats Gemini CLI's startup cleanup deletes
// within the window. Gemini ages a session by its lastUpdated field, which
// the parser carries as the session's Updated time.
func geminiRetentionDue(now time.Time) (retentionDue, bool) {
	age, ok := geminiRetention()
	if !ok {
		return retentionDue{}, false
	}
	d := retentionDue{harness: "gemini", client: "Gemini CLI", days: int(age / (24 * time.Hour))}
	cutoff := now.Add(retentionWindow).Add(-age)
	for _, p := range sources.GeminiChatFiles() {
		ss, err := sources.ParseGeminiFile(p)
		if err != nil {
			continue
		}
		for _, s := range ss {
			last := s.Updated
			if last.IsZero() {
				if fi, err := os.Stat(p); err == nil {
					last = fi.ModTime()
				}
			}
			if !last.IsZero() && last.Before(cutoff) {
				d.n++
			}
		}
	}
	return d, true
}

// hermesRetention is sessions.auto_prune / sessions.retention_days from a
// Hermes config.yaml: on, 90 days, when the file does not say.
func hermesRetention(config string) (days int, ok bool) {
	days = 90
	b, err := os.ReadFile(config)
	if os.IsNotExist(err) {
		return days, true
	}
	if err != nil {
		return 0, false
	}
	text := string(b)
	if v, _, found := yamlLookup(text, "sessions", "auto_prune"); found && strings.EqualFold(v, "false") {
		return 0, false
	}
	if v, _, found := yamlLookup(text, "sessions", "retention_days"); found {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return 0, false
		}
		days = n
	}
	return days, true
}

// hermesRetentionDue counts the ended sessions Hermes's auto-prune deletes
// within the window, across every store. A profile's own config.yaml governs
// its store; the root one governs the rest.
func hermesRetentionDue(now time.Time) (retentionDue, bool) {
	d := retentionDue{harness: "hermes", client: "Hermes"}
	for _, db := range sources.HermesDBs() {
		config := filepath.Join(filepath.Dir(db), "config.yaml")
		if _, err := os.Stat(config); err != nil {
			config = filepath.Join(sources.HermesHome(), "config.yaml")
		}
		days, ok := hermesRetention(config)
		if !ok {
			continue
		}
		n, ok := sources.HermesPruneDue(db, time.Duration(days)*24*time.Hour, retentionWindow, now)
		if !ok {
			continue
		}
		if d.days == 0 || days < d.days {
			d.days = days
		}
		d.n += n
	}
	return d, d.days > 0
}
