# Voice listening samples

The 34 WAVs are kept in `~/src/model-survey-samples/` on the survey Mac.
The hub sends these files to Bo. No WAV is stored in this branch's history.

[`samples.json`](samples.json) lists each exact filename, sentence, language,
model, voice, exact byte size (`size_bytes`), SHA-256, sample rate and duration. The
[prompt text](../tts_prompts.json) and [measurements](../RESULTS-2026-10.md)
remain separate from a listening judgment. All files are mono PCM16, normalized
to a 0.95 peak after timed synthesis; none was played automatically.

Start with prompt index 0 for each model/language. Listen for warmth, natural
pauses, Chinese pronunciation, API pronunciation, missing words and clip endings.
Back-ASR scores measure intelligibility through another recognizer and do not
decide which voice sounds natural.

| Model | English primary voice | Chinese primary voice | Additional first-sentence voices |
|---|---|---|---|
| Kokoro English v0.19 | `0`, three sentences | English only | English `1` |
| MeloTTS zh_en | `0`, three sentences | `0`, three sentences | One speaker |
| Kokoro v1.1 int8 | `0`, three sentences | `3`, three sentences | English `1`, Chinese `58` |
| Kokoro v1.0 MLX | `af_heart`, three sentences | `zf_xiaobei`, three sentences | `am_adam`, `zm_yunxi` |
| Ming 3.0 0.5B 8-bit MLX | `UK_Charlotte`, three sentences | `灵小柔`, three sentences | `US_James`, `灵信远` |

Kokoro's eSpeak phonemizer makes its licence status restricted: GPL component,
needs source offer and notices if bundled. Ming is the cleared default
without a GPL phonemizer; it is substantially slower in the brief probe. Kokoro
can be recommended only when `gplComplianceAccepted` is explicitly true; the
committed manifest defaults it to false.

Run from `asr-eval/` to verify the retained files without downloading models:

```sh
python3 validate_survey.py --samples-dir ~/src/model-survey-samples
```

Full-brief throughput WAVs were temporary evaluation output. Their measurements
remain in the run JSON, and superseded/truncated timings remain diagnostic only.
