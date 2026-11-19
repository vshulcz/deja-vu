package digest

import (
	"github.com/vshulcz/deja-vu/internal/model"
	"strings"
	"testing"
)

// The last message is always a candidate conclusion, and it was served
// whatever it said. On a 2,285-session store 132 of the lines served as what a
// session settled were deja's own recall echoed back ("deja-vu recalled: …"),
// which is how an agent's earlier answer from memory came back as a fact, and
// 104 were an acknowledgement — "ok", "done", "No response requested."
func TestConclusionsSkipTheRecallEchoAndAcknowledgements(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "why does the nightly export stall"},
		{Role: "assistant", Text: "Root cause: the exporter holds the table lock while it uploads, so the vacuum job waits. Fixed by releasing the lock before the upload."},
		{Role: "assistant", Text: "deja-vu recalled: nightly export stall — confirmed the lock is held for 47 minutes."},
		{Role: "assistant", Text: "ok"},
	}}
	got := Conclusions(s, 800, 3)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "recalled") || strings.Contains(joined, "47 minutes") {
		t.Errorf("deja's own recall was served as a conclusion:\n%s", joined)
	}
	for _, line := range got {
		if strings.TrimSpace(line) == "ok" {
			t.Errorf("an acknowledgement was served as a conclusion: %q", got)
		}
	}
	if !strings.Contains(joined, "Root cause") {
		t.Errorf("the session's real conclusion is missing: %q", got)
	}

	// The echo as the last word, which is where the real ones sit: a session
	// that asked deja and answered with what came back.
	s.Messages = s.Messages[:3]
	got = Conclusions(s, 800, 3)
	joined = strings.Join(got, "\n")
	if strings.Contains(joined, "recalled") || strings.Contains(joined, "47 minutes") {
		t.Errorf("deja's own recall, as the last message, was served as a conclusion:\n%s", joined)
	}
	if !strings.Contains(joined, "Root cause") {
		t.Errorf("the session's real conclusion is missing: %q", got)
	}
}

// The credit line is deja's words, the rest of the message is the agent's. A
// reply that opens with the credit and then does the work keeps the work.
func TestConclusionsKeepTheWorkAfterACreditLine(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "the golden test never runs"},
		{Role: "assistant", Text: "déjà vu: the golden test is gated by a build tag — reusing it (deja:8ab41c37).\n\nFixed: SVC_FIXTURES=$PWD/fixtures GOFLAGS_EXTRA=\"-tags golden\" make test passes, ok example.com/svc/internal/store."},
	}}
	got := Conclusions(s, 800, 2)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "déjà vu:") {
		t.Errorf("the credit line was served as the conclusion: %q", got)
	}
	if !strings.Contains(joined, "make test passes") {
		t.Errorf("the work after the credit line was dropped: %q", got)
	}
}

// A usage-limit notice or an API error is written by the harness in the
// assistant's place, and it ends the turn, so it was served as what the turn
// settled.
func TestConclusionsSkipHarnessNotices(t *testing.T) {
	for _, notice := range []string{
		"You've hit your session limit · resets 2:40am (Europe/Moscow)",
		"You've reached your Fable limit. Run /usage-credits to continue or switch models with /model.",
		"API Error: 529 Overloaded",
	} {
		s := model.Session{Messages: []model.Message{
			{Role: "user", Text: "why does the nightly export stall"},
			{Role: "assistant", Text: "Root cause: the exporter holds the table lock while it uploads. Fixed by releasing the lock before the upload."},
			{Role: "assistant", Text: notice},
		}}
		joined := strings.Join(Conclusions(s, 800, 3), "\n")
		if strings.Contains(joined, notice[:12]) {
			t.Errorf("a harness notice was served as a conclusion: %q", joined)
		}
		if !strings.Contains(joined, "Root cause") {
			t.Errorf("the session's real conclusion is missing next to %q: %q", notice, joined)
		}
	}
}

func TestConclusionsSurfacesTheOutcomeNewestFirst(t *testing.T) {
	s := model.Session{Messages: []model.Message{
		{Role: "user", Text: "the deploy keeps failing with a 502"},
		{Role: "assistant", Text: "Checking the logs now."},
		{Role: "assistant", Text: "Root cause: the health check hit / instead of /healthz, so the balancer killed pods mid-rollout. Fixed by pointing the probe at /healthz."},
		{Role: "assistant", Text: "Deploy passes now, three consecutive green rollouts."},
	}}
	got := Conclusions(s, 800, 3)
	if len(got) == 0 {
		t.Fatal("no conclusions from a session that states one")
	}
	// Newest first: the outcome outranks the first thing tried.
	if !strings.Contains(got[0], "passes now") {
		t.Errorf("expected the outcome first, got %q", got[0])
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "Root cause") {
		t.Errorf("the root-cause line is missing: %v", got)
	}
}

func TestConclusionsHonoursBudgetAndCount(t *testing.T) {
	ms := make([]model.Message, 0, 10)
	for range 10 {
		ms = append(ms, model.Message{Role: "assistant", Text: "The fix was to bump the timeout, and it works now."})
	}
	if got := Conclusions(model.Session{Messages: ms}, 4000, 2); len(got) > 2 {
		t.Errorf("max=2 not honoured: %d lines", len(got))
	}
	total := 0
	for _, c := range Conclusions(model.Session{Messages: ms}, 120, 5) {
		total += len(c)
	}
	if total > 120 {
		t.Errorf("budget=120 exceeded: %d bytes", total)
	}
}

func TestConclusionsEmptyForNothingToConclude(t *testing.T) {
	if got := Conclusions(model.Session{}, 500, 3); len(got) != 0 {
		t.Errorf("empty session produced %v", got)
	}
	userOnly := model.Session{Messages: []model.Message{{Role: "user", Text: "hello there"}}}
	if got := Conclusions(userOnly, 500, 3); len(got) != 0 {
		t.Errorf("a session with no assistant turn produced %v", got)
	}
	if got := Conclusions(model.Session{Messages: []model.Message{{Role: "assistant", Text: "x"}}}, 0, 3); len(got) != 0 {
		t.Errorf("zero budget produced %v", got)
	}
}

func TestFirstSentencesKeepsTheOpeningClaim(t *testing.T) {
	in := "Root cause found. The lock was never released. Everything after this is detail that recall does not pay for."
	got := firstSentences(in, 2)
	if !strings.HasPrefix(got, "Root cause found.") || strings.Contains(got, "does not pay") {
		t.Errorf("firstSentences = %q", got)
	}
	// A version number is not a sentence end.
	if got := firstSentences("Upgraded to v1.2 and the crash stopped.", 1); !strings.Contains(got, "crash stopped") {
		t.Errorf("split on a version number: %q", got)
	}
}

func TestAnnouncesPlanAndCarriesUserCorrection(t *testing.T) {
	if !AnnouncesPlan("Понял, пересобираю картину.") {
		t.Error("expected AnnouncesPlan to match Russian rebuild opener")
	}
	if !AnnouncesPlan("Сейчас проверю конфиг.") {
		t.Error("expected AnnouncesPlan to match Russian check opener")
	}
	if AnnouncesPlan("Архитектура: envoy 95410 -> grpc -> 203.0.113.10") {
		t.Error("expected AnnouncesPlan to reject architectural conclusion")
	}
	if !CarriesDecision("Архитектура: envoy 95410 -> grpc -> 203.0.113.10") {
		t.Error("expected CarriesDecision to recognize Russian architecture summary")
	}
	if !CarriesUserCorrection("причем тут стейджинг, у меня два разных контура — один для витрины, второй для биллинга") {
		t.Error("expected CarriesUserCorrection to recognize user disambiguation")
	}
	if CarriesUserCorrection("почему тормозит загрузка") {
		t.Error("expected CarriesUserCorrection to be false for ordinary question")
	}
}

func TestConclusionsPromotesTrajectoryVerifiedSynthesisWithoutMarkers(t *testing.T) {
	s := model.Session{
		ID:      "traj-conclusions",
		Harness: "claude",
		Messages: []model.Message{
			{Role: "user", Text: "fix the race in manifest writing"},
			{Role: "assistant", Text: "Looking into the manifest writer."},
			{Role: "files", Text: "internal/index/sync.go"},
			{Role: "edit", Text: "internal/index/sync.go"},
			{Role: "command", Text: "go test ./internal/index/... → exit 0"},
			// No word from decisionMarkers ("fixed", "because", "решили", etc.),
			// but structurally the turn-final synthesis after Edit + passing command.
			{Role: "assistant", Text: "Wrapped `writeManifest` in `internal/index/sync.go` with `flock` to serialize concurrent writers."},
			{Role: "user", Text: "show me the struct"},
			{Role: "assistant", Text: "Here is the struct:\n```go\ntype Manifest struct{}\n```"},
		},
	}
	got := Conclusions(s, 300, 1)
	if len(got) == 0 || !strings.Contains(got[0], "Wrapped `writeManifest`") {
		t.Fatalf("Conclusions = %v, want trajectory-verified synthesis to outrank trailing code-fence turn", got)
	}
}
