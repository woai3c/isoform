# onnxdl

Shared ONNX Runtime resolution, download, and process-wide initialization for
local inference in the desktop app.

## Why this package exists

Yansu uses ONNX Runtime from more than one feature:

- memory embeddings (`all-MiniLM-L6-v2`)
- PII masking (`gliner-pii-base`)
- sherpa-onnx speech recognition also ships ONNX Runtime in packaged builds

`github.com/yalue/onnxruntime_go` exposes a process-wide runtime environment.
All in-process ONNX callers must therefore agree on one native runtime library.

## Runtime selection contract

Use `ResolveRuntimePaths(configDir)` instead of constructing paths manually.

Resolution order:

1. If the packaged app already contains ONNX Runtime, use that copy.
   - macOS: `Yansu.app/Contents/Frameworks/libonnxruntime.<version>.dylib`
     or `libonnxruntime.dylib`
   - Windows: `onnxruntime.dll` beside `Yansu.exe`
2. Otherwise, download/extract a runtime under
   `<configDir>/onnxruntime/`.

Do not make feature-specific runtime directories. A previous crash report
showed both the bundled runtime and a user-directory runtime loaded in the same
macOS process after ~20 hours of uptime. The crash stack was native and not
fully attributable, but duplicate ORT copies are a high-risk C/C++ global-state
configuration and should be avoided.

## Version notes

- Main packaged/runtime version: `1.24.4`
- macOS x86_64 fallback: `1.23.0`, because newer ORT releases dropped that
  archive target.

When changing the runtime version, verify both memory embeddings and PII model
startup. The PII smoke test is especially important because the fp16 GLiNER
export has failed session creation on ORT with a Cast output type mismatch;
Yansu currently ships the fp32 PII export for compatibility.

## Concurrency

Use `InitializeRuntime(runtimeLib)` for runtime initialization. It serializes
initialization so memory and PII cannot race each other during startup.

Do not call `ort.SetSharedLibraryPath` or `ort.InitializeEnvironment` directly
from feature packages.
