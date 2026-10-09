<script lang="ts">
    import { startRefresh } from "./refresh";
    import { componentRefreshTick } from "./liveRefreshStores";
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "./stores";
    import { getComponents, getMonitoringHealth, type ComponentsResponse, type MonitoringHealthResponse } from "./api";
    import { summarizeHealth } from "./componentView";
    import { deriveState, errorMessage } from "./dataState";
    import DataStateBanner from "./DataStateBanner.svelte";
    import { overviewRefreshTick } from "./liveRefreshStores";

    // RFC-010 §22: "project component health" and "live-stream lag" on the live overview.
    //
    // The component numbers come from the server's own verdicts (GET /components: process instances plus the server-judged API, Redis
    // delivery, monitoring pipeline, PII scanner, alert evaluator and live gateway), so this card and the Components page cannot disagree.
    // Instances that were stopped ON PURPOSE are counted apart from offline ones: a deploy is not an outage.

    interface LivePipelineLag {
        active_connections: number;
        last_delivery_lag_ms: number;
        max_delivery_lag_ms: number;
        slow_clients_disconnected_total: number;
        rejected_subscriptions_total?: number;
    }

    let report: ComponentsResponse | null = null;
    let live: LivePipelineLag | null = null;
    let error: unknown = null;
    let stopRefresh: (() => void) | undefined;
    let hasLoadedOnce = false;
    let isFetching = false;

    async function load() {
        isFetching = true;
        try {
            const [components, monitoring] = await Promise.all([getComponents(), getMonitoringHealth()]);
            report = components;
            live = (monitoring as MonitoringHealthResponse & { live_pipeline?: LivePipelineLag }).live_pipeline ?? null;
            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        load();
        stopRefresh = startRefresh(load, { ticks: [componentRefreshTick], gaugeEveryMs: 10000 });
    });

    onDestroy(() => stopRefresh?.());

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });
    $: summary = summarizeHealth(report?.components, report?.derived);

    // same guarded-on-> 0 shape every other Overview card uses
    let lastOverviewTick = 0;
    $: if ($overviewRefreshTick !== lastOverviewTick) {
        lastOverviewTick = $overviewRefreshTick;
        if (lastOverviewTick > 0) load();
    }
</script>

<h3>Project Components &amp; Live Stream</h3>

{#if state === "READY" || state === "REFRESHING"}
    <div class="cards">
        <div class="card"><span>Healthy</span><strong>{summary.healthy}</strong></div>
        <div class="card" class:warn={summary.degraded > 0}><span>Degraded</span><strong>{summary.degraded}</strong></div>
        <div class="card" class:bad={summary.unhealthy + summary.offline > 0}><span>Unhealthy / Offline</span><strong>{summary.unhealthy + summary.offline}</strong></div>
        <div class="card"><span>Unknown</span><strong>{summary.unknown}</strong></div>
        <div class="card"><span>Stopped on purpose</span><strong>{summary.stopped}</strong></div>

        <div class="card" data-testid="live-lag">
            <span>Live-stream lag</span>
            {#if live}
                <strong>{live.last_delivery_lag_ms} ms</strong>
                <small>worst {live.max_delivery_lag_ms} ms · {live.active_connections} connection(s) · {live.slow_clients_disconnected_total} slow client(s) cut</small>
            {:else}
                <strong>—</strong>
                <small>monitoring/health did not report live_pipeline</small>
            {/if}
        </div>
    </div>

    {#if summary.worst.length > 0}
        <ul class="attention">
            {#each summary.worst as w (w.name)}
                <li><span class="badge {w.status}">{w.status}</span> <strong>{w.name}</strong> — {w.reason}</li>
            {/each}
        </ul>
    {/if}
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 24px; display: block; }
    .card small { color: #888; font-size: 11px; }
    .card.warn { border-color: #fcd34d; background: #fffbeb; }
    .card.bad { border-color: #fca5a5; background: #fef2f2; }
    .attention { margin: 12px 0 0; padding-left: 18px; font-size: 13px; color: #444; }
    .attention li { margin-bottom: 4px; }
    .badge { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; background: #eee; }
    .badge.degraded { background: #fef3c7; color: #92400e; }
    .badge.unhealthy, .badge.offline { background: #fee2e2; color: #991b1b; }
</style>
