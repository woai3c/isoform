import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import type { StreamItem } from '@/types';
import { FromPhoneLabel, isFromPhone } from './from-phone-label';

afterEach(async () => {
    await i18n.changeLanguage('en');
});

const userItem = (toolInput: unknown): Pick<StreamItem, 'toolInput'> => ({ toolInput: toolInput as StreamItem['toolInput'] });

describe('From your iPhone marker (issue 635)', () => {
    it('recognises only a phone origin in the user row', () => {
        expect(isFromPhone(userItem({ origin: 'phone' }))).toBe(true);
        expect(isFromPhone(userItem(undefined))).toBe(false);
        expect(isFromPhone(userItem({ origin: 'desktop' }))).toBe(false);
        expect(isFromPhone(userItem('phone'))).toBe(false);
    });

    it('shows its label in English and in Chinese', async () => {
        render(<FromPhoneLabel />);
        expect(screen.getByText('From your iPhone')).toBeTruthy();
        cleanup();
        await i18n.changeLanguage('zh-CN');
        render(<FromPhoneLabel />);
        expect(screen.getByText('来自你的 iPhone')).toBeTruthy();
    });
});
