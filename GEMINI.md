# deja

This file is loaded in every session the extension is enabled in, so it stays
short. The detail lives in the tool descriptions.

Search deja before re-deriving past work: when the user refers to an earlier
session or decision, before debugging an error, and before implementing
something that may already exist. It searches this machine's own history across
every AI coding tool used on it, further back than deja itself was installed.

- `recall` — search with the most specific token available: an exact error
  string, a function name, a file path, a flag.
- `recall_context` — the full digest of the best-matching session, once a hit
  looks right and the reasoning behind it matters.

If recalled history genuinely helped, say so in one line: what was recalled and
how you used it. Say nothing about recalls that did not help.

deja is a binary you install yourself; this extension only points Gemini at it:

    brew install deja-vu

or

    curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
