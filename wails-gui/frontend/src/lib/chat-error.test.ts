import { describe, it, expect } from "vitest";
import { parseChatError } from "./chat-error";

describe("parseChatError", () => {
    it("classifies the Kimi 2 MiB request-body 400 as request_too_large", () => {
        // Verbatim payload observed in production (PostHog agentloop.run.error).
        const raw =
            'anthropic: HTTP 400: {"error":{"type":"invalid_request_error","message":"total message size 3561473 exceeds limit 2097152"},"type":"error"}';
        expect(parseChatError(raw).kind).toBe("request_too_large");
    });

    it("classifies generic 413 / payload-too-large phrasings", () => {
        expect(parseChatError("Request Entity Too Large").kind).toBe("request_too_large");
        expect(parseChatError("413 payload too large").kind).toBe("request_too_large");
    });

    it("does NOT mistake a token/context-window limit for request_too_large", () => {
        // Token-limit overflow is a different problem (compaction's job), and
        // must not be swallowed by the byte-limit bucket.
        expect(parseChatError("exceeded model token limit: 262144").kind).not.toBe(
            "request_too_large",
        );
    });

    it("still classifies the weekly quota 429 correctly", () => {
        const raw =
            'anthropic: HTTP 429: {"code":"USAGE_LIMIT_EXCEEDED","message":"error: code=429 reason=\\"WEEKLY_LIMIT_EXCEEDED\\" message=\\"weekly usage limit exceeded\\""}';
        const err = parseChatError(raw);
        expect(err.kind).toBe("quota_period");
        expect(err.period).toBe("weekly");
    });

    it("falls back to unknown for unclassifiable text", () => {
        expect(parseChatError("something inexplicable happened").kind).toBe("unknown");
    });
});

describe('parseChatError cause codes', () => {
    const authWords = 'anthropic: HTTP 500: {"error":"unauthorized: authentication failed, run claude login, reconnecting 401 403"}';

    it('never reads sign-in from the words', () => {
        for (const raw of ['401 Unauthorized', 'HTTP 403 forbidden', "run 'claude login'", 'reconnecting...', authWords]) {
            expect(parseChatError(raw).kind).not.toBe('signed_out');
        }
    });

    it('takes the proven code over the words', () => {
        expect(parseChatError(authWords, 'assistant_error').kind).toBe('assistant_error');
        expect(parseChatError('anthropic: HTTP 401: nope', 'signed_out').kind).toBe('signed_out');
        expect(parseChatError('', 'no_assistant').kind).toBe('no_assistant');
        expect(parseChatError('the selected task runner is unavailable', 'runner_unavailable').kind).toBe('runner_unavailable');
        expect(parseChatError('daily quota exhausted', 'server').kind).toBe('server');
    });

    it('still reads usage and network from the text when there is no code', () => {
        expect(parseChatError('weekly quota exceeded').kind).toBe('quota_period');
        expect(parseChatError('dial tcp: connection refused').kind).toBe('network');
    });
});
