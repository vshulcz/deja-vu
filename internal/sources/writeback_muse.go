package sources

// Muse's log is event-sourced (muse.go), and no build of it was at hand to
// check a rebuilt one against (#4617).
func init() {
	refuseWriteBack("muse", "deja has not been able to check a rebuilt log against Muse, whose session.jsonl is an event stream of runtime records, so it does not write one")
}
