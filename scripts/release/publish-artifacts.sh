#!/usr/bin/env bash
# The caller must authorize publication before invoking this command.
set -euo pipefail
if [[ $# != 3 ]]; then echo 'Usage: publish-artifacts.sh VERSION stable|beta ARTIFACT_DIR' >&2; exit 2; fi
version="$1" channel="$2" artifacts="$3" repo=yubozhao/isoform-releases
case "$channel" in stable|beta) ;; *) exit 2 ;; esac
[[ -f "$artifacts/version.json" && -f "$artifacts/checksums.txt" ]] || exit 1
python_cmd=python3
if command -v python3.12 >/dev/null 2>&1; then python_cmd=python3.12; fi
"$python_cmd" - "$artifacts/version.json" "$version" "$channel" <<'PY'
import hashlib, json, pathlib, re, sys
feed = json.load(open(sys.argv[1]))
version, channel = sys.argv[2:]
stable = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
pattern = stable if channel == "stable" else stable + r"-beta\.(?:0|[1-9][0-9]*)"
def require(condition, message):
    if not condition:
        raise SystemExit(message)
require(re.fullmatch(pattern, version) and feed["version"] == version, "Invalid release version")
require(set(feed["updater_assets"]) == set(feed["updater_checksums"]) == {"darwin-arm64", "darwin-x64"}, "Missing updater architecture")
directory = pathlib.Path(sys.argv[1]).parent
expected_files = {f"Isoform-{version}-{arch}{suffix}" for arch in ("darwin-arm64", "darwin-x64") for suffix in (".dmg", ".app.tar.gz")}
entries = {}
for line in (directory / "checksums.txt").read_text().splitlines():
    digest, separator, name = line.partition("  ")
    require(separator and re.fullmatch(r"[a-f0-9]{64}", digest) and name not in entries, "Invalid checksum entry")
    entries[name] = digest
require(set(entries) == expected_files, "Missing or unexpected checksum entries")
for name, digest in entries.items():
    with (directory / name).open("rb") as asset:
        require(hashlib.file_digest(asset, "sha256").hexdigest() == digest, f"Checksum mismatch: {name}")
for arch in feed["updater_assets"]:
    require(feed["updater_assets"][arch] == f"https://github.com/yubozhao/isoform-releases/releases/download/v{version}/Isoform-{version}-{arch}.app.tar.gz", f"Unexpected asset URL: {arch}")
    require(re.fullmatch(r"[a-f0-9]{64}", feed["updater_checksums"][arch]), f"Invalid feed checksum: {arch}")
    require(entries[f"Isoform-{version}-{arch}.app.tar.gz"] == feed["updater_checksums"][arch], f"Feed checksum mismatch: {arch}")
PY
(cd "$artifacts" && shasum -a 256 -c checksums.txt)
files=("$artifacts/Isoform-$version-darwin-arm64.dmg" "$artifacts/Isoform-$version-darwin-x64.dmg" "$artifacts/Isoform-$version-darwin-arm64.app.tar.gz" "$artifacts/Isoform-$version-darwin-x64.app.tar.gz" "$artifacts/version.json" "$artifacts/checksums.txt")
if [[ $channel == beta ]]; then
    gh release create "v$version" --repo "$repo" --draft --prerelease --latest=false --title "Isoform $version" --notes-file "$artifacts/notes.txt" "${files[@]}"
    gh release edit "v$version" --repo "$repo" --draft=false --latest=false
    if gh release view beta-feed --repo "$repo" >/dev/null 2>&1; then
        gh release upload beta-feed "$artifacts/version.json" --repo "$repo" --clobber
    else
        gh release create beta-feed "$artifacts/version.json" --repo "$repo" --prerelease --latest=false --title 'Beta update feed' --notes 'Rolling beta update feed.'
    fi
    url="https://github.com/$repo/releases/download/beta-feed/version.json"
else
    gh release create "v$version" --repo "$repo" --draft --title "Isoform $version" --notes-file "$artifacts/notes.txt" "${files[@]}"
    gh release edit "v$version" --repo "$repo" --draft=false --latest
    url="https://github.com/$repo/releases/latest/download/version.json"
fi
"$python_cmd" - "$url" "$version" <<'PY'
import hashlib, json, sys, urllib.request
with urllib.request.urlopen(sys.argv[1], timeout=60) as response:
    feed = json.load(response)
if feed['version'] != sys.argv[2]:
    raise SystemExit('published feed version mismatch')
if set(feed.get('updater_assets', {})) != {'darwin-arm64', 'darwin-x64'} or set(feed.get('updater_checksums', {})) != {'darwin-arm64', 'darwin-x64'}:
    raise SystemExit('published feed is missing an architecture')
for arch, url in feed['updater_assets'].items():
    digest = hashlib.sha256()
    with urllib.request.urlopen(url, timeout=120) as response:
        for chunk in iter(lambda: response.read(1024 * 1024), b''):
            digest.update(chunk)
    if digest.hexdigest() != feed['updater_checksums'][arch]:
        raise SystemExit(f'{arch} checksum mismatch')
    print(f'{arch}: published checksum verified')
PY
