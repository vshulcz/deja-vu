package digest

import (
	"reflect"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// An issue opened together with its PR closes when the PR does: the PR says
// "Closes #N", GitHub closes the issue on merge, and the session only ever
// writes that the PR was merged. Both lines below stayed on a live packet for
// days after their PRs were merged.
func TestCarryIssueClosesWithItsPR(t *testing.T) {
	cases := []struct {
		name string
		msgs []model.Message
		prev []model.ContextCarry
		want []carryWant
	}{
		{"opened and merged in the same segment",
			carryEv("u", "да", "a", "Открыл issue #4638 и PR #4639, жду CI без Windows и смёржу по зелёному.",
				"a", "PR #4639 смёржен в main (коммит `4af775b57`), все 25 проверок CI прошли.", "u", "что дальше"),
			nil, nil},
		{"merged with ё in the verb",
			carryEv("u", "да", "a", "Opened issue #4603 and PR #4604, merge on green.",
				"a", "Смёржил PR #4604, коммит `e767a7e34`.", "u", "дальше"),
			nil, nil},
		{"carried from the last compaction, merged in this one",
			carryEv("u", "ok", "a", "PR #4604 merged as e767a7e34.", "u", "next"),
			[]model.ContextCarry{{Kind: carryOpen, Text: "Opened issue #4603 and PR #4604, will merge.", Key: "#4603,#4604"}},
			nil},
		{"the PR is not merged yet, so the issue stays",
			carryEv("u", "go", "a", "Открыл issue #4638 и PR #4639, жду ревью."),
			nil,
			[]carryWant{{"open", "Открыл issue #4638 и PR #4639, жду ревью."}}},
		{"two PRs named: which one closes the issue is not said",
			carryEv("u", "go", "a", "Issue #300 is still open, PR #301 and PR #302 are waiting.", "a", "PR #301 merged."),
			nil,
			[]carryWant{{"open", "Issue #300 is still open, PR #301 and PR #302 are waiting."}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := carryGot(ExtractCarry(tc.msgs, tc.prev))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCarryIssuePRPairs(t *testing.T) {
	for s, want := range map[string]map[int]int{
		"Открыл issue #4638 и PR #4639":           {4638: 4639},
		"PR #12 (issue #10, issue #11)":           {10: 12, 11: 12},
		"задача #77, пулл-реквест #78":            {77: 78},
		"PR #1 and PR #2 for issue #3":            nil,
		"issue #5 only":                           nil,
		"See PR #9.":                              {},
		"APR #12 is a date, not a pull request #": nil,
	} {
		got := carryIssuePRPairs(carryBNorm(s))
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v, want %v", s, got, want)
		}
	}
}
