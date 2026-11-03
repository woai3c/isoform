import path from 'node:path';
import { mergeConfig } from 'vite';
import appConfig from './vite.config';

// Only the component fixture combines the two separately built frontends.
export default mergeConfig(appConfig, {
    plugins: [{
        name: 'violet-fixture-spotlight-binding',
        enforce: 'pre',
        resolveId(source: string) {
            if (source.endsWith('wailsjs/go/main/app')) {
                return path.resolve(__dirname, '../quick-chat-spotlight/frontend/src/lib/wails-app-stub.ts');
            }
            return null;
        },
    }],
});
