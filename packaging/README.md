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

For the first Scoop publication, copy `scoop/deja-vu.json` to
`bucket/deja-vu.json` in a fork of `ScoopInstaller/Main`, run the checks above,
and open a pull request there. After acceptance, Scoop's checkver automation
can propose routine version updates; keep this source copy in sync.

For WinGet, copy the three files into
`manifests/v/vshulcz/deja-vu/<version>/` in a fork of
`microsoft/winget-pkgs`. Run `winget validate` against that directory, install
from the local manifests, and then open a pull request. Do not replace the
previous version directory.

Add package-manager commands to the project README only after each upstream
manifest is accepted. Until then, the checked-in manifests are publication
sources, not working install channels.
