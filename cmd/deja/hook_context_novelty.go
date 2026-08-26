package main

import (
	"os"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// hookProjectKey is the project name the session-start hook records against,
// the same first candidate the per-prompt path uses so the two share one seen
// list rather than each keeping half the picture.
// It is deliberately not the same key the per-prompt path writes. The two
// surfaces answer different questions — session start says "here is where you
// left off", per-prompt says "this exact question was settled before" — and
// sharing one cooldown let the start use up the prompt's: a session shown as
// context could no longer be served as an answer, which took two existing
// tests red for the right reason.
func hookProjectKey() string {
	cwd := os.Getenv("CLAUDE_PROJECT_DIR")
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	cands := digest.ProjectNameCandidates(cwd)
	if len(cands) == 0 {
		return ""
	}
	return sessionStartKeyPrefix + cands[0]
}

// sessionStartKeyPrefix separates what a session start was told from what a
// prompt was answered with, in the one file that records both.
const sessionStartKeyPrefix = "start:"

// sessionStartWindow is how far back "this project has already been told that"
// reaches at session start. Wider than the per-prompt cooldown because the
// events are rarer: a session start happens a few times a day, a prompt many
// times an hour, so ten of them would be a single afternoon.
const sessionStartWindow = 40

// leadWithUnseen reorders the session-start candidates so the agent hears
// something it has not been told before, without ever going quiet.
//
// Measured before this existed: eight consecutive session starts served the
// same three sessions, every time — 87.5% repeats (#2038). The agent is told to
// say nothing about a recall that did not help, so the eighth serving of the
// same three bought silence, and the silence was counted against deja.
//
// The order is deliberate and it is not "newest first":
//
//  1. What this project has not been shown recently, in the order the ranking
//     already put it. Continuity is still the point of a session start — the
//     first serving of "here is where you left off" is the valuable one.
//  2. Then what it has been shown, ordered by how often agents have *asked*
//     for it. Demand is evidence; being pushed is not, which is why worn is
//     built from pulls only (usage.servedKind).
//
// Nothing is dropped. A start with no memory is worse than a repeat, and a
// project whose whole recent tail has been served still deserves its best
// candidate — it simply goes behind anything newer to say.
func leadWithUnseen(dir string, projects []string, ss []model.Session) []model.Session {
	if len(ss) < 2 {
		return ss
	}
	seen := map[string]bool{}
	for _, p := range projects {
		for id := range recentlyInjectedInProject(dir, sessionStartKeyPrefix+p, sessionStartWindow) {
			seen[id] = true
		}
	}
	if len(seen) == 0 {
		return ss
	}
	worn := usage.WornSessions(dir)
	unseen := make([]model.Session, 0, len(ss))
	repeats := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if seen[s.ID] {
			repeats = append(repeats, s)
			continue
		}
		unseen = append(unseen, s)
	}
	if len(unseen) == 0 || len(repeats) == 0 {
		return ss
	}
	// Stable by demand so the repeat that does get through is the one agents
	// keep coming back to, rather than whichever happened to be newest.
	stableSortByDemand(repeats, worn)
	return append(unseen, repeats...)
}

// stableSortByDemand orders sessions by how many agent-initiated recalls asked
// for them, keeping the incoming order among equals.
func stableSortByDemand(ss []model.Session, worn map[string]int) {
	if len(worn) == 0 {
		return
	}
	// Insertion sort: these lists are a handful of sessions long, and it keeps
	// equal elements in the order the ranking above chose.
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && worn[ss[j].ID] > worn[ss[j-1].ID]; j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}
