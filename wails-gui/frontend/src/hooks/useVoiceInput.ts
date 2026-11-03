import { useState, useRef, useCallback, useEffect } from "react";
import { EventsOn, LogError, LogInfo } from "../../wailsjs/runtime/runtime";
import {
    GetPlatformPermissions,
    InitVoiceInput,
    TranscribeAudio,
    GetVoiceInputStatus,
    OpenPermissionSettings,
    RequestPermission,
} from "../../wailsjs/go/main/App";

export type VoiceInputState =
    | "idle"
    | "initializing"
    | "recording"
    | "transcribing";

interface VoiceInputStatus {
    available: boolean;
    ready: boolean;
    downloading: boolean;
    building: boolean;
    error?: string;
}

interface UseVoiceInputReturn {
    state: VoiceInputState;
    error: string | null;
    statusMessage: string | null;
    liveTranscript: string;
    startRecording: () => Promise<void>;
    stopAndTranscribe: () => Promise<string>;
    cancelRecording: () => void;
    retryInitialization: () => Promise<void>;
    openMicrophoneSettings: () => Promise<void>;
    needsMicrophonePermission: boolean;
    /** Download progress, null when not downloading */
    downloadProgress: {
        fileName: string;
        downloaded: number;
        total: number;
        bytesPerSec: number;
        percent: number;
        speed: string;
    } | null;
    /** Real-time audio level (0-1), updated ~60fps during recording */
    audioLevel: number;
}

interface UseVoiceInputOptions {
    onRecordingStart?: () => void;
    onTranscriptUpdate?: (text: string) => void;
    onRecordingCancel?: () => void;
}

const MICROPHONE_PERMISSION_ERROR =
    "Microphone permission is blocked. Allow Isoform in System Settings > Privacy & Security > Microphone, then try voice input again.";

const VOICE_STREAM_MIN_CHUNK_MS = 2500;
const VOICE_STREAM_MAX_CHUNK_MS = 6000;
const VOICE_STREAM_SILENCE_FLUSH_MS = 500;
const VOICE_STREAM_SILENCE_THRESHOLD = 0.015;

function isMicrophonePermissionDeniedMessage(message: string): boolean {
    const normalized = message.toLowerCase();
    return (
        normalized.includes("notallowederror") ||
        normalized.includes("permission denied") ||
        normalized.includes("permission dismissed") ||
        normalized.includes("microphone access denied") ||
        normalized.includes("not allowed by the user agent") ||
        normalized.includes(
            "the request is not allowed by the user agent or the platform in the current context",
        ) ||
        normalized.includes("access denied") ||
        normalized.includes("denied")
    );
}

function isMicrophonePermissionDeniedError(err: unknown): boolean {
    const message = err instanceof Error ? err.message : String(err);
    const name = err instanceof Error ? err.name : "";
    return (
        isMicrophonePermissionDeniedMessage(message) ||
        name.toLowerCase().includes("notallowederror")
    );
}

function isMicrophoneUnavailableMessage(message: string): boolean {
    const normalized = message.toLowerCase();
    return (
        normalized.includes("notfounderror") ||
        normalized.includes("requested device not found") ||
        normalized.includes("no microphone")
    );
}

function isVoiceModelMissingMessage(message: string): boolean {
    return message.toLowerCase().includes("model not downloaded");
}

/**
 * Encode raw PCM float32 samples as a 16-bit mono WAV file.
 */
function encodeWav(samples: Float32Array, sampleRate: number): ArrayBuffer {
    const numSamples = samples.length;
    const buffer = new ArrayBuffer(44 + numSamples * 2);
    const view = new DataView(buffer);

    const writeString = (offset: number, str: string) => {
        for (let i = 0; i < str.length; i++) {
            view.setUint8(offset + i, str.charCodeAt(i));
        }
    };

    // RIFF header
    writeString(0, "RIFF");
    view.setUint32(4, 36 + numSamples * 2, true);
    writeString(8, "WAVE");

    // fmt chunk
    writeString(12, "fmt ");
    view.setUint32(16, 16, true); // chunk size
    view.setUint16(20, 1, true); // PCM format
    view.setUint16(22, 1, true); // mono
    view.setUint32(24, sampleRate, true); // sample rate
    view.setUint32(28, sampleRate * 2, true); // byte rate
    view.setUint16(32, 2, true); // block align
    view.setUint16(34, 16, true); // bits per sample

    // data chunk
    writeString(36, "data");
    view.setUint32(40, numSamples * 2, true);

    // PCM data — convert float32 [-1, 1] to int16
    const offset = 44;
    for (let i = 0; i < numSamples; i++) {
        const s = Math.max(-1, Math.min(1, samples[i]));
        view.setInt16(offset + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true);
    }

    return buffer;
}

function arrayBufferToBase64(buffer: ArrayBuffer): Promise<string> {
    return new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onerror = () =>
            reject(
                reader.error ??
                    new Error("Failed to encode WAV audio as base64"),
            );
        reader.onload = () => {
            const result = reader.result;
            if (typeof result !== "string") {
                reject(new Error("Unexpected FileReader result type"));
                return;
            }

            const [, base64 = ""] = result.split(",", 2);
            resolve(base64);
        };
        reader.readAsDataURL(new Blob([buffer], { type: "audio/wav" }));
    });
}

function formatSpeed(bytesPerSec: number): string {
    if (bytesPerSec > 1024 * 1024) {
        return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
    }
    return `${(bytesPerSec / 1024).toFixed(0)} KB/s`;
}

function mergeFloat32Chunks(
    chunks: Float32Array[],
    totalLength?: number,
): Float32Array {
    const length =
        totalLength ??
        chunks.reduce((sum, chunk) => sum + chunk.length, 0);
    const merged = new Float32Array(length);
    let offset = 0;
    for (const chunk of chunks) {
        merged.set(chunk, offset);
        offset += chunk.length;
    }
    return merged;
}

function isCJKChar(char: string): boolean {
    if (!char) {
        return false;
    }

    const code = char.charCodeAt(0);
    return (
        (code >= 0x3400 && code <= 0x4dbf) ||
        (code >= 0x4e00 && code <= 0x9fff) ||
        (code >= 0xf900 && code <= 0xfaff)
    );
}

function appendTranscriptText(existing: string, next: string): string {
    const left = existing.trim();
    const right = next.trim();
    if (!left) {
        return right;
    }
    if (!right) {
        return left;
    }

    const trailing = left[left.length - 1];
    const leading = right[0];
    const shouldInsertSpace =
        !/\s/.test(trailing) &&
        !/[\s,.;:!?，。！？、]/.test(leading) &&
        !isCJKChar(trailing) &&
        !isCJKChar(leading);

    return shouldInsertSpace ? `${left} ${right}` : `${left}${right}`;
}

export function useVoiceInput(
    options: UseVoiceInputOptions = {},
): UseVoiceInputReturn {
    const { onRecordingStart, onTranscriptUpdate, onRecordingCancel } = options;
    const [state, setState] = useState<VoiceInputState>("idle");
    const [error, setError] = useState<string | null>(null);
    const [statusMessage, setStatusMessage] = useState<string | null>(null);
    const [liveTranscript, setLiveTranscript] = useState("");
    const [downloadProgress, setDownloadProgress] = useState<{
        fileName: string;
        downloaded: number;
        total: number;
        bytesPerSec: number;
        percent: number;
        speed: string;
    } | null>(null);

    const [audioLevel, setAudioLevel] = useState(0);

    const streamRef = useRef<MediaStream | null>(null);
    const contextRef = useRef<AudioContext | null>(null);
    const processorRef = useRef<ScriptProcessorNode | null>(null);
    const chunksRef = useRef<Float32Array[]>([]);
    const recordedSampleCountRef = useRef(0);
    const streamChunksRef = useRef<Float32Array[]>([]);
    const streamSampleCountRef = useRef(0);
    const silenceMsRef = useRef(0);
    const transcriptTextRef = useRef("");
    const transcriptionChainRef = useRef<Promise<void>>(Promise.resolve());
    const streamFallbackRequiredRef = useRef(false);
    const streamErrorRef = useRef<string | null>(null);
    const sampleRateRef = useRef<number>(16000);
    const lastLevelUpdateRef = useRef<number>(0);
    const startRequestIdRef = useRef(0);
    const shouldRecordRef = useRef(false);

    const syncBackendStatus = useCallback((status: VoiceInputStatus) => {
        if (status.ready) {
            setState((prev) =>
                prev === "recording" || prev === "transcribing" ? prev : "idle",
            );
            setError(null);
            setStatusMessage(null);
            setDownloadProgress(null);
            return;
        }

        if (status.error && isVoiceModelMissingMessage(status.error)) {
            setState("idle");
            setError(null);
            return;
        }

        if (status.downloading || status.building) {
            setState((prev) =>
                prev === "recording" || prev === "transcribing"
                    ? prev
                    : "initializing",
            );
            setError(status.error ?? null);
            setStatusMessage((prev) => {
                if (prev) {
                    return prev;
                }
                return status.building
                    ? "Preparing voice input..."
                    : "Downloading voice input model...";
            });
            return;
        }

        setState((prev) =>
            prev === "recording" || prev === "transcribing" ? prev : "idle",
        );
        if (status.error) {
            if (isMicrophonePermissionDeniedMessage(status.error)) {
                setError(MICROPHONE_PERMISSION_ERROR);
                return;
            }
            if (isMicrophoneUnavailableMessage(status.error)) {
                setError(
                    "No microphone is available. Connect a microphone, then try voice input again.",
                );
                return;
            }
            setError(status.error);
        }
    }, []);

    const ensureBackgroundInitialization = useCallback(
        async (force: boolean) => {
            setError(null);

            try {
                const status: VoiceInputStatus = await GetVoiceInputStatus();
                syncBackendStatus(status);
                if (status.ready || status.downloading || status.building) {
                    return;
                }

                if (!force) {
                    setState("initializing");
                    setStatusMessage("Preparing voice input...");
                } else {
                    setState("initializing");
                    setStatusMessage("Retrying voice input setup...");
                }

                await InitVoiceInput();
            } catch (err) {
                const msg = err instanceof Error ? err.message : String(err);
                if (isVoiceModelMissingMessage(msg)) {
                    setError(null);
                    setState("idle");
                    return;
                }
                if (msg.includes("already initializing")) {
                    setState("initializing");
                    return;
                }

                setError(msg);
                setState("idle");
            }
        },
        [syncBackendStatus],
    );

    // Listen for backend events.
    useEffect(() => {
        void ensureBackgroundInitialization(false);

        const cancelVoiceStatus = EventsOn(
            "voice:status",
            (data: VoiceInputStatus) => {
                syncBackendStatus(data);
            },
        );
        const cancelStatus = EventsOn(
            "voice:event",
            (data: { kind: string; message: string }) => {
                setStatusMessage(data.message);
            },
        );
        const cancelProgress = EventsOn(
            "voice:download-progress",
            (data: {
                fileName: string;
                downloaded: number;
                total: number;
                bytesPerSec: number;
            }) => {
                if (data.total > 0) {
                    setDownloadProgress({
                        fileName: data.fileName,
                        downloaded: data.downloaded,
                        total: data.total,
                        bytesPerSec: data.bytesPerSec,
                        percent: Math.round(
                            (data.downloaded / data.total) * 100,
                        ),
                        speed: formatSpeed(data.bytesPerSec),
                    });
                }
            },
        );
        return () => {
            cancelVoiceStatus();
            cancelStatus();
            cancelProgress();
        };
    }, [ensureBackgroundInitialization, syncBackendStatus]);

    const cleanup = useCallback(() => {
        if (processorRef.current) {
            processorRef.current.disconnect();
            processorRef.current = null;
        }
        if (contextRef.current) {
            contextRef.current.close();
            contextRef.current = null;
        }
        if (streamRef.current) {
            streamRef.current.getTracks().forEach((t) => t.stop());
            streamRef.current = null;
        }
        setAudioLevel(0);
    }, []);

    useEffect(() => cleanup, [cleanup]);

    const resetStreamingBuffers = useCallback(() => {
        chunksRef.current = [];
        recordedSampleCountRef.current = 0;
        streamChunksRef.current = [];
        streamSampleCountRef.current = 0;
        silenceMsRef.current = 0;
        transcriptTextRef.current = "";
        transcriptionChainRef.current = Promise.resolve();
        streamFallbackRequiredRef.current = false;
        streamErrorRef.current = null;
        setLiveTranscript("");
    }, []);

    const enqueueStreamTranscription = useCallback(
        (
            samples: Float32Array,
            sampleRate: number,
            requestId: number,
            reason: "partial" | "final",
        ): Promise<void> => {
            transcriptionChainRef.current = transcriptionChainRef.current
                .catch(() => {})
                .then(async () => {
                    if (startRequestIdRef.current !== requestId) {
                        return;
                    }

                    try {
                        const wavBuffer = encodeWav(samples, sampleRate);
                        const wavBase64 = await arrayBufferToBase64(wavBuffer);
                        LogInfo(
                            `[voice-input] ${reason} chunk queued: samples=${samples.length} sampleRate=${sampleRate} payload=${wavBase64.length}`,
                        );
                        const text = await TranscribeAudio(wavBase64);
                        if (startRequestIdRef.current !== requestId) {
                            return;
                        }

                        const trimmed = text.trim();
                        if (!trimmed) {
                            return;
                        }

                        transcriptTextRef.current = appendTranscriptText(
                            transcriptTextRef.current,
                            trimmed,
                        );
                        setLiveTranscript(transcriptTextRef.current);
                        onTranscriptUpdate?.(transcriptTextRef.current);
                    } catch (err) {
                        const message =
                            err instanceof Error ? err.message : String(err);
                        streamFallbackRequiredRef.current = true;
                        streamErrorRef.current = message;
                        LogError(
                            `[voice-input] ${reason} chunk transcription failed: ${message}`,
                        );
                    }
                });

            return transcriptionChainRef.current;
        },
        [onTranscriptUpdate],
    );

    const queueBufferedChunk = useCallback(
        (requestId: number, reason: "partial" | "final"): Promise<void> => {
            if (streamSampleCountRef.current === 0) {
                return Promise.resolve();
            }

            const sampleRate = sampleRateRef.current;
            const bufferedMs =
                (streamSampleCountRef.current / sampleRate) * 1000;
            if (
                reason === "partial" &&
                bufferedMs < VOICE_STREAM_MIN_CHUNK_MS
            ) {
                return Promise.resolve();
            }

            const chunk = mergeFloat32Chunks(
                streamChunksRef.current,
                streamSampleCountRef.current,
            );
            streamChunksRef.current = [];
            streamSampleCountRef.current = 0;
            silenceMsRef.current = 0;

            return enqueueStreamTranscription(
                chunk,
                sampleRate,
                requestId,
                reason,
            );
        },
        [enqueueStreamTranscription],
    );

    const openMicrophoneSettings = useCallback(async () => {
        await OpenPermissionSettings("microphone");
    }, []);

    const waitForVoiceReady = useCallback(
        async (requestId: number): Promise<boolean> => {
            const deadline = Date.now() + 30 * 60 * 1000;

            while (Date.now() < deadline) {
                if (
                    !shouldRecordRef.current ||
                    startRequestIdRef.current !== requestId
                ) {
                    return false;
                }

                const status: VoiceInputStatus = await GetVoiceInputStatus();
                if (status.ready) {
                    return true;
                }
                if (status.error) {
                    throw new Error(status.error);
                }

                await new Promise<void>((resolve) => {
                    window.setTimeout(resolve, 250);
                });
            }

            throw new Error("Voice input initialization timed out");
        },
        [],
    );

    const ensureMicrophonePermission = useCallback(async (): Promise<boolean> => {
        const permissions = await GetPlatformPermissions();
        const microphoneItem = permissions.items.find(
            (item) => item.key === "microphone",
        );

        LogInfo(
            `[voice-input] platform permissions loaded: platform=${permissions.platform} microphoneItem=${microphoneItem ? JSON.stringify({ granted: microphoneItem.granted, actionable: microphoneItem.actionable }) : "missing"}`,
        );

        // Non-macOS platforms do not expose a microphone permission item.
        if (!microphoneItem || !microphoneItem.actionable) {
            return true;
        }

        if (microphoneItem.granted) {
            return true;
        }

        const granted = await RequestPermission("microphone");
        const nextPermissions = await GetPlatformPermissions();
        const updatedMicrophoneItem = nextPermissions.items.find(
            (item) => item.key === "microphone",
        );

        LogInfo(
            `[voice-input] RequestPermission(microphone): granted=${granted} backendGranted=${updatedMicrophoneItem?.granted ?? false}`,
        );

        return granted && (updatedMicrophoneItem?.granted ?? false);
    }, []);

    const startRecording = useCallback(async () => {
        const requestId = startRequestIdRef.current + 1;
        startRequestIdRef.current = requestId;
        shouldRecordRef.current = true;

        setError(null);
        setStatusMessage(null);
        setDownloadProgress(null);
        resetStreamingBuffers();

        // Ensure whisper is ready (lazy init).
        try {
            const status: VoiceInputStatus = await GetVoiceInputStatus();
            if (!status.ready) {
                setState("initializing");
                if (!status.downloading && !status.building) {
                    try {
                        await InitVoiceInput();
                    } catch (err) {
                        const msg =
                            err instanceof Error ? err.message : String(err);
                        if (!msg.includes("already initializing")) {
                            throw err;
                        }
                    }
                }

                const ready = await waitForVoiceReady(requestId);
                if (!ready) {
                    setState("idle");
                    return;
                }
            }
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            setError(msg);
            setState("idle");
            return;
        }

        try {
            const hasMicrophonePermission =
                await ensureMicrophonePermission();
            if (!hasMicrophonePermission) {
                setError(MICROPHONE_PERMISSION_ERROR);
                setState("idle");
                return;
            }
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            const name = err instanceof Error ? err.name : "UnknownError";
            LogError(
                `[voice-input] RequestPermission(microphone) failed: ${name}: ${msg}`,
            );
            if (isMicrophonePermissionDeniedError(err)) {
                setError(MICROPHONE_PERMISSION_ERROR);
            } else {
                setError(`Failed to request microphone permission: ${msg}`);
            }
            setState("idle");
            return;
        }

        // Request microphone access.
        try {
            const stream = await navigator.mediaDevices.getUserMedia({
                audio: {
                    channelCount: 1,
                    echoCancellation: true,
                    noiseSuppression: true,
                },
            });

            if (
                !shouldRecordRef.current ||
                startRequestIdRef.current !== requestId
            ) {
                stream.getTracks().forEach((track) => track.stop());
                setState("idle");
                return;
            }

            const audioCtx = new AudioContext();
            const source = audioCtx.createMediaStreamSource(stream);

            // ScriptProcessorNode is deprecated but widely supported in WKWebView.
            // AudioWorklet may not be available in all WebView contexts.
            const processor = audioCtx.createScriptProcessor(4096, 1, 1);

            sampleRateRef.current = audioCtx.sampleRate;

            processor.onaudioprocess = (e) => {
                if (
                    !shouldRecordRef.current ||
                    startRequestIdRef.current !== requestId
                ) {
                    return;
                }

                const inputData = e.inputBuffer.getChannelData(0);
                const chunk = new Float32Array(inputData);
                chunksRef.current.push(chunk);
                recordedSampleCountRef.current += chunk.length;
                streamChunksRef.current.push(chunk);
                streamSampleCountRef.current += chunk.length;

                // Compute RMS audio level directly from PCM buffer.
                // Throttle React state updates to ~15fps to avoid perf issues.
                const now = performance.now();
                let sum = 0;
                for (let i = 0; i < inputData.length; i++) {
                    sum += inputData[i] * inputData[i];
                }
                const rms = Math.sqrt(sum / inputData.length);
                const bufferDurationMs =
                    (chunk.length / sampleRateRef.current) * 1000;
                if (rms <= VOICE_STREAM_SILENCE_THRESHOLD) {
                    silenceMsRef.current += bufferDurationMs;
                } else {
                    silenceMsRef.current = 0;
                }

                const bufferedMs =
                    (streamSampleCountRef.current / sampleRateRef.current) *
                    1000;
                if (
                    bufferedMs >= VOICE_STREAM_MAX_CHUNK_MS ||
                    (bufferedMs >= VOICE_STREAM_MIN_CHUNK_MS &&
                        silenceMsRef.current >=
                            VOICE_STREAM_SILENCE_FLUSH_MS)
                ) {
                    void queueBufferedChunk(requestId, "partial");
                }

                if (now - lastLevelUpdateRef.current < 66) return;
                lastLevelUpdateRef.current = now;

                // Higher gain + power-curve compression so whispers
                // produce visible waveform movement.
                const amplified = Math.min(1, rms * 10);
                setAudioLevel(amplified > 0 ? Math.pow(amplified, 0.35) : 0);
            };

            source.connect(processor);
            processor.connect(audioCtx.destination);

            streamRef.current = stream;
            contextRef.current = audioCtx;
            processorRef.current = processor;

            setState("recording");
            onRecordingStart?.();
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            const name = err instanceof Error ? err.name : "UnknownError";
            LogError(`[voice-input] getUserMedia failed: ${name}: ${msg}`);
            if (isMicrophonePermissionDeniedError(err)) {
                setError(MICROPHONE_PERMISSION_ERROR);
            } else if (isMicrophoneUnavailableMessage(msg)) {
                setError(
                    "No microphone is available. Connect a microphone, then try voice input again.",
                );
            } else {
                setError(`Microphone access failed: ${msg}`);
            }
            setState("idle");
        }
    }, [
        ensureMicrophonePermission,
        onRecordingStart,
        queueBufferedChunk,
        resetStreamingBuffers,
        waitForVoiceReady,
    ]);

    const stopAndTranscribe = useCallback(async (): Promise<string> => {
        if (state !== "recording") return "";

        const requestId = startRequestIdRef.current;
        shouldRecordRef.current = false;
        setState("transcribing");
        setStatusMessage("Finalizing transcript...");

        const recordedChunks = chunksRef.current;
        const recordedSampleCount = recordedSampleCountRef.current;
        cleanup();

        if (recordedChunks.length === 0) {
            resetStreamingBuffers();
            setState("idle");
            setStatusMessage(null);
            return "";
        }

        try {
            await queueBufferedChunk(requestId, "final");
            await transcriptionChainRef.current.catch(() => {});

            let text = transcriptTextRef.current.trim();
            if (streamFallbackRequiredRef.current) {
                const merged = mergeFloat32Chunks(
                    recordedChunks,
                    recordedSampleCount,
                );
                const wavBuffer = encodeWav(merged, sampleRateRef.current);
                const wavBase64 = await arrayBufferToBase64(wavBuffer);
                LogInfo(
                    `[voice-input] full fallback payload: len=${wavBase64.length} prefix=${wavBase64.slice(0, 16)}`,
                );
                text = (await TranscribeAudio(wavBase64)).trim();
            }

            setLiveTranscript(text);
            onTranscriptUpdate?.(text);
            resetStreamingBuffers();
            setState("idle");
            setStatusMessage(null);
            if (onTranscriptUpdate) {
                return "";
            }
            return text;
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            setError(msg);
            resetStreamingBuffers();
            setState("idle");
            setStatusMessage(null);
            return "";
        }
    }, [
        cleanup,
        onTranscriptUpdate,
        queueBufferedChunk,
        resetStreamingBuffers,
        state,
    ]);

    const cancelRecording = useCallback(() => {
        startRequestIdRef.current += 1;
        shouldRecordRef.current = false;
        cleanup();
        onRecordingCancel?.();
        resetStreamingBuffers();
        setState("idle");
        setStatusMessage(null);
        setDownloadProgress(null);
    }, [cleanup, onRecordingCancel, resetStreamingBuffers]);

    return {
        state,
        error,
        statusMessage,
        liveTranscript,
        startRecording,
        stopAndTranscribe,
        cancelRecording,
        retryInitialization: () => ensureBackgroundInitialization(true),
        openMicrophoneSettings,
        needsMicrophonePermission: error === MICROPHONE_PERMISSION_ERROR,
        downloadProgress,
        audioLevel,
    };
}
