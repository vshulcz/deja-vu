package model

import "time"

// ContextRef identifies the normalized transcript record that supplied a
// compaction-context entry. The text stays beside the claim; this reference
// lets a reader distinguish something the user said, an assistant conclusion,
// and a command the harness recorded.
type ContextRef struct {
	SessionID string    `json:"session_id"`
	Harness   string    `json:"harness"`
	Role      string    `json:"role"`
	At        time.Time `json:"at,omitzero"`
}

// ContextFact is transcript-derived text with its source record.
type ContextFact struct {
	Text       string     `json:"text"`
	Provenance ContextRef `json:"provenance"`
}

// ContextTest is a verification command recorded by a harness. Outcome is
// "passed" only for an explicit exit 0, "failed" for an explicit non-zero
// exit, and "recorded" when the transcript contains no exit status.
type ContextTest struct {
	Command    string     `json:"command"`
	Outcome    string     `json:"outcome"`
	Provenance ContextRef `json:"provenance"`
}

// ContextOpenItem is an explicitly labelled gap or conflict from the
// transcript. It deliberately does not infer open work from ordinary prose.
type ContextOpenItem struct {
	Text       string     `json:"text"`
	Provenance ContextRef `json:"provenance"`
}

// RepositoryFreshness is captured by the compaction hook. Digest receives it
// as data so transcript extraction never runs git or turns an unavailable
// repository check into an unstated clean result.
type RepositoryFreshness struct {
	Head          string    `json:"head,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	WorktreeState string    `json:"worktree_state,omitempty"`
	CheckedAt     time.Time `json:"checked_at,omitzero"`
	Error         string    `json:"error,omitempty"`
}

// CompactionContext is the bounded, transcript-derived state saved at a
// compaction boundary. Every substantive entry has normalized-record
// provenance. It contains no inference that a test passed or a task remains
// open beyond what the transcript itself explicitly records.
type CompactionContext struct {
	Objective   ContextFact         `json:"objective,omitempty"`
	Conclusions []ContextFact       `json:"conclusions,omitempty"`
	Tests       []ContextTest       `json:"tests,omitempty"`
	Gaps        []ContextOpenItem   `json:"gaps,omitempty"`
	Conflicts   []ContextOpenItem   `json:"conflicts,omitempty"`
	Sources     []ContextRef        `json:"sources,omitempty"`
	Freshness   RepositoryFreshness `json:"freshness,omitempty"`
	Truncated   bool                `json:"truncated,omitempty"`
}
