import { describe, expect, it } from "vitest";
import {
    formatYourAgentClock,
    isUsableYourAgentName,
    isValidYourAgentEmail,
    isValidYourAgentNameFormat,
    nextUtcHour,
    normalizeYourAgentName,
    sanitizeYourAgentNameInput,
    sanitizeYourAgentCode,
} from "./your-agent";

describe("Agent of You name field as the person types", () => {
    it("lowercases and refuses anything but a to z, 0 to 9 and dots", () => {
        expect(sanitizeYourAgentNameInput("Alex Rivera!")).toBe("alexrivera");
        expect(sanitizeYourAgentNameInput("José")).toBe("jos");
    });

    it("refuses a second dot in a row but keeps a single dot", () => {
        expect(sanitizeYourAgentNameInput("a..b")).toBe("a.b");
        expect(sanitizeYourAgentNameInput("...ab")).toBe(".ab");
    });

    it("leaves length and dots at the ends for the rules line", () => {
        expect(sanitizeYourAgentNameInput(".ab")).toBe(".ab");
    });
});

describe("Agent of You clock and retry time", () => {
    it("formats a short local time in English", () => {
        expect(formatYourAgentClock(new Date(2026, 9, 27, 21, 40), "en")).toBe("9:40 PM");
        expect(formatYourAgentClock(new Date(2026, 9, 27, 0, 5), "en")).toBe("12:05 AM");
        expect(formatYourAgentClock(new Date(2026, 9, 27, 12, 30), "en")).toBe("12:30 PM");
    });

    it("formats the Chinese period before the time", () => {
        expect(formatYourAgentClock(new Date(2026, 9, 27, 21, 40), "zh-CN")).toBe("晚上 9:40");
        expect(formatYourAgentClock(new Date(2026, 9, 27, 9, 5), "zh-CN")).toBe("上午 9:05");
        expect(formatYourAgentClock(new Date(2026, 9, 27, 3, 0), "zh-CN")).toBe("凌晨 3:00");
    });

    it("falls back to the start of the next UTC hour", () => {
        expect(nextUtcHour(new Date(Date.UTC(2026, 9, 27, 21, 40, 5)))).toEqual(new Date(Date.UTC(2026, 9, 27, 22, 0, 0)));
    });
});

describe("Agent of You name rules", () => {
    it("accepts 3 to 30 characters of a to z, 0 to 9 and dots", () => {
        for (const name of ["abc", "alex.rivera", "a.b.c", "x1.2y", "a".repeat(30)]) {
            expect(isValidYourAgentNameFormat(name)).toBe(true);
        }
    });

    it("refuses dots first or last, doubled dots, other characters and bad lengths", () => {
        for (const name of ["", "ab", ".abc", "abc.", "a..bc", "ab-c", "ab_c", "ab c", "a".repeat(31)]) {
            expect(isValidYourAgentNameFormat(name)).toBe(false);
        }
    });

    it("refuses names kept for Isoform", () => {
        expect(isUsableYourAgentName("support")).toBe(false);
        expect(isUsableYourAgentName("isoform")).toBe(false);
        expect(isUsableYourAgentName("alex.rivera")).toBe(true);
    });

    it("lowercases and trims what the person typed", () => {
        expect(normalizeYourAgentName("  Alex.Rivera ")).toBe("alex.rivera");
    });
});

describe("Agent of You email rule", () => {
    it("checks the form only", () => {
        expect(isValidYourAgentEmail("alex@example.com")).toBe(true);
        expect(isValidYourAgentEmail("  alex@example.com  ")).toBe(true);
        for (const email of ["", "alex", "alex@", "@example.com", "a b@example.com", "alex@example"]) {
            expect(isValidYourAgentEmail(email)).toBe(false);
        }
    });
});

describe("Agent of You code field", () => {
    it("keeps up to six digits from typed or pasted input", () => {
        expect(sanitizeYourAgentCode("1a23 4567")).toBe("123456");
    });
});
