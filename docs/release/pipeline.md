# Release pipeline

Isoform's desktop releases are built from tagged commits with `wails-gui/scripts/build-wails.sh`, which signs the app with a Developer ID certificate, notarizes and staples it, and packages a DMG plus a full `.app.tar.gz` for each architecture.

Each release is published to [yubozhao/isoform-releases](https://github.com/yubozhao/isoform-releases) with a `version.json` update feed and `checksums.txt`. The app's updater reads `releases/latest/download/version.json` and verifies the SHA-256 of the archive it downloads.

To build locally without publishing, see [docs/development.md](../development.md).
