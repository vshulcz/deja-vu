package hookengine

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// MaxExtRequestBytes caps the JSON stdin payload accepted by hook-ext.
const MaxExtRequestBytes = 8 << 20

const (
	defaultPrecompactMaxBytes    = 768 * 1024
	defaultPrecompactMaxRunes    = 4000
	defaultPrecompactMaxMessages = 60
	hermesHeldNotesLimit         = 8
)

// ExtRequest carries structured input from an external JS or Python host plugin.
type ExtRequest struct {
	SessionID    string   `json:"session_id,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	Query        string   `json:"query,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	Tool         string   `json:"tool,omitempty"`
	ToolUseID    string   `json:"tool_use_id,omitempty"`
	HookEvent    string   `json:"hook_event_name,omitempty"`
	Cwd          string   `json:"cwd,omitempty"`
	Args         string   `json:"args,omitempty"`
	Text         string   `json:"text,omitempty"`
	Content      string   `json:"content,omitempty"`
	Source       string   `json:"source,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	Project      string   `json:"project,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Offset       int      `json:"offset,omitempty"`
	Limit        int      `json:"limit,omitempty"`
	Plain        bool     `json:"plain,omitempty"`
	Once         bool     `json:"once,omitempty"`
	Silent       bool     `json:"silent,omitempty"`
	ShowsLine    bool     `json:"shows_line,omitempty"`
	Neutralize   bool     `json:"neutralize,omitempty"`
	NotesOnly    bool     `json:"notes_only,omitempty"`
	IsError      bool     `json:"is_error,omitempty"`
	ToolInput    any      `json:"tool_input,omitempty"`
	ToolResponse any      `json:"tool_response,omitempty"`
	Messages     []any    `json:"messages,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Notes        []string `json:"notes,omitempty"`
	HeldNotes    []string `json:"held_notes,omitempty"`
	Reply        string   `json:"reply,omitempty"`
	Sender       string   `json:"sender,omitempty"`
	SessionKey   string   `json:"session_key,omitempty"`
	ChatType     string   `json:"chat_type,omitempty"`
	Channel      string   `json:"channel,omitempty"`
	IsDirect     bool     `json:"is_direct,omitempty"`
}

// ExtResponse is the structured JSON envelope returned by deja hook-ext.
type ExtResponse struct {
	Ok               bool           `json:"ok"`
	Kind             string         `json:"kind,omitempty"`
	Context          string         `json:"context,omitempty"`
	Note             string         `json:"note,omitempty"`
	Output           string         `json:"output,omitempty"`
	Text             string         `json:"text,omitempty"`
	Reply            string         `json:"reply,omitempty"`
	IndicatorLabel   string         `json:"indicator_label,omitempty"`
	Receipt          string         `json:"receipt,omitempty"`
	HeldNotes        []string       `json:"held_notes,omitempty"`
	DroppedNotes     int            `json:"dropped_notes,omitempty"`
	Decision         string         `json:"decision,omitempty"`
	UpdatedInput     map[string]any `json:"updated_input,omitempty"`
	Reason           string         `json:"reason,omitempty"`
	HookEvent        string         `json:"hook_event_name,omitempty"`
	Messages         []any          `json:"messages,omitempty"`
	Dropped          int            `json:"dropped,omitempty"`
	Failed           bool           `json:"failed,omitempty"`
	ExitCode         *int           `json:"exit_code,omitempty"`
	ExitSignal       string         `json:"exit_signal,omitempty"`
	Error            string         `json:"error,omitempty"`
	AdditionalOutput string         `json:"additional_output,omitempty"`
}

// ReadExtRequest reads and decodes one JSON ExtRequest within MaxExtRequestBytes.
func ReadExtRequest(r io.Reader) (ExtRequest, error) {
	if r == nil {
		return ExtRequest{}, nil
	}
	raw, err := io.ReadAll(io.LimitReader(r, MaxExtRequestBytes+1))
	if err != nil {
		return ExtRequest{}, fmt.Errorf("read hook-ext request: %w", err)
	}
	if len(raw) > MaxExtRequestBytes {
		return ExtRequest{}, fmt.Errorf("hook-ext request exceeds %d bytes", MaxExtRequestBytes)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return ExtRequest{}, nil
	}
	var req ExtRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return ExtRequest{}, fmt.Errorf("decode hook-ext request: %w", err)
	}
	return req, nil
}

// ClampToolWindow truncates text to at most maxRunes UTF-8 runes from either
// the beginning (fromEnd == false) or the end (fromEnd == true) without
// splitting any multi-byte character.
func ClampToolWindow(text string, maxRunes int, fromEnd bool) string {
	if maxRunes <= 0 || text == "" {
		return ""
	}
	if !fromEnd {
		count := 0
		for i := range text {
			if count == maxRunes {
				return text[:i]
			}
			count++
		}
		return text
	}
	total := utf8.RuneCountInString(text)
	if total <= maxRunes {
		return text
	}
	skip := total - maxRunes
	count := 0
	for i := range text {
		if count == skip {
			return text[i:]
		}
		count++
	}
	return text
}

// NeutralizeHostTemplates breaks adjacent braces (`{{` -> `{ ` and `}}` -> `} `)
// in a single pass so template engines such as DeepSeek Harness do not evaluate
// recalled fragments while preserving single braces verbatim.
func NeutralizeHostTemplates(s string) string {
	if !strings.Contains(s, "{{") && !strings.Contains(s, "}}") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '{' && i+1 < len(s) && s[i+1] == '{' {
			b.WriteString("{ ")
			continue
		}
		if c == '}' && i+1 < len(s) && s[i+1] == '}' {
			b.WriteString("} ")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

var (
	numericRecallOpenRE  = regexp.MustCompile(`(?i)(?:&#0*60;|&#x0*3c;)(\s*/?\s*deja-recall)`)
	numericRecallCloseRE = regexp.MustCompile(`(?i)(/?\s*deja-recall[^<>\n]*)(?:&#0*62;|&#x0*3e;)`)
)

// SanitizeInnerRecallTags neutralizes raw, case-varied, named-entity, and
// numeric HTML character-reference recall frame tags inside untrusted bodies.
func SanitizeInnerRecallTags(body string) string {
	if body == "" {
		return ""
	}
	normalized := numericRecallOpenRE.ReplaceAllString(body, "&lt;$1")
	normalized = numericRecallCloseRE.ReplaceAllString(normalized, "$1&gt;")
	return NeutralizeFrameMarkers(normalized)
}

// BoundPrecompactMessages keeps the newest pre-compaction messages within
// byte, per-message rune, and message-count budgets, returning messages in
// chronological order alongside the number of dropped items.
func BoundPrecompactMessages(messages []any, maxBytes, maxContentRunes, maxMessages int) (bounded []any, dropped int) {
	if maxBytes <= 0 {
		maxBytes = defaultPrecompactMaxBytes
	}
	if maxContentRunes <= 0 {
		maxContentRunes = defaultPrecompactMaxRunes
	}
	if maxMessages <= 0 {
		maxMessages = defaultPrecompactMaxMessages
	}
	if len(messages) == 0 {
		return nil, 0
	}
	kept := make([]any, 0, min(len(messages), maxMessages))
	size := 0
	for i := len(messages) - 1; i >= 0; i-- {
		m, ok := messages[i].(map[string]any)
		if !ok || m == nil {
			continue
		}
		cp := make(map[string]any, len(m))
		for k, v := range m {
			cp[k] = v
		}
		if content, ok := cp["content"].(string); ok && utf8.RuneCountInString(content) > maxContentRunes {
			cp["content"] = ClampToolWindow(content, maxContentRunes, false)
		}
		raw, err := json.Marshal(cp)
		if err != nil {
			continue
		}
		if len(kept) >= maxMessages || size+len(raw) > maxBytes {
			break
		}
		size += len(raw)
		kept = append(kept, cp)
	}
	slices.Reverse(kept)
	return kept, len(messages) - len(kept)
}

var (
	dshKilledSignalRE = regexp.MustCompile(`\n\[killed by signal: ([^\]\n]+)\]\z`)
	dshExitCodeRE     = regexp.MustCompile(`\n\[exit code: (\d+)\]\z`)
)

// ParseDSHExitStatus splits DeepSeek Harness's trailing "[exit code: N]" or
// "[killed by signal: X]" status line off tool output, failing only when the
// final line reports a non-zero exit code or signal termination.
func ParseDSHExitStatus(text string) (body string, failed bool, exitCode *int, signal string) {
	if loc := dshKilledSignalRE.FindStringSubmatchIndex(text); loc != nil {
		return text[:loc[0]], true, nil, text[loc[2]:loc[3]]
	}
	if loc := dshExitCodeRE.FindStringSubmatchIndex(text); loc != nil {
		code, err := strconv.Atoi(text[loc[2]:loc[3]])
		if err == nil {
			return text[:loc[0]], code != 0, &code, ""
		}
		return text[:loc[0]], true, nil, ""
	}
	return text, false, nil, ""
}

var (
	dshSessionFlagRE      = regexp.MustCompile(`^--session\s+(\S+)(?:\s+--\s*([\s\S]*))?$`)
	dshMalformedSessionRE = regexp.MustCompile(`^--session(?:\s|$)`)
	dshValidSessionIDRE   = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// ParseDSHSlashInput parses `/deja` slash-command arguments into an optional
// session scope and query, returning an actionable error string on invalid syntax.
func ParseDSHSlashInput(raw string) (session, query string, scoped bool, errMsg string) {
	trimmed := strings.TrimSpace(raw)
	if m := dshSessionFlagRE.FindStringSubmatch(trimmed); m != nil {
		bare := strings.TrimPrefix(m[1], "session-")
		q := strings.TrimSpace(m[2])
		if bare != "" && !dshValidSessionIDRE.MatchString(bare) {
			return "", "", true, "That session id is not one dsh writes — /deja --session <id> -- <query> searches one conversation."
		}
		if q == "" {
			return bare, "", true, "A scoped search needs a query — /deja --session <id> -- <query>."
		}
		return bare, q, true, ""
	}
	if dshMalformedSessionRE.MatchString(trimmed) {
		return "", "", false, "A scoped search looks like /deja --session <id> -- <query>."
	}
	if trimmed == "" {
		return "", "", false, "Say what to look for: /deja <error, file, or decision>"
	}
	return "", trimmed, false, ""
}

// SplitHookReply splits a JSON hook reply into model-facing context and
// user-facing notes, treating plain non-envelope stdout as model context.
func SplitHookReply(stdout string) (context, notes string) {
	return splitHookReply(stdout, false)
}

// SplitHookReplyKeepSurrounding behaves like SplitHookReply while preserving
// any non-envelope stdout lines alongside additionalContext.
func SplitHookReplyKeepSurrounding(stdout string) (context, notes string) {
	return splitHookReply(stdout, true)
}

func splitHookReply(stdout string, keepSurrounding bool) (context, notes string) {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return "", ""
	}
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var reply map[string]any
		if err := json.Unmarshal([]byte(line), &reply); err != nil || reply == nil {
			continue
		}
		_, hasOut := reply["hookSpecificOutput"]
		_, hasNote := reply["systemMessage"]
		if !hasOut && !hasNote {
			continue
		}
		var ctxText string
		if outMap, ok := reply["hookSpecificOutput"].(map[string]any); ok {
			if s, ok := outMap["additionalContext"].(string); ok {
				ctxText = strings.TrimSpace(s)
			}
		}
		if noteStr, ok := reply["systemMessage"].(string); ok {
			notes = strings.TrimSpace(noteStr)
		}
		if keepSurrounding {
			before := strings.TrimSpace(strings.Join(lines[:i], "\n"))
			after := strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
			var parts []string
			for _, p := range []string{before, ctxText, after} {
				if p != "" {
					parts = append(parts, p)
				}
			}
			return strings.Join(parts, "\n"), notes
		}
		return ctxText, notes
	}
	return trimmed, ""
}

var openClawMarkdownEscaper = strings.NewReplacer(
	`\`, `\\`,
	"`", "\\`",
	`*`, `\*`,
	`_`, `\_`,
	`~`, `\~`,
	`|`, `\|`,
	`[`, `\[`,
	`]`, `\]`,
)

// EscapeOpenClawMarkdown escapes Markdown formatting characters in quoted
// topics so chat channels render receipt footers intact.
func EscapeOpenClawMarkdown(s string) string {
	return openClawMarkdownEscaper.Replace(s)
}

var (
	openClawReceiptRE    = regexp.MustCompile(`^deja-vu\s+—\s+([^"]+?):\s+("(?:[^"\\]|\\.)*")(?:\s+\((.+)\))?$`)
	openClawViaOnlyRE    = regexp.MustCompile(`^via:?\s`)
	openClawViaSplitRE   = regexp.MustCompile(`\s+·\s+via:?\s+`)
	openClawLegacyLabels = map[string]string{
		"you have been here":               "you've been here",
		"this was done on another machine": "done on another machine",
	}
)

// FormatOpenClawReceipt extracts and formats deja's user-facing prompt note
// into OpenClaw's escaped footer receipt line, or "" if notes holds none.
func FormatOpenClawReceipt(notes string) string {
	for line := range strings.SplitSeq(notes, "\n") {
		m := openClawReceiptRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		topic := m[2][1 : len(m[2])-1]
		var unquoted string
		if err := json.Unmarshal([]byte(m[2]), &unquoted); err == nil {
			topic = unquoted
		}
		if old, ok := openClawLegacyLabels[m[1]]; ok {
			when := ""
			if m[3] != "" && !openClawViaOnlyRE.MatchString(m[3]) {
				when = strings.TrimSpace(openClawViaSplitRE.Split(m[3], 2)[0])
			}
			lineOut := `↺ ` + old + `: "` + EscapeOpenClawMarkdown(topic) + `"`
			if when != "" {
				lineOut += " · " + EscapeOpenClawMarkdown(when)
			}
			return lineOut + " · deja"
		}
		return `↺ ` + EscapeOpenClawMarkdown(m[1]) + `: "` + EscapeOpenClawMarkdown(topic) + `" · deja`
	}
	return ""
}

var (
	hermesBrandRE    = regexp.MustCompile(`(?i)^deja(?:-vu)?(?:\s+—\s+|\s*:\s+|\s+)`)
	hermesProgressRE = regexp.MustCompile(`(?i)\bindexing your history\b`)
)

// SettleHermesNotes updates Hermes's bounded note ring buffer (dropping stale
// build-progress notices when nothing was recalled) and renders the indicator label.
func SettleHermesNotes(hasRecallText bool, notes, heldNotes []string) (active, nextHeld []string, label string) {
	lines := make([]string, 0, len(notes))
	for _, note := range notes {
		for line := range strings.SplitSeq(note, "\n") {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	if hasRecallText {
		combined := make([]string, 0, len(heldNotes)+len(lines))
		combined = append(combined, heldNotes...)
		combined = append(combined, lines...)
		active = tailStrings(combined, hermesHeldNotesLimit)
		return active, nil, FormatHermesIndicatorLabel(active)
	}
	combined := make([]string, 0, len(heldNotes)+len(lines))
	combined = append(combined, heldNotes...)
	for _, line := range lines {
		if !hermesProgressRE.MatchString(line) {
			combined = append(combined, line)
		}
	}
	return nil, tailStrings(combined, hermesHeldNotesLimit), ""
}

// FormatHermesIndicatorLabel formats settled notes into Hermes's recall indicator label.
func FormatHermesIndicatorLabel(notes []string) string {
	var cleaned []string
	for _, note := range notes {
		for line := range strings.SplitSeq(note, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if loc := hermesBrandRE.FindStringIndex(trimmed); loc != nil && loc[0] == 0 {
				trimmed = trimmed[loc[1]:]
			}
			trimmed = strings.ReplaceAll(trimmed, "via: ", "via ")
			if trimmed != "" {
				cleaned = append(cleaned, trimmed)
			}
		}
	}
	if len(cleaned) == 0 {
		return "deja"
	}
	if len(cleaned) == 1 {
		return "deja · " + cleaned[0]
	}
	return "deja · " + strings.Join(cleaned, "\n   ") + "\n  "
}

func tailStrings(xs []string, limit int) []string {
	if limit <= 0 || len(xs) == 0 {
		return nil
	}
	if len(xs) > limit {
		xs = xs[len(xs)-limit:]
	}
	out := make([]string, len(xs))
	copy(out, xs)
	return out
}

// BuildQueryArgs builds subcommand arguments and inserts "--" before query
// whenever query starts with "-", preventing dash-prefixed terms from parsing
// as CLI flags.
func BuildQueryArgs(cmd string, flags []string, query string) []string {
	out := make([]string, 0, 1+len(flags)+2)
	if cmd != "" {
		out = append(out, cmd)
	}
	out = append(out, flags...)
	if strings.HasPrefix(query, "-") {
		out = append(out, "--")
	}
	out = append(out, query)
	return out
}
