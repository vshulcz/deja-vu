package main

import (
	"encoding/json"
	"fmt"
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
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// runHookContext prints session-start context. plain=false emits the Claude
// Code / Codex hook JSON envelope; plain=true prints the bare digest for
// hosts that inject raw text (the opencode plugin).
func runHookContext(plain bool) error {
	digest, sessions := hookDigestResult()
	if digest == "" {
		return nil
	}
	// One actionable line so injected memory leads somewhere: models that see
	// bare data tend to ignore it.
	digest = "The sessions below are from this project's recent history. If any is relevant to what the user asks next, call recall_context with a term from it to pull the full details before acting.\n" + digest
	digest = frameRecall(digest)
	usage.RecordResult(index.DefaultDir(), usage.KindHook, len(digest), sessions, false)
	if plain {
		fmt.Fprintln(os.Stdout, digest)
		return nil
	}
	var resp sessionStartHookResponse
	resp.HookSpecificOutput.HookEventName = "SessionStart"
	resp.HookSpecificOutput.AdditionalContext = digest
	b, err := json.Marshal(resp)
	if err != nil {
		return nil
	}
	fmt.Fprintln(os.Stdout, string(b))
	return nil
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
