# Security Policy

This bundle is distributed on its own — plugin directories mirror the folder
rather than the repository — so the policy travels with it.

## Reporting a vulnerability

Report privately via GitHub:
[Security → Report a vulnerability](https://github.com/vshulcz/deja-vu/security/advisories/new).
Do not open a public issue for anything you believe is exploitable. Expect an
initial response within 72 hours.

## What this bundle runs

The hooks and the MCP entry point are shell scripts that locate a `deja` binary
you installed yourself and exec it. The bundle ships no binary and downloads
nothing at run time. With no `deja` on the machine, the hooks stay silent and
the MCP server reports what is missing.

Everything deja reads is already on your disk, and the index it builds stays
there: no network calls, no telemetry. Credentials are redacted as the index is
built. Full policy: [the repository's SECURITY.md](https://github.com/vshulcz/deja-vu/blob/main/SECURITY.md).
