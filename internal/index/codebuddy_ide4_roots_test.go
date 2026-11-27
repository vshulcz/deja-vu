package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// DEJA_CODEBUDDY_LEGACY_ROOTS may name a history directory, under any name,
// or one workspace copied without its own index.json. A workspace index sits
// one level under a history root, the depth a conversation index has under a
// workspace root, so the walk has to tell them apart. Deleting a conversation
// must then take its turns out of the index, as a rebuild of the same files
// does, and never leave the workspace index behind as a row of its own.

const (
	cb4RootsKeepID = "1111111111111111aaaaaaaaaaaaaaaa"
	cb4RootsGoneID = "2222222222222222bbbbbbbbbbbbbbbb"
)

// cb4RootsConversation writes one conversation, its two messages and their
// words, under the workspace directory ws.
func cb4RootsConversation(t *testing.T, ws, id, word string) string {
	t.Helper()
	conv := filepath.Join(ws, id)
	index := filepath.Join(conv, "index.json")
	cb4Write(t, index, cb4ConversationIndex(t, "m1", "m2"))
	m1 := filepath.Join(conv, "messages", "m1.json")
	m2 := filepath.Join(conv, "messages", "m2.json")
	cb4Write(t, m1, cb4MessageFile(t, "user", "<user_query>\n"+word+" question\n</user_query>"))
	cb4Write(t, m2, cb4MessageFile(t, "assistant", word+" answer"))
	cb4Stamp(t, index, m1, m2)
	return index
}

// cb4RootsWorkspaceIndex lists the conversations a workspace holds.
func cb4RootsWorkspaceIndex(t *testing.T, ids ...string) string {
	t.Helper()
	type entry struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		CreatedAt string `json:"createdAt"`
	}
	idx := struct {
		Conversations []entry `json:"conversations"`
	}{}
	for _, id := range ids {
		idx.Conversations = append(idx.Conversations, entry{ID: id, Type: "craft", Name: "title " + id[:4], CreatedAt: cb4CreatedAt})
	}
	b, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCodeBuddyIDE4DeletedConversationUnderNarrowRoots(t *testing.T) {
	for _, tc := range []struct {
		name string
		// root is the configured legacy root under the temp dir; ws is the
		// workspace directory relative to it.
		root, ws string
		// wsIndex says whether the workspace keeps its own index.json.
		wsIndex bool
	}{
		{"history directory", filepath.Join("copy", "u1", "CodeBuddyIDE", "u1", "history"), cb4WorkspaceID, true},
		{"history directory by another name", filepath.Join("copy", "hist-backup"), cb4WorkspaceID, true},
		{"workspace copied without its index", filepath.Join("copy", "one-workspace"), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, dir := cb4Env(t)
			root := filepath.Join(filepath.Dir(f.legacyRoot), tc.root)
			t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", root)
			ws := filepath.Join(root, tc.ws)
			keep := cb4RootsConversation(t, ws, cb4RootsKeepID, "ostrakindle")
			gone := cb4RootsConversation(t, ws, cb4RootsGoneID, "pellucidfern")
			wsIndex := filepath.Join(ws, "index.json")
			if tc.wsIndex {
				cb4Write(t, wsIndex, cb4RootsWorkspaceIndex(t, cb4RootsKeepID, cb4RootsGoneID))
				cb4Stamp(t, wsIndex)
			}
			indexPass(t, dir)
			for q, id := range map[string]string{"ostrakindle": cb4RootsKeepID, "pellucidfern": cb4RootsGoneID} {
				if got := cb4Sessions(t, dir, q); !reflect.DeepEqual(got, []string{"codebuddy:" + id}) {
					t.Fatalf("search %q = %v, want codebuddy:%s", q, got, id)
				}
			}
			m, err := readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{keep, gone} {
				if _, ok := m.Files[p]; !ok {
					t.Errorf("%s is not in the manifest", p)
				}
			}
			if _, ok := m.Files[wsIndex]; ok {
				t.Errorf("the workspace index is held as a conversation file: %v", sortedKeys(m.Files))
			}
			if got := len(m.Files); got != 2 {
				t.Errorf("manifest holds %d files, want the two conversations: %v", got, sortedKeys(m.Files))
			}

			// The IDE deletes a conversation's directory and drops it from the
			// workspace's list.
			if rmErr := os.RemoveAll(filepath.Dir(gone)); rmErr != nil {
				t.Fatal(rmErr)
			}
			if tc.wsIndex {
				cb4Rewrite(t, wsIndex, cb4RootsWorkspaceIndex(t, cb4RootsKeepID))
			}
			indexPass(t, dir)
			if got := cb4Sessions(t, dir, "pellucidfern"); len(got) != 0 {
				t.Errorf("the deleted conversation is still searchable: %v", got)
			}
			if got := cb4KeyRows(t, dir, cb4RootsGoneID); got != 0 {
				t.Errorf("the deleted conversation still has %d rows", got)
			}
			if got := cb4Sessions(t, dir, "ostrakindle"); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4RootsKeepID}) {
				t.Errorf("the remaining conversation = %v", got)
			}
			m, err = readManifest(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := m.Files[gone]; ok {
				t.Errorf("the deleted conversation's index is still held: %+v", m.Files[gone])
			}
			if _, ok := m.Files[wsIndex]; ok {
				t.Error("the workspace index came into the manifest after the deletion")
			}
			matchesRebuild(t, dir, "codebuddy", cb4RootsKeepID)
			matchesRebuild(t, dir, "codebuddy", cb4RootsGoneID)
			sameAsRebuild(t, dir)
		})
	}
}

// The pair helper hands back the 4.x index and the transcript in their roles
// whichever order they arrive, and no pair when both or neither are 4.x, or
// the other file is not a CodeBuddy Code transcript.
func TestCodeBuddyIDE4ConversionPairRoles(t *testing.T) {
	legacy := filepath.Join("data", "u1", "CodeBuddyIDE", "u1", "history", cb4WorkspaceID, cb4SessionID, "index.json")
	other := filepath.Join("data", "u1", "CodeBuddyIDE", "u1", "history", cb4WorkspaceID, "ffff0123456789abcdef0123456789ab", "index.json")
	jsonl := filepath.Join("cli", "work-p", cb4SessionID+".jsonl")
	for _, tc := range []struct {
		name       string
		a, b       string
		wantLegacy string
		wantJSONL  string
		wantOK     bool
	}{
		{"4.x held, transcript arriving", legacy, jsonl, legacy, jsonl, true},
		{"transcript held, 4.x arriving", jsonl, legacy, legacy, jsonl, true},
		{"two 4.x indexes", legacy, other, "", "", false},
		{"two transcripts", jsonl, filepath.Join("cli", "x", cb4SessionID+".jsonl"), "", "", false},
		{"4.x and a non-JSONL file", legacy, filepath.Join("cli", "work-p", cb4SessionID+".json"), legacy, filepath.Join("cli", "work-p", cb4SessionID+".json"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, j, ok := codeBuddyIDE4ConversionPair(tc.a, tc.b, nil)
			if l != tc.wantLegacy || j != tc.wantJSONL || ok != tc.wantOK {
				t.Errorf("pair(%s, %s) = %q, %q, %v; want %q, %q, %v", tc.a, tc.b, l, j, ok, tc.wantLegacy, tc.wantJSONL, tc.wantOK)
			}
		})
	}
	// Under no root, a path two levels short of history is not a 4.x index.
	if sources.CodeBuddyIDE4IndexPath(filepath.Join("data", "history", cb4SessionID, "index.json"), nil) {
		t.Error("a workspace index named as a conversation index")
	}
	// Under a root named by DEJA_CODEBUDDY_LEGACY_ROOTS, a conversation is
	// told by its depth alone, whatever the directories are called.
	root := filepath.Join("copy", "hist-backup")
	narrow := filepath.Join(root, cb4WorkspaceID, cb4SessionID, "index.json")
	if l, j, ok := codeBuddyIDE4ConversionPair(jsonl, narrow, []string{root}); !ok || l != narrow || j != jsonl {
		t.Errorf("pair under a renamed history root = %q, %q, %v", l, j, ok)
	}
	if _, _, ok := codeBuddyIDE4ConversionPair(jsonl, narrow, nil); ok {
		t.Error("a renamed history directory paired with no root configured")
	}
}

// A deleted CodeBuddy transcript hands its conversation back to the 4.x index
// with the same id only when the converter's marker names it as that
// conversation's conversion and the index is listed and not itself kept, and
// a 4.x index that is not listed hands over to a listed copy of it.
func TestCodeBuddyIDE4ConversionWentAwayCases(t *testing.T) {
	tmp := t.TempDir()
	cli := filepath.Join(tmp, "cli")
	t.Setenv("DEJA_CODEBUDDY_ROOTS", cli)
	t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", filepath.Join("copy", "one-workspace"))
	legacy := filepath.Join("data", "u1", "CodeBuddyIDE", "u1", "history", cb4WorkspaceID, cb4SessionID, "index.json")
	twin := filepath.Join("data", "u1", "CodeBuddyIDE", "u1", "history", "fedcba9876543210fedcba9876543210", cb4SessionID, "index.json")
	narrow := filepath.Join("copy", "one-workspace", cb4SessionID, "index.json")
	// The transcripts are gone; the converter's marker is what is left of a
	// conversion, and the unmarked project never had one.
	jsonl := filepath.Join(cli, "work-p", cb4SessionID+".jsonl")
	cb4Write(t, filepath.Join(cli, "work-p", cb4SessionID, ".codebuddy-ide-4-conversion.json"),
		`{"conversationId":"`+cb4SessionID+`"}`)
	unmarked := filepath.Join(cli, "work-q", cb4SessionID+".jsonl")
	cb4Write(t, filepath.Join(cli, "work-r", cb4SessionID, ".codebuddy-ide-4-conversion.json"), `{"conversationId":"another"}`)
	misnamed := filepath.Join(cli, "work-r", cb4SessionID+".jsonl")
	outside := filepath.Join(tmp, "claude", "-work-p", cb4SessionID+".jsonl")
	listed := map[string]FileState{legacy: {Path: legacy}}
	for _, tc := range []struct {
		name  string
		p     string
		files map[string]FileState
		// went is conversionWentAway's answer, copy copyListed's.
		went, copy bool
	}{
		{"transcript gone, 4.x listed", jsonl, listed, true, false},
		{"transcript gone, 4.x listed under a workspace root", jsonl, map[string]FileState{narrow: {Path: narrow}}, true, false},
		{"transcript gone, 4.x kept", jsonl, map[string]FileState{legacy: {Path: legacy, Kept: true}}, false, false},
		{"transcript gone, no 4.x", jsonl, map[string]FileState{}, false, false},
		{"another id's transcript gone", filepath.Join(cli, "work-p", "other.jsonl"), listed, false, false},
		{"a transcript with no marker gone", unmarked, listed, false, false},
		{"a transcript whose marker names another conversation gone", misnamed, listed, false, false},
		{"another agent's transcript with the id gone", outside, listed, false, false},
		{"a 4.x copy gone, the other listed", twin, listed, false, true},
		{"a 4.x copy gone, the other kept", twin, map[string]FileState{legacy: {Path: legacy, Kept: true}}, false, false},
		{"not a transcript", filepath.Join(cli, "work-p", cb4SessionID+".json"), listed, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := listedCodeBuddyIDE4(tc.files)
			if got := l.conversionWentAway(tc.p); got != tc.went {
				t.Errorf("conversionWentAway(%s) = %v, want %v", tc.p, got, tc.went)
			}
			if got := l.copyListed(tc.p); got != tc.copy {
				t.Errorf("copyListed(%s) = %v, want %v", tc.p, got, tc.copy)
			}
		})
	}
}
