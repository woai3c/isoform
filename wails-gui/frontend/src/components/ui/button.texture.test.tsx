import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Button } from './button';
import { Card } from './card';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from './dialog';

describe.each(['paper', 'onedark'])('Brand violet Button in %s', (theme) => {
    it('marks only the filled primary variant, including slotted actions', () => {
        document.documentElement.dataset.theme = theme;
        const { container } = render(<>
            <Button>Primary</Button>
            <Button asChild><a href="#next">Next</a></Button>
            {(['secondary', 'connect', 'ghost', 'outline', 'destructive', 'link', 'quiet'] as const).map((variant) =>
                <Button key={variant} variant={variant}>{variant}</Button>)}
        </>);
        expect(screen.getByRole('button', { name: 'Primary' })).toHaveClass('patina-verdigris');
        expect(screen.getByRole('link', { name: 'Next' })).toHaveClass('patina-verdigris');
        expect(container.querySelectorAll('.patina-verdigris')).toHaveLength(2);
        expect(container.querySelector('.patina-ground')).toBeNull();
    });

    it('keeps cards and portaled dialogs flat above a flat ground', () => {
        document.documentElement.dataset.theme = theme;
        render(<div className="patina-ground">
            <Card data-testid="flat-card">Card</Card>
            <Dialog open>
                <DialogContent>
                    <DialogTitle>Dialog</DialogTitle>
                    <DialogDescription>A flat surface</DialogDescription>
                </DialogContent>
            </Dialog>
        </div>);
        for (const surface of [screen.getByTestId('flat-card'), screen.getByRole('dialog')]) {
            expect(surface).toHaveClass('bg-panel');
            expect(surface).not.toHaveClass('patina-ground', 'patina-verdigris');
        }
    });
});
