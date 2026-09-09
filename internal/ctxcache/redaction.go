package ctxcache

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/redact"
)

// RedactSnapshot returns a copy of s with agent-authored text passed through
// Deja's standard redactor. It deliberately leaves cache identity, freshness,
// source descriptors, and provenance intact: those values are operational
// metadata used to resolve and refresh a checkpoint rather than cached agent
// content.
//
// The copy is deep for mutable State fields. Callers can therefore redact a
// snapshot before persistence without changing the State instance retained by
// an upstream adapter. DEJA_NO_REDACT is honored by redact.Text.
func RedactSnapshot(s Snapshot) Snapshot {
	s.State = redactState(s.State)
	s.Invalid = append([]string(nil), s.Invalid...)
	return s
}

func redactState(state State) State {
	state.Objective = redactString("objective", state.Objective)
	state.Status = redactString("status", state.Status)
	state.Project = redactMap(state.Project)
	state.ProjectItems = redactItems(state.ProjectItems)
	state.Confirmed = redactItems(state.Confirmed)
	state.Implemented = redactItems(state.Implemented)
	state.Failing = redactItems(state.Failing)
	state.Unknown = redactItems(state.Unknown)
	state.Decisions = redactItems(state.Decisions)
	state.Tests = redactMap(state.Tests)
	state.NextActions = redactItems(state.NextActions)
	state.Session = redactItems(state.Session)
	state.Evidence = redactItems(state.Evidence)
	state.Gaps = redactGaps(state.Gaps)
	state.Conflicts = redactConflicts(state.Conflicts)
	state.Sources = append([]Source(nil), state.Sources...)
	state.Promoted = copyStrings(state.Promoted)
	return state
}

func redactItems(items []Item) []Item {
	out := append([]Item(nil), items...)
	for i := range out {
		// IDs and Source are stable references. Status and Supports are
		// agent-authored context and can contain command or tool output.
		out[i].Text = redactString("text", out[i].Text)
		out[i].Status = redactString("status", out[i].Status)
		out[i].Supports = redactStrings("supports", out[i].Supports)
	}
	return out
}

func redactGaps(gaps []Gap) []Gap {
	out := append([]Gap(nil), gaps...)
	for i := range out {
		// Severity and Source drive cache behavior, while the remaining fields
		// are descriptions supplied by an adapter or agent.
		out[i].Subject = redactString("subject", out[i].Subject)
		out[i].Reason = redactString("reason", out[i].Reason)
		out[i].RetrievalHint = redactString("retrieval_hint", out[i].RetrievalHint)
	}
	return out
}

func redactConflicts(conflicts []Conflict) []Conflict {
	out := append([]Conflict(nil), conflicts...)
	for i := range out {
		out[i].Subject = redactString("subject", out[i].Subject)
		out[i].Candidates = redactStrings("candidate", out[i].Candidates)
		out[i].Resolution = redactString("resolution", out[i].Resolution)
		out[i].Reason = redactString("reason", out[i].Reason)
	}
	return out
}

func redactStrings(key string, values []string) []string {
	out := append([]string(nil), values...)
	for i := range out {
		out[i] = redactString(key, out[i])
	}
	return out
}

func redactMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = redactValue(key, value)
	}
	return out
}

func redactValue(key string, value any) any {
	switch v := value.(type) {
	case string:
		return redactString(key, v)
	case map[string]any:
		return redactMap(v)
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = redactValue(key, v[i])
		}
		return out
	case map[string]string:
		out := make(map[string]string, len(v))
		for nestedKey, nestedValue := range v {
			out[nestedKey] = redactString(nestedKey, nestedValue)
		}
		return out
	case []string:
		return redactStrings(key, v)
	default:
		return value
	}
}

func redactString(key, value string) string {
	redacted, _ := redact.Text(value)
	if key == "" || value == "" {
		return redacted
	}

	// redact.Text correctly identifies a bare opaque value only when its
	// surrounding text names the credential. Give it the map key as that
	// context, then remove the synthetic prefix from the result. This handles
	// JSON such as {"password":"low-entropy-but-sensitive"} without treating
	// map keys themselves as user content.
	prefix := key + ": "
	withKey, _ := redact.Text(prefix + value)
	if strings.HasPrefix(withKey, prefix) {
		return strings.TrimPrefix(withKey, prefix)
	}
	return redacted
}
