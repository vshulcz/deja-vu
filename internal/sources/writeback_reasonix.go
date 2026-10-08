package sources

// Reasonix 2.31 sends the transcript's first message as the system prompt on
// --resume, and saves nothing back when that line is missing (#4617).
func init() {
	refuseWriteBack("reasonix", "Reasonix resumes with the system prompt saved as the transcript's first message, which the index does not keep; without it the session runs with no system prompt and Reasonix does not save it")
}
