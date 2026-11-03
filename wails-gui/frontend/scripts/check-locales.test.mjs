import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

for (const [name, en, zh, expectedError] of [
    ['matching nested keys and plural forms', { nested: { key: 'A' }, count_one: 'One' }, { nested: { key: '甲' }, count_one: '一' }, null],
    ['chat copy without a qualifier', { label: 'Chat', detail: 'In your chat today' }, { label: '聊天', detail: '今天聊天里有' }, null],
    ['retired English chat qualifier', { label: 'Main chat' }, { label: '聊天' }, 'retired chat qualifier in en'],
    ['retired Chinese chat qualifier', { label: 'Chat' }, { label: '主聊天' }, 'retired chat qualifier in zh-CN'],
    ['retired alternate Chinese chat qualifier', { label: 'Chat' }, { label: '主对话' }, 'retired chat qualifier in zh-CN'],
    ['missing Chinese key', { nested: { key: 'A' } }, { nested: {} }, 'missing in zh-CN'],
    ['missing English key', {}, { key: '甲' }, 'missing in en'],
    ['incompatible subtree', { nested: { key: 'A' } }, { nested: '甲' }, 'incompatible translation shapes'],
    ['missing namespace', { key: 'A' }, null, 'missing namespace in zh-CN'],
]) {
    test(name, () => {
        const root = mkdtempSync(join(tmpdir(), 'locale-check-'));
        try {
            for (const [lang, value] of [['en', en], ['zh-CN', zh]]) {
                mkdirSync(join(root, lang));
                if (value !== null) writeFileSync(join(root, lang, 'test.json'), JSON.stringify(value));
            }
            const result = spawnSync(process.execPath, [fileURLToPath(new URL('./check-locales.mjs', import.meta.url)), root], { encoding: 'utf8' });
            assert.equal(result.status, expectedError ? 1 : 0, result.stderr);
            if (expectedError) assert.match(result.stderr, new RegExp(expectedError));
        } finally {
            rmSync(root, { recursive: true, force: true });
        }
    });
}
