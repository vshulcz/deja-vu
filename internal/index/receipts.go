package index

import (
	"github.com/vshulcz/deja-vu/internal/evidence"
	"github.com/vshulcz/deja-vu/internal/model"
)

// updateReceipts brings the receipts an incremental pass carries from the
// live index in from up to date in dir, for the sessions of keys, read whole
// from the record log in dir, whose manifest is m. Until it did, a session
// that arrived after the last full build had none, and recall fell back to
// the unchecked lines for it until the next rebuild.
//
// When the sessions cannot be read the receipts go: kept, the file would
// cover them with what it held before the change.
func updateReceipts(from, dir string, m Manifest, keys map[string]bool) {
	if m.SearchOnly {
		return
	}
	var ss []model.Session
	var err error
	if len(keys) > 0 {
		ss, err = rowSessionsFromRecords(dir, m, keys)
	}
	if err != nil || !evidence.Update(from, dir, ss, nil) {
		evidence.Drop(dir)
	}
}
