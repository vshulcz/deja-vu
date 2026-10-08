package sources

// Roo reopens a task from its own task history, which deja does not write:
// the editor keeps it in its global state, and a task of the Roo CLI also
// needs history_item.json with the workspace it ran in, which the index does
// not keep. The CLI is no longer published (Roo Code was archived on
// 2026-05-15), so there is nothing to check a rebuilt task against either.
func init() {
	refuseWriteBack("roo", "Roo lists a task from its own task history, which deja does not write, and a Roo CLI task also needs history_item.json with the workspace it ran in, which the index does not keep")
}
