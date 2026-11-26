package index

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A case found on a real Linux 4.x store: the same conversation in two
// workspace directories.

// The IDE can hold one conversation in two workspace directories, byte for
// byte. It is one conversation: the index lists it once, does not mark it
// shared or count it as a clash, and when the copy it reads is rewritten or
// deleted the other takes over, holding what a rebuild of the same files
// holds.
func TestCodeBuddyIDE4CopiedConversationIsOneSession(t *testing.T) {
	f, dir := cb4Env(t)
	cb4Seed(t, f)
	historyDir := filepath.Dir(f.workspaceDir)
	twinDir := filepath.Join(historyDir, "fedcba9876543210fedcba9876543210", cb4SessionID)
	twinWS := filepath.Join(filepath.Dir(twinDir), "index.json")
	twinIndex := filepath.Join(twinDir, "index.json")
	copyFile := func(from, to string) {
		t.Helper()
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		cb4Write(t, to, string(b))
	}
	copyFile(f.wsIndex, twinWS)
	copyFile(f.convIndex, twinIndex)
	for _, m := range []string{"m1", "m2", "m3"} {
		copyFile(cb4MessagePath(f, m), filepath.Join(twinDir, "messages", m+".json"))
	}
	// Both copies at once, as the IDE leaves them: the smaller path is read.
	at := time.Now().Add(-time.Hour)
	for _, p := range []string{f.convIndex, twinIndex} {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}

	check := func(name, owner, want string) {
		t.Helper()
		m, err := readManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		row, ok := m.Sessions["codebuddy:"+cb4SessionID]
		if !ok {
			t.Fatalf("%s: the conversation has no row", name)
		}
		if row.Path != owner {
			t.Errorf("%s: the row is read from %s, want %s", name, row.Path, owner)
		}
		if row.Shared || len(row.Copies) != 0 {
			t.Errorf("%s: one conversation filed as a clash: shared=%v copies=%v", name, row.Shared, row.Copies)
		}
		if got := cb4KeyRows(t, dir, cb4SessionID); got != 1 {
			t.Errorf("%s: %d rows, want one", name, got)
		}
		text := cb4Text(cb4Session(t, dir))
		if n := strings.Count(text, "quasithorn question"); n != 1 {
			t.Errorf("%s: the question is held %d times:\n%s", name, n, text)
		}
		if want != "" && !strings.Contains(text, want) {
			t.Errorf("%s: %q is missing:\n%s", name, want, text)
		}
		if got := cb4Sessions(t, dir, "quokkamint"); !reflect.DeepEqual(got, []string{"codebuddy:" + cb4SessionID}) {
			t.Errorf("%s: search = %v", name, got)
		}
		matchesRebuild(t, dir, "codebuddy", cb4SessionID)
		sameAsRebuild(t, dir)
	}

	collisions.Store(nil)
	indexPass(t, dir)
	if n := len(collisions.Swap(nil)); n != 0 {
		t.Errorf("the first pass counted %d clashes, want none", n)
	}
	first, second := f.convIndex, twinIndex
	if second < first {
		first, second = second, first
	}
	check("first pass", first, "")

	// The IDE goes on writing the other copy: it is read from then on.
	other := f.conversationDir
	if first == f.convIndex {
		other = twinDir
	}
	cb4Rewrite(t, filepath.Join(other, "messages", "m4.json"), cb4MessageFile(t, "user", "marrowfinch appended"))
	cb4Rewrite(t, second, cb4ConversationIndex(t, "m1", "m2", "m3", "m4"))
	indexPass(t, dir)
	check("the other copy written", second, "marrowfinch appended")

	// The copy now read is deleted: the first takes the conversation back.
	if err := os.RemoveAll(filepath.Dir(second)); err != nil {
		t.Fatal(err)
	}
	indexPass(t, dir)
	check("the copy read deleted", first, "")
	if strings.Contains(cb4Text(cb4Session(t, dir)), "marrowfinch") {
		t.Error("the deleted copy's own turn is still held")
	}
}
