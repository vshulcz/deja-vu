package digest

import "testing"

// A person reports a decision as often by naming the state it left things in as
// by naming the act of deciding. These lines are the shapes that were read as
// passing mentions: sampled from a real store, the marker list caught 4% of
// assistant lines, and the misses were declarative like these.
func TestCarriesDecisionReadsAStateAsAConclusion(t *testing.T) {
	concluded := []string{
		"прод-пины теперь ложатся на `deploy/prod`, она сбрасывается на main",
		"бывшая ведущая становится ведомой, роли поменялись",
		"конфиг лежит в /etc/deja и читается на старте",
		"аутентификация работает через прокси, напрямую больше не ходим",
		"retries are 4 by default now",
		"the socket now lives under /run/deja",
	}
	for _, line := range concluded {
		if !CarriesDecision(line) {
			t.Errorf("a line stating what things came to is not read as a conclusion:\n  %q", line)
		}
	}

	// The other half of the bargain: a plan or a passing mention still must not
	// count, or the list stops telling the two apart at all.
	mentions := []string{
		"потом посмотрю про кеш, пока не трогаю",
		"will look at the retries later, not touching them yet",
		"надо будет разобраться с прокси на неделе",
	}
	for _, line := range mentions {
		if CarriesDecision(line) {
			t.Errorf("a passing mention is read as a conclusion:\n  %q", line)
		}
	}
}
