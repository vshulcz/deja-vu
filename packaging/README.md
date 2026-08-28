# Windows package manifests

This directory keeps the source manifests for Scoop and WinGet. They are
pinned to a published release so their URLs and SHA-256 values can be checked
before submission.

## Update checklist

After GoReleaser publishes a tag:

1. Download `checksums.txt` from the GitHub release and confirm both Windows
   zip assets are listed.
2. In `scoop/deja-vu.json`, set `version`, the two initial download URLs, and
   their hashes. Leave the `$version` autoupdate URLs unchanged.
3. In all three files under `winget/`, set `PackageVersion`. Update the release
   URLs, `InstallerSha256` values, `LicenseUrl`, and `ReleaseNotesUrl`.
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
manifest is accepted. Until then, the checked-in manifests are publication
sources, not working install channels.
