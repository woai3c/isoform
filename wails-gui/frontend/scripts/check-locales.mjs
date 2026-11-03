import { readdir, readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = process.argv[2] ?? fileURLToPath(new URL('../src/i18n/locales/', import.meta.url));
const errors = [];
const retiredChatQualifier = /\bmain chat\b|主聊天|主对话/i;
const files = await Promise.all(['en', 'zh-CN'].map(async (lang) =>
    (await readdir(resolve(root, lang))).filter((name) => name.endsWith('.json')).sort()));

function compare(left, right, key) {
    const object = (value) => value !== null && typeof value === 'object';
    if (object(left) !== object(right) || Array.isArray(left) !== Array.isArray(right)) {
        errors.push(`${key}: incompatible translation shapes`);
        return;
    }
    if (!object(left)) {
        if (typeof left !== 'string' || typeof right !== 'string') {
            errors.push(`${key}: translations must be strings`);
        }
        for (const [lang, value] of [['en', left], ['zh-CN', right]]) {
            if (typeof value === 'string' && retiredChatQualifier.test(value)) {
                errors.push(`${key}: retired chat qualifier in ${lang}`);
            }
        }
        return;
    }
    for (const child of new Set([...Object.keys(left), ...Object.keys(right)])) {
        if (!Object.hasOwn(left, child)) errors.push(`${key}.${child}: missing in en`);
        else if (!Object.hasOwn(right, child)) errors.push(`${key}.${child}: missing in zh-CN`);
        else compare(left[child], right[child], `${key}.${child}`);
    }
}

try {
    for (const name of new Set(files.flat())) {
        if (!files[0].includes(name)) errors.push(`${name}: missing namespace in en`);
        else if (!files[1].includes(name)) errors.push(`${name}: missing namespace in zh-CN`);
        else compare(...await Promise.all(['en', 'zh-CN'].map(async (lang) =>
            JSON.parse(await readFile(resolve(root, lang, name), 'utf8')))), name);
    }
} catch (error) {
    errors.push(String(error));
}
if (errors.length) {
    console.error(errors.join('\n'));
    process.exitCode = 1;
} else {
    console.log(`Locale parity passed (${files[0].length} namespaces, en and zh-CN).`);
}
