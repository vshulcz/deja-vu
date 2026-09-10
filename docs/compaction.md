# Automatic compaction recovery

With auto-recall installed, `hook-precompact` reads the current Claude Code or
Codex JSONL transcript before the host compacts it. It extracts the user's
objective, assistant conclusions, recorded verification commands, and explicitly
stated gaps or conflicts. Each item identifies its source session, harness,
role, and recorded timestamp. No checkpoint command or model call is required.

The next session-start, prompt, or tool hook for that same session and workspace
returns a recovery packet once. Its 4 KiB limit includes the untrusted-history
frame. The packet labels assistant conclusions as reported claims. A recorded
verification command is marked passed or failed only when its transcript record
contains an explicit exit status; otherwise its outcome is unknown. Repository
HEAD, branch, and working-tree fingerprints are compared at recovery. Changed,
partial, or unavailable fingerprints require validation instead of presenting
old conclusions as current.

## Storage and limits

Packets live in the existing `manifest.gob`, alongside index metadata. There is
no separate context store, additional MCP tool, or synthetic searchable session.
The index keeps the latest packet for up to 32 session/workspace pairs, with a
24 KiB limit per stored packet. A new capture replaces that session's packet;
the oldest captures are evicted when the session limit is reached. There is no
packet history. Full and incremental index rebuilds preserve retained packets.
Packets are not included in sync exports.

The reader inspects at most 4 MiB of transcript tail plus a 64 KiB header. The
extractor selects at most 64 useful records and 32 KiB of text, with at most
eight items in each category. Omitted content is identified in the recovery
packet. Git output is streamed and its subprocesses share a 750 ms deadline.
Untracked-file hashing stops at 4,096 files, 1 MiB per file, or 8 MiB total;
incomplete fingerprints remain visibly partial.

Capture uses the existing index lock without waiting behind a rebuild. A busy
or unreadable store causes a best-effort miss. A packet captured before the first
index build does not mark the search index ready. The hook still requests the
usual background index warmup.

## Privacy and supported hosts

The source must declare the exact native session ID and workspace supplied by
the hook. Unknown formats, missing transcript paths, mismatched identities, and
files that change during reading cannot produce a successful capture. Other
hosts keep the existing warmup and recall behavior until a supported transcript
reader and the required hook payload are available.

Stored text uses the existing redaction pass; `DEJA_NO_REDACT=1` retains its
documented opt-out behavior. `DEJA_RECALL=off`, automatic-recall policy, ignored
directories, excluded projects, and forgotten-session tombstones apply to this
path. `deja forget` removes matching packets, including a packet whose session
has not yet been indexed, and prevents it from being captured again. As with the
rest of the index, redaction does not remove all sensitive prose and storage is
not encrypted.

## Measuring recovery

`deja stats` reports locally observed raw tool calls between capture and the
first explicit editing tool, excluding the edit itself. It shows the sample
count, median, and 75th percentile, separately from pending and unmeasured
captures. The metric is reconstructed from the existing bounded usage log;
measurement events contain identifiers and counts, not transcript text.

The count comes from native transcript tool invocations, including tools that
do not run a deja hook. It does not count normalized message records or hook
invocations. A later editing hook can recover the first-edit boundary already
recorded in the transcript. Rewritten logs, unreadable evidence, or an interval
outside the bounded transcript window remain unmeasured. These observations
measure local recovery cost; they do not establish a speedup or compare against
a built-in baseline.
