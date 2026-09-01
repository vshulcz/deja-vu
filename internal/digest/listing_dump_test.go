package digest

import "testing"

// looksLikeListingDump counted path-shaped fields and never read the count, so
// the rule it actually applied was "eight or more words, none of them a
// stopword" — which is an ordinary sentence in any language the stopword list
// does not cover. The list has eighteen English words and twelve Russian ones,
// and half this store is Russian.
//
// Measured over 277375 prose lines of a real store: 20294 dropped, 55 of them
// listings.
func TestASentenceIsNotAListingDump(t *testing.T) {
	prose := []string{
		// Russian, eight or more words, none of them in the stopword list.
		"Монорепа смержена (#1562). Оба внешних PR перенацелены, жду их проверки.",
		"Оба утверждения теперь острее: тест первого запуска слово «forgotten» прямо запрещает.",
		"Хорошо — XDG уважается, значит живой тест изолируется. Пишу защиту.",
		// English written without any of the eighteen: still a sentence.
		"Raised pgbouncer default_pool_size from twenty up through forty, timeouts stopped afterwards.",
	}
	for _, l := range prose {
		if looksLikeListingDump(l) {
			t.Errorf("a sentence was dropped as a listing: %q", l)
		}
	}
}

// The rule still has to do its job. A listing is what it was written for, and
// `ls` inside a directory prints no slashes at all — so bare filenames count.
func TestAListingIsStillARejected(t *testing.T) {
	listings := []string{
		"internal/index/ingest.go internal/index/manifest.go internal/search/recall.go cmd/deja/mcp.go cmd/deja/fix.go cmd/deja/how.go internal/digest/digest.go internal/model/model.go",
		"main.go util.go parser.go writer.go reader.go index.go search.go digest.go",
		"- Repositories (`order_repository.go`, `payment_repository.go`, `profile_repository.go`, `dftype_repository.go`, `wallet_repository.go`, `audit_repository.go`, `session_repository.go`)",
	}
	for _, l := range listings {
		if !looksLikeListingDump(l) {
			t.Errorf("a listing dump got through: %q", l)
		}
	}
}

// The short-line and stopword exits are unchanged, and are what keeps an
// ordinary sentence about files from being read as a list of them.
func TestTheListingRuleKeepsItsEarlierExits(t *testing.T) {
	if looksLikeListingDump("main.go util.go parser.go") {
		t.Error("a three-field line is not a listing")
	}
	if looksLikeListingDump("the files are main.go util.go parser.go writer.go reader.go index.go search.go") {
		t.Error("a sentence with a stopword in it was read as a listing")
	}
}

// A word is letters once the punctuation a sentence puts around one is off.
// Everything an `ls` line is made of is not.
func TestPlainWordFieldNamesOnlyWords(t *testing.T) {
	for _, f := range []string{"pgbouncer", "перенацелены", "(смержена)", "root", "**Домены**"} {
		if !plainWordField(f) {
			t.Errorf("%q is a word", f)
		}
	}
	for _, f := range []string{"internal/index/ingest.go", "main.go", "v5.4.3", "4096", "drwxr-xr-x", "00:00", "a"} {
		if plainWordField(f) {
			t.Errorf("%q is not a word", f)
		}
	}
}

// The shape this regressed on first: `ls -l` output is three words and six
// fields that are not, and the old rule caught it only by accident.
func TestAnLsLineIsStillAListing(t *testing.T) {
	if !looksLikeListingDump("drwxr-xr-x 5 root root 4096 Jan 1 00:00 /usr/lib/x86_64-linux-gnu/libfoo.so.1.2.3") {
		t.Error("an ls -l line got through")
	}
}
