package sources

// Amp keeps threads on ampcode.com: `amp threads continue <id>` asks for a
// login before anything else and loads the thread from the server, and the
// data directory holds no thread files (0.0.1791446565, #4355).
func init() {
	refuseWriteBack("amp", "Amp keeps its threads on ampcode.com and `amp threads continue` loads them from there, so there is no local file to write back")
}
