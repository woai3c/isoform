export type DiffOp = 'equal' | 'add' | 'remove';

export interface DiffLine {
    op: DiffOp;
    text: string;
}

/**
 * LCS-based line-level diff between two strings. Returns a flat sequence
 * of operations walking from the start of both inputs: `equal` preserves
 * a line that exists in both, `remove` marks a line present only in the
 * old input, `add` marks a line present only in the new one.
 *
 * Time complexity is O(n*m). Fine for memory bodies (typically < 100
 * lines) — for very large inputs prefer a Myers diff, which we haven't
 * imported yet because nothing else needs it.
 */
export function lineDiff(oldText: string, newText: string): DiffLine[] {
    const a = oldText.split('\n');
    const b = newText.split('\n');
    const n = a.length;
    const m = b.length;

    // lcs[i][j] = length of longest common subsequence of a[i..] and b[j..]
    const lcs: number[][] = Array.from({ length: n + 1 }, () =>
        Array<number>(m + 1).fill(0),
    );
    for (let i = n - 1; i >= 0; i--) {
        for (let j = m - 1; j >= 0; j--) {
            if (a[i] === b[j]) {
                lcs[i][j] = lcs[i + 1][j + 1] + 1;
            } else {
                lcs[i][j] = Math.max(lcs[i + 1][j], lcs[i][j + 1]);
            }
        }
    }

    const out: DiffLine[] = [];
    let i = 0;
    let j = 0;
    while (i < n && j < m) {
        if (a[i] === b[j]) {
            out.push({ op: 'equal', text: a[i] });
            i++;
            j++;
        } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
            out.push({ op: 'remove', text: a[i] });
            i++;
        } else {
            out.push({ op: 'add', text: b[j] });
            j++;
        }
    }
    while (i < n) out.push({ op: 'remove', text: a[i++] });
    while (j < m) out.push({ op: 'add', text: b[j++] });
    return out;
}

/**
 * Quick summary: counts of added / removed lines. Equal lines ignored.
 * Useful for showing "+3 −1" style chips next to a diff toggle.
 */
export function diffStats(diff: DiffLine[]): { added: number; removed: number } {
    let added = 0;
    let removed = 0;
    for (const line of diff) {
        if (line.op === 'add') added++;
        else if (line.op === 'remove') removed++;
    }
    return { added, removed };
}

export interface DiffRowPair {
    /** Left column cell (snapshot side). `undefined` means render a blank gutter. */
    left?: DiffLine;
    /** Right column cell (current side). `undefined` means render a blank gutter. */
    right?: DiffLine;
}

/**
 * Pair a flat `DiffLine` sequence into side-by-side rows. `equal` lines
 * emit one row with both sides populated. Consecutive `remove`/`add`
 * runs are zipped index-by-index — the k-th remove pairs with the k-th
 * add — and any trailing remainder produces half-empty rows (blank
 * cell on the missing side). This mirrors the classic two-pane diff
 * layout used by GitHub / VS Code split view.
 */
export function pairDiff(diff: DiffLine[]): DiffRowPair[] {
    const pairs: DiffRowPair[] = [];
    let i = 0;
    while (i < diff.length) {
        const line = diff[i];
        if (line.op === 'equal') {
            pairs.push({ left: line, right: line });
            i++;
            continue;
        }
        // Collect a contiguous run of non-equal lines so paired changes
        // (typical case: one bullet rewritten into another) line up
        // side-by-side instead of stacking vertically.
        const removes: DiffLine[] = [];
        const adds: DiffLine[] = [];
        while (i < diff.length && diff[i].op !== 'equal') {
            if (diff[i].op === 'remove') removes.push(diff[i]);
            else adds.push(diff[i]);
            i++;
        }
        const rows = Math.max(removes.length, adds.length);
        for (let k = 0; k < rows; k++) {
            pairs.push({ left: removes[k], right: adds[k] });
        }
    }
    return pairs;
}
