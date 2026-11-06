// Browser fixture for R38: a chat brief card draws at its final size. The
// brief read is held for a while, so the card paints before its words; the
// marker below the card must not move when they arrive.
import React from 'react';
import { createRoot } from 'react-dom/client';
import i18n from '@/i18n';
import '@/index.css';
import { MainChatBriefCard } from '@/components/main-chat-brief-card';

const briefId = 'brief-2026-11-04-evening';
const brief = {
    briefId,
    slot: 'evening',
    asOf: '2026-11-04T18:57:00-08:00',
    timezone: 'America/Los_Angeles',
    narrative: [
        { text: 'Two replies are waiting on you, and the deck for Thursday is due by noon.' },
        { text: 'The library forms were filed, and the film review moved to Friday.' },
    ],
};
const noop = () => {};
(window as any).runtime = { EventsOn: () => noop };
// Bindings this fixture does not serve answer null; the brief read is held
// for a moment so the card paints before its words.
const served: Record<string, unknown> = {
    GetTodayBriefByID: async () => {
        await new Promise((resolve) => setTimeout(resolve, 700));
        return { brief };
    },
};
const app = new Proxy(served, { get: (target, key: string) => (key in target ? target[key] : async () => null) });
(window as any).go = { main: { App: app } };

async function mount() {
    const params = new URLSearchParams(location.search);
    // The same surface as the proactive card in Chat: rounded, bordered, warm panel.
    const withWords = params.get('lead') !== '0';
    const theme = params.get('theme');
    if (theme) document.documentElement.dataset.theme = theme;
    await i18n.changeLanguage('en');
    createRoot(document.getElementById('root')!).render(
        <div className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
            <section className="flex min-w-0 flex-col gap-2.5 rounded-xl border border-border bg-panel-warm px-[18px] py-4">
                <MainChatBriefCard
                    briefId={briefId}
                    display={{ slot: 'evening', asOf: brief.asOf, timezone: brief.timezone }}
                    lead={withWords ? brief.narrative[0].text : undefined}
                    meta={() => <p className="text-[12px] leading-relaxed text-muted">From your notes and calendar</p>}
                />
            </section>
            <div data-testid="below" className="h-24 rounded-xl border border-border p-3 text-[13px]">Next message</div>
        </div>,
    );
}
void mount();
