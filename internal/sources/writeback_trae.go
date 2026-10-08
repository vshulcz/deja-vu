package sources

// TRAE CLI is a closed fork of codex-rs whose rollouts differ from Codex's
// (trae.go), and no build of it was at hand to check one against (#4617).
func init() {
	refuseWriteBack("trae", "deja has not been able to check a rebuilt rollout against TRAE CLI, which records turns its own way since 0.208, so it does not write one")
}
