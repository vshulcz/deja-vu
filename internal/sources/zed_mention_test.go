package sources

import (
	"fmt"
	"strings"
	"testing"
)

// @-mentioning another thread inlines Zed's own generated summary of it into
// the message. deja indexed that as the person's words — 48798 characters
// across the 12 mentions on this machine's store, each standing above the
// sentence someone actually typed (#3336). A selection mention is the other
// kind and is theirs: code they attached on purpose.
const zedMentionBody = `{"version":"0.3.0","title":"mention thread","updated_at":"2026-02-02T12:35:02Z","messages":[
{"User":{"id":"u1","content":[
 {"Mention":{"uri":{"Thread":{"id":"a6c02f18-ab43-4848-97a4-2a84f53ab927","name":"Deep Code Review Advertising Feature Branch"}},"content":"# Conversation Summary: Deep Code Review\n## 1. Overview\n- The user requested a deep review of the advertising branch."}},
 {"Text":"\ncarry on with the errors listed in KNOWN_ISSUES.md"}]}},
{"User":{"id":"u2","content":[
 {"Mention":{"uri":{"Selection":{"abs_path":"/w/app/pool.go","line_range":{"start":12,"end":18}}},"content":"item := pool.Get()\npool.Put(item)"}},
 {"Text":"\nwhy does this leak"}]}}
]}`

func TestZedDoesNotTakeTheSummaryOfAMentionedThreadAsSpeech(t *testing.T) {
	zedHome(t)
	hex := zedZstdHex(t, zedMentionBody)
	sql := fmt.Sprintf("%s\ninsert into threads (id, summary, updated_at, data_type, data, folder_paths, created_at) values ('m1', 'mention thread', '2026-02-02T12:35:02Z', 'zstd', x'%s', '[\"/w/app\"]', '2026-02-02T12:35:00Z');", zedSchema, hex)
	db := zedTestDB(t, sql)
	if !ZstdAvailable() {
		t.Skip("zstd not installed")
	}
	ss, err := ParseZedDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1", len(ss))
	}
	var users []string
	for _, m := range ss[0].Messages {
		if m.Role == "user" {
			users = append(users, m.Text)
		}
	}
	if len(users) != 2 {
		t.Fatalf("user turns = %d, want 2: %q", len(users), users)
	}
	if strings.Contains(users[0], "Conversation Summary") {
		t.Errorf("the editor's summary of another thread is indexed as the person's words: %q", users[0])
	}
	if !strings.Contains(users[0], "carry on with the errors listed in KNOWN_ISSUES.md") {
		t.Errorf("the person's own sentence went with it: %q", users[0])
	}
	if !strings.Contains(users[1], "pool.Put(item)") {
		t.Errorf("a selection the person attached is theirs and must stay: %q", users[1])
	}
}
