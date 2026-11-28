package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

// CodeBuddy IDE 4.x kept a conversation in a directory of its own: a workspace
// index names it, a conversation index lists its messages, and each message is
// a file the IDE rewrites while the reply streams. The 5.x IDE converts one
// into a CodeBuddy Code transcript under the same id, and the transcript then
// holds every turn, including the ones that came after the conversion.

const (
	cb4WorkspaceID = "0123456789abcdef0123456789abcdef"
	cb4SessionID   = "abcdef0123456789abcdef0123456789"
	cb4CreatedAt   = "2026-01-02T03:04:05Z"
	cb4StartedMs   = 1767323045000
)

// cb4Fixture is one synthetic 4.x store and its converted transcript store.
type cb4Fixture struct {
	legacyRoot, cliRoot           string
	workspaceDir, conversationDir string
	wsIndex, convIndex            string
}

// A 4.x store is read as the codebuddy harness: the turns are searchable and
// the conversation's index and files are held in the manifest.
func TestCodeBuddyIDE4StoreIsIndexedAndSearchable(t *testing.T) {
	f, dir := cb4Env(t)
	cb4Seed(t, f)
	indexPass(t, dir)
	for _, q := range []string{"quasithorn", "quokkamint", "vellumglow"} {
		if got := cb4Sessions(t, dir, q); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4SessionID}) {
			t.Errorf("search %q = %v, want the 4.x conversation", q, got)
		}
	}
	s := cb4Session(t, dir)
	if got := cb4Text(s); !strings.Contains(got, "quasithorn question") || !strings.Contains(got, "quokkamint answer") {
		t.Errorf("the conversation holds:\n%s", got)
	}
	if got := cb4KeyRows(t, dir, cb4SessionID); got != 1 {
		t.Errorf("the 4.x conversation has %d rows, want one", got)
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files[f.convIndex]; !ok {
		t.Errorf("the conversation index is not in the manifest: %v", sortedKeys(m.Files))
	}
}

// A streamed reply rewrites the last message file in place without touching
// index.json. The sidecar state must make the pass re-read the conversation,
// and it must hold what a rebuild of the same files holds.
func TestCodeBuddyIDE4IncrementalUpdatesMatchARebuild(t *testing.T) {
	f, dir := cb4Env(t)
	cb4Seed(t, f)
	indexPass(t, dir)
	if got := cb4KeyRows(t, dir, cb4SessionID); got != 1 {
		t.Fatalf("the first pass holds %d rows, so this measures nothing", got)
	}

	step := func(name, want, notWant, title string, update func()) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			update()
			indexPass(t, dir)
			s := cb4Session(t, dir)
			text := cb4Text(s)
			if want != "" && !strings.Contains(text, want) {
				t.Errorf("the pass did not read %q:\n%s", want, text)
			}
			if notWant != "" && strings.Contains(text, notWant) {
				t.Errorf("the pass kept the old %q:\n%s", notWant, text)
			}
			if title != "" {
				metas, err := AllMeta(dir)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, m := range metas {
					if m.Harness == "codebuddy" && m.ID == cb4SessionID {
						found = true
						if m.Title != title {
							t.Errorf("title = %q, want %q", m.Title, title)
						}
					}
				}
				if !found {
					t.Error("the session row went away")
				}
			}
			matchesRebuild(t, dir, "codebuddy", cb4SessionID)
			sameAsRebuild(t, dir)
		})
	}

	step("streamed reply", "brindleplume streamed", "assistant|quokkamint answer\n", "", func() {
		cb4Rewrite(t, cb4MessagePath(f, "m2"), cb4MessageFile(t, "assistant", "quokkamint answer and brindleplume streamed"))
	})
	step("new message", "coralwhorl appended", "", "", func() {
		cb4Rewrite(t, cb4MessagePath(f, "m4"), cb4MessageFile(t, "user", "coralwhorl appended"))
		cb4Rewrite(t, f.convIndex, cb4ConversationIndex(t, "m1", "m2", "m3", "m4"))
	})
	step("title change", "", "", "umberlantern retitled", func() {
		cb4Rewrite(t, f.wsIndex, cb4WorkspaceIndex("umberlantern retitled"))
	})
	step("missing middle message", "quasithorn question", "vellumglow detail", "", func() {
		if err := os.Remove(cb4MessagePath(f, "m3")); err != nil {
			t.Fatal(err)
		}
		cb4Stamp(t, filepath.Join(f.conversationDir, "messages"))
	})
}

// The converted transcript stands in for the 4.x directory. Neither store's
// turns may be duplicated, and the switch back and forth must agree with a
// rebuild (#4432 is the same handover for a ZCode snapshot).
func TestCodeBuddyIDE4ConversionSwitchMatchesARebuild(t *testing.T) {
	f, dir := cb4Env(t)
	cb4ConversionSwitch(t, f, dir)
}

// DEJA_CODEBUDDY_LEGACY_ROOTS may name a history directory by another name or
// one workspace's directory. A conversation found there is a 4.x index by its
// place under the root, not by a directory named history above it, and the
// conversion hands over the same way.
func TestCodeBuddyIDE4ConversionSwitchUnderNarrowRoots(t *testing.T) {
	for _, tc := range []struct {
		name string
		// root is the configured legacy root under the temp dir; ws is the
		// workspace directory relative to it.
		root, ws string
	}{
		{"history directory by another name", filepath.Join("copy", "hist-backup"), cb4WorkspaceID},
		{"one workspace copied", filepath.Join("copy", "one-workspace"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, dir := cb4Env(t)
			root := filepath.Join(filepath.Dir(f.legacyRoot), tc.root)
			t.Setenv("DEJA_CODEBUDDY_LEGACY_ROOTS", root)
			f.legacyRoot = root
			f.workspaceDir = filepath.Join(root, tc.ws)
			f.conversationDir = filepath.Join(f.workspaceDir, cb4SessionID)
			f.wsIndex = filepath.Join(f.workspaceDir, "index.json")
			f.convIndex = filepath.Join(f.conversationDir, "index.json")
			cb4ConversionSwitch(t, f, dir)
		})
	}
}

// cb4ConversionSwitch converts the seeded conversation and deletes the
// transcript again, checking each pass against a rebuild.
func cb4ConversionSwitch(t *testing.T, f cb4Fixture, dir string) {
	t.Helper()
	cb4Seed(t, f)
	indexPass(t, dir)
	if got := cb4Sessions(t, dir, "vellumglow"); len(got) != 1 {
		t.Fatalf("the 4.x conversation is not indexed first: %v", got)
	}

	transcript := cb4Convert(t, f)
	indexPass(t, dir)
	if got := cb4KeyRows(t, dir, cb4SessionID); got != 1 {
		t.Errorf("conversion left %d rows for codebuddy:%s", got, cb4SessionID)
	}
	s := cb4Session(t, dir)
	text := cb4Text(s)
	if !strings.Contains(text, "zephyrubium continued") || !strings.Contains(text, "zephyrubium resolved") {
		t.Errorf("the continued turns are missing:\n%s", text)
	}
	if strings.Contains(text, "vellumglow detail") {
		t.Errorf("the 4.x-only turn was duplicated into the converted conversation:\n%s", text)
	}
	if strings.Count(text, "quasithorn question") != 1 || strings.Count(text, "quokkamint answer") != 1 {
		t.Errorf("the converted turns are duplicated:\n%s", text)
	}
	if got := cb4Sessions(t, dir, "zephyrubium"); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4SessionID}) {
		t.Errorf("search for the continued word = %v", got)
	}
	if got := cb4Sessions(t, dir, "vellumglow"); len(got) != 0 {
		t.Errorf("the 4.x-only word is still indexed: %v", got)
	}
	matchesRebuild(t, dir, "codebuddy", cb4SessionID)
	sameAsRebuild(t, dir)

	// A transcript that cannot be read leaves the 4.x copy as the conversation.
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	if got := cb4KeyRows(t, dir, cb4SessionID); got != 1 {
		t.Errorf("switching back left %d rows for codebuddy:%s", got, cb4SessionID)
	}
	if got := cb4Sessions(t, dir, "vellumglow"); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4SessionID}) {
		t.Errorf("the 4.x conversation did not come back: %v", got)
	}
	if s := cb4Session(t, dir); strings.Contains(cb4Text(s), "zephyrubium") {
		t.Errorf("the converted turns stayed after their transcript went away:\n%s", cb4Text(s))
	}
	matchesRebuild(t, dir, "codebuddy", cb4SessionID)
	sameAsRebuild(t, dir)
}

// Both stores hold sessions when their ids differ; the CLI store needs none of
// the 4.x files to be read.
func TestCodeBuddyIDE4AndCLIStoresAreIndependent(t *testing.T) {
	const cliID = "cli-only-abcdef0123456789"
	t.Run("cli only", func(t *testing.T) {
		f, dir := cb4Env(t)
		cb4Write(t, filepath.Join(f.cliRoot, "work-p", cliID+".jsonl"),
			cb4CLILine(t, cliID, "c1", "user", "glimmerstone cli question", cb4StartedMs)+
				cb4CLILine(t, cliID, "c2", "assistant", "glimmerstone cli answer", cb4StartedMs+60_000))
		indexPass(t, dir)
		if got := cb4Sessions(t, dir, "glimmerstone"); !reflect.DeepEqual(got, []string{"codebuddy:" + cliID}) {
			t.Errorf("the CLI-only session = %v", got)
		}
		if got := cb4KeyRows(t, dir, cb4SessionID); got != 0 {
			t.Errorf("a 4.x row appeared with no 4.x store: %d", got)
		}
		matchesRebuild(t, dir, "codebuddy", cliID)
	})

	t.Run("both stores", func(t *testing.T) {
		f, dir := cb4Env(t)
		cb4Seed(t, f)
		cb4Write(t, filepath.Join(f.cliRoot, "work-p", cliID+".jsonl"),
			cb4CLILine(t, cliID, "c1", "user", "glimmerstone cli question", cb4StartedMs)+
				cb4CLILine(t, cliID, "c2", "assistant", "glimmerstone cli answer", cb4StartedMs+60_000))
		indexPass(t, dir)
		for q, id := range map[string]string{"quasithorn": cb4SessionID, "glimmerstone": cliID} {
			if got := cb4Sessions(t, dir, q); !reflect.DeepEqual(got, []string{"codebuddy:" + id}) {
				t.Errorf("search %q = %v, want codebuddy:%s", q, got, id)
			}
		}
		if got := cb4KeyRows(t, dir, cb4SessionID); got != 1 {
			t.Errorf("4.x rows = %d, want one", got)
		}
		if got := cb4KeyRows(t, dir, cliID); got != 1 {
			t.Errorf("CLI rows = %d, want one", got)
		}
		matchesRebuild(t, dir, "codebuddy", cb4SessionID)
		matchesRebuild(t, dir, "codebuddy", cliID)
		sameAsRebuild(t, dir)
	})
}

// A CLI transcript with the same id as a 4.x conversation but no converter
// marker is a different conversation, not the newer copy of it. Both sets of
// turns stay searchable under the one shared row, and a pass holds what a
// rebuild does.
func TestCodeBuddyIDE4UnrelatedCLITranscriptSharesTheRow(t *testing.T) {
	f, dir := cb4Env(t)
	cb4Seed(t, f)
	cb4Write(t, filepath.Join(f.cliRoot, "work-p", cb4SessionID+".jsonl"),
		cb4CLILine(t, cb4SessionID, "c1", "user", "gravelstone cli question", cb4StartedMs)+
			cb4CLILine(t, cb4SessionID, "c2", "assistant", "gravelstone cli answer", cb4StartedMs+60_000))
	indexPass(t, dir)
	indexPass(t, dir)
	for _, q := range []string{"quasithorn", "vellumglow", "gravelstone"} {
		if got := cb4Sessions(t, dir, q); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4SessionID}) {
			t.Errorf("search %q = %v, want the shared row", q, got)
		}
	}
	text := cb4Text(cb4Session(t, dir))
	for _, q := range []string{"quasithorn question", "vellumglow detail", "gravelstone cli question", "gravelstone cli answer"} {
		if !strings.Contains(text, q) {
			t.Errorf("%q is missing from the shared row:\n%s", q, text)
		}
	}
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if row := m.Sessions["codebuddy:"+cb4SessionID]; !row.Shared {
		t.Errorf("the row is not marked shared: %+v", row)
	}
	matchesRebuild(t, dir, "codebuddy", cb4SessionID)
	sameAsRebuild(t, dir)
}

// A rebuild of both stores writes the same session rows and file states twice.
func TestCodeBuddyIDE4RebuildIsDeterministic(t *testing.T) {
	f, dir := cb4Env(t)
	cb4Seed(t, f)
	cb4Convert(t, f)
	first := filepath.Join(filepath.Dir(dir), "rebuild-a")
	second := filepath.Join(filepath.Dir(dir), "rebuild-b")
	parityPass(t, first, true)
	parityPass(t, second, true)
	a, b := cb4ManifestView(t, first), cb4ManifestView(t, second)
	if a != b {
		t.Errorf("two rebuilds differ:\nfirst:\n%s\nsecond:\n%s", a, b)
	}
	if got, want := paritySnapshot(t, first), paritySnapshot(t, second); !reflect.DeepEqual(got, want) {
		t.Errorf("two rebuilds hold different sessions:\n%v\n%v", got, want)
	}
	samePostings(t, first, second)
}
func cb4Env(t *testing.T) (cb4Fixture, string) {
	t.Helper()
	tmp := parityEnv(t, map[string]string{
		"DEJA_CODEBUDDY_LEGACY_ROOTS": "legacy",
		"DEJA_CODEBUDDY_ROOTS":        "cli",
	})
	t.Setenv("CODEBUDDY_CONFIG_DIR", "")
	t.Setenv("WORKBUDDY_CONFIG_DIR", "")
	var f cb4Fixture
	f.legacyRoot = filepath.Join(tmp, "legacy")
	f.cliRoot = filepath.Join(tmp, "cli")
	f.workspaceDir = filepath.Join(f.legacyRoot, "u1", "CodeBuddyIDE", "u1", "history", cb4WorkspaceID)
	f.conversationDir = filepath.Join(f.workspaceDir, cb4SessionID)
	f.wsIndex = filepath.Join(f.workspaceDir, "index.json")
	f.convIndex = filepath.Join(f.conversationDir, "index.json")
	return f, filepath.Join(tmp, "inc")
}

func cb4Write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cb4Stamp gives every rewrite a distinct mtime, as the IDE's writes do and
// the sidecar fingerprint requires.
func cb4Stamp(t *testing.T, paths ...string) {
	t.Helper()
	later := time.Now().Add(2 * time.Second)
	for i, p := range paths {
		at := later.Add(time.Duration(i) * time.Millisecond)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
}

func cb4WorkspaceIndex(title string) string {
	return fmt.Sprintf(`{"conversations":[{"id":%q,"type":"craft","name":%q,"createdAt":%q,"lastMessageAt":%q}]}`,
		cb4SessionID, title, cb4CreatedAt, "2026-01-02T03:05:05Z")
}

func cb4ConversationIndex(t *testing.T, ids ...string) string {
	t.Helper()
	type message struct {
		ID string `json:"id"`
	}
	type request struct {
		ID        string   `json:"id"`
		Messages  []string `json:"messages"`
		StartedAt int64    `json:"startedAt"`
	}
	idx := struct {
		Messages []message `json:"messages"`
		Requests []request `json:"requests"`
	}{}
	for _, id := range ids {
		idx.Messages = append(idx.Messages, message{ID: id})
	}
	idx.Requests = append(idx.Requests, request{ID: "r1", Messages: ids, StartedAt: cb4StartedMs})
	b, err := json.Marshal(idx)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cb4MessageFile is a messages/<id>.json as the IDE's recorder writes it:
// the body and extra fields hold JSON strings.
func cb4MessageFile(t *testing.T, role, text string) string {
	t.Helper()
	inner, err := json.Marshal(map[string]any{
		"role":    role,
		"content": []map[string]any{{"type": "text", "text": text}},
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(string(inner))
	if err != nil {
		t.Fatal(err)
	}
	extra, err := json.Marshal("{}")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"role":      role,
		"message":   json.RawMessage(message),
		"extra":     json.RawMessage(extra),
		"createdAt": cb4CreatedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cb4MessagePath(f cb4Fixture, id string) string {
	return filepath.Join(f.conversationDir, "messages", id+".json")
}

// The 4.x index.json and the converted JSONL are one conversation in two
// storage generations. The JSONL owns the row whichever order they are read
// in, as a restored ZCode database row owns its snapshot's (#4432).
func TestCodeBuddyIDE4ConversionOwnershipHandover(t *testing.T) {
	f, _ := cb4Env(t)
	cb4Seed(t, f)
	cli := cb4Convert(t, f)
	check := func(heldPath, arrivingPath string, wantOwns bool) {
		t.Helper()
		held := SessionMeta{Harness: "codebuddy", ID: cb4SessionID, Path: heldPath}
		owns, collided := attributeSession(held, model.Session{Harness: "codebuddy", ID: cb4SessionID, Path: arrivingPath})
		if collided || owns != wantOwns {
			t.Errorf("%s then %s: owns=%v collided=%v, want owns=%v without a clash", heldPath, arrivingPath, owns, collided, wantOwns)
		}
	}
	check(f.convIndex, cli, true)
	check(cli, f.convIndex, false)
}

// cb4Seed writes a three-message conversation with words no other store has.
// The 32-hex ids and the path shape are what make it a 4.x store.
func cb4Seed(t *testing.T, f cb4Fixture) {
	t.Helper()
	cb4Write(t, f.wsIndex, cb4WorkspaceIndex("quasithorn title"))
	cb4Write(t, f.convIndex, cb4ConversationIndex(t, "m1", "m2", "m3"))
	cb4Write(t, cb4MessagePath(f, "m1"), cb4MessageFile(t, "user", "<user_query>\nquasithorn question\n</user_query>"))
	cb4Write(t, cb4MessagePath(f, "m2"), cb4MessageFile(t, "assistant", "quokkamint answer"))
	cb4Write(t, cb4MessagePath(f, "m3"), cb4MessageFile(t, "assistant", "vellumglow detail"))
	cb4Stamp(t, f.wsIndex, f.convIndex, cb4MessagePath(f, "m1"), cb4MessagePath(f, "m2"), cb4MessagePath(f, "m3"))
}

func cb4Rewrite(t *testing.T, path, body string) {
	t.Helper()
	cb4Write(t, path, body)
	cb4Stamp(t, path)
}

func cb4CLILine(t *testing.T, id, msgID, role, text string, at int64) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"id":        msgID,
		"timestamp": at,
		"type":      "message",
		"role":      role,
		"content":   []map[string]any{{"type": "input_text", "text": text}},
		"sessionId": id,
		"cwd":       "/work/p",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

// cb4Convert writes the transcript the 5.x converter leaves, including a turn
// the old store does not hold, and the marker that names its conversation.
func cb4Convert(t *testing.T, f cb4Fixture) string {
	t.Helper()
	project := filepath.Join(f.cliRoot, "work-p")
	transcript := filepath.Join(project, cb4SessionID+".jsonl")
	body := cb4CLILine(t, cb4SessionID, "a1", "user", "quasithorn question", cb4StartedMs) +
		cb4CLILine(t, cb4SessionID, "a2", "assistant", "quokkamint answer", cb4StartedMs+60_000) +
		cb4CLILine(t, cb4SessionID, "a3", "user", "zephyrubium continued", cb4StartedMs+120_000) +
		cb4CLILine(t, cb4SessionID, "a4", "assistant", "zephyrubium resolved", cb4StartedMs+180_000)
	cb4Write(t, transcript, body)
	cb4Write(t, filepath.Join(project, cb4SessionID, ".codebuddy-ide-4-conversion.json"),
		fmt.Sprintf(`{"conversationId":%q}`, cb4SessionID))
	cb4Stamp(t, transcript)
	return transcript
}

func cb4Sessions(t *testing.T, dir, q string) []string {
	t.Helper()
	ss, err := Search(dir, search.Options{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Harness+":"+s.ID)
	}
	return out
}

func cb4Session(t *testing.T, dir string) model.Session {
	t.Helper()
	s, ok, err := FindByIdentity(dir, "codebuddy", cb4SessionID)
	if err != nil || !ok {
		t.Fatalf("codebuddy:%s is not in the index: %v", cb4SessionID, err)
	}
	return s
}

func cb4KeyRows(t *testing.T, dir, id string) int {
	t.Helper()
	metas, err := AllMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, m := range metas {
		if m.Harness == "codebuddy" && m.ID == id {
			rows++
		}
	}
	return rows
}

func cb4Text(s model.Session) string {
	out := make([]string, 0, len(s.Messages))
	for _, m := range s.Messages {
		out = append(out, m.Role+"|"+m.Text)
	}
	return strings.Join(out, "\n")
}

func cb4ManifestView(t *testing.T, dir string) string {
	t.Helper()
	m, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := make([]string, 0, len(m.Files))
	for p, f := range m.Files {
		files = append(files, fmt.Sprintf("%s|%d|%d|%d|%d|kept=%v", p, f.Size, f.MTime, f.MetadataSize, f.MetadataMTime, f.Kept))
	}
	sort.Strings(files)
	sessions := make([]string, 0, len(m.Sessions))
	for key, s := range m.Sessions {
		sessions = append(sessions, fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d|%v", key, s.Path, s.Title, s.Project,
			s.Started.UTC().Format(time.RFC3339Nano), s.Updated.UTC().Format(time.RFC3339Nano),
			s.Settled, s.Counted, s.Copies))
	}
	sort.Strings(sessions)
	return strings.Join(sessions, "\n") + "\nfiles\n" + strings.Join(files, "\n")
}
