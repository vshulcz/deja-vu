package main

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

func TestChangeIdentPartsAndQueryTerms(t *testing.T) {
	parts := changeIdentParts("QuerySet.order_by XMLParser MaxIdle2Conns")
	for _, want := range []string{"queryset", "query", "set", "order", "xmlparser", "xml", "parser", "maxidle2conns", "max", "idle2", "conns"} {
		if !slices.Contains(parts, want) {
			t.Fatalf("changeIdentParts missing %q in %v", want, parts)
		}
	}

	qTerms := changeQueryTerms("Fix TypeError in QuerySet.order_by with MaxIdleConns")
	for _, want := range []string{"typeerror", "queryset.order_by", "maxidleconns", "order_by", "conns"} {
		if !slices.Contains(qTerms, want) {
			t.Fatalf("changeQueryTerms missing %q in %v", want, qTerms)
		}
	}
}

func TestIsTestChangePath(t *testing.T) {
	for _, p := range []string{
		"tests/models/test_query.py",
		"pkg/auth/token_test.go",
		"frontend/__tests__/form.ts",
		"src/app.spec.ts",
		"app/tests.py",
	} {
		if !isTestChangePath(p) {
			t.Errorf("isTestChangePath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{
		"pkg/db/pool.go",
		"core/models/query.py",
		"src/testing_helper/client.go",
	} {
		if isTestChangePath(p) {
			t.Errorf("isTestChangePath(%q) = true, want false", p)
		}
	}
}

func TestRankChangesForQueryPutsRelevantSourceFirst(t *testing.T) {
	cs := []sources.Change{
		{Path: "pkg/noise/unrelated.go", Old: "x := 1", New: "x := 2"},
		{Path: "tests/cache/cache_test.go", Old: "MaxIdleConns := 2", New: "MaxIdleConns := 8"},
		{Path: "pkg/db/pool.go", Old: "MaxIdleConns: 2", New: "MaxIdleConns: 16"},
	}
	ranked := rankChangesForQuery(cs, "MaxIdleConns pool timeout")
	if len(ranked) != 3 {
		t.Fatalf("got %d ranked items, want 3", len(ranked))
	}
	if ranked[0].Path != "pkg/db/pool.go" {
		t.Fatalf("ranked[0] = %q, want pkg/db/pool.go (relevant source before unrelated and test)", ranked[0].Path)
	}
	if ranked[2].Path != "tests/cache/cache_test.go" {
		t.Fatalf("ranked[2] = %q, want tests/cache/cache_test.go (source files before tests)", ranked[2].Path)
	}

	// Multiple edits in the same file must stay in their original relative order.
	sameFile := []sources.Change{
		{Path: "pkg/db/pool.go", Old: "a := 1", New: "a := 2"},
		{Path: "pkg/db/pool.go", Old: "b := 1", New: "b := MaxIdleConns"},
	}
	rankedSame := rankChangesForQuery(sameFile, "MaxIdleConns")
	if rankedSame[0].New != "a := 2" || rankedSame[1].New != "b := MaxIdleConns" {
		t.Fatalf("same-file edits reordered unexpectedly: %+v", rankedSame)
	}
}

func TestChangesBindToQueryRejectsGenericNoiseAndAcceptsRealMatch(t *testing.T) {
	unrelatedSameRepo := []sources.Change{
		{
			Path: "widget/forms/choices.py",
			Old:  "def label(self):\n    return self.title",
			New:  "def label(self):\n    return str(self.title)",
		},
	}
	if changesBindToQuery(unrelatedSameRepo, "TypeError in QuerySet.order_by on EmptyQuerySet") {
		t.Fatalf("expected unrelated change not to bind to query")
	}

	// Single generic suffix overlap ("error") must not bind CamelCase "TypeError".
	valueErrChange := []sources.Change{
		{
			Path: "widget/core/parser.py",
			Old:  "raise Exception('bad')",
			New:  "raise ValueError('bad parser token')",
		},
	}
	if changesBindToQuery(valueErrChange, "TypeError serializer crashes on header") {
		t.Fatalf("expected TypeError query not to bind via Error suffix alone to ValueError")
	}

	bound := []sources.Change{
		{
			Path: "widget/models/sql/query.py",
			Old:  "def order_by(self, *cols):\n    return cols",
			New:  "def order_by(self, *cols):\n    if isinstance(self, EmptyQuerySet):\n        return []\n    return cols",
		},
	}
	if !changesBindToQuery(bound, "TypeError in QuerySet.order_by on EmptyQuerySet") {
		t.Fatalf("expected matching diff with order_by and EmptyQuerySet to bind")
	}

	sess := model.Session{
		ID:      "bind-sess",
		Harness: "aider",
		Messages: []model.Message{
			{Role: "edit", Text: "widget/forms/choices.py\nreturn self.title"},
			{Role: "wrote", Text: "widget/forms/choices.py\nreturn str(self.title)"},
		},
	}
	if blk := changesBlockFor(sess, changesBlockMin, "QuerySet.order_by EmptyQuerySet", true); blk != "" {
		t.Fatalf("expected requireBind=true to suppress unbound changes block, got %q", blk)
	}
	if blk := changesBlockFor(sess, changesBlockMin, "QuerySet.order_by EmptyQuerySet", false); blk == "" {
		t.Fatalf("expected requireBind=false to keep changes block")
	}

	hunkContextChange := []sources.Change{
		{
			Path: "widget/math/ops.py",
			Old:  "def compute_hyperbolic(x):\n    return 0",
			New:  "def compute_hyperbolic(x):\n    return 1",
		},
	}
	if !changesBindToQuery(hunkContextChange, "wrong result from compute_hyperbolic on negative input") {
		t.Fatalf("expected unchanged hunk signature compute_hyperbolic to bind to query")
	}

	// Plural path stem matches singular compound query term when hunk hits once.
	sitemapChange := []sources.Change{
		{
			Path: "widget/contrib/sitemaps/__init__.py",
			Old:  "    try:\n        return max([self.lastmod(item) for item in self.items()])\n    except TypeError:\n        return None",
			New:  "    try:\n        return max([self.lastmod(item) for item in self.items()])\n    except (TypeError, ValueError):\n        return None",
		},
	}
	if !changesBindToQuery(sitemapChange, "Add lastmod support to sitemapindex") {
		t.Fatalf("expected sitemap path stem + lastmod hunk hit to bind")
	}

	// Generic Python exceptions and boilerplate literals must not bind a 7-term unrelated query.
	exceptionOnlyChange := []sources.Change{
		{
			Path: "widget/config/loader.py",
			Old:  "try:\n    return load_cfg()\nexcept ImportError:\n    return False",
			New:  "try:\n    return load_cfg()\nexcept ImportError:\n    return True",
		},
	}
	if changesBindToQuery(exceptionOnlyChange, "Serialize nested queryset filter fails with ImportError returning false") {
		t.Fatalf("expected generic ImportError/return/false overlap not to bind unrelated 7-term query")
	}
}

func TestRecallCodeChangedLinesAlignsWindowOnMatchedLine(t *testing.T) {
	var oldLines, newLines []string
	for i := 1; i <= 14; i++ {
		line := fmt.Sprintf("val%d := %d", i, i)
		oldLines = append(oldLines, line)
		if i == 12 {
			newLines = append(newLines, "retryBudget := clampRetryBudget(cfg.MaxAttempts)")
		} else {
			newLines = append(newLines, fmt.Sprintf("val%d := %d", i, i+100))
		}
	}
	s := model.Session{
		ID:      "align-diff",
		Harness: "aider",
		Messages: []model.Message{
			{Role: "edit", Text: "internal/client/retry.go\n" + strings.Join(oldLines, "\n")},
			{Role: "wrote", Text: "internal/client/retry.go\n" + strings.Join(newLines, "\n")},
		},
	}
	got := recallCodeChangedLines(s, "clampRetryBudget")
	joined := strings.Join(got, "")
	if !strings.Contains(joined, "clampRetryBudget") {
		t.Fatalf("expected excerpt aligned around matched diff line, got:\n%s", joined)
	}
}

func TestChangeDiffSplitsDistantEditsInLargeFile(t *testing.T) {
	var oldLines, newLines []string
	for i := 1; i <= 100; i++ {
		line := fmt.Sprintf("def fn_%03d(x): return x + %d", i, i)
		oldLines = append(oldLines, line)
		switch i {
		case 5:
			newLines = append(newLines, "def fn_005(x): return normalize_pivot(x)")
		case 80:
			newLines = append(newLines, "def fn_080(x): return cache_sparse_factor(x)")
		default:
			newLines = append(newLines, line)
		}
	}
	got := changeDiff(strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"))
	if !strings.Contains(got, "normalize_pivot") || !strings.Contains(got, "cache_sparse_factor") {
		t.Fatalf("expected both distant edits in diff, got:\n%s", got)
	}
	if !strings.Contains(got, "\n  …\n") {
		t.Fatalf("expected distant edits to be split by context separator, got:\n%s", got)
	}
	if strings.Contains(got, "fn_040") {
		t.Fatalf("expected unchanged middle lines around line 40 to be elided, got:\n%s", got)
	}
}

func TestFormatChangeDiffBoundedPreservesAddedFixUnderLargeDeletion(t *testing.T) {
	oldLines := make([]string, 0, 64)
	oldLines = append(oldLines, "func keepBefore() {}", "func header() {}")
	for i := 1; i <= 60; i++ {
		oldLines = append(oldLines, fmt.Sprintf("var deprecatedSlot%02d = %d // obsolete legacy buffer entry padding", i, i))
	}
	oldLines = append(oldLines, "return nil", "func keepAfter() {}")
	newLines := []string{"func keepBefore() {}", "func header() {}", "return applySparsePivotFix(ctx)", "func keepAfter() {}"}

	got := formatChangeDiffBounded(sources.Change{
		Path: "solver/pivot.go",
		Old:  strings.Join(oldLines, "\n"),
		New:  strings.Join(newLines, "\n"),
	}, "applySparsePivotFix", 1100)
	if !strings.Contains(got, "+ return applySparsePivotFix(ctx)") {
		t.Fatalf("expected added fix to survive large deletion cap, got:\n%s", got)
	}
	if !strings.Contains(got, "lines deleted)") {
		t.Fatalf("expected deletion summary marker, got:\n%s", got)
	}
}

func TestChangesBindToQueryWithIDFRejectsLowIDFSameRepoNoise(t *testing.T) {
	sameRepoOtherBug := []sources.Change{
		{
			Path: "/testbed/sympy/solvers/matrix_solver.py",
			Old:  "def solve_matrix(expr, dtype):\n    return expr",
			New:  "def solve_matrix(expr, dtype):\n    if dtype is None:\n        return simplify(expr)\n    return expr",
		},
	}
	q := "sympy matrix solver fails on sparse_cholesky with invalid_pivot inside block_diagonal"
	lowIDF := map[string]float64{
		"sympy":    0.5,
		"matrix":   1.1,
		"solver":   1.2,
		"sparse":   2.4,
		"cholesky": 3.9,
		"invalid":  2.2,
		"pivot":    3.1,
		"block":    1.8,
		"diagonal": 2.7,
		"fails":    0.9,
	}
	if changesBindToQueryWithIDF(sameRepoOtherBug, q, lowIDF) {
		t.Fatalf("expected low-IDF same-repo overlap (sympy/matrix/solver) not to bind unrelated issue")
	}

	matchingFix := []sources.Change{
		{
			Path: "/testbed/sympy/matrices/block_diagonal.py",
			Old:  "def cholesky_pivot(block):\n    return None",
			New:  "def cholesky_pivot(block):\n    return invalid_pivot_guard(block)",
		},
	}
	if !changesBindToQueryWithIDF(matchingFix, q, lowIDF) {
		t.Fatalf("expected high-IDF matching fix to bind")
	}
}

func TestChangeNonRootPathStripsSourceLayouts(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"lib/matplotlib/colorbar.py", "colorbar.py"},
		{"src/_pytest/skipping.py", "skipping.py"},
		{"/testbed/sympy/sympy/parsing/sympy_parser.py", "parsing/sympy_parser.py"},
		{"widget/contrib/sitemaps/__init__.py", "contrib/sitemaps/__init__.py"},
	}
	for _, tc := range cases {
		if got := changeNonRootPath(tc.in); got != tc.want {
			t.Errorf("changeNonRootPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestChangesBindToQueryWithIDFIgnoresDocsAndDeduplicatesParts(t *testing.T) {
	// Doc/changelog files must be skipped when primary code files exist, and
	// repeated compound tokens matching the same single low-IDF sub-token
	// ("figure" from plt.figure, figure.clear, figure) must count once.
	cs := []sources.Change{
		{
			Path: "changelog/10811.bugfix.rst",
			Old:  "",
			New:  "Fixed minimal problem when importing plugin configuration.",
		},
		{
			Path: "lib/matplotlib/figure.py",
			Old:  "def clear(self):\n    pass",
			New:  "def clear(self):\n    self.figure = None",
		},
	}
	q := strings.Join([]string{
		"plt.figure crashes when figure.clear is called after figure setup",
		"Minimal example reproducing problem with colorbar rendering and figure layout",
		"Expected colorbar output without figure state corruption across subplots",
	}, "\n")
	idf := map[string]float64{
		"figure":    2.2,
		"plt":       1.4,
		"minimal":   2.5,
		"problem":   2.4,
		"colorbar":  2.9,
		"rendering": 2.7,
		"layout":    2.3,
		"subplots":  2.6,
	}
	if changesBindToQueryWithIDF(cs, q, idf) {
		t.Fatalf("expected doc changelog + single deduplicated ubiquitous token not to bind long unrelated issue")
	}
}

func TestChangesBindToQueryWithSmallStoreTopIDF(t *testing.T) {
	// On an 8-session store (TopIDF = ln(9/2) ≈ 1.504 < 2.0), short queries
	// with df=1 anchors bind, long prose/path-only overlap stays blocked, and
	// long bug reports bind when a df=1 code identifier appears in changed code.
	topIDF := math.Log(9.0 / 2.0)
	smallIDF := map[string]float64{
		"reduced_totient":   topIDF,
		"modular_order":     topIDF,
		"residue":           topIDF,
		"totient":           topIDF,
		"modular":           topIDF,
		"reproducing":       topIDF,
		"reproducing_notes": topIDF,
		"figure":            0.55,
		"problem":           0.55,
		"example":           0.55,
	}
	fix := []sources.Change{
		{
			Path: "sympy/ntheory/residue_ntheory.py",
			Old:  "def reduced_totient(n):\n    return 1",
			New:  "def reduced_totient(n):\n    return modular_order(n)",
		},
	}
	goodQ := "reduced_totient wrong modular result in residue calculation"
	if !changesBindToQueryWithTopIDF(fix, goodQ, smallIDF, topIDF) {
		t.Fatalf("expected short query with df=1 anchors on 8-session store to bind")
	}

	proseAndPathOnly := []sources.Change{
		{
			Path: "lib/plot/reproducing_notes.py",
			Old:  "def draw(self, figure):\n    pass",
			New:  "def draw(self, figure):\n    self.example = 'reproducing problem'\n    self.problem = None",
		},
	}
	unrelatedLongQ := strings.Join([]string{
		"Matplotlib reproducing colorbar figure problem with reproducing_notes layout example",
		"When subplots initialize background axes margins with default figure padding",
		"and legend bounding boxes overlap canvas ticks unexpectedly",
	}, "\n")
	if changesBindToQueryWithTopIDF(proseAndPathOnly, unrelatedLongQ, smallIDF, topIDF) {
		t.Fatalf("expected long unrelated issue with only df=1 prose and path overlap not to bind on tiny store")
	}

	longBugQ := strings.Join([]string{
		"reduced_totient returns wrong result when computing modular_order on composites",
		"Reproducing minimal example with sympy ntheory arithmetic helpers during verification",
		"Expected reduced_totient and modular_order output across prime powers without mismatch",
	}, "\n")
	changedFix := []sources.Change{
		{
			Path: "sympy/ntheory/residue_ntheory.py",
			Old:  "def reduced_totient_old(n):\n    return 1",
			New:  "def reduced_totient(n):\n    return modular_order(n)",
		},
	}
	if !changesBindToQueryWithTopIDF(changedFix, longBugQ, smallIDF, topIDF) {
		t.Fatalf("expected long bug report matching df=1 changed code identifier on tiny store to bind")
	}
}

func TestChangeIdentPartsExtractsSingleLetterAcronymSpan(t *testing.T) {
	parts := changeIdentParts("MutableDenseNDimArray QPixmapData")
	for _, want := range []string{"ndim", "mutable", "dense", "array", "qpixmap", "data"} {
		if !slices.Contains(parts, want) {
			t.Fatalf("changeIdentParts missing %q in %v", want, parts)
		}
	}
}

func TestChangesBindToQueryPartialCamelCaseAcronym(t *testing.T) {
	cs := []sources.Change{
		{
			Path: "tensorlib/array/ndim_array.py",
			Old:  "def _build_ndim_shape(rank):\n    return rank",
			New:  "def _build_ndim_shape(rank):\n    return tuple(rank)",
		},
	}
	q := "MutableDenseNDimArray indexing returns wrong broadcast shape"
	termIDF := map[string]float64{
		"mutabledensendimarray": 3.5,
		"ndim":                  3.2,
		"array":                 2.1,
		"indexing":              2.4,
		"wrong":                 1.2,
		"broadcast":             2.8,
		"shape":                 2.2,
	}
	sessIDF := map[string]float64{
		"ndim":      3.0,
		"array":     2.0,
		"indexing":  2.1,
		"wrong":     1.1,
		"broadcast": 2.4,
		"shape":     2.1,
	}
	if !changesBindToQueryWithSessionIDF(cs, q, termIDF, sessIDF, 3.8) {
		t.Fatalf("expected MutableDenseNDimArray to bind to ndim_array.py via partial acronym span")
	}
}

func TestChangesBindToQuerySessionIDFAndTinyLongHunkContext(t *testing.T) {
	// 1. Small store (8 sessions): df=1 snake_case identifier in hunk context binds,
	// even if the def header line itself was not modified.
	topIDF := math.Log(9.0 / 2.0)
	smallIDF := map[string]float64{
		"stream_decode_response_unicode": topIDF,
		"decode":                         1.1,
		"response":                       0.8,
		"unicode":                        1.2,
		"streaming":                      topIDF,
		"chunks":                         topIDF,
		"expected":                       0.6,
		"empty":                          0.9,
		"payload":                        1.0,
		"returned":                       0.7,
		"behavior":                       0.8,
		"iteration":                      1.1,
	}
	hunkCtxFix := []sources.Change{
		{
			Path: "weblib/utils.py",
			Old:  "def stream_decode_response_unicode(iterator, r):\n    for chunk in iterator:\n        yield chunk",
			New:  "def stream_decode_response_unicode(iterator, r):\n    for chunk in iterator:\n        if chunk is not None:\n            yield chunk",
		},
	}
	longHunkQ := strings.Join([]string{
		"stream_decode_response_unicode fails during streaming chunks iteration",
		"Expected payload decoding to yield empty string when empty chunk arrived",
		"Actual returned behavior skips final response separator during iteration",
	}, "\n")
	if !changesBindToQueryWithSessionIDF(hunkCtxFix, longHunkQ, smallIDF, smallIDF, topIDF) {
		t.Fatalf("expected tinyLong to bind df=1 code identifier in unchanged hunk context")
	}

	// 2. Large/moderate store: plain prose with inflated message-capped TermIDF (>2.35)
	// must still be blocked when whole-session SessionIDF shows it is common prose.
	proseOnlyChange := []sources.Change{
		{
			Path: "weblib/pipeline/summary.py",
			Old:  "def render_report(items):\n    columns = len(items)\n    return columns",
			New:  "def render_report(items):\n    columns = len(items)\n    fitted = True\n    return columns if fitted else 0",
		},
	}
	unrelatedIssue := strings.Join([]string{
		"Columns validation fails when fitted estimator receives empty matrix",
		"<!-- Check template instructions before reporting -->",
		"First check that columns match expected schema rather than falling back silently.",
		"See https://forums.example.org/threads/common-columns-issue.",
	}, "\n")
	inflatedTermIDF := map[string]float64{
		"columns":    3.9,
		"fitted":     3.6,
		"first":      3.5,
		"validation": 2.9,
		"estimator":  3.2,
		"matrix":     2.5,
		"schema":     3.1,
	}
	lowSessionIDF := map[string]float64{
		"columns":    1.2,
		"fitted":     1.3,
		"first":      0.9,
		"validation": 2.4,
		"estimator":  3.0,
		"matrix":     2.1,
		"schema":     2.8,
	}
	if changesBindToQueryWithSessionIDF(proseOnlyChange, unrelatedIssue, inflatedTermIDF, lowSessionIDF, 3.8) {
		t.Fatalf("expected plain prose tokens with low SessionIDF not to bind despite high message-capped TermIDF")
	}
}

func TestChangesBindToQueryEnclosingDefAndRareChangedDef(t *testing.T) {
	// 1. Enclosing def header beyond 2-line docstring context on a 6-session store.
	topIDF := math.Log(7.0 / 2.0)
	moderateDF := math.Log(7.0 / 4.0)
	smallIDF := map[string]float64{
		"stream_decode_chunk_payload": moderateDF,
		"decode_chunk":                topIDF,
		"encode_native_header":        moderateDF,
		"payload":                     0.9,
		"iterator":                    1.0,
		"header":                      0.8,
		"response":                    0.7,
		"encoding":                    0.8,
		"fallback":                    0.9,
		"stream":                      0.7,
		"decode":                      0.9,
		"native":                      1.0,
		"unexpected":                  0.6,
		"traceback":                   0.7,
		"behavior":                    0.6,
		"expected":                    0.5,
	}
	docstringGapFix := []sources.Change{
		{
			Path: "weblib/utils.py",
			Old: strings.Join([]string{
				"def _helper_prev(x):",
				"    return x",
				"",
				"def stream_decode_chunk_payload(iterator, resp):",
				"    \"\"\"Stream decodes a payload iterator.\"\"\"",
				"",
				"    if resp.encoding is None:",
				"        for item in iterator:",
				"            yield item",
			}, "\n"),
			New: strings.Join([]string{
				"def _helper_prev(x):",
				"    return x",
				"",
				"def stream_decode_chunk_payload(iterator, resp):",
				"    \"\"\"Stream decodes a payload iterator.\"\"\"",
				"",
				"    encoding = resp.encoding",
				"    if encoding is None:",
				"        encoding = resp.fallback_encoding",
				"    if encoding is None:",
				"        for item in iterator:",
				"            yield item",
			}, "\n"),
		},
	}
	if got := enclosingChangeHeaders(docstringGapFix[0].Old, docstringGapFix[0].New); !strings.Contains(got, "stream_decode_chunk_payload") || strings.Contains(got, "_helper_prev") {
		t.Fatalf("enclosingChangeHeaders = %q, want enclosing def without sibling _helper_prev", got)
	}
	matchingLongQ := strings.Join([]string{
		"stream_decode_chunk_payload raises AttributeError when fallback decode_chunk path runs",
		"Traceback inside response payload iterator during chunk encoding handling",
		"Expected fallback behavior when stream encoding attribute stays empty",
		"Actual unexpected failure leaves native header state untouched",
	}, "\n")
	if !changesBindToQueryWithSessionIDF(docstringGapFix, matchingLongQ, smallIDF, smallIDF, topIDF) {
		t.Fatalf("expected enclosing multi-segment def header beyond 2-line docstring to bind matching bug report")
	}
	unrelatedLongQ := strings.Join([]string{
		"encode_native_header fails when fallback response encoding receives non-ascii text",
		"Traceback inside native header serializer during response payload preparation",
		"Expected fallback encoding behavior when stream payload arrives across iterator chunks",
		"Actual unexpected traceback reports unexpected character width mismatch",
	}, "\n")
	if changesBindToQueryWithSessionIDF(docstringGapFix, unrelatedLongQ, smallIDF, smallIDF, topIDF) {
		t.Fatalf("expected unrelated query without enclosing def symbol not to bind")
	}

	// 2. Rare 4-letter symbol in a newly declared def header binds when both termIDF and sessionIDF agree.
	rareDefFix := []sources.Change{
		{
			Path: "mathlib/printing/ccode.py",
			Old: strings.Join([]string{
				"class CCodePrinter:",
				"    def _print_sign(self, func):",
				"        return 'sign'",
				"    def indent_code(self, code):",
				"        return code",
			}, "\n"),
			New: strings.Join([]string{
				"class CCodePrinter:",
				"    def _print_sign(self, func):",
				"        return 'sign'",
				"    def _format_sinc(self, func):",
				"        return 'sinc(x/pi)'",
				"    def indent_code(self, code):",
				"        return code",
			}, "\n"),
		},
	}
	rareQ := strings.Join([]string{
		"octave_code sinc expression formatting fails when printing piecewise trig ratios",
		"Calling octave_code on sinc(x) emits unsupported comment instead of scaled output",
		"Look at recent _format_gamma converter implementation for normalized special functions",
		"Expected generated printer code string without fallback warning comment",
	}, "\n")
	highSincIDF := map[string]float64{
		"sinc":          math.Log(78.0 / 5.0),
		"octave_code":   3.5,
		"_format_gamma": 3.0,
		"printing":      1.1,
		"ccode":         2.4,
		"expression":    1.2,
		"formatting":    1.3,
		"piecewise":     1.4,
		"normalized":    2.7,
		"special":       1.5,
		"functions":     1.2,
		"converter":     1.9,
		"generated":     1.4,
		"printer":       2.1,
		"comment":       1.3,
		"fallback":      1.6,
	}
	if !changesBindToQueryWithSessionIDF(rareDefFix, rareQ, highSincIDF, highSincIDF, math.Log(78.0/2.0)) {
		t.Fatalf("expected rare newly declared def symbol _format_sinc to bind")
	}
	lowSincSessIDF := map[string]float64{
		"sinc":          1.4,
		"octave_code":   3.5,
		"_format_gamma": 3.0,
		"printing":      1.1,
		"ccode":         2.4,
		"expression":    1.2,
		"formatting":    1.3,
		"piecewise":     1.4,
		"normalized":    2.7,
		"special":       1.5,
		"functions":     1.2,
		"converter":     1.9,
		"generated":     1.4,
		"printer":       2.1,
		"comment":       1.3,
		"fallback":      1.6,
	}
	if changesBindToQueryWithSessionIDF(rareDefFix, rareQ, highSincIDF, lowSincSessIDF, math.Log(78.0/2.0)) {
		t.Fatalf("expected low sessionIDF on changed def symbol not to bind")
	}
}
