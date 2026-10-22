package index

import (
	"path/filepath"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// A harness that compacts by rewriting its transcript leaves a file that opens
// with the summary and holds nothing before it. Continue does this: the session
// file goes from every turn to one summary item, and a pass that re-read it
// replaced the session's records with that one line, so every turn indexed
// before the compaction was gone from search (#4795). The file is the only copy
// Continue keeps, and it has no hook to run before the rewrite, so the records
// the index already holds are the only record of those turns left anywhere.
//
// So a re-read session whose file now opens with a summary keeps the records it
// had under that file and no longer holds. What the file still holds is read
// from it as before, so nothing is counted twice, and a second compaction keeps
// the first summary along with the turns after it.

// compactedInPlace returns, for every session in ss whose first message is a
// summary, the messages it holds as role and indexed text. raw says the texts
// have not been through indexedText yet.
func compactedInPlace(ss []model.Session, raw bool) map[string]map[string]bool {
	var out map[string]map[string]bool
	for _, s := range ss {
		if len(s.Messages) == 0 || s.Messages[0].Role != sources.RoleSummary {
			continue
		}
		held := make(map[string]bool, len(s.Messages))
		for _, m := range s.Messages {
			text := m.Text
			if raw {
				text, _, _ = indexedText(text)
			}
			held[m.Role+"\x00"+text] = true
		}
		if out == nil {
			out = map[string]map[string]bool{}
		}
		out[s.Harness+":"+s.ID] = held
	}
	return out
}

// keptThroughCompaction reports a record of a re-read file that the file lost
// to a compaction rather than to an edit of what it says.
func keptThroughCompaction(r Record, compacted map[string]map[string]bool) bool {
	held, ok := compacted[r.Key]
	return ok && !held[r.Role+"\x00"+r.Text]
}

// carryCompactedAway is the rebuild's half: the sessions a source handed back
// opening with a summary get back the records the last build held for them
// under the same file, ahead of what the file holds now.
func carryCompactedAway(dir string, ss []model.Session) []model.Session {
	compacted := compactedInPlace(ss, true)
	if len(compacted) == 0 {
		return ss
	}
	m, err := readManifest(dir)
	if err != nil {
		return ss
	}
	at := map[string]int{}
	for i, s := range ss {
		if _, ok := compacted[s.Harness+":"+s.ID]; ok {
			at[s.Harness+":"+s.ID] = i
		}
	}
	carried := map[string][]model.Message{}
	_ = eachRecord(filepath.Join(dir, "records.bin"), tablesFromManifest(m), func(r Record) {
		i, ok := at[r.Key]
		if !ok || r.SourcePath != ss[i].Path || !keptThroughCompaction(r, compacted) {
			return
		}
		carried[r.Key] = append(carried[r.Key], model.Message{Role: r.Role, Text: carriedText(ss[i].Harness, r.Role, r.Text), Time: r.Time})
	})
	for key, ms := range carried {
		s := &ss[at[key]]
		for _, msg := range ms {
			s.Touch(msg.Time)
		}
		s.Messages = append(ms, s.Messages...)
	}
	return ss
}
