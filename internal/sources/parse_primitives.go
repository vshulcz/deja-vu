package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// int64Of reads an integer off a decoded JSON value (float64, json.Number, or string).
func int64Of(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return int64(f)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return int64(i)
		}
	}
	return 0
}

// exitCode reads the status a harness records for a command. sqlite3 -json
// and JSONL stores hand numbers back as json.Number, float64, or string
// depending on the shape. Signal deaths (-9 for SIGKILL) stay negative;
// out-of-range values clamp to 1.
func exitCode(v any) int {
	n := int64Of(v)
	if n < -255 || n > 255 {
		return 1
	}
	return int(n)
}

// anyExitCode reports an explicit command exit code when v carries one,
// keeping absent, null, and non-numeric values distinct from exit 0.
func anyExitCode(v any) (int, bool) {
	if n, ok := anyInt(v); ok {
		if n < -255 || n > 255 {
			return 1, true
		}
		return n, true
	}
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, false
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, false
		}
		if n < -255 || n > 255 {
			return 1, true
		}
		return int(n), true
	}
	return 0, false
}

// applyUsageMap adds token counters from a JSON usage or tokens map across
// OpenAI, Anthropic, Gemini, Pi, and Copilot field layouts.
func applyUsageMap(s *model.Session, m map[string]any) {
	if s == nil || len(m) == 0 {
		return
	}
	pick := func(src map[string]any, keys ...string) int64 {
		for _, k := range keys {
			if n := int64Of(src[k]); n > 0 {
				return n
			}
		}
		return 0
	}
	in := pick(m, "input_tokens", "inputTokens", "input", "prompt_tokens", "promptTokenCount", "inputTokenCount", "input_token_count", "prompt_token_count")
	out := pick(m, "output_tokens", "outputTokens", "output", "completion_tokens", "candidatesTokenCount", "outputTokenCount", "output_token_count", "candidates_token_count")
	reasoning := pick(m, "reasoning_tokens", "reasoningTokens", "reasoning_output_tokens", "reasoning", "thinking_tokens", "thinkingTokens", "thinking", "thoughts", "thoughtsTokenCount")
	if reasoning == 0 {
		if d, _ := m["output_tokens_details"].(map[string]any); d != nil {
			reasoning = pick(d, "thinking_tokens", "reasoning_tokens")
		} else if d, _ := m["completion_tokens_details"].(map[string]any); d != nil {
			reasoning = pick(d, "reasoning_tokens", "thinking_tokens")
		}
	}
	cacheRead := pick(m, "cache_read_input_tokens", "cacheReadInputTokens", "cached_input_tokens", "cache_read_tokens", "cacheReadTokens", "cached_tokens", "cachedTokens", "cacheRead", "cached", "cachedContentTokenCount")
	if cacheRead == 0 {
		if d, _ := m["input_tokens_details"].(map[string]any); d != nil {
			cacheRead = pick(d, "cached_tokens")
		} else if d, _ := m["prompt_tokens_details"].(map[string]any); d != nil {
			cacheRead = pick(d, "cached_tokens")
		}
	}
	s.AddTokens(in, out, reasoning, cacheRead)
}

// sqliteStderrMax bounds what a failing sqlite3 is quoted saying. Its first
// line names the cause; the rest is the query it was handed, which is a
// screenful and says nothing a reader does not already have.
const sqliteStderrMax = 200

// withStderr turns "exit status 1" into what the tool actually said.
func withStderr(err error, buf *bytes.Buffer) error {
	if buf == nil {
		return err
	}
	msg := strings.TrimSpace(buf.String())
	if msg == "" {
		return err
	}
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = strings.TrimSpace(msg[:i])
	}
	if len(msg) > sqliteStderrMax {
		msg = strings.TrimSpace(truncateRunes(msg, sqliteStderrMax)) + "…"
	}
	return fmt.Errorf("%s (%w)", msg, err)
}
