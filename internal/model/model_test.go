package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSessionSetSource(t *testing.T) {
	local := Session{Project: "agent-fabric"}
	local.SetSource("workstation")
	if local.Source.Origin != "local" || local.Source.Instance != "workstation" {
		t.Fatalf("local source = %#v", local.Source)
	}
	imported := Session{Project: "imported:agent-fabric"}
	imported.SetSource("must-not-leak")
	if imported.Source.Origin != "imported" || imported.Source.Instance != "" {
		t.Fatalf("imported source = %#v", imported.Source)
	}
	repoSession := Session{Project: "agent-fabric", From: "repo"}
	repoSession.SetSource("must-not-leak")
	if repoSession.Source.Origin != "repo" || repoSession.Source.Instance != "" {
		t.Fatalf("repo source = %#v", repoSession.Source)
	}
}

func TestUnsetSourceIsOmittedInsteadOfEmittedEmpty(t *testing.T) {
	b, err := json.Marshal(Session{ID: "x", Harness: "claude"})
	if err != nil || strings.Contains(string(b), `"source"`) {
		t.Fatalf("session JSON = %s, err = %v", b, err)
	}
}

func TestTouchSetsAndWidensWindow(t *testing.T) {
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

	var s Session
	s.Touch(base)
	if !s.Started.Equal(base) || !s.Updated.Equal(base) {
		t.Fatalf("first touch: started=%v updated=%v", s.Started, s.Updated)
	}

	earlier := base.Add(-time.Hour)
	s.Touch(earlier)
	if !s.Started.Equal(earlier) || !s.Updated.Equal(base) {
		t.Fatalf("earlier touch moved started only: started=%v updated=%v", s.Started, s.Updated)
	}

	later := base.Add(time.Hour)
	s.Touch(later)
	if !s.Started.Equal(earlier) || !s.Updated.Equal(later) {
		t.Fatalf("later touch moved updated only: started=%v updated=%v", s.Started, s.Updated)
	}

	// A time already inside the window changes nothing.
	s.Touch(base)
	if !s.Started.Equal(earlier) || !s.Updated.Equal(later) {
		t.Fatalf("inside-window touch changed the window: started=%v updated=%v", s.Started, s.Updated)
	}
}

func TestTouchIgnoresZero(t *testing.T) {
	s := Session{Started: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	before := s.Started
	s.Touch(time.Time{})
	if !s.Started.Equal(before) || !s.Updated.IsZero() {
		t.Fatalf("zero touch changed state: started=%v updated=%v", s.Started, s.Updated)
	}
}

func TestRolesAndMessagePredicates(t *testing.T) {
	for _, r := range []string{RoleToolOutput, RoleFiles, RoleCommand, RoleEdit, RoleSummary, RoleWrote} {
		if !IsWorkRole(r) || IsSpokenRole(r) {
			t.Fatalf("role %q: IsWorkRole=%v IsSpokenRole=%v", r, IsWorkRole(r), IsSpokenRole(r))
		}
		m := Message{Role: r}
		if !m.IsWorkRecord() || m.IsSpoken() {
			t.Fatalf("message(%q) predicates mismatch", r)
		}
	}
	for _, r := range []string{RoleUser, RoleAssistant} {
		if IsWorkRole(r) || !IsSpokenRole(r) {
			t.Fatalf("role %q: IsWorkRole=%v IsSpokenRole=%v", r, IsWorkRole(r), IsSpokenRole(r))
		}
		m := Message{Role: r}
		if m.IsWorkRecord() || !m.IsSpoken() {
			t.Fatalf("message(%q) predicates mismatch", r)
		}
	}
	if !RoleMatches(RoleToolOutput, RoleTool) || !RoleMatches(RoleUser, RoleUser) || RoleMatches(RoleUser, RoleTool) {
		t.Fatal("RoleMatches failed")
	}
}

func TestSessionProjectAndPromotedNoteMethods(t *testing.T) {
	s := Session{Harness: "claude", ID: "s1", Project: "imported:acme/api"}
	if s.Key() != "claude:s1" || !s.IsImported() || s.BareProject() != "acme/api" {
		t.Fatalf("session key/imported/bare mismatch: %q %v %q", s.Key(), s.IsImported(), s.BareProject())
	}
	if !IsRepoProject("repo") || !IsRepoProject("repo:acme") || IsRepoProject("acme") {
		t.Fatal("IsRepoProject mismatch")
	}
	if s.IsPromotedNote() || s.PromotedNoteID() != "" {
		t.Fatal("non-note session reported as promoted note")
	}
	if _, ok := s.PromotedNoteSourceKey(); ok {
		t.Fatal("non-note session returned source key")
	}
	if got := s.CandidatePromotedNoteIDs(); len(got) != 2 || got[0] != "deja-note-claude-s1" || got[1] != "deja-repo-note-claude-s1" {
		t.Fatalf("CandidatePromotedNoteIDs(no OrigID) = %v", got)
	}
	s.OrigID = "orig1"
	if got := s.CandidatePromotedNoteIDs(); len(got) != 4 || got[1] != "deja-note-claude-orig1" || got[3] != "deja-repo-note-claude-orig1" {
		t.Fatalf("CandidatePromotedNoteIDs(with OrigID) = %v", got)
	}

	note := Session{Harness: NotesHarness, ID: PromotedNoteID("claude:s1")}
	if !note.IsPromotedNote() || note.PromotedNoteID() != "deja-note-claude-s1" {
		t.Fatalf("local note mismatch: %v %q", note.IsPromotedNote(), note.PromotedNoteID())
	}
	if src, ok := note.PromotedNoteSourceKey(); !ok || src != "claude:s1" {
		t.Fatalf("local note source key = (%q, %v)", src, ok)
	}

	syncedRepo := Session{Harness: NotesHarness, ID: "imported-123", OrigID: RepoPromotedNoteID("copilot-chat:c-1")}
	if !syncedRepo.IsPromotedNote() || syncedRepo.PromotedNoteID() != "deja-repo-note-copilot-chat-c-1" {
		t.Fatalf("synced repo note mismatch: %v %q", syncedRepo.IsPromotedNote(), syncedRepo.PromotedNoteID())
	}
	if src, ok := syncedRepo.PromotedNoteSourceKey(); !ok || src != "copilot-chat:c-1" {
		t.Fatalf("synced repo note source key = (%q, %v)", src, ok)
	}
	if _, ok := PromotedNoteSourceKey("deja-note-"); ok {
		t.Fatal("empty promoted note suffix should fail")
	}
	if IsPromotedNote(NotesHarness, "deja-2026-08-09-manual", "") || !IsPromotedNote(NotesHarness, "deja-note-claude-s1", "") {
		t.Fatal("IsPromotedNote mismatch")
	}
}

func TestSessionLineageAndCompositeKeys(t *testing.T) {
	var empty Session
	if empty.IsSpawned() || empty.InParentFile() || empty.ParentKey() != "" || empty.OrigKey() != "" || empty.FormerKey() != "" {
		t.Fatal("empty session lineage methods returned non-empty")
	}
	child := Session{
		Harness:  "copilot",
		ID:       "task1",
		Kind:     KindSidechain,
		Parent:   "root1",
		OrigID:   "orig-task1",
		FormerID: "legacy-task1",
	}
	if !child.IsSpawned() || !child.InParentFile() {
		t.Fatalf("copilot sidechain: IsSpawned=%v InParentFile=%v", child.IsSpawned(), child.InParentFile())
	}
	if child.ParentKey() != "copilot:root1" || child.OrigKey() != "copilot:orig-task1" || child.FormerKey() != "copilot:legacy-task1" {
		t.Fatalf("keys: parent=%q orig=%q former=%q", child.ParentKey(), child.OrigKey(), child.FormerKey())
	}
	codexFork := Session{Harness: "codex", ID: "f1", Kind: KindSubagentFork, Parent: "p1"}
	if !codexFork.IsSpawned() || codexFork.InParentFile() {
		t.Fatalf("codex subagent_fork: IsSpawned=%v InParentFile=%v", codexFork.IsSpawned(), codexFork.InParentFile())
	}
}

// IsSpokenRole reports whether a message role represents conversational speech
// rather than a tool or file-activity work record.
func IsSpokenRole(role string) bool {
	return !IsWorkRole(role)
}

func TestModelTokensAndToolCallHelpers(t *testing.T) {
	var nilModel *Model
	if nilModel.Clone() != nil {
		t.Fatal("nil Model.Clone should return nil")
	}
	base := &Model{Provider: "anthropic", ID: "claude-opus-4-6"}
	if got := base.Merge(nil); got == nil || got == base || *got != *base {
		t.Fatalf("base.Merge(nil) = %#v", got)
	}
	if got := nilModel.Merge(&Model{ID: "gpt-5"}); got == nil || got.ID != "gpt-5" || got.Provider != "" {
		t.Fatalf("nil.Merge(id) = %#v", got)
	}
	if got := (&Model{Provider: "openai", ID: "gpt-4o"}).Merge(&Model{}); got.Provider != "openai" || got.ID != "gpt-4o" {
		t.Fatalf("Merge(empty) = %#v", got)
	}

	var nilTokens *Tokens
	if nilTokens.Clone() != nil {
		t.Fatal("nil Tokens.Clone should return nil")
	}
	t1 := &Tokens{Input: 10, Output: 5, Reasoning: 2, CacheRead: 3}
	if got := t1.Add(nil); got == nil || got == t1 || *got != *t1 {
		t.Fatalf("t1.Add(nil) = %#v", got)
	}
	if got := nilTokens.Add(t1); got == nil || got == t1 || *got != *t1 {
		t.Fatalf("nil.Add(t1) = %#v", got)
	}
	if got := t1.Add(&Tokens{Input: 1, Output: 2, Reasoning: 3, CacheRead: 4}); *got != (Tokens{Input: 11, Output: 7, Reasoning: 5, CacheRead: 7}) {
		t.Fatalf("t1.Add(t2) = %#v", got)
	}

	var s Session
	s.SetModel("", "")
	if s.Model != nil {
		t.Fatalf("empty SetModel allocated %#v", s.Model)
	}
	s.SetModel("", "openai/gpt-5")
	if s.Model == nil || s.Model.Provider != "openai" || s.Model.ID != "gpt-5" {
		t.Fatalf("slash SetModel = %#v", s.Model)
	}
	s.Model.Provider = ""
	s.SetModel("anthropic", "<synthetic>")
	if s.Model.Provider != "anthropic" || s.Model.ID != "gpt-5" {
		t.Fatalf("synthetic provider backfill = %#v", s.Model)
	}
	for _, tc := range []struct {
		id, wantProvider string
	}{
		{"claude-sonnet-4-6", "anthropic"},
		{"gpt-5.4", "openai"},
		{"o3-mini", "openai"},
		{"gemini-2.5-pro", "google"},
		{"deepseek-chat", ""},
	} {
		var m Session
		m.SetModel("", tc.id)
		if m.Model == nil || m.Model.Provider != tc.wantProvider || m.Model.ID != tc.id {
			t.Fatalf("SetModel(%q) = %#v, want provider %q", tc.id, m.Model, tc.wantProvider)
		}
	}

	s.AddTokens(0, 0, 0, 0)
	if s.Tokens != nil {
		t.Fatalf("zero AddTokens allocated %#v", s.Tokens)
	}
	s.AddTokens(100, 50, 20, 10)
	if *s.Tokens != (Tokens{Input: 100, Output: 50, Reasoning: 20, CacheRead: 10}) {
		t.Fatalf("AddTokens = %#v", s.Tokens)
	}

	s.AddToolCall("   ", true)
	if s.ToolCalls != nil || s.ToolErrors != 1 {
		t.Fatalf("blank failed AddToolCall: calls=%v errors=%d", s.ToolCalls, s.ToolErrors)
	}
	s.AddToolCall("Read", false)
	s.AddToolCall("Read", true)
	if s.ToolCalls["Read"] != 2 || s.ToolErrors != 2 {
		t.Fatalf("AddToolCall(Read): calls=%v errors=%d", s.ToolCalls, s.ToolErrors)
	}
}
