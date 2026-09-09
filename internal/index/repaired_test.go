package index

import "testing"

// The shapes a repair actually takes, counted over 2,953 failed commands: a
// glob that needed quoting, a flag that had to go, a path that was wrong. Each
// is the failing command with a small correction, which is why it is evidence
// on its own — unlike "the command that ran next", which named nothing in 87%
// of cases.
func TestARepairIsTheFailingCommandCorrected(t *testing.T) {
	repairs := [][2]string{
		// zsh will not expand the glob; quoting it is the whole fix.
		{`grep -rn "hookDigest" --include=*.go internal`, `grep -rn "hookDigest" --include="*.go" internal`},
		// The flag the tool does not take.
		{`go test ./internal/index -run TestFix -count=1 -race -json`, `go test ./internal/index -run TestFix -count=1 -race`},
		// The path that was wrong.
		{`python3 scripts/report.py --out /var/reports`, `python3 scripts/report.py --out ./reports`},
		// A wrapper in front does not change what is being run.
		{`sudo systemctl restart deja-sync.service --now`, `systemctl restart deja-sync.service --now`},
	}
	for _, r := range repairs {
		if !repairedVariant(r[0], r[1]) {
			t.Errorf("not read as a repair:\n  was: %s\n  now: %s", r[0], r[1])
		}
	}
}

// What the bound is for. The most common shape below it was one program run
// twice on unrelated arguments, which says nothing about the error.
func TestMovingOnIsNotARepair(t *testing.T) {
	notRepairs := [][2]string{
		// Going somewhere else carries no work, whether or not it was the fix.
		// `cd` is 705 of the 1,892 same-program successes, the largest single
		// source of noise in the shape.
		{`cd /work/api && ls`, `cd /work/web && ls`},
		// A different program entirely.
		{`go build ./...`, `npm run build`},
		// The identical command again is a retry, not a repair.
		{`go test ./internal/index -count=1`, `go test ./internal/index -count=1`},
		// One word is not enough to compare.
		{`make`, `ninja`},
	}
	for _, r := range notRepairs {
		if repairedVariant(r[0], r[1]) {
			t.Errorf("read as a repair when it is not:\n  was: %s\n  now: %s", r[0], r[1])
		}
	}
}
