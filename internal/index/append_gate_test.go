package index

import (
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// A kind that declares an offset parser is a kind whose files can be read from
// where the last pass stopped. The gate that decides this kept a list of
// harness names instead, and six kinds with a parser were never on it — so
// their files were re-read whole on every pass, which is what #2075 found on
// the database side (#2870).
func TestEveryKindWithAnOffsetParserCanAppend(t *testing.T) {
	for _, k := range sources.KindsWithOffsetParsers() {
		if !appendableKind(k) {
			t.Errorf("%s declares an offset parser the append gate never reaches", k)
		}
	}
}
