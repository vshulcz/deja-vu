package sources

func init() {
	refuseWriteBack("ante", "Ante stores events in append-only session JSONL logs with separate meta/dialog files; run ante directly instead of rewriting history.")
}
