package evidence

import "github.com/vshulcz/deja-vu/internal/model"

// Receipts finds, for each assistant message of one session, the records of
// its own turn that carry its facts. What the environment recorded beats
// what a person said about it, except a person's rule or instruction, which
// is shown first and never replaced: on the stand the rule "run genfeed.sh
// after editing docs/" lost to the genfeed.sh command it had caused, and the
// agent never saw it.
func Receipts(s model.Session, clean func(string) string) map[uint64]string {
	out, _ := receipts(s, clean)
	return out
}
