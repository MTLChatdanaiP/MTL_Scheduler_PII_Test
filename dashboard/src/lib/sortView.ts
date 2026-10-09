// Batch 7 (RFC-009 §5): ?sort=field (ascending) or ?sort=-field (descending) on the small client-side lists. An unknown field is
// ignored rather than guessed at, so a stale or hand-edited link still shows the page in its normal order.
export interface SortSpec {
    key: string;
    desc: boolean;
}

export function parseSort(raw: string | null | undefined, allowed: string[]): SortSpec | null {
    if (!raw) return null;
    const desc = raw.startsWith("-");
    const key = desc ? raw.slice(1) : raw;
    return allowed.includes(key) ? { key, desc } : null;
}

export function formatSort(spec: SortSpec | null): string {
    return spec ? `${spec.desc ? "-" : ""}${spec.key}` : "";
}

type Value = string | number | boolean | null | undefined;

// Stable: rows that compare equal keep their incoming order. Missing values sort last in both directions.
export function sortBy<T>(rows: T[], spec: SortSpec | null, accessors: Record<string, (row: T) => Value>): T[] {
    if (!spec || !accessors[spec.key]) return rows;
    const get = accessors[spec.key];
    const sign = spec.desc ? -1 : 1;
    return rows
        .map((row, i) => ({ row, i, v: get(row) }))
        .sort((a, b) => {
            const aMissing = a.v === null || a.v === undefined;
            const bMissing = b.v === null || b.v === undefined;
            if (aMissing || bMissing) return aMissing === bMissing ? a.i - b.i : aMissing ? 1 : -1;
            let c: number;
            if (typeof a.v === "number" && typeof b.v === "number") c = a.v - b.v;
            else c = String(a.v).localeCompare(String(b.v));
            return c !== 0 ? sign * c : a.i - b.i;
        })
        .map((x) => x.row);
}
