package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/usage"
)

const warmupRetryAfter = 10 * time.Minute

var spawnWarmup = startDetachedWarmup

type sessionStartHookResponse struct {
	// SystemMessage surfaces a one-line receipt in the user's UI when memory
	// actually landed; silent success builds no habit.
	SystemMessage      string `json:"systemMessage,omitempty"`
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

type precompactHookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	Trigger        string `json:"trigger"`
}

// runHookPrecompact is deliberately best effort: Claude must be able to
// compact even when the input is incomplete or the index cannot start.
func runHookPrecompact() {
	var input precompactHookInput
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	requestWarmup(index.DefaultDir())
}

// runHookContext prints session-start context. plain=false emits the Claude
// Code / Codex hook JSON envelope; plain=true prints the bare digest for
// hosts that inject raw text (the opencode plugin).
func runHookContext(plain bool) error {
	// SessionStart fires for startup, resume, clear and compact; the payload
	// says which. After a compaction the model just lost its working context,
	// so the lead line changes to say the memory below survived it.
	var input struct {
		Source string `json:"source"`
	}
	_ = json.NewDecoder(os.Stdin).Decode(&input)
	digest, sessions := hookDigestResult()
	if digest == "" {
		return nil
	}
	// One actionable line so injected memory leads somewhere: models that see
	// bare data tend to ignore it.
	lead := "The sessions below are from this project's recent history. If any is relevant to what the user asks next, call recall_context with a term from it to pull the full details before acting. If recalled history genuinely helps the task, tell the user in one short line what deja-vu recalled and how you reused it; otherwise do not mention it.\n"
	if input.Source == "compact" {
		lead = "Context was just compacted. The project memory below is from deja's index and survived the compaction; call recall_context with a term from it to restore any details you lost.\n"
	}
	digest = lead + digest
	if tip := limitHandoffTip(); tip != "" {
		digest += "\n" + tip
	}
	digest = frameRecall(digest)
	usage.RecordResult(index.DefaultDir(), usage.KindHook, len(digest), sessions, false)
	if plain {
		fmt.Fprintln(os.Stdout, digest)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "SessionStart"
	resp.HookSpecificOutput.AdditionalContext = digest
	// Announce only when the recalled set changed since the last announcement:
	// injection is recency-ranked, so repeating the same receipt every session
	// start is wallpaper, and wallpaper builds no habit.
	if sessions > 0 && receiptIsNews(digest) {
		plural := ""
		if sessions > 1 {
			plural = "s"
		}
		resp.SystemMessage = fmt.Sprintf("deja: recalled %d prior session%s from this project (~%dKB) — the agent starts already knowing them", sessions, plural, (len(digest)+1023)/1024)
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(os.Stdout, string(b))
	return nil
}

// receiptIsNews reports whether this digest differs from the one last
// announced (per index, 24h window). Best-effort: on any error, announce.
func receiptIsNews(digest string) bool {
	h := fnv.New64a()
	_, _ = h.Write([]byte(digest))
	sum := fmt.Sprintf("%x", h.Sum64())
	p := index.DefaultDir() + ".receipt"
	if b, err := os.ReadFile(p); err == nil {
		parts := strings.Fields(string(b))
		if len(parts) == 2 && parts[0] == sum {
			if ts, err := strconv.ParseInt(parts[1], 10, 64); err == nil && time.Since(time.Unix(ts, 0)) < 24*time.Hour {
				return false
			}
		}
	}
	_ = os.WriteFile(p, []byte(sum+" "+strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
	return true
}

func hookDigest() string {
	digest, _ := hookDigestResult()
	return digest
}

func hookDigestResult() (string, int) {
	defer func() { _ = recover() }()
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DEJA_RECALL")))
	if mode == search.RecallOff {
		return "", 0
	}
	dir := index.DefaultDir()
	if !index.HasManifest(dir) {
		requestWarmup(dir)
		return "", 0
	}
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return "", 0
		}
	}
	names := []string{sources.ClaudeProjectName(cwd)}
	if base := filepath.Base(cwd); base != "" {
		if two := filepath.Join(filepath.Base(filepath.Dir(cwd)), base); two != names[0] {
			names = append(names, two)
		}
		if base != names[0] {
			names = append(names, base)
		}
	}
	localOnly := os.Getenv("DEJA_AUTORECALL_LOCAL_ONLY") == "1"
	var ss []model.Session
	seen := map[string]bool{}
	lookupNames := names
	if mode == search.RecallAggressive {
		recent, err := index.Recent(dir, 12)
		if err == nil {
			lookupNames = nil
			for _, s := range recent {
				lookupNames = append(lookupNames, s.Project)
			}
		}
	}
	for _, name := range lookupNames {
		got, err := index.RecentProject(dir, name, 3)
		if err != nil {
			continue
		}
		for _, s := range got {
			if localOnly && strings.HasPrefix(s.Project, "imported:") {
				continue
			}
			k := s.Harness + ":" + s.ID
			if seen[k] {
				continue
			}
			seen[k] = true
			ss = append(ss, s)
		}
	}
	if len(ss) == 0 {
		return "", 0
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].Updated.After(ss[j].Updated) })
	if len(ss) > 12 {
		ss = ss[:12]
	}
	result := search.BuildAutoRecall(ss, search.AutoRecallOptions{Mode: mode, ProjectNames: names})
	return result.Text, result.Sessions
}

func requestWarmup(dir string) {
	if os.Getenv("DEJA_WARMUP_SENTINEL") != "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	sentinel := filepath.Join(dir, "warmup.sentinel")
	now := time.Now()
	f, err := os.OpenFile(sentinel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return
		}
		b, readErr := os.ReadFile(sentinel)
		stamp, parseErr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if readErr == nil && parseErr == nil && now.Sub(time.Unix(0, stamp)) < warmupRetryAfter {
			return
		}
		if os.Remove(sentinel) != nil {
			return
		}
		f, err = os.OpenFile(sentinel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return
		}
	}
	if _, err := fmt.Fprintln(f, now.UnixNano()); err != nil {
		_ = f.Close()
		return
	}
	if err := f.Close(); err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if err := spawnWarmup(exe, sentinel); err != nil {
		return
	}
}

func startDetachedWarmup(exe, sentinel string) error {
	cmd := exec.Command(exe, "index")
	cmd.Env = append(os.Environ(), "DEJA_WARMUP_SENTINEL="+sentinel)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdout = devNull
	cmd.Stderr = cmd.Stdout
	return startDetached(cmd)
}

// limitMarkers are the strings harnesses print when a session dies on quota.
var limitMarkers = []string{
	"usage limit reached",
	"rate limit reached",
	"You've reached your usage limit",
	"usage limit will reset",
	"quota exceeded",
	"out of free quota",
	"upgrade to continue",
	"5-hour limit",
	"weekly limit",
}

// limitHandoffTip checks whether the newest indexed session ended on a usage
// limit and, if so, suggests continuing in a different agent via handoff —
// the cross-agent escape hatch is exactly what limits are for.
func limitHandoffTip() string {
	recent, err := index.Recent(index.DefaultDir(), 1)
	if err != nil || len(recent) == 0 {
		return ""
	}
	s := recent[0]
	// Only a fresh limit matters; an old one is stale advice.
	if s.Updated.IsZero() || time.Since(s.Updated) > 6*time.Hour {
		return ""
	}
	// Recent returns metadata only; the tail scan needs the transcript.
	if full, ok, err := findByPrefix(s.ID); err == nil && ok {
		s = full
	}
	tail := s.Messages
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	for _, m := range tail {
		lower := strings.ToLower(m.Text)
		for _, marker := range limitMarkers {
			if strings.Contains(lower, strings.ToLower(marker)) {
				return fmt.Sprintf("Note: the most recent %s session appears to have hit a usage limit. If that blocks the user again, suggest `deja handoff --to <agent>` — it packages the live context and continues the work in another agent.", s.Harness)
			}
		}
	}
	return ""
}
