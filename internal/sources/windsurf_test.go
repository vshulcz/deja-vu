package sources

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// windsurfStore copies the committed fixtures into a fresh root and points
// DEJA_WINDSURF_ROOT at it, so a test can add files beside them.
func windsurfStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	dir := filepath.Join(root, "cascade")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{windsurfFixtureChat, windsurfFixtureError} {
		b, err := os.ReadFile(filepath.Join(windsurfFixtureDir(), "cascade", id+".pb"))
		if err != nil {
			t.Fatal(err)
		}
		writeWindsurf(t, root, id, b)
	}
	return root
}

func writeWindsurf(t *testing.T, root, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(root, "cascade", name+".pb")
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWindsurfReadsPromptsAndReplies(t *testing.T) {
	root := windsurfStore(t)
	p := filepath.Join(root, "cascade", windsurfFixtureChat+".pb")
	if !isWindsurfTrajectory(p) {
		t.Fatalf("%s not matched", p)
	}
	ss, err := ParseWindsurfFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	s := ss[0]
	if s.ID != windsurfFixtureChat || s.Harness != "windsurf" || s.Title != "Cursor pagination duplicates" {
		t.Fatalf("session = %q %q %q", s.ID, s.Harness, s.Title)
	}
	// The git root, not the subfolder Cascade was opened on.
	if want := projectName("/home/dev/src/notes-api"); s.Project != want {
		t.Fatalf("project = %q, want %q", s.Project, want)
	}
	var got []string
	for _, m := range s.Messages {
		got = append(got, m.Role+": "+m.Text)
	}
	want := []string{
		"user: why does GET /notes return duplicates after the cursor",
		"assistant: The cursor compares created_at, which ties for notes saved in the same second; filter on (created_at, id) instead.",
		"user: add a test for the tie",
		// response is empty on this step, so modified_response stands in.
		"assistant: Added TestListCursorTie, which saves two notes in one second and pages through them.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("messages:\n%s", strings.Join(got, "\n"))
	}
	t0 := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	if !s.Started.Equal(t0) || !s.Updated.Equal(t0.Add(55*time.Second)) {
		t.Fatalf("started %v updated %v", s.Started, s.Updated)
	}
	if !s.Messages[0].Time.Equal(t0.Add(5*time.Second)) || !s.Messages[1].Time.Equal(t0.Add(20*time.Second)) {
		t.Fatalf("message times %v %v", s.Messages[0].Time, s.Messages[1].Time)
	}
	for _, m := range s.Messages {
		if strings.Contains(m.Text, "chain of thought") || strings.Contains(m.Text, "go test") {
			t.Fatalf("thinking or a tool call was indexed: %q", m.Text)
		}
	}
}

func TestWindsurfErrorOnlyTrajectoryKeepsItsPrompts(t *testing.T) {
	root := windsurfStore(t)
	ss, err := ParseWindsurfFile(filepath.Join(root, "cascade", windsurfFixtureError+".pb"))
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	s := ss[0]
	if len(s.Messages) != 2 || s.Messages[0].Role != "user" || s.Messages[1].Role != "user" {
		t.Fatalf("messages = %#v", s.Messages)
	}
	if !strings.HasPrefix(s.Title, "Do not use tools") || s.Project != projectName("/tmp/deja-windsurf-synthetic") {
		t.Fatalf("title %q project %q", s.Title, s.Project)
	}
}

// A tampered trajectory is skipped and counted; it costs only itself, and the
// count is what doctor and the index run report.
func TestWindsurfTamperedTrajectoryIsSkippedAndCounted(t *testing.T) {
	root := windsurfStore(t)
	if got := SkipReason("windsurf"); got != "" {
		t.Fatalf("clean store reports %q", got)
	}
	b := windsurfChatTrajectory()
	b[len(b)-1] ^= 0x01
	bad := writeWindsurf(t, root, "0f0e0d0c-tampered", b)
	if ss, err := ParseWindsurfFile(bad); len(ss) != 0 || err != nil {
		t.Fatalf("tampered tag: %d sessions, err %v", len(ss), err)
	}
	if got := DiagFailedPaths()[bad]; got != errWindsurfAuth.Error() {
		t.Fatalf("tampered file recorded as %q, want the authentication failure", got)
	}
	// A file too short to hold a nonce and a tag is refused the same way.
	writeWindsurf(t, root, "short", []byte("not a trajectory"))
	ss := LoadWindsurf()
	if len(ss) != 2 {
		t.Fatalf("loaded %d sessions, want the two good ones", len(ss))
	}
	if n := WindsurfAuthFailures(); n != 2 {
		t.Fatalf("auth failures = %d, want 2", n)
	}
	if got := SkipReason("windsurf"); got != "2 trajectories failed authentication" {
		t.Fatalf("skip reason = %q", got)
	}
}

// Authenticated bytes that are not a trajectory are a decode error, not an
// authentication failure, and do not stop the store.
func TestWindsurfMalformedTrajectoryDoesNotStopTheStore(t *testing.T) {
	root := windsurfStore(t)
	whole := pbMsg(nil).str(6, "x").bytes(2, pbMsg(nil).bytes(19, pbMsg(nil).str(2, "hello")))
	truncated := windsurfSeal(whole[:len(whole)-3], 3)
	p := writeWindsurf(t, root, "truncated", truncated)
	if ss, err := ParseWindsurfFile(p); len(ss) != 0 || err != nil {
		t.Fatalf("truncated: %d sessions, err %v", len(ss), err)
	}
	if got := DiagFailedPaths()[p]; !strings.HasPrefix(got, "decode trajectory") {
		t.Fatalf("truncated file recorded as %q, want a decode error", got)
	}
	group := windsurfSeal(pbMsg(nil).key(9, 3), 4)
	writeWindsurf(t, root, "group", group)
	if ss := LoadWindsurf(); len(ss) != 2 {
		t.Fatalf("loaded %d sessions, want the two good ones", len(ss))
	}
	if n := WindsurfAuthFailures(); n != 0 {
		t.Fatalf("auth failures = %d, want 0", n)
	}
}

func TestWindsurfFallbacks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DEJA_WINDSURF_ROOT", root)
	if err := os.MkdirAll(filepath.Join(root, "cascade"), 0o755); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	// No cascade_id, no git root, a Windows workspace, and a prompt only in
	// query.
	m := pbMsg(nil).
		bytes(2, windsurfUserStep(at, "", "rename the flag")).
		bytes(7, windsurfMeta(at, [2]string{"file:///C:/Users/dev/src/cli%20tool", ""}))
	p := writeWindsurf(t, root, "named-by-file", windsurfSeal(m, 5))
	ss, err := ParseWindsurfFile(p)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions=%d err=%v", len(ss), err)
	}
	s := ss[0]
	if s.ID != "named-by-file" || s.Title != "rename the flag" || s.Messages[0].Text != "rename the flag" {
		t.Fatalf("session = %q %q %#v", s.ID, s.Title, s.Messages)
	}
	if s.Project != "src/cli tool" {
		t.Fatalf("project = %q, want the path the file URI names", s.Project)
	}
	// Nothing typed and nothing answered is no session.
	empty := writeWindsurf(t, root, "empty", windsurfSeal(pbMsg(nil).str(6, "e"), 6))
	if ss, err := ParseWindsurfFile(empty); err != nil || len(ss) != 0 {
		t.Fatalf("empty trajectory: %d sessions, err %v", len(ss), err)
	}
	// implicit/ and brain/ are not conversations.
	if isWindsurfTrajectory(filepath.Join(root, "implicit", "x.pb")) {
		t.Fatal("implicit trajectory matched")
	}
}

func TestWindsurfStringsComeOutAsText(t *testing.T) {
	if got := pbString([]byte("a\x00b\xff")); got != "ab�" {
		t.Fatalf("pbString = %q", got)
	}
}
