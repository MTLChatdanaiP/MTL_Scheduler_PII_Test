<script lang="ts">
    import { CATEGORIES, bucketEvents, type Bucket, type Category } from "../lib/activity";
    import type { LiveEvent } from "./liveClient";

    // Already-filtered events, and the current time. `now` is passed in (the
    // parent ticks it every second) so the chart keeps sliding when it's quiet.
    export let events: LiveEvent[];
    export let now: number;

    const BUCKET_MS = 10_000;
    const BUCKET_COUNT = 30; // 30 x 10s = a 5 minute window

    const W = 600;
    const H = 150;
    const PAD_L = 30;
    const PAD_R = 6;
    const PAD_T = 8;
    const PAD_B = 20;
    const plotW = W - PAD_L - PAD_R;
    const plotH = H - PAD_T - PAD_B;
    const slot = plotW / BUCKET_COUNT;

    const COLORS: Record<Category, string> = {
        task: "#3b82f6",
        alert: "#ef4444",
        pii: "#22c55e",
        other: "#9ca3af",
    };

    $: buckets = bucketEvents(events, now, BUCKET_MS, BUCKET_COUNT);
    // at least 5, so a single event doesn't fill the whole chart height
    $: yMax = Math.max(5, ...buckets.map((b) => b.total));
    $: totalInWindow = buckets.reduce((sum, b) => sum + b.total, 0);

    // Stack the non-empty categories of one bucket from the bottom up.
    function segments(b: Bucket, yMax: number) {
        let stacked = 0;
        return CATEGORIES.filter((c) => b.counts[c] > 0).map((c) => {
            const h = (b.counts[c] / yMax) * plotH;
            const y = PAD_T + plotH - stacked - h;
            stacked += h;
            return { cat: c, y, h };
        });
    }

    function tooltip(b: Bucket): string {
        const start = new Date(b.start).toLocaleTimeString();
        const parts = CATEGORIES.filter((c) => b.counts[c] > 0).map((c) => `${c} ${b.counts[c]}`);
        return `${start}: ${b.total} event${b.total === 1 ? "" : "s"}${parts.length ? ` (${parts.join(", ")})` : ""}`;
    }
</script>

<div class="graph">
    <svg
        viewBox="0 0 {W} {H}"
        role="img"
        aria-label="Events per 10 seconds over the last 5 minutes. {totalInWindow} events in the window."
    >
        <!-- top gridline (= yMax) and baseline -->
        <line x1={PAD_L} x2={W - PAD_R} y1={PAD_T} y2={PAD_T} class="grid" />
        <line x1={PAD_L} x2={W - PAD_R} y1={PAD_T + plotH} y2={PAD_T + plotH} class="axis" />
        <text x={PAD_L - 4} y={PAD_T + 3} text-anchor="end" class="label">{yMax}</text>
        <text x={PAD_L - 4} y={PAD_T + plotH + 3} text-anchor="end" class="label">0</text>

        {#each buckets as b, i (b.start)}
            <g>
                <title>{tooltip(b)}</title>
                <!-- invisible full-height hit area so quiet buckets still show a tooltip -->
                <rect x={PAD_L + i * slot} y={PAD_T} width={slot} height={plotH} fill="transparent" />
                {#each segments(b, yMax) as s (s.cat)}
                    <rect
                        x={PAD_L + i * slot + 1}
                        y={s.y}
                        width={Math.max(1, slot - 2)}
                        height={s.h}
                        fill={COLORS[s.cat]}
                    />
                {/each}
            </g>
        {/each}

        <text x={PAD_L} y={H - 5} class="label">5 min ago</text>
        <text x={W - PAD_R} y={H - 5} text-anchor="end" class="label">now</text>
    </svg>

    <div class="legend">
        {#each CATEGORIES as c}
            <span class="key"><i style="background:{COLORS[c]}"></i>{c}</span>
        {/each}
        <span class="total">{totalInWindow} event{totalInWindow === 1 ? "" : "s"} in the last 5 min</span>
    </div>
</div>

<style>
    .graph { border: 1px solid #ddd; border-radius: 8px; padding: 8px 12px; margin-bottom: 10px; }
    svg { width: 100%; height: auto; display: block; }
    .grid { stroke: #e5e7eb; stroke-dasharray: 3 3; }
    .axis { stroke: #d1d5db; }
    .label { font-size: 10px; fill: #888; }
    .legend { display: flex; gap: 14px; align-items: center; font-size: 11px; color: #555; margin-top: 4px; }
    .key { display: inline-flex; align-items: center; gap: 4px; }
    .key i { width: 9px; height: 9px; border-radius: 2px; display: inline-block; }
    .total { margin-left: auto; color: #888; }
</style>