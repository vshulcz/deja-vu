package sources

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParsePrimitivesInt64AndExitCode(t *testing.T) {
	cases := []struct {
		in       any
		wantI64  int64
		wantExit int
	}{
		{nil, 0, 0},
		{float64(42), 42, 42},
		{int64(127), 127, 127},
		{int(-9), -9, -9},
		{json.Number("137"), 137, 137},
		{json.Number("15.0"), 15, 15},
		{json.Number("not-a-number"), 0, 0},
		{" 255 ", 255, 255},
		{"not-an-int", 0, 0},
		{int64(9999), 9999, 1},
		{int64(-9999), -9999, 1},
	}
	for _, tc := range cases {
		if got := int64Of(tc.in); got != tc.wantI64 {
			t.Errorf("int64Of(%#v) = %d, want %d", tc.in, got, tc.wantI64)
		}
		if got := exitCode(tc.in); got != tc.wantExit {
			t.Errorf("exitCode(%#v) = %d, want %d", tc.in, got, tc.wantExit)
		}
	}
}

func TestWithStderrBoundsAndPreservesWrappedError(t *testing.T) {
	baseErr := errors.New("exit status 1")
	if got := withStderr(baseErr, nil); got.Error() != baseErr.Error() {
		t.Fatalf("nil buffer changed error: %v", got)
	}
	if got := withStderr(baseErr, bytes.NewBufferString("   \n  ")); got.Error() != baseErr.Error() {
		t.Fatalf("blank buffer changed error: %v", got)
	}
	longLine := strings.Repeat("x", sqliteStderrMax+50) + "\nsecond line ignored"
	wrapped := withStderr(baseErr, bytes.NewBufferString(longLine))
	if !errors.Is(wrapped, baseErr) {
		t.Fatalf("wrapped error lost base cause: %v", wrapped)
	}
	if strings.Contains(wrapped.Error(), "second line") {
		t.Fatalf("second line leaked into error: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "…") {
		t.Fatalf("expected ellipsis truncation in %q", wrapped.Error())
	}
}
