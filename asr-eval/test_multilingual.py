"""Corpus, provenance, sentence delivery and language-coverage guards."""

import json
import unittest
from multilingual_eval import ROOT, OUT, normalize, score, fleurs_rows
from multilingual_selection import (
    qualify_voice,
    primary_briefs,
    rank_asr,
    rank_voice,
    REQUIRED_ASR,
    REQUIRED_VOICE,
)
from multilingual_tts import brief_sentences, sentence_delivery_gaps


class LanguageDeclarationTests(unittest.TestCase):
    def test_distinct_checkpoints_do_not_inherit_family_language_claims(self):
        models = json.loads((OUT / "languages.json").read_text())["models"]
        self.assertEqual(set(models["funasr_nano"]["languages"]), {"en", "zh", "ja"})
        self.assertEqual(len(models["qwen17"]["languages"]), 30)
        self.assertEqual(len(models["sensevoice"]["languages"]), 5)
        self.assertNotIn("de", models["kokoro_mlx"]["languages"])
        self.assertNotIn("ja", models["ppocr6"]["languages"])
        self.assertNotIn("ko", models["ppocr6"]["languages"])
        self.assertNotIn("zh", models["granite-ctc"]["languages"])

    def test_empty_language_lists_explain_support_or_disabled_semantics(self):
        models = json.loads((OUT / "languages.json").read_text())["models"]
        for model_id, entry in models.items():
            with self.subTest(model=model_id):
                self.assertEqual(len(entry["languages"]), len(set(entry["languages"])))
                self.assertTrue(entry["language_evidence"])
                if not entry["languages"]:
                    self.assertTrue(
                        "support" in entry["language_scope"]
                        or "disabled" in entry["language_scope"]
                    )


class ASRQualificationTests(unittest.TestCase):
    def candidate(self, error=0.1):
        return {
            "status": "complete",
            "datasets": {
                lang: {
                    "status": "complete",
                    "samples": [{}] * 100,
                    "failed_samples": 0,
                    "rtf": 0.2,
                    "accuracy": {"error_rate": error},
                }
                for lang in REQUIRED_ASR
            },
        }

    def test_subset_accuracy_cannot_win_a_global_job(self):
        partial = self.candidate(0.01)
        partial["datasets"].pop("ko")
        self.assertEqual(
            rank_asr({"partial": partial, "global": self.candidate()}, 0.25),
            [(0.1, "global")],
        )

    def test_each_language_must_meet_speed_not_only_the_average(self):
        run = self.candidate()
        run["datasets"]["ja"]["rtf"] = 0.3
        self.assertEqual(rank_asr({"candidate": run}, 0.25), [])
        self.assertEqual(rank_asr({"candidate": run}, 1.0), [(0.1, "candidate")])

    def test_failed_observations_and_pending_sensevoice_cannot_win(self):
        run = self.candidate()
        run["datasets"]["en"]["failed_samples"] = 1
        self.assertEqual(
            rank_asr({"failed": run, "sensevoice": self.candidate(0.01)}, 0.25), []
        )


class VoiceQualificationTests(unittest.TestCase):
    def candidate(self):
        return {
            "status": "complete",
            "briefs": [
                {
                    "language": lang,
                    "voice": "probe",
                    "first_sentence_s": 1.5,
                    "rtf": 0.5,
                }
                for lang in REQUIRED_VOICE
            ],
            "first_sentence_trials": [
                {"language": lang, "voice": "probe", "seconds": [1.1, 1.2, 1.3]}
                for lang in REQUIRED_VOICE
            ],
        }

    def test_speed_requires_every_language_and_each_primary_voice_probe(self):
        run = self.candidate()
        self.assertTrue(qualify_voice(run))
        run["first_sentence_trials"][-1]["seconds"][-1] = 2.01
        self.assertFalse(qualify_voice(run))

    def test_voice_switch_requires_explicit_selection_and_measured_brief(self):
        run = self.candidate()
        run["primary_voices"] = {"fr": "alternative"}
        self.assertFalse(qualify_voice(run))
        run["briefs"].append(
            {
                "language": "fr",
                "voice": "alternative",
                "first_sentence_s": 0.9,
                "rtf": 0.4,
            }
        )
        run["first_sentence_trials"].append(
            {"language": "fr", "voice": "alternative", "seconds": [0.8, 0.9, 0.8]}
        )
        self.assertTrue(qualify_voice(run))
        self.assertEqual(primary_briefs(run)["fr"]["voice"], "alternative")
        del run["primary_voices"]
        self.assertEqual(primary_briefs(run)["fr"]["voice"], "probe")

    def test_quality_ranking_requires_clearance_and_speed_first(self):
        runs = {name: self.candidate() for name in ["fast", "slow", "pending"]}
        runs["slow"]["briefs"][0]["rtf"] = 1.1
        back = {
            "by_model_language_voice": {
                "/".join([name, lang, "probe", "sentence"]): {
                    "error_rate": 0.1 if name == "fast" else 0.0
                }
                for name in runs
                for lang in REQUIRED_VOICE
            }
        }
        self.assertEqual(rank_voice(runs, back, {"fast", "slow"}), [(0.1, "fast")])

    def test_slow_alternative_voice_does_not_disqualify_the_tested_default(self):
        run = self.candidate()
        run["first_sentence_trials"].append(
            {"language": "en", "voice": "alternative", "seconds": [3.0, 3.1, 3.2]}
        )
        self.assertTrue(qualify_voice(run))

    def test_equal_voice_quality_prefers_lower_synthesis_cost(self):
        fast, slow = self.candidate(), self.candidate()
        for brief in slow["briefs"]:
            brief["rtf"] = 0.8
        back = {
            "by_model_language_voice": {
                "/".join([name, lang, "probe", "sentence"]): {"error_rate": 0.0}
                for name in ["a_slow", "z_fast"]
                for lang in REQUIRED_VOICE
            }
        }
        self.assertEqual(
            rank_voice({"a_slow": slow, "z_fast": fast}, back, {"a_slow", "z_fast"}),
            [(0.0, "z_fast"), (0.0, "a_slow")],
        )

    def test_saved_primary_greeting_is_also_an_observed_first_sentence(self):
        run = self.candidate()
        run["samples"] = [
            {
                "language": "en",
                "voice": "probe",
                "sentence_index": 0,
                "synthesis_s": 2.01,
            }
        ]
        self.assertFalse(qualify_voice(run))

    def test_fast_total_does_not_erase_missing_languages_or_slow_sentence(self):
        run = self.candidate()
        run["briefs"].pop()
        self.assertFalse(qualify_voice(run))
        run = self.candidate()
        run["briefs"][-1]["first_sentence_s"] = 2.1
        self.assertFalse(qualify_voice(run))
        run = self.candidate()
        run["briefs"][-1]["rtf"] = 1.0
        self.assertFalse(qualify_voice(run))


class MultilingualMetricsTests(unittest.TestCase):
    def test_fleurs_literal_quotes_cannot_consume_the_phonetic_column(self):
        raw = '1749\ta.wav\t"Raw Vichy"" text"\t"normalized vichy"" text\t" p h o n e s |\t363840\tMALE\n'
        row = fleurs_rows(raw, 1)[0]
        self.assertEqual(row[3], '"normalized vichy"" text')
        self.assertNotIn("p h o n e s", row[3])
        with self.assertRaises(ValueError):
            fleurs_rows("broken\trow", 1)

    def test_accents_hangul_and_kana_survive_normalization(self):
        self.assertEqual(
            normalize("Été, Grüße! 한글 カタカナ １２３"),
            ["été", "grüsse", "한글", "カタカナ", "123"],
        )
        self.assertNotEqual(normalize("café"), normalize("cafe"))

    def test_european_word_boundaries_are_preserved(self):
        result = score(["un élève très prêt"], ["un élève prêt"], "fr")
        self.assertEqual(result["metric"], "WER")
        self.assertAlmostEqual(result["error_rate"], 1 / 4)

    def test_east_asian_character_metric_does_not_need_a_tokenizer(self):
        for lang, reference, hypothesis in [
            ("ja", "今日は晴れ", "今日は雨れ"),
            ("ko", "안녕하세요", "안녕하세오"),
        ]:
            with self.subTest(language=lang):
                result = score([reference], [hypothesis], lang)
                self.assertEqual(result["metric"], "CER")
                self.assertAlmostEqual(result["error_rate"], 1 / 5)

    def test_aggregate_is_weighted_by_reference_units(self):
        self.assertAlmostEqual(
            score(["one", "a b c d e f g h i"], ["two", "a b c d e f g h i"], "en")[
                "error_rate"
            ],
            0.1,
        )

    def test_empty_output_is_a_deletion_not_a_missing_observation(self):
        result = score(["bonjour à tous"], [""], "fr")
        self.assertEqual(result["error_rate"], 1)
        self.assertEqual(result["word_edits"]["deletions"], 3)

    def test_average_faster_than_realtime_can_still_have_sentence_stalls(self):
        traces = [
            {"synthesis_s": 0.5, "audio_s": 1.0},
            {"synthesis_s": 3.0, "audio_s": 8.0},
        ]
        self.assertLess(
            sum(t["synthesis_s"] for t in traces) / sum(t["audio_s"] for t in traces), 1
        )
        self.assertEqual(sentence_delivery_gaps(traces), [0.0, 2.0])

    def test_short_and_empty_inputs_are_rejected(self):
        with self.assertRaises(ValueError):
            score(["one"], [], "en")
        with self.assertRaises(ValueError):
            score([], [], "en")

    def test_brief_keeps_complete_sentences_and_a_substantive_second_sentence(self):
        prompts = json.loads((OUT / "prompts.json").read_text())
        for lang, sentences in prompts.items():
            with self.subTest(language=lang):
                brief = brief_sentences(sentences, lang)
                self.assertEqual(brief[:2], sentences[:2])
                self.assertTrue(all(s in sentences for s in brief))
                text = " ".join(brief)
                if lang in ("zh", "ja"):
                    self.assertGreaterEqual(len("".join(normalize(text))), 552)
                else:
                    self.assertGreaterEqual(len(normalize(text)), 300)


if __name__ == "__main__":
    unittest.main()
