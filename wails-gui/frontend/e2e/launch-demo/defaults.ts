// Typed empty answers for every binding, so a read the demo does not script
// renders the app's own empty state. Return types
// come from the generated declarations and the shim's appBinding lines.
// The double slash keeps the wailsjs shim resolver from redirecting it.
import generated from '/wailsjs/go/main//App.d.ts?raw';
import shim from '/src/lib/wails-shim-app.ts?raw';

function empty(type: string): unknown {
    const t = type.trim().replace(/\s*\|\s*null$/, '');
    if (t === 'void' || t === 'null' || t === 'undefined' || t === 'unknown' || t === 'any') return null;
    if (t.startsWith('Array<') || t.endsWith('[]')) return [];
    if (t === 'boolean') return false;
    if (t === 'string') return '';
    if (t === 'number') return 0;
    // Objects stay absent: optional cards hide, and a read a page needs is
    // scripted in fixture.ts.
    return null;
}

export const typedDefaults: Record<string, unknown> = {};
for (const [, name, type] of generated.matchAll(/export function (\w+)\([^)]*\):Promise<(.*)>;/g)) typedDefaults[name] = empty(type);
for (const [, type, name] of shim.matchAll(/appBinding<\s*\[[^\]]*\]\s*,\s*([\s\S]*?)>\(\s*["'](\w+)["']\s*\)/g)) typedDefaults[name] = empty(type);
