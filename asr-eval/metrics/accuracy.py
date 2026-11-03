"""Accuracy metrics for ASR evaluation: WER, CER, MER."""

from jiwer import wer, cer, mer

from metrics.normalize import normalize_text, split_for_cer


def compute_accuracy_metrics(
    hypothesis: str,
    reference: str,
    language: str = "en",
) -> dict[str, float]:
    """Compute WER, CER, and MER between hypothesis and reference.

    Args:
        hypothesis: ASR engine output text.
        reference: Ground-truth transcription.
        language: Language code for normalization ("en", "zh", "zh-en").

    Returns:
        Dict with keys: wer, cer, mer (values in [0, 1] range, lower is better).
    """
    norm_hyp = normalize_text(hypothesis, language)
    norm_ref = normalize_text(reference, language)

    if not norm_ref:
        return {"wer": 0.0, "cer": 0.0, "mer": 0.0}

    if not norm_hyp:
        return {"wer": 1.0, "cer": 1.0, "mer": 1.0}

    if language in ("zh", "zh-en"):
        wer_hyp = " ".join(split_for_cer(norm_hyp))
        wer_ref = " ".join(split_for_cer(norm_ref))
    else:
        wer_hyp = norm_hyp
        wer_ref = norm_ref

    cer_hyp = " ".join(split_for_cer(norm_hyp))
    cer_ref = " ".join(split_for_cer(norm_ref))

    return {
        "wer": wer(wer_ref, wer_hyp),
        "cer": cer(cer_ref, cer_hyp),
        "mer": mer(wer_ref, wer_hyp),
    }


def compute_batch_accuracy(
    hypotheses: list[str],
    references: list[str],
    language: str = "en",
) -> dict[str, float]:
    """Compute aggregate accuracy metrics over a batch of utterances."""
    assert len(hypotheses) == len(references), "Mismatched hypothesis/reference counts"

    all_metrics = [
        compute_accuracy_metrics(h, r, language) for h, r in zip(hypotheses, references)
    ]

    n = len(all_metrics)
    return {
        "wer": sum(m["wer"] for m in all_metrics) / n,
        "cer": sum(m["cer"] for m in all_metrics) / n,
        "mer": sum(m["mer"] for m in all_metrics) / n,
        "num_samples": n,
    }
