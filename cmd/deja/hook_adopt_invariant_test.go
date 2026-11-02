package main

import (
	"encoding/json"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestAllHookInputsAdoptCamelCaseEnvelopeUniformly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		wantSID string
	}{
		{
			name:    "grok-sessionId",
			payload: `{"sessionId":"grok-s1","transcriptPath":"/tmp/grok.jsonl","workspaceRoot":"/tmp/proj"}`,
			wantSID: "grok-s1",
		},
		{
			name:    "cursor-conversation_id",
			payload: `{"conversation_id":"cur-c1","transcriptPath":"/tmp/grok.jsonl","workspaceRoot":"/tmp/proj"}`,
			wantSID: "cur-c1",
		},
		{
			name:    "antigravity-conversationId",
			payload: `{"conversationId":"agy-a1","transcriptPath":"/tmp/grok.jsonl","workspaceRoot":"/tmp/proj"}`,
			wantSID: "agy-a1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pre precompactHookInput
			if err := json.Unmarshal([]byte(tc.payload), &pre); err != nil {
				t.Fatal(err)
			}
			pre.adopt()

			var prm promptHookInput
			if err := json.Unmarshal([]byte(tc.payload), &prm); err != nil {
				t.Fatal(err)
			}
			prm.adopt()

			var tl toolHookInput
			if err := json.Unmarshal([]byte(tc.payload), &tl); err != nil {
				t.Fatal(err)
			}
			tl.adopt()

			for label, got := range map[string]struct {
				sid, tp string
				roots   []string
			}{
				"precompact": {pre.SessionID, pre.TranscriptPath, pre.WorkspaceRoots},
				"prompt":     {prm.SessionID, prm.TranscriptPath, prm.WorkspaceRoots},
				"tool":       {tl.SessionID, tl.TranscriptPath, tl.WorkspaceRoots},
			} {
				if got.sid != tc.wantSID {
					t.Errorf("%s SessionID = %q, want %q", label, got.sid, tc.wantSID)
				}
				if got.tp != "/tmp/grok.jsonl" {
					t.Errorf("%s TranscriptPath = %q, want /tmp/grok.jsonl", label, got.tp)
				}
				if len(got.roots) != 1 || got.roots[0] != "/tmp/proj" {
					t.Errorf("%s WorkspaceRoots = %v, want [/tmp/proj]", label, got.roots)
				}
			}
		})
	}
}

func TestSessionsHoldingHandlesUTF8PunctuationYoFoldAndWorkRecords(t *testing.T) {
	hits := []search.Hit{
		{
			Session: model.Session{
				ID: "s1",
				Messages: []model.Message{
					{Role: "user", Text: "Why did “retries” stall—and «счётчика» overflow…"},
				},
			},
		},
		{
			Session: model.Session{
				ID: "s2",
				Messages: []model.Message{
					{Role: "tool-output", Text: "retries in tool output should not count as spoken"},
				},
			},
		},
	}

	if got := sessionsHolding(hits, "retries"); got != 1 {
		t.Errorf("sessionsHolding(retries) = %d, want 1 (s1 spoken with UTF-8 quotes/dash, s2 tool-output ignored)", got)
	}
	if got := sessionsHolding(hits, "счетчика"); got != 1 {
		t.Errorf("sessionsHolding(счетчика) = %d, want 1 (s1 with ё->е fold inside «guillemets»)", got)
	}
}

func TestTextCarriesTermHandlesUnicodePunctuationAndExtendedLatin(t *testing.T) {
	if !search.TextCarriesTerm("We switched —retry— and “токен”…", "retry") {
		t.Error("TextCarriesTerm failed to match 'retry' bounded by em-dashes (U+2014)")
	}
	if !search.TextCarriesTerm("Проверь «токен»—он истёк…", "токен") {
		t.Error("TextCarriesTerm failed to match 'токен' bounded by guillemets and em-dash")
	}
	if search.TextCarriesTerm("retrying uberalles", "über") {
		t.Error("TextCarriesTerm matched 'über' when not present")
	}
	if !search.TextCarriesTerm("configured überproxy for local dev", "überproxy") {
		t.Error("TextCarriesTerm failed to match extended-Latin term 'überproxy'")
	}
}
