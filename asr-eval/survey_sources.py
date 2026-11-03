"""Condense fetched hub/release metadata into a reviewable provenance snapshot."""

import json
from pathlib import Path

from survey_assets import ROOT, RAW


def main():
    records = []
    if not any(RAW.glob("*.json")):
        raise SystemExit(
            "No fetched metadata; preserve the committed sources.json snapshot"
        )
    for path in sorted(RAW.glob("*.json")):
        data = json.loads(path.read_text())
        if isinstance(data, list):
            repo = path.stem.removesuffix("-releases").replace("--", "/")
            records.append(
                {
                    "url": f"https://api.github.com/repos/{repo}/releases?per_page=15",
                    "releases": [
                        {
                            "tag": r["tag_name"],
                            "published_at": r["published_at"],
                            "url": r["html_url"],
                            "commitish": r["target_commitish"],
                        }
                        for r in data
                    ],
                }
            )
        elif "siblings" in data:
            records.append(
                {
                    "url": "https://huggingface.co/" + data["id"],
                    "revision": data["sha"],
                    "created_at": data.get("createdAt"),
                    "last_modified": data.get("lastModified"),
                    "licence": data.get("cardData", {}).get("license"),
                    "licence_name": data.get("cardData", {}).get("license_name"),
                    "licence_link": data.get("cardData", {}).get("license_link"),
                    "languages": data.get("cardData", {}).get("language"),
                    "files": [
                        {
                            "path": r["rfilename"],
                            "size_bytes": r.get("size"),
                            "git_blob_id": r.get("blobId"),
                            "lfs_sha256": (r.get("lfs") or {}).get("sha256"),
                        }
                        for r in data["siblings"]
                    ],
                }
            )
    (ROOT / "survey-2026-10/sources.json").write_text(
        json.dumps(
            {
                "retrieved_date": "2026-10-23",
                "note": "Hub metadata is a licence indicator, not a substitute for upstream weight and runtime terms. Git blob IDs are not file SHA-256 digests.",
                "sources": records,
            },
            ensure_ascii=False,
            indent=2,
        )
        + "\n"
    )


if __name__ == "__main__":
    main()
