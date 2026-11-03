import { useState } from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Dialog, DialogDescription, DialogTitle } from '@/components/ui/dialog';
import { RetainedDialogContent } from './retained-dialog-content';

function SourcePage() {
    const [entry, setEntry] = useState('Selected entry');
    return <input aria-label="Source entry" value={entry} onChange={(event) => setEntry(event.target.value)} />;
}

function Harness({ open, retain }: { open: boolean; retain: boolean }) {
    return <Dialog open={open}>
        <RetainedDialogContent open={open} retain={retain}>
            <DialogTitle>Source page</DialogTitle>
            <DialogDescription>Source page fixture</DialogDescription>
            <SourcePage />
        </RetainedDialogContent>
    </Dialog>;
}

describe('Activity source dialog retention', () => {
    it('preserves the selected entry and draft without a live modal over Activity', async () => {
        const { rerender } = render(<Harness open retain={false} />);
        const field = screen.getByRole('textbox', { name: 'Source entry' });
        fireEvent.change(field, { target: { value: 'Selected history version and unsaved edit' } });
        rerender(<Harness open={false} retain />);
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(screen.queryByRole('textbox')).toBeNull();
        expect(document.body.style.pointerEvents).toBe('');
        rerender(<Harness open retain={false} />);
        expect(screen.getByRole('textbox', { name: 'Source entry' })).toBe(field);
        expect(field).toHaveValue('Selected history version and unsaved edit');
    });

    it('keeps the page mounted after an ordinary closure, so a reopen shows the same state', async () => {
        // The page's dialog content is retained after the first open: closing
        // detaches it (no live modal over the app) and reopening shows exactly
        // what the user left.
        const { rerender } = render(<Harness open retain={false} />);
        fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Prior selection' } });
        rerender(<Harness open={false} retain={false} />);
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        rerender(<Harness open retain={false} />);
        expect(screen.getByRole('textbox')).toHaveValue('Prior selection');
    });
});

function StatefulPane() {
    const [count, setCount] = useState(0);
    return (
        <div>
            <button data-testid="count" onClick={() => setCount((n) => n + 1)}>count {count}</button>
            <div data-testid="scroller" style={{ height: 100, overflowY: 'auto' }}>
                <div style={{ height: 1000 }}>rows</div>
            </div>
        </div>
    );
}

function ToggleHarness({ startOpen = true }: { startOpen?: boolean }) {
    const [open, setOpen] = useState(startOpen);
    return (
        <>
            <button data-testid="toggle" onClick={() => setOpen((v) => !v)}>toggle</button>
            <Dialog open={open} onOpenChange={setOpen}>
                <RetainedDialogContent open={open} retain={false} aria-describedby={undefined}>
                    <DialogTitle>Pane</DialogTitle>
                    <StatefulPane />
                </RetainedDialogContent>
            </Dialog>
        </>
    );
}

describe('retained page state across close and reopen', () => {
    it('keeps component state and scroll offset across close and reopen', () => {
        render(<ToggleHarness />);
        fireEvent.click(screen.getByTestId('count'));
        fireEvent.click(screen.getByTestId('count'));
        expect(screen.getByTestId('count').textContent).toBe('count 2');
        screen.getByTestId('scroller').scrollTop = 77;

        fireEvent.click(screen.getByTestId('toggle'));
        expect(screen.queryByTestId('count')).toBeNull();

        fireEvent.click(screen.getByTestId('toggle'));
        expect(screen.getByTestId('count').textContent).toBe('count 2');
        expect(screen.getByTestId('scroller').scrollTop).toBe(77);
    });

    it('renders nothing until the first open', () => {
        render(<ToggleHarness startOpen={false} />);
        expect(screen.queryByTestId('count')).toBeNull();
        fireEvent.click(screen.getByTestId('toggle'));
        expect(screen.getByTestId('count')).toBeTruthy();
    });

    it('a second close still keeps the page mounted', () => {
        render(<ToggleHarness />);
        fireEvent.click(screen.getByTestId('count'));
        for (let i = 0; i < 2; i++) {
            fireEvent.click(screen.getByTestId('toggle'));
            expect(screen.queryByTestId('count')).toBeNull();
            fireEvent.click(screen.getByTestId('toggle'));
            expect(screen.getByTestId('count').textContent).toBe('count 1');
        }
    });
});
