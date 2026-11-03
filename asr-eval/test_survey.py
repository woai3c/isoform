"""Measurement invariants; no mocked model integration tests."""

import subprocess
import sys
import unittest
from pathlib import Path

from survey_metrics import corpus_scores, ocr_score, speech_tokens, speech_chunks
from survey_manifest import APACHE, FUNASR, MIT, apply_licence_status


class SurveyMetricsTests(unittest.TestCase):
    def test_pending_permission_cannot_become_a_recommendation(self):
        entry = {"id": "sensevoice", "licence": FUNASR, "recommended": True}
        apply_licence_status(entry)
        self.assertEqual(entry["licence_status"], "pending")
        self.assertIsNone(entry["non_commercial"])
        self.assertFalse(entry["recommended"])
        self.assertFalse(entry["selectable"])
        self.assertTrue(any("3715" in e["url"] for e in entry["licence_evidence"]))

    def test_phonemizer_obligations_are_separate_from_weight_licence(self):
        entry = {"id": "kokoro_mlx", "licence": APACHE, "non_commercial": False}
        apply_licence_status(entry)
        self.assertEqual(entry["licence_status"], "restricted")
        self.assertEqual(entry["component_licences"][0]["name"], "GPL-3.0-or-later")
        self.assertIn("source offer and notices", entry["licence_status_note"])
        alternative = {"id": entry["alternative_without_gpl"], "licence": MIT}
        apply_licence_status(alternative)
        self.assertEqual(alternative["licence_status"], "cleared")
        self.assertEqual(alternative["gpl_components"], [])

    def test_new_artifact_requires_explicit_licence_review(self):
        with self.assertRaisesRegex(ValueError, "Missing artifact licence review"):
            apply_licence_status({"id": "unreviewed-model", "licence": MIT})

    def test_long_tts_input_is_bounded_without_truncation(self):
        text = ("早上好。请检查代码审查。" * 40) + ("Keep every word and space. " * 20)
        chunks = speech_chunks(text)
        self.assertEqual("".join(chunks), text)
        self.assertTrue(all(0 < len(c) <= 80 for c in chunks))
        self.assertEqual("".join(speech_chunks("无标点" * 150)), "无标点" * 150)

    def test_corpus_weights_reference_length(self):
        # One wrong short utterance plus nine correct words: 1/10, not 1/2.
        scores = corpus_scores(
            ["one", "a b c d e f g h i"], ["two", "a b c d e f g h i"]
        )
        self.assertAlmostEqual(scores["token_error_rate"], 0.1)
        self.assertEqual(scores["reference_tokens"], 10)

    def test_code_switch_tokens_and_true_character_error(self):
        self.assertEqual(
            speech_tokens("请检查 API v2。"), ["请", "检", "查", "api", "v2"]
        )
        scores = corpus_scores(["我们 check API"], ["我们 check APX"])
        self.assertAlmostEqual(scores["token_error_rate"], 1 / 4)
        self.assertAlmostEqual(scores["character_error_rate"], 1 / 10)

    def test_silence_hallucinations_count_as_insertions(self):
        scores = corpus_scores(["", "hello"], ["invented text", "hello"])
        self.assertEqual(scores["token_insertions"], 2)
        self.assertEqual(scores["token_error_rate"], 2)

    def test_ocr_preserves_case_and_code_punctuation(self):
        scores = ocr_score("x != Y", "x = y")
        self.assertEqual(scores["edits"], 2)
        self.assertEqual(scores["reference_characters"], 4)
        self.assertEqual(ocr_score("你好\n世界", "你好 世界")["edits"], 0)

    def test_engine_registry_does_not_require_unselected_runtimes(self):
        subprocess.run(
            [
                sys.executable,
                "-c",
                "import sys; from engines import list_engines; assert 'sherpa_qwen3' in list_engines(); assert 'mlx_qwen3_asr' not in sys.modules; assert 'parakeet_mlx' not in sys.modules; assert 'sherpa_onnx' not in sys.modules",
            ],
            check=True,
            cwd=Path(__file__).parent,
        )

    def test_misaligned_samples_are_rejected(self):
        with self.assertRaises(ValueError):
            corpus_scores(["one"], [])


if __name__ == "__main__":
    unittest.main()
