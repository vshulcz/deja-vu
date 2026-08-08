package index

import (
	"math"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

// PlanCooccurrence is one factual join between a recurring wall and a plan
// step's informative terms. The wall side is the claim: a census of one
// personal index found a matching command logged alongside the wall for only
// 1 of its 19 walls, and 84 of its 110 carrier sessions hold no command
// record at all, so requiring one would reject nearly every real recurrence.
// Command and CommandSessions are a strengthening when a matching command
// happens to exist, not a precondition — Command is "" when none was found.
type PlanCooccurrence struct {
	Wall            Friction
	Command         string
	CommandSessions []SessionMeta
}

type planWallCluster struct {
	hash  uint64
	metas []SessionMeta
}

type planIndexedTerm struct {
	text     string
	sessions map[uint32]bool
}

// planIdentifierIDFFloor is the informativeness bar for an identifier-shaped
// plan term — lower than dejaVuIDFFloor, but not zero. A census of 2,886 real
// sessions found identifier-shaped false-positive anchors (cannot, directory,
// branch, runner) topping out at idf 0.70, while true anchors (hermes 1.09,
// hermes-agent 1.21) started at 1.09; this floor sits in the gap.
const planIdentifierIDFFloor = 1.0

// PlanFrictionMatches joins existing manifest wall hashes to a plan step's
// informative terms — the wall text naming one of them is the finding; a
// matching command record is looked up too but only strengthens it (see
// PlanCooccurrence). keep is applied before any session is materialized, so
// callers can enforce activation policy without denied text crossing the
// boundary.
//
// The index is read as-is. This function never builds, repairs, or refreshes
// it.
func PlanFrictionMatches(dir string, steps [][]string, keep func(SessionMeta) bool, limit int) []PlanCooccurrence {
	if dir == "" {
		dir = DefaultDir()
	}
	if len(steps) == 0 {
		return nil
	}

	unlock, locked, err := tryLockDir(dir)
	if err != nil {
		return nil
	}
	if locked {
		defer unlock()
	}

	manifest, err := readManifestCached(dir)
	if err != nil || len(manifest.Sessions) == 0 || !recordsIntact(dir, manifest) {
		return nil
	}

	clusters := planWallClusters(manifest, keep)
	if len(clusters) == 0 {
		return nil
	}

	indexedSteps, candidates, needs := planIndexedSteps(dir, manifest, steps)
	if len(indexedSteps) == 0 {
		return nil
	}

	// The first cut is manifest and posting only: a wall survives when one of
	// its carrier sessions has at least the plan step's informative-term
	// floor. carriers records, per eligible cluster, which of its own metas
	// were the ones that actually matched — not the whole cluster — so the
	// session read below stays scoped to what earned it.
	var eligible []planWallCluster
	carriers := map[uint64][]SessionMeta{}
	for _, cluster := range clusters {
		var hit []SessionMeta
		for _, meta := range cluster.metas {
			for step := range indexedSteps {
				if candidates[step][meta.Ord] {
					hit = append(hit, meta)
					break
				}
			}
		}
		if len(hit) > 0 {
			eligible = append(eligible, cluster)
			carriers[cluster.hash] = hit
		}
	}
	if len(eligible) == 0 {
		return nil
	}

	// Wall text recovery reads full sessions too, but only this cluster's
	// own carriers and it stops at the first hit — cheaper than
	// materializing every candidate session for a command search that most
	// carriers will never satisfy. Doing it first lets the mandatory
	// cooccurrence check (the wall text itself naming a plan term) prune the
	// set before the command pass touches anything.
	type wallHit struct {
		cluster planWallCluster
		text    string
	}
	var hits []wallHit
	candidateMetas := map[string]SessionMeta{}
	for _, cluster := range eligible {
		wanted := map[uint64]string{cluster.hash: ""}
		frictionTexts(dir, manifest, cluster.metas, wanted, cluster.hash)
		text := wanted[cluster.hash]
		if text == "" || !planWallSharesTerm(text, indexedSteps) {
			continue
		}
		hits = append(hits, wallHit{cluster: cluster, text: text})
		for _, meta := range carriers[cluster.hash] {
			candidateMetas[meta.Harness+":"+meta.ID] = meta
		}
	}
	if len(hits) == 0 {
		return nil
	}

	metas := make([]SessionMeta, 0, len(candidateMetas))
	for _, meta := range candidateMetas {
		metas = append(metas, meta)
	}
	sort.Slice(metas, func(i, j int) bool {
		return newestFirstMeta(metas[i], metas[j])
	})

	sessions, err := sessionsForMetas(dir, metas)
	if err != nil {
		return nil
	}

	// No hash-to-command index exists. Materialize only sessions carrying a
	// wall that already cleared the cooccurrence check, then look for a
	// stored command carrying the same step's informative terms. This is a
	// strengthening pass: a step with no matching command simply leaves
	// Command empty, it does not drop the finding (see PlanCooccurrence).
	commands := map[string]map[int]string{}
	for i, session := range sessions {
		if i >= len(metas) {
			break
		}
		meta := metas[i]
		key := meta.Harness + ":" + meta.ID
		for step, terms := range indexedSteps {
			if !candidates[step][meta.Ord] {
				continue
			}
			if command := planCommandForStep(session, terms, needs[step]); command != "" {
				if commands[key] == nil {
					commands[key] = map[int]string{}
				}
				commands[key][step] = command
			}
		}
	}

	var out []PlanCooccurrence
	for _, hit := range hits {
		command, commandSessions := planWallCommand(
			hit.text, hit.cluster, indexedSteps, commands,
		)

		out = append(out, PlanCooccurrence{
			Wall: Friction{
				Text:     hit.text,
				Sessions: hit.cluster.metas,
				Last:     hit.cluster.metas[0].Updated,
			},
			Command:         command,
			CommandSessions: commandSessions,
		})
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// planWallSharesTerm reports whether the wall's own text names one of the
// plan's informative terms. This is the cooccurrence claim itself, so unlike
// a command match it is never optional.
func planWallSharesTerm(wallText string, steps [][]planIndexedTerm) bool {
	for _, terms := range steps {
		for _, term := range terms {
			if planTextHasTerm(wallText, term.text) {
				return true
			}
		}
	}
	return false
}

func planWallClusters(manifest Manifest, keep func(SessionMeta) bool) []planWallCluster {
	byHash := map[uint64][]SessionMeta{}
	for _, meta := range manifest.Sessions {
		if keep != nil && !keep(meta) {
			continue
		}
		for _, hash := range meta.Hit {
			byHash[hash] = append(byHash[hash], meta)
		}
	}

	var clusters []planWallCluster
	for hash, metas := range byHash {
		if len(metas) < FrictionMinSessions {
			continue
		}
		sort.Slice(metas, func(i, j int) bool {
			return newestFirstMeta(metas[i], metas[j])
		})
		clusters = append(clusters, planWallCluster{hash: hash, metas: metas})
	}
	sort.Slice(clusters, func(i, j int) bool {
		if len(clusters[i].metas) != len(clusters[j].metas) {
			return len(clusters[i].metas) > len(clusters[j].metas)
		}
		left, right := clusters[i].metas[0], clusters[j].metas[0]
		if !left.Updated.Equal(right.Updated) {
			return left.Updated.After(right.Updated)
		}
		return clusters[i].hash < clusters[j].hash
	})
	return clusters
}

func planIndexedSteps(dir string, manifest Manifest, steps [][]string) ([][]planIndexedTerm, []map[uint32]bool, []int) {
	var indexed [][]planIndexedTerm
	var candidates []map[uint32]bool
	var needs []int
	totalDocs := float64(len(manifest.Sessions)) + 1

	for _, step := range steps {
		// A step naming an identifier is as specific on one word as an
		// ordinary step is on two — cmd/deja's hook-prompt path already
		// trusts a lone identifier hit, and this join follows the same call
		// so a plan step doesn't need a stronger bar than a prompt does.
		need := 2
		if planHasIdentifierTerm(step) {
			need = 1
		}

		seen := map[string]bool{}
		var terms []planIndexedTerm
		for _, raw := range step {
			term := strings.ToLower(strings.TrimSpace(raw))
			if term == "" || seen[term] {
				continue
			}
			seen[term] = true

			sessions := planTermSessions(dir, term)
			if len(sessions) == 0 {
				continue
			}
			// A wall's own vocabulary is repeated-work vocabulary, so it is
			// common by construction — a census against the real corpus
			// measured "hermes" at idf 1.10 and "pytest" at idf 1.23, both
			// under the full floor, and every one of ~14 known-good plan
			// recurrences was lost to it. An identifier is informative by
			// shape (a name, not a sentence word), not by rarity, so it gets
			// a lower bar — but not none: re-measured at 2,886 sessions,
			// identifier-shaped false positives (cannot, directory, branch,
			// runner) topped out at idf 0.70 while true anchors (hermes
			// 1.09, hermes-agent 1.21) started at 1.09, so
			// planIdentifierIDFFloor sits at the gap between them.
			// Non-identifier terms still need the full floor.
			floor := dejaVuIDFFloor
			if planIsIdentifierTerm(term) {
				floor = planIdentifierIDFFloor
			}
			idf := math.Log(totalDocs / float64(len(sessions)+1))
			if idf < floor && len(sessions) > 2 {
				continue
			}
			terms = append(terms, planIndexedTerm{
				text:     term,
				sessions: sessions,
			})
		}
		if len(terms) < need {
			continue
		}

		counts := map[uint32]int{}
		for _, term := range terms {
			for ord := range term.sessions {
				counts[ord]++
			}
		}
		matched := map[uint32]bool{}
		for ord, count := range counts {
			if count >= need {
				matched[ord] = true
			}
		}
		if len(matched) == 0 {
			continue
		}
		indexed = append(indexed, terms)
		candidates = append(candidates, matched)
		needs = append(needs, need)
	}
	return indexed, candidates, needs
}

// planHasIdentifierTerm reports whether any term in a step is
// identifier-shaped (see planIsIdentifierTerm). Used to size the step's need
// (1 term vs. 2) at both the raw-term-selection layer (cmd/deja) and here.
func planHasIdentifierTerm(terms []string) bool {
	for _, t := range terms {
		if planIsIdentifierTerm(t) {
			return true
		}
	}
	return false
}

// planIsIdentifierTerm mirrors cmd/deja's hasIdentifierTerm at the single-term
// level: a term is identifier-shaped when it is long (>=6) or carries a
// symbol/digit an English word wouldn't. Duplicated rather than imported
// because cmd/deja depends on internal/index, not the other way around.
func planIsIdentifierTerm(t string) bool {
	if len(t) >= 6 {
		return true
	}
	for _, r := range t {
		if r == '_' || r == '.' || r == '/' || r == '-' || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

func planTermSessions(dir, term string) map[uint32]bool {
	keys := queryKeys(term)
	if len(keys) == 0 {
		return nil
	}

	var intersection map[uint32]bool
	for _, key := range keys {
		postings, err := postingsFor(dir, key)
		if err != nil || len(postings) == 0 {
			return nil
		}
		current := map[uint32]bool{}
		for _, posting := range postings {
			current[posting.Sid] = true
		}
		if intersection == nil {
			intersection = current
			continue
		}
		for ord := range intersection {
			if !current[ord] {
				delete(intersection, ord)
			}
		}
		if len(intersection) == 0 {
			return nil
		}
	}
	return intersection
}

func planCommandForStep(session model.Session, terms []planIndexedTerm, need int) string {
	best := ""
	bestMatches := 0
	for _, message := range session.Messages {
		if message.Role != roleCommand {
			continue
		}
		command := strings.Join(strings.Fields(message.Text), " ")
		if command == "" {
			continue
		}
		matched := 0
		for _, term := range terms {
			if planTextHasTerm(command, term.text) {
				matched++
			}
		}
		if matched < need {
			continue
		}
		if matched > bestMatches ||
			(matched == bestMatches && (best == "" || len(command) < len(best))) ||
			(matched == bestMatches && len(command) == len(best) && command < best) {
			best = command
			bestMatches = matched
		}
	}
	return best
}

func planWallCommand(
	wallText string,
	cluster planWallCluster,
	steps [][]planIndexedTerm,
	commands map[string]map[int]string,
) (string, []SessionMeta) {
	groups := map[string]map[string]SessionMeta{}
	for step, terms := range steps {
		sharesTerm := false
		for _, term := range terms {
			if planTextHasTerm(wallText, term.text) {
				sharesTerm = true
				break
			}
		}
		if !sharesTerm {
			continue
		}
		for _, meta := range cluster.metas {
			key := meta.Harness + ":" + meta.ID
			command := commands[key][step]
			if command == "" {
				continue
			}
			if groups[command] == nil {
				groups[command] = map[string]SessionMeta{}
			}
			groups[command][key] = meta
		}
	}

	best := ""
	bestCount := 0
	for command, sessions := range groups {
		if len(sessions) > bestCount ||
			(len(sessions) == bestCount && (best == "" || command < best)) {
			best = command
			bestCount = len(sessions)
		}
	}
	if best == "" {
		return "", nil
	}

	selected := groups[best]
	out := make([]SessionMeta, 0, len(selected))
	for _, meta := range cluster.metas {
		if _, ok := selected[meta.Harness+":"+meta.ID]; ok {
			out = append(out, meta)
		}
	}
	return best, out
}

func planTextHasTerm(text, term string) bool {
	terms, phrases := query.QueryParts(term)
	if len(terms) == 0 && len(phrases) == 0 {
		return false
	}
	return query.MatchesParts(text, terms, phrases, nil)
}
