# Doubao Desktop

- **ID**: `doubao`
- **Store**: `%LOCALAPPDATA%\Doubao\User Data\<profile>\IndexedDB\chrome_doubao-chat_0.indexeddb.leveldb\` — a Chromium IndexedDB database in a LevelDB directory (macOS: `~/Library/Application Support/Doubao/`, Linux: `~/.config/Doubao/`)
- **Read overrides**: `DEJA_DOUBAO_ROOT` replaces the user-data root
- **Format**: Chromium IndexedDB — LevelDB + snappy + Blink SerializedScriptValue / V8 structured clone
- **Needs**: nothing extra

Doubao Desktop (ByteDance's 豆包) keeps its conversations in a Chromium IndexedDB database, one LevelDB directory per profile. Only the chat window's database (`chrome_doubao-chat`) holds session bodies; the auxiliary windows (background, launcher, office) keep caches.

deja decodes three layers: LevelDB (SSTables and the write-ahead log, with the internal-key trailer stripped and deleted keys kept as history), Chromium's IDBValue wrapper (`FF 11 02` marks a snappy-compressed value), and the V8 structured-serialization stream. It reads the whole version history of every record, because Doubao rewrites the streaming state of the protocol record on every increment and clears it once a reply finishes — the current value alone usually reads as an empty store, and the bodies live in the superseded snapshots. Tasks merge across snapshots by keeping the longest reply per task id.

Conversation metadata comes from `conversation-list-data-*` records, merged across the schema variants Doubao writes. Turns come from the protocol record `default-chat-im-protocol-store` — 2.35+ writes it with an account prefix (`im-sdk:user:<uid>:default-chat-im-protocol-store`) — one task per question-answer pair, `block_type` 10000 as the only text-bearing block. 2.37+ writes `client_meta.conversation_id` as an empty string and carries the id on each received message's `extra` instead.

Only conversations whose local snapshots hold readable text are indexed: Doubao keeps full bodies server-side and clears the local snapshot when a reply finishes, so a store read between sessions yields what it has. Non-text blocks (images, cards) are counted, not turned into placeholder text.

The store shape was attested by Nwflower/dsh-chat-import (`lib/convert/doubao.mjs`, the 2.35/2.37 drift adapted in PR #123) and checked against a real 2.35.12 → 2.37.8 store; the synthetic fixture in `fixtures/registry/doubao/` keeps the reader honest.

**Last verified:** 2026-11-21
