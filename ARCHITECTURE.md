# Architecture

## Notes source

Explicit notes are stored as one JSON object per line in
`~/.local/share/deja/notes.jsonl`, or under `XDG_DATA_HOME`; `DEJA_NOTES_FILE`
overrides the path. Each record contains an RFC3339 `ts`, `project`, and
`text`. Notes are grouped into one user-message session per project and UTC
calendar day, then redacted and indexed like every other source. The file is
primary data; the index remains a rebuildable cache.

## Semantic sidecar

`deja embed` writes `<index-dir>.vectors.bin`. The file begins with `DJV1`, a
version, vector dimension, model name, manifest generation, vector count, and
covered-record watermark. Each entry stores the records.bin byte offset, its
session key, and fixed-width float32 values. Writes use a temporary file and
rename. A changed manifest generation or model discards old entries; a corrupt
sidecar is treated as absent and rebuilt.

## Hybrid ranking

Search first produces lexical BM25 results. When a matching sidecar exists, up
to 64 candidates are reranked using the query vector. The final score is
`0.5 * normalized lexical score + 0.5 * cosine similarity`. A failed query
embedding prints one notice and returns the original lexical order.
