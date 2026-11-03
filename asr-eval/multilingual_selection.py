"""Qualification before ranking: coverage, completed measurements and speed.

These are evaluation decisions, not installation authority or licence consent.
"""

import statistics

REQUIRED_ASR = ["en", "zh", "ja", "ko", "es", "fr", "de"]
REQUIRED_VOICE = ["en", "zh", "ja", "es", "fr", "de"]


def rank_asr(runs, maximum_rtf):
    eligible = []
    for name, run in runs.items():
        data = run.get("datasets", {})
        if (
            name == "sensevoice"
            or run.get("status") != "complete"
            or not set(REQUIRED_ASR) <= set(data)
        ):
            continue
        if any(
            data[lang].get("status") != "complete"
            or len(data[lang]["samples"]) != 100
            or data[lang]["failed_samples"]
            or data[lang]["rtf"] > maximum_rtf
            for lang in REQUIRED_ASR
        ):
            continue
        eligible.append(
            (
                statistics.mean(
                    data[lang]["accuracy"]["error_rate"] for lang in REQUIRED_ASR
                ),
                name,
            )
        )
    return sorted(eligible)


def primary_briefs(run):
    """Select an explicit measured voice; appending observations cannot switch it."""
    chosen = {}
    for brief in run["briefs"]:
        chosen.setdefault(brief["language"], brief)
    for lang, voice in run.get("primary_voices", {}).items():
        matches = [
            b for b in run["briefs"] if b["language"] == lang and b["voice"] == voice
        ]
        if len(matches) != 1:
            raise ValueError(f"Primary voice needs one measured brief: {lang}/{voice}")
        chosen[lang] = matches[0]
    return chosen


def qualify_voice(run, maximum_first_sentence_s=2.0):
    if run.get("status") != "complete":
        return False
    try:
        briefs = primary_briefs(run)
    except ValueError:
        return False
    if not set(REQUIRED_VOICE) <= set(briefs):
        return False
    for lang in REQUIRED_VOICE:
        trials = [
            t
            for r in run["first_sentence_trials"]
            if r["language"] == lang and r["voice"] == briefs[lang]["voice"]
            for t in r["seconds"]
        ]
        trials.extend(
            r["synthesis_s"]
            for r in run.get("samples", [])
            if r["language"] == lang
            and r["voice"] == briefs[lang]["voice"]
            and r["sentence_index"] == 0
        )
        if not trials or max(trials) > maximum_first_sentence_s:
            return False
        if (
            briefs[lang]["first_sentence_s"] > maximum_first_sentence_s
            or briefs[lang]["rtf"] >= 1
        ):
            return False
    return True


def rank_voice(runs, back_asr, cleared_profiles):
    """Rank eligible primary voices on the five common sentence samples.

    Repeated full briefs remain separate quality evidence. They must not
    overwhelm the short-sentence comparison by repeating the same sentences.
    """
    eligible = []
    for name, run in runs.items():
        if name not in cleared_profiles or not qualify_voice(run):
            continue
        voices = {lang: b["voice"] for lang, b in primary_briefs(run).items()}
        errors = [
            back_asr["by_model_language_voice"][
                "/".join([name, lang, voices[lang], "sentence"])
            ]["error_rate"]
            for lang in REQUIRED_VOICE
        ]
        eligible.append((statistics.mean(errors), name))
    # Equal intelligibility scores prefer lower full-brief synthesis cost.
    return sorted(
        eligible,
        key=lambda item: (
            item[0],
            statistics.mean(b["rtf"] for b in primary_briefs(runs[item[1]]).values()),
            item[1],
        ),
    )
