import ReactDOM from 'react-dom/client';
import { ThemeProvider } from '@/hooks/useTheme';
import App from '@/App';
import { StorageStartupGate } from '@/components/storage-startup-gate';
import '@/i18n';
import '@fontsource-variable/inter';
import '@/index.css';

export function mount() {
    ReactDOM.createRoot(document.getElementById('root')!).render(
        <ThemeProvider><StorageStartupGate><App /></StorageStartupGate></ThemeProvider>,
    );
}
