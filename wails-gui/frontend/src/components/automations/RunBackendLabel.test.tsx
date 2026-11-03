import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import { RunBackendLabel } from './RunBackendLabel';

afterEach(async () => { cleanup(); await i18n.changeLanguage('en'); });

describe('RunBackendLabel in scheduled run history', () => {
    it.each([
        ['claude-code', 'Claude Code'],
        ['codex', 'Codex'],
        ['native', 'Isoform'],
    ])('shows the recorded %s runner', async (backend, name) => {
        await i18n.changeLanguage('en');
        render(<RunBackendLabel run={{ backend, status: 'done' }} />);
        expect(screen.getByText(`Ran on ${name}`)).toBeInTheDocument();
    });

    it.each([
        ['running', 'Running on Claude Code'],
        ['blocked', 'Ran on Claude Code. Stopped; no automatic retry.'],
    ])('words the %s state', async (status, text) => {
        await i18n.changeLanguage('en');
        render(<RunBackendLabel run={{ backend: 'claude-code', status }} />);
        expect(screen.getByText(text)).toBeInTheDocument();
    });

    it('never explains a skipped assistant here: that lives in Settings', async () => {
        await i18n.changeLanguage('en');
        render(<RunBackendLabel run={{ backend: 'native', status: 'done' }} />);
        expect(screen.getByText('Ran on Isoform')).toBeInTheDocument();
        expect(screen.queryByRole('button')).not.toBeInTheDocument();
    });

    it('does not attribute legacy runs to an assumed backend', () => {
        const { container } = render(<RunBackendLabel run={{ status: 'ok' }} />);
        expect(container).toBeEmptyDOMElement();
    });

    it('speaks Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        render(<RunBackendLabel run={{ backend: 'native', status: 'blocked' }} />);
        expect(screen.getByText('由 Isoform 执行。已停止；不会自动重试。')).toBeInTheDocument();
    });
});
