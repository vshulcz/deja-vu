# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately via GitHub:
[Security → Report a vulnerability](https://github.com/vshulcz/deja-vu/security/advisories/new).
Do not open a public issue for anything you believe is exploitable.

You can expect an initial response within 72 hours. Please include a minimal
reproduction and, if the issue involves a session file format, a redacted
sample.

## What `deja update` checks

It downloads `checksums.txt` and the release archive for your platform from the
same GitHub release and compares sha256 before replacing the binary. That
catches a corrupt or truncated download; it does not catch a tampered release,
since whoever could replace the archive could replace the checksums beside it.

Releases are signed with cosign and carry build provenance, and the client does
not verify either. If you need that guarantee, verify before installing:

```sh
cosign verify-blob --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp 'https://github.com/vshulcz/deja-vu/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
```

Nightly builds are prereleases and are not signed. `deja update` follows
`releases/latest`, which excludes them.

## Scope notes

deja-vu reads coding-agent session logs from the local disk and builds a local
index. It has no network listener; the only network operations are `deja
update` (fetches releases from GitHub) and `deja sync ssh` (your own SSH
connection). Reports about secrets surviving redaction in indexed or shared
output are in scope and appreciated.

See the [security model](docs/SECURITY-MODEL.md) for data flows, redaction
limits, trust assumptions, and release verification.

## Supported versions

Only the latest release receives fixes.
