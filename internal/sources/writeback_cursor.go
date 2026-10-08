package sources

// cursor-agent (2026.09.02) reopens a chat from chats/<md5 of dir>/<id>/
// store.db: a SQLite store of content-addressed blobs under a protobuf
// conversation state, loaded with resetFromDb. The agent-transcripts JSONL
// deja reads is generated from that store for citation and is never read
// back. The Cursor IDE keeps its chats in state.vscdb. Neither is a file deja
// can rebuild from turns of text.
func init() {
	refuseWriteBack("cursor", "cursor-agent reopens a chat only from its store.db, a SQLite blob store under a protobuf conversation state; the index holds the turns' text, not that state or its blob ids, and the agent-transcripts file deja reads is never read back by cursor-agent")
}
