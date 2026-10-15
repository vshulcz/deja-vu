package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/policy"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// Claude Code deletes a transcript after cleanupPeriodDays, 30 by default,
// and says nothing; deja keeps what it indexed, and said nothing either. The
// week note's last line says it, with one id to paste.

// weekDeletedNamed is how many agents the line names before "and N more".
const weekDeletedNamed = 2

// weekDeletedSince is the sessions whose transcript a pass found gone since
// the last week note, as the person may see them. It reads the manifest only.
func weekDeletedSince(dir string, since, now time.Time) []index.KeptSession {
	kept, err := index.KeptSince(dir, since, now)
	if err != nil || len(kept) == 0 {
		return nil
	}
	pol := policy.Load()
	out := kept[:0]
	for _, k := range kept {
		if strings.HasPrefix(k.Project, "imported:") || !pol.Allows(policy.ActivationSearch, k.Project) ||
			pol.Ignored(k.Path, k.Project) || strings.ContainsFunc(k.ID, isSpaceOrControl) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// weekDeletedLine is that line, or "" when nothing was deleted:
//
//	37 Claude Code sessions deleted this week; deja kept them — deja resume 5a1c0de0-111 --write-back
//
// It says the files went, not who took them: the agent's cleanup and a
// person's own rm are kept alike. The id is the newest deletion, and
// --write-back is offered only for an agent deja can write back to; for the
// rest it is `deja show`. kept is newest first, as KeptSince returns it.
func weekDeletedLine(dir string, kept []index.KeptSession, since, now time.Time) string {
	if len(kept) == 0 {
		return ""
	}
	count := map[string]int{}
	var order []string
	for _, k := range kept {
		if count[k.Harness] == 0 {
			order = append(order, k.Harness)
		}
		count[k.Harness]++
	}
	sort.SliceStable(order, func(i, j int) bool { return count[order[i]] > count[order[j]] })
	names := make([]string, 0, weekDeletedNamed+1)
	for i, h := range order {
		if i == weekDeletedNamed && len(order) > weekDeletedNamed+1 {
			names = append(names, fmt.Sprintf("%d more agents", len(order)-weekDeletedNamed))
			break
		}
		names = append(names, agentName(h))
	}
	// The note runs a week apart when sessions start every week; after a
	// longer gap "this week" would claim a window the count does not cover.
	when := "this week"
	if days := int(now.Sub(since).Hours()/24 + 0.5); days > 8 {
		when = fmt.Sprintf("in the last %d days", days)
	}
	n := len(kept)
	them := "them"
	if n == 1 {
		them = "it"
	}
	rest := fmt.Sprintf(" deleted %s; deja kept %s", when, them)
	bare := fmt.Sprintf("%d session%s", n, pluralS(n)) + rest
	named := fmt.Sprintf("%d %s session%s", n, names[0], pluralS(n)) + rest
	if len(names) > 1 {
		who := strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
		named = fmt.Sprintf("%d sessions from %s", n, who) + rest
	}
	first := kept[0]
	id := weekUsedID(dir, first.SessionMeta)
	cmd := "deja show " + id
	if sources.CanWriteBack(first.Harness) {
		cmd = "deja resume " + id + " --write-back"
	}
	// Names first, then the count alone, then, for an id too long to type,
	// the screen that lists them.
	for _, line := range []string{named + " — " + cmd, bare + " — " + cmd, named + " — " + weekDeletedTab, bare + " — " + weekDeletedTab} {
		if termwidth.Columns(line) <= noteWidth {
			return line
		}
	}
	return bare + " — " + weekDeletedTab
}

const weekDeletedTab = "the Deleted tab of deja lists them"
