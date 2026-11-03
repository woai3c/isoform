import { markLaunchStage, markLaunchPaint } from '@/lib/launch-perf';
import React from 'react';
import ReactDOM from 'react-dom/client';
import { ThemeProvider } from '@/hooks/useTheme';
import App from './App';
import { StorageStartupGate } from '@/components/storage-startup-gate';
import { startTitleStripDragRegions } from '@/lib/title-strip-drag-regions';
import i18n from './i18n';
import { GetClockFormat } from '@/lib/wails-shim-app';
import { setComputerClock } from '@/lib/brief-time';
// Cross-platform fallback grotesque for the neutral UI stack. macOS resolves to
// SF Pro first (via -apple-system); Windows/Linux fall back to Inter Variable.
import '@fontsource-variable/inter';
import './index.css';

markLaunchStage("frontend_boot");
markLaunchPaint("first_paint");

const root = document.getElementById('root');

if (!root) {
    throw new Error('Root element not found');
}

// The first click on an inactive window drags it from the title strip.
startTitleStripDragRegions();

// Every clock follows the computer's 12- or 24-hour setting.
void GetClockFormat().then((format) => {
    setComputerClock(format);
    if (format === '24h') void i18n.changeLanguage(i18n.language);
}).catch(() => undefined);

ReactDOM.createRoot(root).render(
    <React.StrictMode>
        <ThemeProvider>
            <StorageStartupGate>
                <App />
            </StorageStartupGate>
        </ThemeProvider>
    </React.StrictMode>
);
