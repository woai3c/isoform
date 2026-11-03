import { describe, expect, it } from "vitest";
import ts from "typescript";
import en from "./locales/en/today.json?raw";
import zhCN from "./locales/zh-CN/today.json?raw";

// JSON.parse silently keeps only the last duplicate. Inspect the source tree
// before parsing so a textual merge cannot overwrite a translation unnoticed.
describe("Today translation resources", () => {
    it.each([["en", en], ["zh-CN", zhCN]])("has no duplicate object keys in %s/today.json", (language, source) => {
        expect(() => JSON.parse(source)).not.toThrow();
        const duplicates: string[] = [];
        const visit = (node: ts.Node) => {
            if (ts.isObjectLiteralExpression(node)) {
                const keys = new Set<string>();
                for (const property of node.properties) {
                    if (!ts.isPropertyAssignment(property) || !ts.isStringLiteral(property.name)) continue;
                    const key = property.name.text;
                    if (keys.has(key)) duplicates.push(key);
                    keys.add(key);
                }
            }
            ts.forEachChild(node, visit);
        };
        visit(ts.parseJsonText(`${language}/today.json`, source));
        expect(duplicates).toEqual([]);
    });
});
