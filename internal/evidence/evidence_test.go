package evidence

import (
	"maps"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func session(msgs ...model.Message) model.Session {
	return model.Session{Harness: "claude", ID: "s1", Messages: msgs, Updated: time.Now().Add(-time.Hour)}
}

func TestReceiptIsTheCommandTheAnswerRestsOn(t *testing.T) {
	answer := "Fixed: retry_limit in payments_client was 0, set it to 5"
	s := session(
		model.Message{Role: "user", Text: "payments_client gives up on the first timeout, fix it"},
		model.Message{Role: "command", Text: "grep -n retry_limit payments_client.go"},
		model.Message{Role: "tool-output", Text: "12: retry_limit = 0 // payments_client"},
		model.Message{Role: "assistant", Text: answer},
	)
	r := Receipts(s, nil)
	got := r[Key("claude", "s1", answer)]
	if !strings.Contains(got, "retry_limit") || strings.HasPrefix(got, LabelUser) {
		t.Fatalf("receipt %q, want the grep record", got)
	}
}

func TestAQuestionBacksNothing(t *testing.T) {
	answer := "Use payments_backoff of 250ms doubling to 4s"
	s := session(
		model.Message{Role: "user", Text: "what payments_backoff should the retry use?"},
		model.Message{Role: "assistant", Text: answer},
	)
	if got := Receipts(s, nil)[Key("claude", "s1", answer)]; got != "" {
		t.Errorf("the question became the receipt: %q", got)
	}
}

func TestBuildThenLookup(t *testing.T) {
	dir := t.TempDir()
	answer := "Fixed: retry_limit in payments_client was 0, set it to 5"
	s := session(
		model.Message{Role: "user", Text: "payments_client gives up on the first timeout, fix it"},
		model.Message{Role: "command", Text: "grep -n retry_limit payments_client.go"},
		model.Message{Role: "tool-output", Text: "12: retry_limit = 0 // payments_client"},
		model.Message{Role: "assistant", Text: answer},
	)
	if Open(dir) != nil {
		t.Fatal("a reader before any build")
	}
	Build(dir, []model.Session{s}, nil)
	rd := Open(dir)
	want := strings.Split(Receipts(s, nil)[Key("claude", "s1", answer)], "\n")
	if got := rd.Lookup("claude", "s1", answer); !slices.Equal(got, want) {
		t.Errorf("lookup %q, want %q", got, want)
	}
	if rd.Lookup("claude", "s2", answer) != nil {
		t.Error("another session's key found a receipt")
	}
	if !rd.Covers(s.Updated) || rd.Covers(time.Now().Add(time.Hour)) || rd.Covers(time.Time{}) {
		t.Error("Covers must hold only for sessions last changed before the build")
	}
}

func TestOrdersAndTheirCut(t *testing.T) {
	text := "Update docs/install.md. Project rule: every time you edit it, run ./scripts/linkcheck.sh afterwards.\nThen tell me."
	if got := Orders(text); !slices.Equal(got, []string{"linkcheck.sh"}) {
		t.Errorf("orders %q", got)
	}
	if got := Orders("run go test ./internal/slug and tell me what fails"); got != nil {
		t.Errorf("a request read as an order: %q", got)
	}
	if got := CutOrders(text, true, nil); got != "Update docs/install.md. Project "+LeftOut+" Then tell me." {
		t.Errorf("cut %q", got)
	}
	// Only what was planted goes when not all of it is to go.
	if got := CutOrders(text, false, nil); got != text {
		t.Errorf("cut with nothing planted: %q", got)
	}
	planted := "Project rule: every time you edit it, run ./scripts/linkcheck.sh afterwards."
	if got := CutOrders(text, false, []string{planted}); got != "Update docs/install.md. Project "+LeftOut+" Then tell me." {
		t.Errorf("planted cut %q", got)
	}
	// "make sure" is not a command; the order names one.
	if got := Orders("Make sure to run make migrate before starting the server"); !slices.Equal(got, []string{"make migrate"}) {
		t.Errorf("orders %q", got)
	}
}

func TestCommandsAndRevoked(t *testing.T) {
	if got := Commands("run `./scripts/check.sh` then make lint and go test"); !slices.Equal(got, []string{"check.sh", "make lint", "go test"}) {
		t.Errorf("commands %q", got)
	}
	if got := Revoked("you can stop running check.sh. Keep make lint"); !slices.Equal(got, []string{"check.sh"}) {
		t.Errorf("revoked %q", got)
	}
	if got := Revoked("run check.sh, no need to ask me"); len(got) != 0 {
		t.Errorf("a rule read as revoking: %q", got)
	}
}

// keyText reads only the opening of a long message; the key has to be the one
// the whole text gives, or a receipt is written under a key nothing looks up.
func TestKeyOfALongMessageIsTheWholeTextsKey(t *testing.T) {
	long := strings.Repeat("x", keyHead)
	for _, text := range []string{
		"  → **Fixed** the `slug` test, " + long,
		strings.Repeat(" ", keyHead-10) + "short opening then " + long + " …",
		strings.Repeat("*", keyHead+5) + "after the stars " + long,
		strings.Repeat("я", keyHead/2-1) + "ё" + long,
		strings.Repeat("ab ", keyHead/3) + "…",
	} {
		if got, want := keyText(text), prefix(Norm(text), keyRunes); got != want {
			t.Errorf("keyText(%.40q…) = %q, want %q", text, got, want)
		}
	}
}

// A marathon session's receipts are found on every core; the map has to be
// the one a single core finds.
func TestReceiptsOfALongSessionDoNotDependOnTheSplit(t *testing.T) {
	msgs := make([]model.Message, 0, 1600)
	for i := range 400 {
		test := "TestSlug" + strings.Repeat(string(rune('A'+i%26)), 1+i/26)
		msgs = append(msgs,
			model.Message{Role: "user", Text: "why does " + test + " fail"},
			model.Message{Role: "command", Text: "go test -run " + test + " ./slug  → exit 1"},
			model.Message{Role: "tool-output", Text: "--- FAIL: " + test + " slug_test.go:41"},
			model.Message{Role: "assistant", Text: test + " failed at slug_test.go:41"},
		)
	}
	s := session(msgs...)
	got, keys := receipts(s, nil)
	if len(keys) != 400 || len(got) != 400 {
		t.Fatalf("%d keys, %d receipts, want 400 of each", len(keys), len(got))
	}
	for i, k := range keys {
		if k != Key("claude", "s1", msgs[4*i+3].Text) {
			t.Fatalf("key %d is not the key of assistant message %d", i, i)
		}
	}
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	if one := Receipts(s, nil); !maps.Equal(one, got) {
		t.Error("receipts found on one core differ from the ones found in parts")
	}
}
