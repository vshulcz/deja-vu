package index

import "github.com/vshulcz/deja-vu/internal/query"

// The CJK tokenisation moved to query, which index and prompt both sit on.
// These keep the in-package names the ingestion path and its tests use.
func expandCJKTokens(toks []string) []string { return query.ExpandCJKTokens(toks) }

func cjkFunctionBigram(tok string) bool { return query.CJKFunctionBigram(tok) }
