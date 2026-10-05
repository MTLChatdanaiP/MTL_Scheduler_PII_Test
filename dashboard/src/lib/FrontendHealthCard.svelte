<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { getTelemetrySnapshot } from "../lib/telemetry";

    // RFC-009 §23 Frontend Observability -- small version. Pure in-memory
    // counters (see telemetry.ts), reset on page reload, nothing sent to a
    // backend. A full ingestion pipeline needs a new backend endpoint,
    // which per §25 must be proposed against RFC-008, not built as
    // frontend-only inference -- that is separate, later work.
    let snap = getTelemetrySnapshot();
    let timer: ReturnType<typeof setInterval>;

    onMount(() => {
        timer = setInterval(() => { snap = getTelemetrySnapshot(); }, 2000);
    });
    onDestroy(() => clearInterval(timer));

    $: apiRows = Object.keys(snap.api_call_count_by_endpoint)
        .sort()
        .map((ep) => ({
            endpoint: ep,
            calls: snap.api_call_count_by_endpoint[ep],
            failures: snap.api_failures_by_endpoint[ep] ?? 0,
            avgMs: snap.api_avg_latency_ms_by_endpoint[ep],
        }));

    $: liveDisconnectTotal = Object.values(snap.live_disconnects_by_state).reduce((a, b) => a + b, 0);
    $: bannerTotal = Object.values(snap.banner_shown_by_kind).reduce((a, b) => a + b, 0);
</script>

<h4>Frontend Health</h4>

<div class="grid">
    <div class="stat"><span>Page views</span><strong>{snap.page_views}</strong></div>
    <div class="stat"><span>Uncaught exceptions</span><strong>{snap.exception_count}</strong></div>
    <div class="stat"><span>Live disconnects</span><strong>{liveDisconnectTotal}</strong></div>
    <div class="stat"><span>Banners shown</span><strong>{bannerTotal}</strong></div>
</div>

{#if apiRows.length > 0}
    <table>
        <thead>
            <tr><th>Endpoint</th><th>Calls</th><th>Failures</th><th>Avg latency</th></tr>
        </thead>
        <tbody>
            {#each apiRows as r (r.endpoint)}
                <tr>
                    <td class="ep">{r.endpoint}</td>
                    <td>{r.calls}</td>
                    <td class:bad={r.failures > 0}>{r.failures}</td>
                    <td>{r.avgMs} ms</td>
                </tr>
            {/each}
        </tbody>
    </table>
{/if}

<p class="note">In-memory only, resets on reload. Endpoints are shown as patterns (e.g. /runs/:id), never as raw ids.</p>

<style>
    h4 { font-size: 13px; margin: 20px 0 8px; color: #555; }
    .grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; margin-bottom: 12px; }
    .stat { padding: 12px; border: 1px solid #ddd; border-radius: 8px; text-align: center; }
    .stat span { display: block; font-size: 11px; color: #666; margin-bottom: 4px; }
    .stat strong { font-size: 22px; }
    table { width: 100%; border-collapse: collapse; font-size: 12px; border: 1px solid #ddd; border-radius: 8px; overflow: hidden; }
    th, td { padding: 6px 12px; text-align: left; border-top: 1px solid #eee; }
    th { background: #f5f5f5; }
    .ep { font-family: monospace; }
    .bad { color: #991b1b; font-weight: bold; }
    .note { font-size: 11px; color: #999; margin-top: 8px; }
</style>
