package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/proc"
	"github.com/vshulcz/deja-vu/internal/userconfig"
)

const hookEmbedFallbackStale = 2 * time.Minute
const hookEmbedMaxRetryBackoff = 15 * time.Minute

var (
	startEmbedWorker = startRefresher
	embedRunner      = func(dir string) error { return runEmbed(dir, nil) }
)

func embedSidecarBase(dir string) string {
	return strings.TrimSuffix(filepath.Clean(dir), string(filepath.Separator))
}

func embedRunningPath(dir string) string { return embedSidecarBase(dir) + ".embed-running" }
func embedLastPath(dir string) string    { return embedSidecarBase(dir) + ".embed-last" }
func embedRetryPath(dir string) string   { return embedSidecarBase(dir) + ".embed-retry" }

// requestPeriodicEmbed spawns a detached `deja hook-embed` from session-end
// hooks when embed.every / DEJA_EMBED_EVERY is positive, elapsed since
// <index>.embed-last, outside any <index>.embed-retry backoff window, and not
// already running.
func requestPeriodicEmbed(dir string) {
	if os.Getenv("DEJA_HOOK_EMBED") != "" {
		return
	}
	every := userconfig.EffectiveEmbedEvery()
	if every <= 0 || embed.Off() || !dirExists(dir) || !index.HasManifest(dir) {
		return
	}
	if lastAt, ok := readTimestampFile(embedLastPath(dir), true); ok && time.Since(lastAt) < every {
		return
	}
	if retryAt, ok := readTimestampFile(embedRetryPath(dir), false); ok && time.Now().Before(retryAt) {
		return
	}

	sentinel := embedRunningPath(dir)
	if !canClaimEmbedSentinel(sentinel) {
		return
	}

	stamp := time.Now().UnixNano()
	if err := atomicfile.Write(sentinel, []byte(strconv.FormatInt(stamp, 10)), 0o600); err != nil {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		_ = os.Remove(sentinel)
		return
	}
	cmd := proc.CommandContext(context.Background(), exe, "hook-embed")
	cmd.Env = setEnvPairs(os.Environ(), "DEJA_HOOK_EMBED", "1", "DEJA_INDEX_DIR", dir)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = os.Remove(sentinel)
		return
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdout = devNull
	cmd.Stderr = devNull

	pid, err := startEmbedWorker(cmd)
	if err != nil {
		_ = os.Remove(sentinel)
		return
	}
	recordWarmupOwner(sentinel, stamp, pid)
}

func runHookEmbed(dir string) {
	if dir == "" {
		return
	}
	defer func() { _ = os.Remove(embedRunningPath(dir)) }()
	if err := embedRunner(dir); err != nil {
		backoff := userconfig.EffectiveEmbedEvery()
		if backoff <= 0 || backoff > hookEmbedMaxRetryBackoff {
			backoff = hookEmbedMaxRetryBackoff
		}
		retryAt := strconv.FormatInt(time.Now().Add(backoff).UnixNano(), 10)
		_ = atomicfile.Write(embedRetryPath(dir), []byte(retryAt), 0o600)
		return
	}
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	_ = atomicfile.Write(embedLastPath(dir), []byte(now), 0o600)
	_ = os.Remove(embedRetryPath(dir))
}

func canClaimEmbedSentinel(sentinel string) bool {
	b, err := os.ReadFile(sentinel)
	if err != nil {
		return true
	}
	if claim, ok := parseWarmupClaim(b); ok {
		switch claim.state() {
		case ownerLive:
			return false
		case ownerGone:
			return true
		case ownerUnknown:
			fi, statErr := os.Stat(sentinel)
			return statErr != nil || time.Since(fi.ModTime()) >= hookEmbedFallbackStale
		}
	}
	fi, err := os.Stat(sentinel)
	return err != nil || time.Since(fi.ModTime()) >= hookEmbedFallbackStale
}

func readTimestampFile(path string, fallbackModTime bool) (time.Time, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	if ns, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && ns > 0 {
		return time.Unix(0, ns), true
	}
	if fallbackModTime {
		if fi, err := os.Stat(path); err == nil {
			return fi.ModTime(), true
		}
	}
	return time.Time{}, false
}

func setEnvPairs(base []string, kv ...string) []string {
	overrides := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		overrides[kv[i]] = kv[i+1]
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, replaced := overrides[name]; replaced {
				continue
			}
		}
		out = append(out, entry)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, kv[i]+"="+kv[i+1])
	}
	return out
}
