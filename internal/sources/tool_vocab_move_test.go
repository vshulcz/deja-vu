package sources

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A rename in the apply_patch format is "*** Update File: a" then "*** Move
// to: b", and its hunks are written to b (codex-rs/apply-patch parser.rs
// change_move). Every reader of the format filed them under a, a file that no
// longer exists, so blame of the line in b found nothing (#595).
func TestApplyPatchMoveFilesTheHunksUnderTheNewName(t *testing.T) {
	const (
		from    = "/tmp/proj/retry.go"
		to      = "/tmp/proj/backoff/retry.go"
		removed = "func retryForever(fn func() error) error {"
		added   = "func retryCapped(fn func() error, maxAttempts int) error {"
	)
	move := "*** Begin Patch\n*** Update File: " + from + "\n*** Move to: " + to + "\n@@\n-" + removed + "\n+" + added + "\n*** End Patch"
	want := []string{vocabEdit(to, removed), vocabWrote(to, added)}
	unwanted := []string{vocabEdit(from, removed), vocabWrote(from, added)}
	cases := []struct {
		name  string
		parse func(t *testing.T) []model.Session
		files bool
	}{
		{"codex", vocabCodexPatch(move), true},
		{"copilot", vocabCopilot(move), true},
		{"openclaw", vocabOpenClaw(move), true},
		{"cline-sdk", vocabClineSDK(move), true},
		{"cline-vscode", vocabClineVSCode(move), true},
		{"opencode", vocabOpencodePatch(move), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ss := c.parse(t)
			vocabCheck(t, ss, want, unwanted)
			if !c.files {
				return
			}
			got := map[string]bool{}
			for _, rec := range vocabRoles(ss, RoleFiles) {
				for _, f := range strings.Split(rec, "\n") {
					got[f] = true
				}
			}
			for _, f := range []string{from, to} {
				if !got[f] {
					t.Errorf("no files record names %s", f)
				}
			}
		})
	}
}

// Codex 0.149: apply_patch as a custom tool call, the patch its input.
func vocabCodexPatch(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		rec := func(typ string, payload any) string {
			return vocabJSON(map[string]any{"timestamp": "2026-10-01T10:00:01.000Z", "type": typ, "payload": payload})
		}
		p := vocabWrite(t, filepath.Join(t.TempDir(), "rollout-2026-10-01T10-00-00-019a0000-1111-7222-8333-444455556667.jsonl"),
			rec("session_meta", map[string]any{"id": "019a0000-1111-7222-8333-444455556667", "cwd": "/tmp/proj"}),
			rec("response_item", map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "move the retry loop"}}}),
			rec("response_item", map[string]any{"type": "custom_tool_call", "name": "apply_patch", "call_id": "c1", "input": patch}),
		)
		return vocabParse(t, ParseCodexRollout, p)
	}
}

// opencode 1.18: apply_patch {patchText}.
func vocabOpencodePatch(patch string) func(t *testing.T) []model.Session {
	return func(t *testing.T) []model.Session {
		data := fmt.Sprintf(`{"type":"tool","tool":"apply_patch","callID":"p1","state":{"status":"completed","input":%s,"output":"ok","time":{"start":1790000002000}}}`,
			vocabJSON(map[string]any{"patchText": patch}))
		db := vocabSQL(t, `create table session(id text primary key, parent_id text, directory text, title text, time_created integer, time_updated integer);
create table message(id text, session_id text, time_created integer, data text);
create table part(id text, message_id text, data text);
insert into session values('ses_1',null,'/tmp/proj','move the retry loop',1790000000000,1790000100000);
insert into message values('m0','ses_1',1790000000000,'{"role":"user","time":{"created":1790000000000}}');
insert into part values('p0','m0','{"type":"text","text":"move the retry loop"}');
insert into message values('m1','ses_1',1790000001000,'{"role":"assistant","time":{"created":1790000001000}}');
insert into part values('p1','m1',`+sqlQuote(data)+`);
`)
		return vocabParse(t, ParseOpencodeDB, db)
	}
}
