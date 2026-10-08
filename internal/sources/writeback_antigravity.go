package sources

// Antigravity reopens a conversation from conversations/<id>.db, a SQLite
// trajectory whose steps are protobuf blobs (agy 1.3.1). The
// brain/<id>/.system_generated/logs/transcript.jsonl deja reads is a log agy
// writes beside it and does not resume from.
func init() {
	refuseWriteBack("antigravity", "agy reopens a conversation from its conversations/<id>.db, a database of protobuf steps the index has no record of; the transcript.jsonl deja reads is a log agy does not resume from")
}
