# extensions/

deja in the shape each harness expects: its own package, under that
ecosystem's naming, installable the way that ecosystem installs things. The
code is thin — every one of these shells out to the same `deja` binary and the
same local index.

| Directory | Published as | Installed with |
|---|---|---|
| [`opencode/`](opencode) | npm `opencode-deja` | `opencode plugin opencode-deja` |
| [`dsh/`](dsh) | npm `dsh-deja` | `dsh plugin add dsh-deja` |
| [`zed/`](zed) | Zed extension `deja-context-server` | Zed → Extensions → deja |
| [`kimi/`](kimi) | Kimi Code plugin `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| [`grok/`](grok) | Grok Build plugin `deja` | `grok plugin install deja` |

Two more integrations live outside this directory because their registries read
a fixed path in this repository: `claude-plugin/` (Claude Code marketplace) and
`codex-plugin/` (Codex). Moving them would break manifests that are already
submitted.

## Publishing

The release publishes the npm packages at the release's own version, so
`opencode-deja@0.21.0` and `dsh-deja@0.21.0` are the ones built against
`deja 0.21.0` — `scripts/release-npm.mjs` sets the version and the
`@vshulcz/deja-vu` dependency together. Each package keeps its own `LICENSE`
and `repository.directory` because npm publishes from that directory.

The two packages were versioned independently before this, so a release whose
version is still behind what npm serves is skipped with a line saying so,
rather than moving the `latest` tag backwards. They join the release version as
soon as it passes them.

Publishing by hand is still possible when a fix should not wait for a release:

```
cd extensions/opencode && npm publish --access public
cd extensions/dsh      && npm publish --access public
```

The Grok plugin is published by a catalog entry, not by us: the entry in
`xai-org/plugin-marketplace` pins a full commit SHA of this repository and a
`path` of `extensions/grok`, and Grok re-verifies the SHA after cloning. A new
version of the plugin means a pull request there that moves the pin — nothing
ships until it does.

The Zed extension is not published by us: `zed-industries/extensions` pins this
repository as a submodule and builds `extensions/zed`. A new version means
bumping `version` in `extensions/zed/extension.toml` and opening a PR there that
moves the submodule to the new commit.

The Kimi Code plugin has two routes, and both are ours to keep working. The
repository form (`/plugins install https://github.com/vshulcz/deja-vu`) reads
`kimi.plugin.json` at the repository root — the only reason that file exists —
and records the release it came from, which is what Kimi's update check reads.
The `kimi-deja.zip` release asset carries the same plugin at 16 KB for a
marketplace entry. `TestKimiManifestsAgree` keeps the two manifests one plugin.

Kimi notifies about updates only for plugins installed from its own
marketplace, so `deja doctor` reports the installed plugin version against the
one this deja ships.

## Rules that cost us time once

- **Resolve the binary in this order, everywhere:** an explicit setting or
  `DEJA_BIN` → a deja the user installed themselves (PATH, then the well-known
  install locations) → the copy the package shipped. Their own `deja update` or
  `brew upgrade` has to win over whatever we bundled.
- **Recall is optional.** A harness must never lose a turn because history was
  unavailable: every call is wrapped, every failure is silent.
- **Assume the other install is there too.** `deja install` wires these four
  harnesses as well, so a user can end up with both at once — opencode loads the
  package and the installer's plugin file side by side, Kimi runs a plugin hook
  next to the one in `config.toml`, dsh composes both into
  one profile, Zed reads both context servers. Whatever the installer writes
  wins, because it is the copy `deja install` keeps current: each package looks
  for those files and drops the part they already cover.
- **Verify by running.** Each of these had a failure that was invisible in the
  source and obvious the moment the real host executed it — a peer dependency
  the host does not install, a hook that accepts input and drops it, a 60-second
  timeout on `initialize`.
