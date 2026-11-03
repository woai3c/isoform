"""October metrics, separate from the historical June normalizer.

Mandarin: CER. English: WER. Mixed: MER (Han characters + Latin words).
Corpus edit counts are summed; utterances are never averaged for accuracy.
OCR retains punctuation/case because both matter in code and UI text.
"""

import re
import unicodedata

import jiwer


def speech_chunks(text, limit=None):
    """Bound long TTS inputs without losing text at Kokoro's phoneme cap."""
    if limit is None:
        limit = 80 if re.search(r"[\u3400-\u9fff]", text) else 200
    pieces = []
    for sentence in re.findall(r".+?(?:[.!?。！？]+|$)", text, flags=re.DOTALL):
        while len(sentence) > limit:
            cut = sentence.rfind(" ", 0, limit + 1)
            cut = cut + 1 if cut > 0 else limit
            pieces.append(sentence[:cut])
            sentence = sentence[cut:]
        if sentence:
            pieces.append(sentence)
    chunks = []
    current = ""
    for piece in pieces:
        if current and len(current) + len(piece) > limit:
            chunks.append(current)
            current = ""
        current += piece
    if current:
        chunks.append(current)
    if "".join(chunks) != text:
        raise ValueError("TTS chunking dropped input text")
    return chunks


def speech_tokens(text):
    text = unicodedata.normalize("NFKC", text).lower()
    text = re.sub(r"<\|[^|]*\|>", "", text)
    return re.findall(
        r"[\u3400-\u9fff]|[a-z0-9]+(?:'[a-z]+)?|[^\W\d_a-z\u3400-\u9fff]+", text
    )


def corpus_scores(references, hypotheses):
    if len(references) != len(hypotheses) or not references:
        raise ValueError("Nonempty aligned reference/hypothesis lists required")
    ref_tokens = [" ".join(speech_tokens(t)) for t in references]
    hyp_tokens = [" ".join(speech_tokens(t)) for t in hypotheses]
    words = jiwer.process_words(ref_tokens, hyp_tokens)
    ref_chars = ["".join(speech_tokens(t)) for t in references]
    hyp_chars = ["".join(speech_tokens(t)) for t in hypotheses]
    chars = jiwer.process_characters(ref_chars, hyp_chars)
    return {
        "token_error_rate": words.wer,
        "character_error_rate": chars.cer,
        "reference_tokens": words.hits + words.substitutions + words.deletions,
        "reference_characters": chars.hits + chars.substitutions + chars.deletions,
        "token_substitutions": words.substitutions,
        "token_deletions": words.deletions,
        "token_insertions": words.insertions,
    }


def ocr_score(reference, hypothesis):
    def normalize(s):
        # Match the app's NormalizeFullwidth; retain case and code punctuation.
        s = "".join(
            chr(ord(c) - 0xFEE0) if 0xFF01 <= ord(c) <= 0xFF5E else c for c in s
        )
        return re.sub(r"\s+", "", unicodedata.normalize("NFC", s))

    result = jiwer.process_characters(normalize(reference), normalize(hypothesis))
    return {
        "edits": result.substitutions + result.deletions + result.insertions,
        "reference_characters": result.hits + result.substitutions + result.deletions,
        "cer": result.cer,
        "character_accuracy": max(0.0, 1 - result.cer),
    }
