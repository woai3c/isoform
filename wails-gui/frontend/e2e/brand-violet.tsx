// Production components with synthetic read state. No app or live store starts.
import React from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import i18n from '@/i18n';
import { ThemeProvider } from '@/hooks/useTheme';
import { AppearanceSettings } from '@/components/settings/pages/general-page';
import { SettingsNav } from '@/components/settings/settings-nav';
import { WelcomeStep } from '@/components/onboarding/steps/WelcomeStep';
import { LogoIcon } from '@/assets/icons/LogoIcon';
import { App as Spotlight } from '../../quick-chat-spotlight/frontend/src/App';

const params = new URLSearchParams(location.search);
const theme = params.get('theme') ?? 'paper';
const noop = () => {};
const fixture = window as unknown as Record<string, any>;
localStorage.setItem('yansu.theme', theme);
localStorage.setItem('yansu.appearance.texture', 'true');
document.documentElement.dataset.theme = theme;
fixture.__spotlightConfig = { yansuPort: 4242, token: 'fixture-token' };
fixture.runtime = { EventsOn: () => noop };
fixture.go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => {
    if (method === 'GetThemePreference') return theme;
    if (method === 'GetTelemetryTier') return 'detailed';
    return null;
} }) } };
if (params.has('spotlight')) await import('../../quick-chat-spotlight/frontend/src/index.css');
await i18n.changeLanguage(params.get('lang') ?? 'en');
createRoot(document.getElementById('root')!).render(params.has('spotlight') ? <Spotlight />
    : params.has('welcome') ? <main className="patina-ground h-screen bg-background"><WelcomeStep onNext={noop} /></main>
    : <ThemeProvider><main className="settings-surface flex h-screen bg-background text-ink p-8 gap-8">
        <aside className="w-60 shrink-0"><LogoIcon size={28} /><SettingsNav activePage="appearance" onSelect={noop} showDevTools={false} /></aside>
        <section className="flex-1 max-w-3xl pt-8"><AppearanceSettings readingWidth /></section>
    </main></ThemeProvider>);
