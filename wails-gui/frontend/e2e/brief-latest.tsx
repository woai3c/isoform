import React from 'react';
import { createRoot } from 'react-dom/client';
import '@/index.css';
import { installFixture, reads } from './launch-demo/fixture';
import { todayNextBrief } from '@/components/today/today-next.fixture';

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get('theme') ?? 'paper';
localStorage.setItem('i18nextLng', params.get('lang') ?? 'en');
installFixture();
reads.GetUserProfile = { name: 'Maya' };
const { default: i18n } = await import('@/i18n');
await i18n.changeLanguage(params.get('lang') ?? 'en');
const { TodayBriefSection } = await import('@/components/today/TodayBriefSection');
const brief = { ...todayNextBrief, asOf: '2026-10-23T09:12:00-07:00', ...(params.get('lang') === 'zh-CN' ? { narrative: [{ text: '今天上午先准备与 Priya 的一对一会议。下午留出时间完成 Halcyon 发布备忘录。', refs: [] }], cards: [] } : {}) };
const view = { brief, nothingNew: true, building: false, enabled: true, nextAt: '', nextSlot: 'evening', empty: false, missing: [] };
createRoot(document.getElementById('root')!).render(<main className="min-h-screen bg-background px-6 py-12"><div className="mx-auto max-w-[760px]"><TodayBriefSection view={view} loading={false} make="idle" nextAllowedAt="" onMakeNow={() => {}} handlers={{} as never} now={new Date('2026-10-23T10:00:00-07:00')} /></div></main>);
