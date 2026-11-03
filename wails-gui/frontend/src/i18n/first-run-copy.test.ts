import { describe, expect, it } from "vitest";
import onboardingEn from "./locales/en/onboarding.json";
import onboardingZh from "./locales/zh-CN/onboarding.json";
import chatEn from "./locales/en/chat.json";
import chatZh from "./locales/zh-CN/chat.json";
import appEn from "./locales/en/app.json";
import appZh from "./locales/zh-CN/app.json";
import settingsEn from "./locales/en/settings.json";
import settingsZh from "./locales/zh-CN/settings.json";

// The first run is one conversation in two places: the onboarding window,
// then the main chat's setup cards. These checks keep its copy in one voice
// and keep English and Chinese saying the same things.

type Tree = Record<string, unknown>;

function flatten(tree: unknown, prefix = ""): Record<string, string> {
    if (typeof tree !== "object" || tree === null) return { [prefix]: String(tree) };
    return Object.entries(tree as Tree).reduce<Record<string, string>>(
        (out, [key, value]) => ({ ...out, ...flatten(value, prefix ? `${prefix}.${key}` : key) }),
        {},
    );
}

const surfaces: Array<{ name: string; en: Tree; zh: Tree }> = [
    { name: "onboarding", en: onboardingEn, zh: onboardingZh },
    { name: "chat.setup", en: chatEn.setup, zh: chatZh.setup },
    { name: "app.onboarding", en: appEn.onboarding, zh: appZh.onboarding },
    { name: "app.onboardingFirstTask", en: appEn.onboardingFirstTask, zh: appZh.onboardingFirstTask },
];

const variables = (text: string) => [...text.matchAll(/{{\s*(\w+)\s*}}/g)].map((m) => m[1]).sort();
const tags = (text: string) => [...text.matchAll(/<\/?(\w+)>/g)].map((m) => m[0]).sort();

describe("first-run copy", () => {
    it.each(surfaces)("$name has the same keys and placeholders in English and Chinese", ({ en, zh }) => {
        const flatEn = flatten(en);
        const flatZh = flatten(zh);
        expect(Object.keys(flatZh).sort()).toEqual(Object.keys(flatEn).sort());
        for (const [key, text] of Object.entries(flatEn)) {
            expect(variables(flatZh[key]), key).toEqual(variables(text));
            expect(tags(flatZh[key]), key).toEqual(tags(text));
            expect(flatZh[key].trim(), key).not.toBe("");
        }
    });

    it.each(surfaces)("$name has no emoji and no jargon", ({ en, zh }) => {
        const emoji = /[\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}\u{2B00}-\u{2BFF}\u{23E9}-\u{23FA}\u{2726}]/u;
        const jargonEn = /\b(connectors?|cron|jobs?|artifacts?|agent loop|LLMs?|onboarding|quest|routing|quota)\b/i;
        const jargonZh = /(连接器|定时任务|工件|智能体循环|LLM|引导任务|路由)/;
        for (const [key, text] of Object.entries(flatten(en))) {
            expect(text, key).not.toMatch(emoji);
            expect(text, key).not.toMatch(jargonEn);
            expect(text, key).not.toMatch(/\.\.\./);
        }
        for (const [key, text] of Object.entries(flatten(zh))) {
            expect(text, key).not.toMatch(emoji);
            expect(text, key).not.toMatch(jargonZh);
        }
    });

    it("names each permission the same way in onboarding and in the main chat", () => {
        const shared = Object.keys(chatEn.setup.names).filter((key) => key in onboardingEn.permissions.items);
        expect(shared.length).toBeGreaterThan(5);
        for (const key of shared) {
            const k = key as keyof typeof chatEn.setup.names & keyof typeof onboardingEn.permissions.items;
            expect(chatEn.setup.names[k], key).toBe(onboardingEn.permissions.items[k].label);
            expect(chatZh.setup.names[k], key).toBe(onboardingZh.permissions.items[k].label);
        }
        // The same action has the same word: Turn on, and On once it is.
        expect(onboardingEn.permissions.grant).toBe("Turn on");
        expect(chatEn.setup.grant.startsWith(onboardingEn.permissions.grant)).toBe(true);
        expect(chatZh.setup.grant.startsWith(onboardingZh.permissions.grant)).toBe(true);
    });

    it("points only at Settings pages that exist", () => {
        const labelsEn = Object.values(settingsEn.pages).map((page) => page.label);
        const labelsZh = Object.values(settingsZh.pages).map((page) => page.label);
        let checked = 0;
        for (const { en, zh } of surfaces) {
            for (const text of Object.values(flatten(en))) {
                for (const match of text.matchAll(/Settings > ([A-Z][\w ]*?)(?= >|[.,;)]|$)/g)) {
                    expect(labelsEn, text).toContain(match[1]);
                    checked++;
                }
            }
            for (const text of Object.values(flatten(zh))) {
                for (const match of text.matchAll(/设置 > ([^」>]+?)(?= >|」)/g)) {
                    expect(labelsZh, text).toContain(match[1]);
                    checked++;
                }
            }
        }
        expect(checked).toBeGreaterThan(8);
    });
});
