# Windows package manifests

This directory keeps the source manifests for Scoop and WinGet, and the
template for the `.mcpb` bundle (`mcpb/manifest.json`, whose `0.0.0` version
`scripts/mcpb` replaces when the release job packs the bundles). The Scoop and
WinGet manifests are pinned to a published release so their URLs and SHA-256
values can be checked before submission.

## Update checklist

After GoReleaser publishes a tag, the release job runs
`go run ./scripts/pinmanifests -version <version> -checksums dist/checksums.txt`,
which sets the version, release URLs and hashes in `scoop/deja-vu.json` and the
three files under `winget/` (and the plugin manifests elsewhere in the repo), and
attaches the Windows manifests to the release. It cannot commit them back, so:

1. Download `checksums.txt` from the GitHub release, run the same command
   locally, and commit the result. CI runs `pinmanifests -check` and fails
   until the committed manifests match the newest release.
2. Do not hand-edit the versions or hashes; if a file carries a version the
   tool does not own, add it to `scripts/pinmanifests`.
3. The `$version` autoupdate URLs in the Scoop manifest stay as they are.
4. Download both archives and confirm each contains `deja.exe` at its root.
5. On Windows, validate the WinGet set:

   ```powershell
   winget validate --manifest packaging\winget
   winget install --manifest packaging\winget
   ```

6. In a Scoop development checkout, copy the Scoop manifest into a bucket and
   validate it:

   ```powershell
   scoop checkver deja-vu
   scoop audit deja-vu
   ```

7. Run `go test ./...` to check local version, URL, architecture, and hash
   consistency across the manifests.

## Publish

deja-vu is in `ScoopInstaller/Main` as of 0.18.0, so `scoop install deja-vu`
works without adding a bucket. Scoop's own automation proposes version bumps
from `checkver` and takes hashes from the release's `checksums.txt`; this source
copy exists so the manifest can be checked before it is submitted, and should be
kept in sync when the shape changes rather than when the version does.

Note that Main is for command-line tools and Extras for the rest — the star and
fork numbers in the criteria are read as "either", not "both". A submission to
Extras was closed and moved here on exactly that basis.

For WinGet, copy the three files into
`manifests/v/vshulcz/deja-vu/<version>/` in a fork of
`microsoft/winget-pkgs`. Run `winget validate` against that directory, install
from the local manifests, and then open a pull request. Do not replace the
previous version directory.

Add package-manager commands to the project README only after each upstream
manifest is accepted. Scoop is, and the README lists it; the WinGet submission
([microsoft/winget-pkgs#428125](https://github.com/microsoft/winget-pkgs/pull/428125))
is still open, so the WinGet manifests here are a publication source, not a
working install channel.
