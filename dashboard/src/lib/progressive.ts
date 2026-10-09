// Batch 7 (RFC-009 §20): a long list is drawn in steps so opening a 1000-entry timeline does not build 1000 detail trees at once.
export const FIRST_STEP = 200;
export const NEXT_STEP = 200;

export function visibleSlice<T>(all: T[], shown: number): { items: T[]; remaining: number } {
    const n = Math.max(0, Math.min(shown, all.length));
    return { items: all.slice(0, n), remaining: all.length - n };
}
