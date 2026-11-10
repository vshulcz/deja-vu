#!/usr/bin/env bash
# Fast pre-push quality gate (<15s):
# 1. Leaf packages with 100% CI coverage floors stay at 100.0%.
# 2. Global CLI boundary, env single-source-of-truth, JSON surface, and
#    44-harness hostile-input property tests pass.
# 3. golangci-lint passes on changed packages when installed.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

LEAF_100_PKGS=(
	./internal/bench
	./internal/cjkfold
	./internal/gitread
	./internal/harnesscolor
	./internal/model
	./internal/nfcfold
	./internal/policy
	./internal/query
	./internal/termwidth
)

echo "==> Checking 100% coverage floor on leaf packages..."
cov_out="$(go test -count=1 -cover "${LEAF_100_PKGS[@]}")"
echo "$cov_out"
if echo "$cov_out" | grep -v '100.0% of statements' | grep -q 'coverage:'; then
	echo "FAIL: one or more leaf packages dropped below 100.0% statement coverage" >&2
	exit 1
fi

echo "==> Running structural contract tests..."
go test ./internal/embed ./internal/sources ./internal/index -run '^TestOnlyOneSwitchesItOff|TestParsePrimitivesInt64AndExitCode|TestWithStderrBoundsAndPreservesWrappedError|TestEveryRegisteredHarnessKindHandlesHostileInputs|TestSessionStatsSurviveFullBuildAndIncrementalAppend$'
go test ./cmd/deja -run '^TestEveryCLICommandRejectsBlankInputsBeforeIndexing|TestEveryCLICommandAcceptsFlagEqualsValueAndRejectsEmptyEquals|TestEverySessionSelectorCommandUnwrapsPastedQuotes|TestTestsJSONAndTextRespectLimitParity|TestDoctorBoundedStoreErrorPreservesValidUTF8|TestDoctorAndRuntimeShareEnvResolvers|TestEveryJSONSurfaceMatchesItsSection|TestEachFlagListNamesEveryFlagItsParserTakes$'

if command -v golangci-lint >/dev/null 2>&1; then
	echo "==> Running golangci-lint..."
	golangci-lint run ./internal/embed/... ./internal/sources/... ./internal/index/...
	golangci-lint run ./cmd/deja/...
fi

echo "==> All fast quality gates passed."
