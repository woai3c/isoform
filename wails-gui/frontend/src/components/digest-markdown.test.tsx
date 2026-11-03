import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { DigestMarkdown } from './digest-markdown';

const browser = vi.hoisted(() => vi.fn());
vi.mock('../../wailsjs/runtime/runtime', () => ({ BrowserOpenURL: browser }));

it('renders Activity summaries as Markdown without executing HTML and opens web links in the browser', () => {
    const { container } = render(<DigestMarkdown text={'## Today\n\n**Finished** the [report](https://example.com/report).\n\n<img src=x onerror=alert(1)>'} />);
    expect(screen.getByRole('heading', { name: 'Today' })).toBeInTheDocument();
    expect(screen.getByText('Finished').tagName).toBe('STRONG');
    expect(container.querySelector('img')).toBeNull();
    fireEvent.click(screen.getByRole('link', { name: 'report' }));
    expect(browser).toHaveBeenCalledWith('https://example.com/report');
});
