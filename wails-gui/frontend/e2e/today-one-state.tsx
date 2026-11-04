import React from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import { installFixture } from './launch-demo/fixture';

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get('theme') ?? 'paper';
localStorage.setItem('i18nextLng', params.get('lang') ?? 'en');
installFixture();
const { default: i18n } = await import('@/i18n');
await i18n.changeLanguage(params.get('lang') ?? 'en');
const { TodayBriefSection } = await import('@/components/today/TodayBriefSection');
const view = { brief: null, building: params.get('state') === 'building', enabled: true, nextAt: '', nextSlot: 'evening', empty: false, missing: [], pendingSlot: 'morning', pendingAt: '2026-10-23T07:00:00-07:00', pendingAttempts: 6, pendingCause: 'timeout', retryAt: '2026-10-23T08:45:00-07:00' };
createRoot(document.getElementById('root')!).render(<main className="min-h-screen bg-background px-6 py-12"><div className="mx-auto max-w-[760px]"><h1 className="display mb-5 text-ink">{i18n.t('today:todayBrief.slot.morning')}</h1><TodayBriefSection view={view} loading={false} make="idle" nextAllowedAt="" onMakeNow={() => {}} handlers={{} as never} now={new Date('2026-10-23T08:30:00-07:00')} /></div></main>);
