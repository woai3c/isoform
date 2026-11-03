"""Apply source-backed part-2 language metadata and completed-run decisions.

Part 1 remains reproducible on its own when the part-2 decision file is absent.
Licence consent is retained from the base generator and never inferred from a
measurement, a language list, or a recommendation file.
"""

import copy
import json
from pathlib import Path

from survey_assets import ROOT

OUT = ROOT / "survey-2026-10-multilingual"


def apply_multilingual(manifest):
    languages_path = OUT / "languages.json"
    if not languages_path.exists():
        return manifest
    languages = json.loads(languages_path.read_text())["models"]
    decision_path = OUT / "manifest-update.json"
    decisions = json.loads(decision_path.read_text()) if decision_path.exists() else {}
    entries = manifest["models"]
    # Duplicated job rows share a pinned artifact; the installer must deduplicate
    # by file digest. Dependencies remain job-specific (live needs speaker pack).
    for alias in decisions.get("aliases", []):
        source = next(m for m in entries if m["id"] == alias["artifact_id"])
        entry = copy.deepcopy(source)
        entry.update(
            id=alias["id"],
            job=alias["job"],
            artifact_id=source["id"],
            recommended=False,
        )
        entry["requires"] = ["speaker-pack"] if entry["job"] == "live" else []
        entries.append(entry)
    for new in decisions.get("additional_models", []):
        entry = copy.deepcopy(new)
        inventory = json.loads((ROOT / entry["inventory"]).read_text())
        entry["files"] = [{**f, "sources": [f["url"]]} for f in inventory["files"]]
        entry["revision"] = inventory.get("revision")
        entry["size_bytes"] = sum(f["size_bytes"] for f in entry["files"])
        entry["installed_size_bytes"] = inventory.get(
            "installed_size_bytes", entry["size_bytes"]
        )
        entry["recommended"] = False
        entries.append(entry)
    entries.extend(copy.deepcopy(decisions.get("system_entries", [])))
    for entry in entries:
        language_id = entry.get("artifact_id", entry["id"])
        if language_id not in languages:
            raise ValueError(f"Missing language declaration: {language_id}")
        entry.update(copy.deepcopy(languages[language_id]))
        entry.update(decisions.get("entry_updates", {}).get(entry["id"], {}))
    recommendations = decisions.get("recommendations", {})
    for job, model_id in recommendations.items():
        manifest["recommendations"][job] = model_id
    manifest["alternatives_without_gpl"].update(
        decisions.get("alternatives_without_gpl", {})
    )
    for entry in entries:
        entry["recommended"] = (
            manifest["recommendations"].get(entry["job"]) == entry["id"]
        )
    manifest["schema_version"] = 4
    manifest["gpl_component_scope"] = (
        "Required components of each evaluated route, not a full runtime SBOM. Package cleared models separately from the survey environment's unused Kokoro/eSpeak dependencies. Acceptance permits a GPL-component option; it does not add language coverage or make it the multilingual default."
    )
    manifest["language_semantics"] = (
        "languages lists artifact-declared BCP-47/ISO language codes; tested_languages lists completed part-2 subsets only. Language-independent support and disabled choices use an empty list, not invented ASR/TTS capabilities."
    )
    manifest["multilingual_evidence"] = (
        str((OUT / "manifest-update.json").relative_to(ROOT))
        if decision_path.exists()
        else None
    )
    manifest["survey_reports"] = [
        "RESULTS-2026-10.md",
        "RESULTS-2026-10-multilingual.md",
    ]
    return manifest
