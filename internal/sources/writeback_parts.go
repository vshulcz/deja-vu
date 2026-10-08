package sources

import (
	"github.com/vshulcz/deja-vu/internal/model"
)

// WriteBackPart is a file a harness needs beside the transcript, or a line it
// needs added to a file of its own, for the transcript to open: Kimi keeps a
// session's directory in state.json and lists sessions in session_index.jsonl.
type WriteBackPart struct {
	Path string
	Data []byte
}

// writeBackExtras builds the parts for a harness that needs them. sidecars
// are new files, written before the transcript; appends are added to the end
// of a store file, created when it is missing, never rewritten.
var writeBackExtras = map[string]func(s model.Session, turns []model.Message, f WriteBackFile) (sidecars, appends []WriteBackPart, err error){}

func registerWriteBackExtras(harness string, fn func(model.Session, []model.Message, WriteBackFile) ([]WriteBackPart, []WriteBackPart, error)) {
	writeBackExtras[harness] = fn
}

// WriteBackExtras is what goes with the transcript RenderWriteBack built for
// s. Each part is checked to lie under the transcript's store root.
func WriteBackExtras(s model.Session, f WriteBackFile) (sidecars, appends []WriteBackPart, err error) {
	fn, ok := writeBackExtras[s.Harness]
	if !ok {
		return nil, nil, nil
	}
	sidecars, appends, err = fn(s, writeBackTurns(s), f)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range append(append([]WriteBackPart{}, sidecars...), appends...) {
		if writeBackRootOf(p.Path, []string{f.Root}) == "" {
			return nil, nil, &WriteBackRefusal{Harness: s.Harness, Reason: p.Path + " is not under " + s.Harness + "'s store, so deja will not write there"}
		}
	}
	return sidecars, appends, nil
}
