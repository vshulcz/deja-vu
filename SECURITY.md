# Security Policy

## Reporting a vulnerability

Please report vulnerabilities privately via GitHub:
[Security → Report a vulnerability](https://github.com/vshulcz/deja-vu/security/advisories/new).
Do not open a public issue for anything you believe is exploitable.

You can expect an initial response within 72 hours. Please include a minimal
reproduction and, if the issue involves a session file format, a redacted
sample.

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
