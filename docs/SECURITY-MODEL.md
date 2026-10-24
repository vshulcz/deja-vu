# Security model

deja indexes existing coding-agent histories on the same machine. This document
describes the data it handles, the boundaries around that data, and the release
artifacts available for verification.

## Data flows

### Local reads

deja discovers and parses the session stores documented in the
[source parser table](ARCHITECTURE.md#source-parsers) — JSON, JSONL, Markdown
and SQLite written by the coding agents listed there. SQLite stores are read by
running the local `sqlite3` executable rather than opening a network
connection.

Indexing reads session messages and metadata needed for search, including
session IDs, project paths, titles, roles, and timestamps. It does not modify
the source session stores.

It also reads what the agent did, from the tool calls the same transcripts
record: the file paths a turn named, the shell commands it ran, the text its
tools printed, and the exact spans its edits replaced. Replaced spans are
verbatim source from the user's own files, kept so a lost change can be handed
back. All of it goes through the same redaction pass as conversation text, which
matters more here than elsewhere: command output carries credentials far more
often than prose does. `DEJA_INDEX_PATHS=0`, `DEJA_INDEX_COMMANDS=0`,
`DEJA_INDEX_EDITS=0`, `DEJA_INDEX_WRITES=0` and `DEJA_INDEX_TOOL_OUTPUT=0` each
disable one of these at ingest, and for records imported from another machine.

The written side of an edit — what the agent typed into the file, which is what
`deja blame` attributes a line by — is stored as one 64-bit hash per line of 24
characters or more, never as text. Attribution needs equality, not the bytes, so
this is the one class of content the index holds without being able to hand it
back: a hash cannot be read, quoted or searched for.

Reading one session back (`deja ctx`, the MCP `context` mode, the session
resource) is different: it reads the session's edits from its transcript at
that moment, both sides, and shows them redacted ahead of the conversation.
Nothing of it is stored, and `DEJA_INDEX_EDITS=0` and `DEJA_INDEX_WRITES=0` do
not turn it off, since they act at ingest.

### Local writes

The default index is `~/.cache/deja/index.db`; `DEJA_INDEX_DIR` changes that
location. The directory contains:

- `records.bin`, with redacted message records;
- `buckets/*.bin`, with token postings into those records, and the `*.ov`
  overlays beside them that hold the postings of records added since;
- `frictext.bin`, the first line of each repeated error's tool output, already
  redacted, and `recoffsets.bin`, where each session's records sit in
  `records.bin`;
- `commands-state.bin`, each session's commands as its redacted records hold
  them, so a pass mines only the sessions it changed;
- `manifest.gob` and `sessions.gob`, with source file state, session metadata,
  redaction counts by rule, sync watermarks, imported-record deduplication keys,
  the few files each session touched most, and hashes — not text — of the
  questions it asked.

Text taken out of a transcript leaves `records.bin` and `buckets/` on the next
`deja index` pass that reads it, and a command taken out leaves the command
tables (`commands.gob`, `commandfails.gob`, `sessionfacts.gob`) on the same
pass. A word no remaining record holds also leaves the co-occurrence map
(`cooccur.gob`) on that pass, and so does one the removed text held only in a
file body or the middle of a long output, which the postings never index, even
when another session still has it there. The pass zeroes the replaced records in
`records.bin` where they stand, and in `buckets/` blanks every word that no
remaining record holds: a word filed under two leading letters keeps those,
which the bucket file is named after, and one filed under a hash, as every
CJK word is, is overwritten whole with filler. A one-letter word of a
non-Latin alphabet can stay as it is. A word another session still contains
stays, as that session's. If the pass is killed between committing the new index and
the zeroing, the next `deja index`, or any deja process that takes the index
lock, finishes it. On
Windows, a search holding the index open as a pass ends can keep the previous
index directory (`index.db.old`, or `index.db.old.N` when that one is held
too) until a later pass removes it; what stays there is usually only the
record log it shares with the live index, already zeroed. deja overwrites in
place and does not attempt secure deletion: an SSD or a copy-on-write
filesystem may keep the old blocks until it reuses them.

At a supported pre-compaction hook, `manifest.gob` also retains redacted,
transcript-derived continuation packets for at most 32 session/workspace pairs,
with a 24 KiB limit per packet. These contain objectives, conclusions, the
commands a session ran with whether each passed or failed, explicit
gaps/conflicts, provenance, repository fingerprints, and a short list of
transcript lines left open (questions to the user, open issue numbers, verdicts,
deferred checks) carried from one compaction to the next. They remain local, are omitted from sync export, and follow ignored/excluded project
and forgotten-session controls. `deja forget` removes matching packets even
before their source sessions have been indexed. See [compaction recovery](compaction.md).

Privacy control files are primary data, not cache data: the XDG-aware
`~/.config/deja/tombstones` list prevents forgotten source sessions from being
re-ingested, and `~/.config/deja/exclude` contains project patterns skipped at
ingest. `DEJA_EXCLUDE_PROJECTS` adds comma-separated patterns. A line in the same file prefixed `harness:` names a store instead of a project — `harness:opencode` — and deja then neither walks it nor asks for the tool that would read it; `deja doctor` and `deja sources` both report it as `excluded` rather than naming a package to install. `DEJA_EXCLUDE_HARNESSES` is the comma-separated form. A store excluded after it was indexed is not walked again on any pass; the sessions already read from it stay in the index, unserved, until `deja index --rebuild` drops them. Store names are exact, not globs: they are the forty-four `deja sources` prints. Patterns and
excluded stores also apply at `sync export`, so a project or store excluded after
the index was built does not leave the machine, and at `sync import`, so a peer's
batch does not put it back; what is already indexed stays searchable locally until
`deja index --rebuild`, and `deja index` says so when the list has changed. A full `forget`
rebuild replaces the index atomically before the tombstone list is used by the
next index pass.

Usage events are appended to the sibling file `<index-dir>.usage.jsonl`. This
sidecar records the kind and time of local search, recall, context, and hook
use. It rotates at 1 MiB and retains 14 days; it does not contain session text
or queries.

Compaction capture and first-edit measurement events use this same bounded log.
They carry hashed session/workspace identifiers, a capture revision, action
counts, and short failure reasons. They do not contain the recovery packet.

The sibling file `<index-dir>.injections.jsonl` is what `deja log` reads: one
line per injection it can show, with the full text as the agent received it,
already redacted, plus the kind, time, receiving session, policy name and the
projects it was built from. It keeps the last 20
entries, rotating at 512 KiB. Unlike the usage log, this file holds session
text.

The interactive screen keeps `<index-dir>.tui-history`: the last 100 searches
typed on it that led to reading, resuming, continuing or copying a session, one
per line, written 0600. Unlike the usage log, this file holds query text.

`deja secrets --scrub` is one of two commands that write into an agent's own
store: it rewrites the harness transcripts that still hold a credential, in place. It
replaces only the rule kinds the report names in the file the report named,
writes the original to `<file>.deja-backup-<stamp>` first and leaves it there,
refuses a session an agent is inside and a file written in the last two minutes,
and re-checks size and mtime between the read and the rename. The rules run
inside each decoded JSON string, or one line at a time in a file that is not
JSON, never over the raw bytes; a rewrite that would change the line count or
break a line that parsed is refused, and a compressed or binary store is left
alone. A store with no
per-session file is not rewritten at all, and the count of what it could not
reach is printed rather than dropped. Without `--scrub` the command only reports.

The other is `deja resume <id> --write-back` (`R` on the screen's Deleted tab). It
recreates a session whose agent deleted it, rebuilt from the index and so
redacted, only at the path the session was read from, only when nothing is there,
and only inside that agent's store directory.

The index and sidecar never leave the machine through indexing, search, MCP,
stats, or hook operation. The MCP server uses JSON-RPC over standard input and
output and does not listen on a network socket. The git commands deja runs to
read a repository (changed files, blame, worktree names) set
`GIT_NO_LAZY_FETCH=1`, so in a partial clone they answer from what is on disk
instead of fetching missing objects from the remote.

### Explicit exports and network paths

Network access happens only after one of these commands is run, or in the
once-a-day release check described last:

- `deja update` connects to GitHub over HTTPS to read release metadata and
  download the selected archive and `checksums.txt`. It verifies the archive's
  SHA-256 checksum before replacing the binary. It does not check the cosign
  signature over `checksums.txt`, so the check catches a corrupt download but
  not a tampered release; verify the signature yourself (below) if you need
  that guarantee.
- `deja sync ssh <host>` runs the system `ssh` and `scp` clients against the
  host supplied by the user. The remote peer receives redacted JSONL sync
  batches and imports them into its own index. `--pull` reverses that flow.
- `deja doctor` makes one HTTPS GET to the GitHub releases API to compare the
  installed version with the latest; `--offline` (or `DEJA_OFFLINE=1`) skips
  it. No session data is sent.
- An interactive command (stdout and stderr both a terminal) checks for a
  newer release at most once a day. It starts a detached `deja version`
  process that makes the same HTTPS GET as `deja doctor` and writes the answer
  to `<index dir>.release`; the next interactive command prints one line on
  stderr if a newer release is out. No session data is sent. Hooks, the MCP
  server and the other commands a harness starts never start it, even when the
  harness gives them a terminal; nor do pipes, dev builds and `deja update`,
  `doctor` and `version`. `DEJA_OFFLINE=1` turns it off, and so does
  `DEJA_NO_UPDATE_NOTICE` set to any non-empty value; either way no request is
  made. `DEJA_NO_UPDATE_NOTICE` also hides the one-time note a new version
  prints on its first run, which `DEJA_OFFLINE=1` leaves on because it comes
  from the binary rather than the network.
- `deja embed` and hybrid search talk to the embedding endpoint you configured
  (an Ollama or LM Studio address, normally on localhost). Without that
  configuration the semantic path is off and nothing is sent anywhere. It is
  the only command that puts session text in front of a third party, so the
  trust policy gates it on every activation at once rather than on `search`:
  content withheld from any read path is not embedded, and the command says how
  many records stayed behind. A search or recall sends its query, redacted by
  the same rules as the index, once per call.

`deja sync export <dir>` and `deja share <id>` write data to a path or output
chosen by the user. They do not transmit it themselves. Exported batches and
shared digests can leave the machine if the destination directory, shell
pipeline, or later user action sends them elsewhere.

## Redaction boundary

Redaction runs before session messages and titles are stored in the index,
runs again for share and sync export output, and runs a third time when
`deja sync import` reads a batch, so a batch written by an older deja, or by
hand, is masked by this machine's rules before it is stored. It replaces matched values while
leaving surrounding text searchable. `deja share` and `deja stats --year` also run an
outbound pass that masks IP addresses, internal hostnames, emails and
home-directory paths, including encoded folder names like `-Users-<name>-src`.
The current patterns cover:

- AWS access key IDs and secret-key assignments;
- generic credential assignments such as `api_key=`, `token=`, `secret=`,
  `password=`, and `authorization=`;
- a secret named and quoted rather than assigned — `password "…"`, `token is
  "…"`, `api-key: "…"` — in every spelling of the name, with or without the
  hyphen, underscore or space, and with a colon or an equals sign as well as
  plain prose. The quotes are what makes this safe to be loose about the
  separator: a name with no quoted value behind it is left alone;
- a password stated in prose, with no delimiter at all — "the admin password is
  …", "passphrase was …", and the same shapes in Russian. Every other rule here
  needs a colon, an equals sign, a quote or a flag, and a person telling an
  agent a password writes none of them. The value has to carry a digit or a
  symbol and it ends at the first space, so "the password is wrong" and "the
  password is the same as staging" are left alone;
- bearer tokens, the `Authorization: Token` scheme, and JWTs;
- PEM and PGP private-key blocks, whole, cut short, indented or JSON-escaped;
- Slack and Discord webhook URLs;
- known GitHub, GitLab, OpenAI/Anthropic-style, Groq, xAI, Hugging Face, npm,
  Slack, and Google token prefixes;
- URLs containing `scheme://user:password@host` credentials;
- a credential handed to a program on its command line: `sshpass -p`,
  `mysql -pSecret`, `curl -u user:pass`, and the long flags that name what they
  carry — `--password`, `--passphrase`, `--secret`, `--token`, `--api-key`.
  With a space rather than an `=`, a value that reads as an ordinary word is
  left alone, so a sentence about the flag keeps its next word;
- bare high-entropy values in secret-shaped positions: the value side of an
  assignment (`ANYTHING=<random-looking string>`, including the Telegram
  `digits:token` shape) and a token standing alone on its own line. Entropy
  alone is not enough — hex digests, UUIDs, paths and identifiers are
  excluded, and the assignment key must be a real word, so ordinary code and
  logs pass through unchanged (measured ~0.4% of messages affected on a real
  23 MB corpus).

Text holding terminal escape sequences is read again without them, so colored
output cannot split a value from its key.

Pattern matching is not secret detection. A new provider's token shape, an
unlabelled credential, encoded or transformed data, and a secret split across
lines can pass through unchanged. Redaction also cannot remove sensitive prose
that does not look like a credential. Review every share or export before
sending it to another person or system.

A credential that appears in a transcript was already sent to the model
provider by the agent that wrote it. Redaction protects the artifacts derived
from that transcript — the local index, shares, sync batches — from spreading
it further; it does not undo the original exposure. Rotate the credential.

Setting `DEJA_NO_REDACT=1` disables this boundary. Plaintext credentials may
then be written to the local index and may appear in search, share, and sync
output. Do not use it with histories that contain secrets.

## Non-goals and trust assumptions

- deja is not an access-control system. Anyone who can read the index files can
  inspect the data stored in them.
- The index and usage sidecar inherit the permissions and protections of their
  directory and filesystem. deja does not encrypt them at rest.
- Sync trusts the SSH host, account, and keys selected by the user. deja does
  not authenticate peers beyond the system SSH client's checks and does not
  revoke data after a peer imports it.
- Redaction reduces accidental credential disclosure; it does not prove that
  output is free of secrets or other private information.

## Release integrity

Each GitHub release includes `checksums.txt` and `checksums.txt.sigstore.json`.
The release workflow creates a keyless cosign signature over `checksums.txt`,
stored as a sigstore bundle, and publishes a GitHub build-provenance
attestation for the same checksum set. Releases also include an SPDX JSON SBOM for each archive.

After downloading both files, verify the signature with:

```sh
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/vshulcz/deja-vu/.github/workflows/release.yml@refs/tags/v[0-9].*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

Releases up to v0.21.4 carry `checksums.txt.sig` and `checksums.txt.pem`
instead of the bundle; pass those as `--signature` and `--certificate` in
place of `--bundle`.

Then verify the downloaded archive against the signed checksum list:

```sh
sha256sum --ignore-missing --check checksums.txt
```

On macOS, use `shasum -a 256 -c checksums.txt` instead.

## Rebuilding a release

Release binaries are built with Go 1.26.x, `CGO_ENABLED=0`, and `-trimpath`.
To compare one target, check out its tag, use the exact Go patch version shown
in the release workflow logs, and set the version embedded by GoReleaser:

```sh
git checkout vX.Y.Z
export CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-trimpath
go build -ldflags='-s -w -X main.version=X.Y.Z' -o deja ./cmd/deja
sha256sum deja
```

Run the equivalent target for the archive being checked and compare the binary
inside that archive. Go patch version, target OS and architecture, environment,
and build flags must match.

For a local archive build, use the same GoReleaser and Syft versions as the
release job and derive the archive timestamp from the tag commit:

```sh
export GOFLAGS=-trimpath
export SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)"
goreleaser release --snapshot --clean --skip=publish
```

The Go binaries are intended to be reproducible when those inputs match.
Release archives are not claimed to be byte-reproducible today: the workflow
does not pin the GoReleaser version, snapshot builds carry a different version,
and archive/SBOM metadata can vary with tool versions. `SOURCE_DATE_EPOCH`
stabilizes timestamps but does not remove those differences.


## Recall trust policy

`~/.config/deja/policy.json` (XDG-aware; `DEJA_POLICY_FILE` moves it) decides
which history may be handed to an agent on each read path. `activations` maps
`search`, `mcp` and `auto` (the hooks) to rules keyed by origin: `local`,
`imported` for anything that arrived by sync, `imported:<group>` for one group
of synced projects, and `*`. The most specific rule wins, and an activation with
no rules allows every origin. `DEJA_AUTORECALL_LOCAL_ONLY=1` denies imported
memory on the `auto` path. Content leaves the machine for an embedding endpoint
only when all three activations allow it.

`ignore` lists shell globs matched against a session's path and project; a
match stays out of recall. With no `ignore` key the default is
`*/.claude/jobs/*`, a background agent's own scratch tree; writing the key
replaces that default. A file that is missing or does not parse means the
default policy, so recall never breaks on a bad file; `deja doctor` reports what
is in force.

The policy filters what recall serves. It does not keep this machine's own
sessions out of the index; the exclude list and `deja forget` above do that. A
peer's records that `ignore` matches are left out at `sync import` and not sent
at `sync export`.

## Recall output framing

Text drawn from history that deja hands to an agent is wrapped in
`<deja-recall>` markers with a preamble stating the content is untrusted
historical data and instructions inside it must not be followed. Transcripts
can carry text an attacker influenced (a directive copied from a web page
persists in the index and replays into later sessions); framing keeps models
from treating replayed text as commands. A marker that appears inside the
recalled text itself, in any spelling a model would read as one, is
neutralised so it cannot close the frame early.

The frame covers:

- every MCP tool mode that returns history — `recall`, `context`, `blame`,
  `fix`, `how`, `orient` and `handoff` — and the session resource; `remember`
  stores a note and returns only a confirmation;
- every hook that adds context: the session-start digest and the machine
  facts block (`hook-context`), per-prompt recall and handoff
  (`hook-prompt`), the line before a command or edit (`hook-tool`), the fix
  after a failed command (`hook-tool-after`, and its deferred delivery on
  hosts that drop the first event's output), the plan check (`hook-plan`),
  the compaction recovery packet and the nudge to `remember` a failure;
- the generated integrations that deliver it themselves: the Aider and Goose
  wrappers, the Antigravity and CodeWhale hooks, and the Reasonix sidecar's
  compaction note;
- `deja recall`, which prints the MCP recall page for skills that drive deja
  through the shell, and any command run with `--framed` in front of it
  (`deja --framed search …`), which the agent tools in the dsh, opencode,
  OpenClaw and Hermes plugins and the skills and slash commands that send an
  agent to the shell use.

Some hosts act on tokens in the text they are handed before the model sees
it. opencode and Kilo run `` !`cmd` `` and attach `@path` in the `/deja` prompt
text, and attach `@path` in a sub-agent's task prompt; deja puts a space into
those tokens in the recall it gives them, so the words still reach the model
and nothing runs or attaches. dsh reads deja's digest and recall as prompt
templates, so both pass `interpolate: false`, and a recalled `{{...}}` is
split into `{ {...} }`, which dsh before 0.2.1-alpha.2 needs.

The framing bytes count against the existing injection budgets, a budget that
cuts a block closes its frame rather than leaving it open, empty results stay
unwrapped, and human-facing CLI output without `--framed` is unchanged.

## Config backups

`deja install` snapshots an agent config once as `<path>.bak` before first
modification. Backups and newly created configs are written owner-only
(0600) because these files can carry MCP server credentials; the mode of an
existing live config is preserved on update.

On non-Windows hosts install also writes `~/.config/deja/bin/deja-hook`, a
shell script the hook entries name (and the MCP entries for OpenClaw, Codex,
Hermes and Grok, which start their servers with a short list of variables) so an upgrade that moves the binary does not
have to rewrite every config. It is 0755 in a directory the user owns, and it
runs the first executable it finds among, in this order, `DEJA_BIN`, the path
the install ran from, `~/.local/bin/deja`, `~/bin/deja`, the usual install
locations (`/usr/local/bin`, `/opt/homebrew/bin`, `/home/linuxbrew/.linuxbrew/bin`,
`/usr/bin`) and finally the `PATH` — so anything that can write those can
already run code as that user. The fixed paths come before the `PATH` because a
GUI-launched agent often hands its hooks a short one. It is removed when the last target is
uninstalled.

The script also records the `XDG_*` base directories that differ from the
defaults and the `DEJA_*` index, store-location and indexing settings that were
set at install, and exports each one only when the hook's own environment
leaves it unset. That is for hosts such as Muse Code that start hooks with only
`HOME` and `PATH`. Keys, connection strings (`DEJA_EMBED_KEY`,
`DEJA_HERMES_PG_DSN`) and debug switches are never written to it.
