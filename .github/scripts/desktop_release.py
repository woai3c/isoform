#!/usr/bin/env python3
"""Generate source-backed desktop release notes and the app's JSON update feed."""

import argparse
from collections import defaultdict
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def area_for(pr):
    for label in pr.get("labels", []):
        match = re.match(r"area[:/]\s*(.+)", label["name"], re.I)
        if match:
            return match[1].strip().title()
    match = re.match(r"\w+\(([^)]+)\)!?:", pr["title"])
    if match:
        return match[1].strip().title()
    title = pr["title"].lower()
    for area, words in (
        ("Privacy", ("privacy", "permission", "retention")),
        ("Chat and voice", ("chat", "voice", "tts", "spotlight")),
        ("Today and brief", ("today", "brief", "upcoming")),
        ("Agent and memory", ("agent", "memory", "purpose", "playbook")),
        ("Release and CI", ("ci:", "release", "pipeline")),
        ("Documentation", ("docs:", "documentation")),
    ):
        if any(word in title for word in words):
            return area
    return "Desktop"


def render_notes(prs, commits, repository, source_sha, previous):
    groups = defaultdict(list)
    for pr in sorted(prs, key=lambda pr: pr["number"]):
        # A closed PR is not necessarily merged. Reachability also excludes
        # merges after this tag and merges already present at the previous tag.
        if pr.get("merged_at") and pr.get("merge_commit_sha") in commits:
            title = " ".join(pr["title"].split())
            groups[area_for(pr)].append(
                f"- {title} ([#{pr['number']}]({pr['html_url']}))"
            )
    lines = ["## What's changed", ""]
    for area, entries in sorted(groups.items()):
        lines.extend([f"### {area}", "", *entries, ""])
    if not groups:
        lines.extend(["No merged pull requests in this source range.", ""])
    if previous:
        lines.extend(
            [
                f"[Source changes](https://github.com/{repository}/compare/{previous}...{source_sha})",
                "",
            ]
        )
    lines.append(
        f"<!-- source-repository: {repository}; source-commit: {source_sha} -->"
    )
    return "\n".join(lines) + "\n"


def generate_notes(output):
    repository = os.environ["GITHUB_REPOSITORY"]
    source_sha = subprocess.check_output(
        ["git", "rev-parse", "HEAD"], text=True
    ).strip()
    previous_result = subprocess.run(
        [
            "git",
            "describe",
            "--tags",
            "--match",
            "v[0-9]*",
            "--match",
            "desktop/v[0-9]*",
            "--exclude",
            "*-beta.*",
            "--abbrev=0",
            "HEAD^",
        ],
        capture_output=True,
        text=True,
    )
    previous = previous_result.stdout.strip() if previous_result.returncode == 0 else ""
    revision = f"{previous}..HEAD" if previous else "HEAD"
    commits = set(
        subprocess.check_output(["git", "rev-list", revision], text=True).splitlines()
    )
    response = subprocess.check_output(
        [
            "gh",
            "api",
            "--paginate",
            "--jq",
            ".[] | @json",
            f"repos/{repository}/pulls?state=closed&per_page=100",
        ],
        text=True,
    )
    prs = [json.loads(line) for line in response.splitlines() if line.strip()]
    Path(output).write_text(
        render_notes(prs, commits, repository, source_sha, previous), encoding="utf-8"
    )


def generate_manifest(directory, version, repository, notes, channel="stable"):
    directory = Path(directory)
    stable = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
    pattern = stable if channel == "stable" else stable + r"-beta\.(?:0|[1-9][0-9]*)"
    if channel not in ("stable", "beta") or not re.fullmatch(pattern, version):
        raise ValueError("Use X.Y.Z for stable or X.Y.Z-beta.N for beta")
    if not re.fullmatch(r"[\w.-]+/[\w.-]+", repository):
        raise ValueError("Releases repository must be owner/name")
    assets, checksums, checksum_lines = {}, {}, []
    for arch in ("arm64", "x64"):
        stem = f"Isoform-{version}-darwin-{arch}"
        for suffix in (".dmg", ".app.tar.gz"):
            path = directory / (stem + suffix)
            # A missing architecture or update archive must block publication.
            if path.stat().st_size == 0:
                raise ValueError(f"Empty release artifact: {path}")
            with path.open("rb") as artifact:
                digest = hashlib.file_digest(artifact, "sha256").hexdigest()
            checksum_lines.append(f"{digest}  {path.name}\n")
            if suffix == ".app.tar.gz":
                platform = f"darwin-{arch}"
                assets[platform] = (
                    f"https://github.com/{repository}/releases/download/v{version}/{path.name}"
                )
                checksums[platform] = digest
    manifest = {
        "version": version,
        "release_notes": notes,
        "updater_assets": assets,
        "updater_checksums": checksums,
    }
    (directory / "version.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )
    (directory / "checksums.txt").write_text("".join(checksum_lines), encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    notes = subparsers.add_parser("notes")
    notes.add_argument("--output", required=True)
    manifest = subparsers.add_parser("manifest")
    manifest.add_argument("--version", required=True)
    manifest.add_argument("--repository", required=True)
    manifest.add_argument("--notes", required=True)
    manifest.add_argument("--artifacts", required=True)
    manifest.add_argument("--channel", choices=("stable", "beta"), default="stable")
    args = parser.parse_args()
    if args.command == "notes":
        generate_notes(args.output)
    else:
        generate_manifest(
            args.artifacts,
            args.version,
            args.repository,
            Path(args.notes).read_text(encoding="utf-8"),
            args.channel,
        )


if __name__ == "__main__":
    main()
