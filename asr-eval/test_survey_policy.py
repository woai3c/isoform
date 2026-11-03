"""Standard-library guards for recommendation consent and external sample sizes."""

import copy
import hashlib
import json
import tempfile
import unittest
import wave
from pathlib import Path

from survey_assets import ROOT
from survey_samples import verify_sample_files
from validate_survey import validate_recommendations


class RecommendationPolicyTests(unittest.TestCase):
    def setUp(self):
        self.manifest = json.loads((ROOT / "manifest-draft.json").read_text())

    def select_voice(self, model_id="kokoro_mlx"):
        self.manifest["recommendations"]["voice"] = model_id
        for model in self.manifest["models"]:
            if model["job"] == "voice":
                model["recommended"] = model["id"] == model_id

    def test_default_has_no_restricted_recommendation(self):
        self.assertIs(self.manifest["gplComplianceAccepted"], False)
        self.assertEqual(self.manifest["recommendations"]["voice"], "ming_tts")
        self.assertTrue(
            all(
                m["licence_status"] == "cleared"
                for m in self.manifest["models"]
                if m["recommended"]
            )
        )
        validate_recommendations(self.manifest)

    def test_restricted_voice_is_rejected_without_acceptance(self):
        self.select_voice()
        with self.assertRaisesRegex(ValueError, "Unaccepted licence status for voice"):
            validate_recommendations(self.manifest)

    def test_restricted_voice_is_accepted_with_explicit_flag(self):
        self.select_voice()
        self.manifest["gplComplianceAccepted"] = True
        validate_recommendations(self.manifest)
        selected = next(m for m in self.manifest["models"] if m["id"] == "kokoro_mlx")
        self.assertEqual(selected["licence_status"], "restricted")

    def test_cleared_default_is_allowed_with_either_flag_value(self):
        for accepted in (False, True):
            with self.subTest(accepted=accepted):
                self.manifest["gplComplianceAccepted"] = accepted
                validate_recommendations(self.manifest)

    def test_restriction_gate_applies_to_every_job(self):
        for job, model_id in self.manifest["recommendations"].items():
            with self.subTest(job=job):
                candidate = copy.deepcopy(self.manifest)
                model = next(m for m in candidate["models"] if m["id"] == model_id)
                model["licence_status"] = "restricted"
                model["gpl_components"] = ["eSpeak-ng phonemizer and data"]
                model["restriction_acceptance_flag"] = "gplComplianceAccepted"
                with self.assertRaisesRegex(ValueError, "Unaccepted licence status"):
                    validate_recommendations(candidate)
                candidate["gplComplianceAccepted"] = True
                validate_recommendations(candidate)

    def test_pending_is_rejected_even_with_gpl_acceptance(self):
        self.manifest["recommendations"]["live"] = "sensevoice"
        for model in self.manifest["models"]:
            if model["job"] == "live":
                model["recommended"] = model["id"] == "sensevoice"
        for accepted in (False, True):
            with self.subTest(accepted=accepted):
                self.manifest["gplComplianceAccepted"] = accepted
                with self.assertRaisesRegex(ValueError, "pending"):
                    validate_recommendations(self.manifest)

    def test_gpl_flag_does_not_accept_unrelated_restrictions(self):
        self.select_voice()
        self.manifest["gplComplianceAccepted"] = True
        model = next(m for m in self.manifest["models"] if m["id"] == "kokoro_mlx")
        model["restriction_acceptance_flag"] = "otherRestrictionAccepted"
        self.manifest["otherRestrictionAccepted"] = True
        with self.assertRaisesRegex(ValueError, "Unaccepted licence status"):
            validate_recommendations(self.manifest)
        model["restriction_acceptance_flag"] = "gplComplianceAccepted"
        model["non_commercial"] = True
        with self.assertRaisesRegex(ValueError, "Unaccepted licence status"):
            validate_recommendations(self.manifest)

    def test_missing_flags_do_not_accept_restrictions(self):
        self.select_voice()
        del self.manifest["gplComplianceAccepted"]
        with self.assertRaisesRegex(ValueError, "Unaccepted licence status"):
            validate_recommendations(self.manifest)
        self.manifest["gplComplianceAccepted"] = True
        model = next(m for m in self.manifest["models"] if m["id"] == "kokoro_mlx")
        del model["restriction_acceptance_flag"]
        with self.assertRaisesRegex(ValueError, "Unaccepted licence status"):
            validate_recommendations(self.manifest)

    def test_acceptance_requires_a_boolean(self):
        for value in ("false", "true", 1, None):
            with self.subTest(value=value):
                self.manifest["gplComplianceAccepted"] = value
                with self.assertRaisesRegex(ValueError, "must be a boolean"):
                    validate_recommendations(self.manifest)

    def test_recommended_entry_cannot_bypass_the_job_map(self):
        next(m for m in self.manifest["models"] if m["id"] == "kokoro019")[
            "recommended"
        ] = True
        with self.assertRaisesRegex(ValueError, "Unlisted recommended entry"):
            validate_recommendations(self.manifest)


class SampleSizeTests(unittest.TestCase):
    def test_real_wav_size_is_checked_separately_from_hash(self):
        with tempfile.TemporaryDirectory(prefix="model-survey-round2-") as folder:
            directory = Path(folder)
            path = directory / "probe.wav"
            with wave.open(str(path), "wb") as audio:
                audio.setnchannels(1)
                audio.setsampwidth(2)
                audio.setframerate(8000)
                audio.writeframes(b"\0\0" * 80)
            sample = {
                "file_name": path.name,
                "size_bytes": path.stat().st_size,
                "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                "sample_rate": 8000,
                "duration_s": 0.01,
            }
            index = {"samples": [sample]}
            verify_sample_files(index, directory)
            sample["size_bytes"] += 1
            with self.assertRaisesRegex(ValueError, "byte size mismatch"):
                verify_sample_files(index, directory)

    def test_all_34_sample_records_have_exact_positive_byte_counts(self):
        index = json.loads((ROOT / "tts-samples/samples.json").read_text())
        self.assertEqual(len(index["samples"]), 34)
        self.assertTrue(
            all(
                type(s["size_bytes"]) is int and s["size_bytes"] > 0
                for s in index["samples"]
            )
        )


if __name__ == "__main__":
    unittest.main()
