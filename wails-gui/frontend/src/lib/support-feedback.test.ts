import { describe, expect, it } from 'vitest';
import { feedbackMailURL, supportEmail } from './support-feedback';

describe('feedback mail', () => {
    it('uses the build-time address and rejects header injection', () => {
        expect(supportEmail(' team+desktop@example.org ')).toBe('team+desktop@example.org');
        for (const value of [undefined, '', 'bad', 'a@example.org?bcc=other@example.org', 'a@example.org\r\nBcc: other@example.org']) {
            expect(supportEmail(value)).toBe('bo@isoform.ai');
        }
    });

    it('encodes localized subject, paragraphs and reserved characters as draft data', () => {
        const subject = '反馈 & questions';
        const body = 'Version: 1.2.3\n\nA question? #1 & more';
        const url = new URL(feedbackMailURL('team+desktop@example.org', subject, body));
        expect(decodeURIComponent(url.pathname)).toBe('team+desktop@example.org');
        expect([...url.searchParams.keys()]).toEqual(['subject', 'body']);
        expect(url.searchParams.get('subject')).toBe(subject);
        expect(url.searchParams.get('body')).toBe(body);
    });
});
