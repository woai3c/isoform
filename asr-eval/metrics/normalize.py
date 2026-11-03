"""Text normalization for fair ASR comparison.

Handles Chinese/English mixed text, punctuation removal, and casing.
"""

import re
import unicodedata


def normalize_text(text: str, language: str = "en") -> str:
    """Normalize transcription text for metric computation.

    For English: lowercase, remove punctuation, collapse whitespace.
    For Chinese: remove punctuation (keep CJK chars), remove spaces between CJK chars.
    For mixed (zh-en): apply both normalizations contextually.
    """
    text = text.strip()
    if not text:
        return ""

    text = _strip_sensevoice_tags(text)

    if language in ("zh", "zh-en"):
        text = _normalize_chinese(text)
    if language in ("en", "zh-en"):
        text = _normalize_english(text)

    text = re.sub(r"\s+", " ", text).strip()
    return text


def _strip_sensevoice_tags(text: str) -> str:
    """Remove SenseVoice control tags like <|zh|>, <|NEUTRAL|>, <|Speech|>."""
    return re.sub(r"<\|[^|]*\|>", "", text)


def _normalize_chinese(text: str) -> str:
    """Normalize Chinese text: remove CJK punctuation, fullwidth chars."""
    cjk_punctuation = (
        "　-〿"  # CJK symbols and punctuation
        "＀-￯"  # fullwidth forms
        " -⁯"  # general punctuation
    )
    text = re.sub(f"[{cjk_punctuation}]", "", text)
    text = unicodedata.normalize("NFKC", text)
    return text


def _normalize_english(text: str) -> str:
    """Normalize English text: lowercase, remove ASCII punctuation."""
    text = text.lower()
    text = re.sub(r"[^\w\s]", "", text)
    return text


def split_for_cer(text: str) -> list[str]:
    """Split text into characters for CER computation.

    CJK characters are split individually. Latin words stay as single tokens
    so CER on English portions still makes sense at the character level.
    """
    tokens = []
    current_word = []
    for char in text:
        if char == " ":
            if current_word:
                tokens.append("".join(current_word))
                current_word = []
            continue
        if _is_cjk(char):
            if current_word:
                tokens.append("".join(current_word))
                current_word = []
            tokens.append(char)
        else:
            current_word.append(char)
    if current_word:
        tokens.append("".join(current_word))
    return tokens


def _is_cjk(char: str) -> bool:
    cp = ord(char)
    return (
        (0x4E00 <= cp <= 0x9FFF)
        or (0x3400 <= cp <= 0x4DBF)
        or (0x20000 <= cp <= 0x2A6DF)
        or (0x2A700 <= cp <= 0x2B73F)
        or (0x2B740 <= cp <= 0x2B81F)
        or (0xF900 <= cp <= 0xFAFF)
        or (0x2F800 <= cp <= 0x2FA1F)
    )
