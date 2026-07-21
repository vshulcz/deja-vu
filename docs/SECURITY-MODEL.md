# Security model

deja indexes existing coding-agent histories on the same machine. This document
describes the data it handles, the boundaries around that data, and the release
artifacts available for verification.

## Data flows

### Local reads

deja discovers and parses the session stores documented in the
[source parser table](ARCHITECTURE.md#source-parsers): JSON, JSONL, Markdown, and
SQLite data written by Claude Code, Codex CLI, opencode, aider, Gemini CLI,
Cursor, Antigravity, and Grok Build. For opencode and Cursor IDE stores it runs
the local `sqlite3` executable rather than opening a network connection.

Indexing reads session messages and metadata needed for search, including
session IDs, project paths, titles, roles, and timestamps. It does not modify
the source session stores.

### Local writes

The default index is `~/.cache/deja/index.db`; `DEJA_INDEX_DIR` changes that
location. The directory contains:

- `records.bin`, with redacted message records;
- `buckets/*.bin`, with token postings into those records;
- `manifest.gob` and `sessions.gob`, with source file state, session metadata,
  redaction counts by rule, sync watermarks, and imported-record deduplication
  keys.

Privacy control files are primary data, not cache data: the XDG-aware
`~/.config/deja/tombstones` list prevents forgotten source sessions from being
re-ingested, and `~/.config/deja/exclude` contains project patterns skipped at
ingest. `DEJA_EXCLUDE_PROJECTS` adds comma-separated patterns. A full `forget`
rebuild replaces the index atomically before the tombstone list is used by the
next index pass.

Usage events are appended to the sibling file `<index-dir>.usage.jsonl`. This
sidecar records the kind and time of local search, recall, context, and hook
use. It rotates at 1 MiB and retains 14 days; it does not contain session text
or queries.

The index and sidecar never leave the machine through indexing, search, MCP,
stats, or hook operation. The MCP server uses JSON-RPC over standard input and
output and does not listen on a network socket.

### Explicit exports and network paths

deja has no background network traffic. Network access happens only after one
of these commands is run:

- `deja update` connects to GitHub over HTTPS to read release metadata and
  download the selected archive and `checksums.txt`. It verifies the archive's
  SHA-256 checksum before replacing the binary.
- `deja sync ssh <host>` runs the system `ssh` and `scp` clients against the
  host supplied by the user. The remote peer receives redacted JSONL sync
  batches and imports them into its own index. `--pull` reverses that flow.
- `deja doctor` makes one HTTPS GET to the GitHub releases API to compare the
  installed version with the latest; `--offline` (or `DEJA_OFFLINE=1`) skips
  it. No session data is sent.
- `deja embed` and hybrid search talk to the embedding endpoint you configured
  (an Ollama or LM Studio address, normally on localhost). Without that
  configuration the semantic path is off and nothing is sent anywhere.

`deja sync export <dir>` and `deja share <id>` write data to a path or output
chosen by the user. They do not transmit it themselves. Exported batches and
shared digests can leave the machine if the destination directory, shell
pipeline, or later user action sends them elsewhere.

## Redaction boundary

Redaction runs before session messages and titles are stored in the index and
runs again for share and sync export output. It replaces matched values while
leaving surrounding text searchable. The current patterns cover:

- AWS access key IDs and secret-key assignments;
- generic credential assignments such as `api_key=`, `token=`, `secret=`,
  `password=`, and `authorization=`;
- bearer tokens and JWTs;
- PEM private-key blocks;
- known GitHub, GitLab, OpenAI/Anthropic-style, Groq, xAI, Hugging Face, npm,
  Slack, and Google token prefixes;
- URLs containing `scheme://user:password@host` credentials.

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

Each GitHub release includes `checksums.txt`, `checksums.txt.sig`, and
`checksums.txt.pem`. The release workflow creates a keyless cosign signature
over `checksums.txt` and publishes a GitHub build-provenance attestation for the
same checksum set. Releases also include an SPDX JSON SBOM for each archive.

After downloading those three checksum files, verify the signature with:

```sh
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github.com/vshulcz/deja-vu/.github/workflows/release.yml@refs/tags/v[0-9].*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

Then verify the downloaded archive against the signed checksum list:

```sh
sha256sum --ignore-missing --check checksums.txt
```

On macOS, use `shasum -a 256 -c checksums.txt` instead.

## Rebuilding a release

Release binaries are built with Go 1.25.x, `CGO_ENABLED=0`, and `-trimpath`.
To compare one target, check out its tag, use the exact Go patch version shown
in the release workflow logs, and set the version embedded by GoReleaser:

```sh
git checkout vX.Y.Z
export CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-trimpath
go build -ldflags='-s -w -trimpath -X main.version=X.Y.Z' -o deja ./cmd/deja
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


## Recall output framing

Agent-facing recall (the `recall` and `recall_context` MCP tools and the
SessionStart hook digest) is wrapped in `<deja-recall>` markers with a
preamble stating the content is untrusted historical data and instructions
inside it must not be followed. Transcripts can carry text an attacker
influenced (a directive copied from a web page persists in the index and
replays into later sessions); framing keeps models from treating replayed
text as commands. The framing bytes count against the existing injection
budgets, empty results stay unwrapped, and human-facing CLI output is
unchanged.

## Config backups

`deja install` snapshots an agent config once as `<path>.bak` before first
modification. Backups and newly created configs are written owner-only
(0600) because these files can carry MCP server credentials; the mode of an
existing live config is preserved on update.