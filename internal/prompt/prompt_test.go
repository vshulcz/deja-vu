package prompt

import (
	"slices"
	"testing"
)

func TestTermsKeepsShortSubjectWG(t *testing.T) {
	got := Terms("что с wg 10.10.0.10 и кластером")
	if !slices.Contains(got, "wg") {
		t.Fatalf("expected Terms to retain short technical subject 'wg', got %v", got)
	}
}
