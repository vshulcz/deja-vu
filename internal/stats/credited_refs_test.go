package stats

import (
	"reflect"
	"testing"
)

// Only an id on the credit line counts, once, and the old shape names none.
func TestCreditedRefsTakesTheIDsOnTheCreditLine(t *testing.T) {
	cases := map[string][]string{
		"déjà vu: the lease fix (claude, Oct 9, deja:65dc9f50-3f6) — reusing it.":                  {"65dc9f50-3f6"},
		"Déjà vu: two at once (deja:65dc9f50-3f6, deja:ses_fc145aa)\nDéjà vu: (deja:65dc9f50-3f6)": {"65dc9f50-3f6", "ses_fc145aa"},
		"see deja:65dc9f50-3f6 for the lease\ndéjà vu: nothing named here":                         nil,
		"déjà vu: too short to be an id (deja:abc)":                                                nil,
		"deja-vu recalled: the webhook retry cap — reusing it.":                                    nil,
		"": nil,
	}
	for text, want := range cases {
		if got := CreditedRefs(text); !reflect.DeepEqual(got, want) {
			t.Errorf("CreditedRefs(%q) = %q, want %q", text, got, want)
		}
	}
}
