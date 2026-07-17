# Roadmap

This roadmap records direction, not release commitments. Linked issues contain
the current scope and are the place to discuss design changes.

## Now

- Add ingest-time project exclusions so selected histories never enter the
  index ([#8](https://github.com/vshulcz/deja-vu/issues/8)).
- Add `deja forget` for deleting sessions, projects, or age ranges from records
  and postings ([#45](https://github.com/vshulcz/deja-vu/issues/45)).
- Validate path discovery, terminal output, install configuration, and locking
  on real Windows installations ([#9](https://github.com/vshulcz/deja-vu/issues/9)).
- Maintain the security model, signed checksums, provenance, release SBOMs, and
  structured parser-format reports as release and harness formats change.

## Next

- Add project and harness filters to `deja last`
  ([#10](https://github.com/vshulcz/deja-vu/issues/10)).
- Derive canonical project identity from a repository remote when available so
  same-named repositories and worktrees do not collide
  ([#44](https://github.com/vshulcz/deja-vu/issues/44)).
- Improve share selection around decisions in tool-heavy sessions without
  exposing raw tool dumps ([#22](https://github.com/vshulcz/deja-vu/issues/22)).

## Later

- Add a typo-tolerant fallback only when exact and substring search return no
  results, while keeping the normal search path unchanged
  ([#11](https://github.com/vshulcz/deja-vu/issues/11)).
- Expose recent sessions as an MCP resource and add bounded recall pagination
  ([#12](https://github.com/vshulcz/deja-vu/issues/12)).

## Not planned

- **Capture daemons:** deja indexes histories already written by each harness;
  a recorder would add a persistent process and a second source of truth.
- **Cloud sync by default:** implicit upload conflicts with local-only indexing;
  sync remains an explicit export or SSH operation.
- **Embeddings in the base binary:** model files and vector runtimes would break
  the zero-runtime-dependency distribution and increase index cost.
