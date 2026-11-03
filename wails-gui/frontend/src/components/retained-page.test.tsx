import { act, fireEvent, render, screen } from '@testing-library/react';
import { useEffect, useState } from 'react';
import { expect, it, vi } from 'vitest';
import { RetainedPage } from './retained-page';

it('keeps the same scroll node, fold, draft and single event handler across three switches', () => {
    const event = vi.fn();
    const mounted = vi.fn();
    function Content() {
        const [draft, setDraft] = useState('');
        useEffect(() => {
            mounted();
            window.addEventListener('fixture-event', event);
            return () => window.removeEventListener('fixture-event', event);
        }, []);
        return <div data-testid="scroll"><textarea aria-label="draft" value={draft} onChange={(e) => setDraft(e.target.value)} /><details><summary>fold</summary>content</details></div>;
    }
    const page = render(<RetainedPage active page="chat"><Content /></RetainedPage>);
    const scroll = screen.getByTestId('scroll');
    scroll.scrollTop = 312;
    fireEvent.change(screen.getByRole('textbox'), { target: { value: 'unfinished words' } });
    const fold = scroll.querySelector('details')!;
    fold.open = true;
    for (let i = 0; i < 3; i++) {
        page.rerender(<RetainedPage active={false} page="chat"><Content /></RetainedPage>);
        expect(scroll.parentElement).toHaveStyle({ display: 'none' });
        page.rerender(<RetainedPage active page="chat"><Content /></RetainedPage>);
    }
    expect(screen.getByTestId('scroll')).toBe(scroll);
    expect(scroll.scrollTop).toBe(312);
    expect(screen.getByRole('textbox')).toHaveValue('unfinished words');
    expect(fold.open).toBe(true);
    act(() => window.dispatchEvent(new Event('fixture-event')));
    expect(event).toHaveBeenCalledTimes(1);
    expect(mounted).toHaveBeenCalledTimes(1);
    page.unmount();
    window.dispatchEvent(new Event('fixture-event'));
    expect(event).toHaveBeenCalledTimes(1);
});
