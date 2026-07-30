package index

import "testing"

func TestPostingRoundTripCarriesTheToolBit(t *testing.T) {
	in := []posting{
		{Off: 10, Sid: 1},
		{Off: 4096, Sid: 1, Tool: true},
		{Off: 1 << 20, Sid: 70000, Tool: true},
		{Off: 1<<20 + 3, Sid: 70000},
	}
	got := decodePostings(encodePostings(in))
	if len(got) != len(in) {
		t.Fatalf("got %d postings, want %d", len(got), len(in))
	}
	for i, p := range got {
		if p != in[i] {
			t.Fatalf("posting %d = %+v, want %+v", i, p, in[i])
		}
	}
}

func TestCapKeepsSpeechBeforeToolRecords(t *testing.T) {
	// One session, four sentences and ninety-six command lines. Reading only
	// half the session's postings must not spend the whole budget on commands.
	var posts []posting
	for i := 0; i < 4; i++ {
		posts = append(posts, posting{Off: int64(i), Sid: 7})
	}
	for i := 0; i < 96; i++ {
		posts = append(posts, posting{Off: int64(100 + i), Sid: 7, Tool: true})
	}
	got := capPostingsPerSession(posts, 50)
	speech, tool := 0, 0
	for _, p := range got {
		if p.Tool {
			tool++
		} else {
			speech++
		}
	}
	if speech != 4 {
		t.Fatalf("kept %d of 4 speech postings", speech)
	}
	if want := 46; tool != want {
		t.Fatalf("kept %d tool postings, want %d", tool, want)
	}
}

func TestCapSpendsEverythingOnSpeechWhenThereIsEnoughOfIt(t *testing.T) {
	var posts []posting
	for i := 0; i < 80; i++ {
		posts = append(posts, posting{Off: int64(i), Sid: 3})
	}
	for i := 0; i < 80; i++ {
		posts = append(posts, posting{Off: int64(1000 + i), Sid: 3, Tool: true})
	}
	got := capPostingsPerSession(posts, 20)
	for _, p := range got {
		if p.Tool {
			t.Fatal("speech alone fills the budget, no tool posting should survive")
		}
	}
	if len(got) != 20 {
		t.Fatalf("kept %d postings, want 20", len(got))
	}
}

func TestCapLeavesSmallSessionsAlone(t *testing.T) {
	posts := []posting{
		{Off: 1, Sid: 2, Tool: true},
		{Off: 2, Sid: 2},
	}
	if got := capPostingsPerSession(posts, 64); len(got) != 2 {
		t.Fatalf("a session under the bound keeps every posting, got %d", len(got))
	}
}

func TestIsToolRole(t *testing.T) {
	for _, r := range []string{roleFiles, roleCommand, roleToolOutput} {
		if !isToolRole(r) {
			t.Errorf("%q should be a tool role", r)
		}
	}
	for _, r := range []string{"user", "assistant", "developer"} {
		if isToolRole(r) {
			t.Errorf("%q is speech", r)
		}
	}
}

func TestTokenizedPartIndexesOnlyThePathOfASpan(t *testing.T) {
	span := "/w/pkg/retry.go\nfunc retry() error {\n\treturn nil\n}"
	if got := tokenizedPart(roleEdit, span); got != "/w/pkg/retry.go" {
		t.Fatalf("got %q, want the path alone", got)
	}
	if got := tokenizedPart("user", span); got != span {
		t.Fatal("speech is indexed whole")
	}
	// A span with no body still has to yield its path rather than nothing.
	if got := tokenizedPart(roleEdit, "/w/only.go"); got != "/w/only.go" {
		t.Fatalf("got %q", got)
	}
}
