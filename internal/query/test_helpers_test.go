package query

// MatchesQuery applies the text-level part of the query. Index candidates
// use this too, so phrase verification is shared by every search frontend.
func MatchesQuery(text, q string) bool {
	terms, phrases := QueryParts(q)
	return MatchesParts(text, terms, phrases, nil)
}
