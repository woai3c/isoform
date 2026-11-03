import { render, screen } from '@testing-library/react';
import { createInstance } from 'i18next';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';
import en from '../../i18n/locales/en/common.json';
import zh from '../../i18n/locales/zh-CN/common.json';
import { ClaudeIcon } from './executor-icons';

for (const locale of ['en', 'zh-CN']) {
    describe(`Isoform executor mark in ${locale}`, () => {
        it('keeps its accessible name and uses the accent in every theme', async () => {
            const i18n = createInstance();
            await i18n.init({
                lng: locale,
                resources: { en: { common: en }, 'zh-CN': { common: zh } },
                defaultNS: 'common',
            });
            const { container } = render(
                <I18nextProvider i18n={i18n}>
                    <ClaudeIcon className="text-ink" />
                </I18nextProvider>,
            );
            const mark = screen.getByRole('img', { name: 'Isoform' });
            expect(mark.tagName.toLowerCase()).toBe('svg');
            expect(mark).not.toHaveAttribute('aria-hidden');
            expect(container.querySelector('img')).toBeNull();
            expect(mark.querySelector('path')).not.toBeNull();
            for (const path of mark.querySelectorAll('path')) {
                expect(path).toHaveAttribute('fill', 'var(--color-accent)');
            }
        });
    });
}
