"""Exercise the real Hugging Face local AudioFolder API; no integration mocks."""

import tempfile
import json
import unittest
import wave
from pathlib import Path

from data_prep.download import _download_fleurs, _download_librispeech, download_ascend


class DownloadCacheTests(unittest.TestCase):
    def run_handler(self, family):
        with tempfile.TemporaryDirectory(prefix="model-survey-cache-test-") as temp:
            root = Path(temp)
            source = root / "source"
            source.mkdir()
            with wave.open(str(source / "sample.wav"), "wb") as audio:
                audio.setnchannels(1)
                audio.setsampwidth(2)
                audio.setframerate(16000)
                audio.writeframes(b"\0\0" * 1600)
            (source / "metadata.jsonl").write_text(
                json.dumps(
                    {
                        "file_name": "sample.wav",
                        "text": "cache probe",
                        "transcription": "缓存检查",
                    }
                )
                + "\n"
            )
            output = root / "eval"
            cfg = {"source": str(source), "split": "train"}
            if family == "librispeech":
                count = _download_librispeech(cfg, output, "train", family)
            elif family == "fleurs":
                count = _download_fleurs(cfg, output, "default", family)
            else:
                count = download_ascend(cfg, output)
            self.assertEqual(count, 1)
            manifest = next(output.glob("*/manifest.tsv"))
            self.assertEqual(len(manifest.read_text().splitlines()), 2)
            if family != "librispeech":
                self.assertTrue(list((output / "cache").rglob("*.arrow")))
            self.assertTrue(list(manifest.parent.glob("*.wav")))

    def test_librispeech_streaming_export(self):
        self.run_handler("librispeech")

    def test_fleurs_cache_stays_in_eval_folder(self):
        self.run_handler("fleurs")

    def test_ascend_cache_stays_in_eval_folder(self):
        self.run_handler("ascend")


if __name__ == "__main__":
    unittest.main()
