<script lang="ts">
    import { startRefresh } from "./refresh";
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getWorkers, type WorkerListItem } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import { workerRefreshTick } from "../lib/liveRefreshStores";

    // Two thresholds, not one -- is_stale alone is a 2-state signal
    // (fresh/stale), but the RFC wants 3 (online/degraded/offline). A worker
    // just past the stale threshold is "degraded" (still probably alive,
    // heartbeat is late); one well past it is "offline" (assume it's gone).
    import { workerStatusOf } from "./thresholds";

    let workers: WorkerListItem[] = [];
    let error: unknown = null;
    let stopRefresh: (() => void) | undefined;
    let hasLoadedOnce = false;
    let isFetching = false;

    function workerStatus(w: WorkerListItem): "online" | "degraded" | "offline" {
        return workerStatusOf(w);
    }

    async function loadWorkers() {
        isFetching = true;
        try {
            const res = await getWorkers();
            workers = res.workers;
            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        loadWorkers();
        stopRefresh = startRefresh(loadWorkers, { gaugeEveryMs: 10000 });
    });

    onDestroy(() => {
        stopRefresh?.();
    });

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });

    // RFC-009 S17 / RFC-010 S22: one shared connection per PAGE (see Workers.svelte/Queues.svelte) bumps this; every component on the page reloads.
    let lastTick = 0;
    $: if ($workerRefreshTick !== lastTick) {
        lastTick = $workerRefreshTick;
        if (lastTick > 0) loadWorkers();
    }

    $: onlineCount = workers.filter(w => workerStatus(w) === "online").length;
    $: degradedCount = workers.filter(w => workerStatus(w) === "degraded").length;
    $: offlineCount = workers.filter(w => workerStatus(w) === "offline").length;

    $: totalRunning = workers.reduce((sum, w) => sum + w.RunningAttempts, 0);
    $: totalCapacity = workers.reduce((sum, w) => sum + w.Capacity, 0);
    $: utilizationPercent = totalCapacity > 0 ? (totalRunning / totalCapacity) * 100 : 0;
</script>

<h3>Worker Health</h3>

{#if state === "READY" || state === "REFRESHING"}
    <div class="cards">
        <div class="card">
            <span>Online / Degraded / Offline</span>
            <strong>{onlineCount} / {degradedCount} / {offlineCount}</strong>
        </div>

        <div class="card">
            <span>Capacity Utilization</span>
            <strong>{utilizationPercent.toFixed(0)}%</strong>
            <small>{totalRunning} / {totalCapacity} slots in use</small>
        </div>

        <div class="card gap-card">
            <span>Workers With Active Lost-Run Candidates</span>
            <strong>—</strong>
            <small>would need per-worker RUN_LOST cross-referencing against
                each worker's latest attempt -- too expensive for a summary
                card at this scale, same call as Run Health's equivalent gap</small>
        </div>
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 28px; display: block; }
    .card small { color: #888; font-size: 11px; }
    .gap-card { background: #fafafa; }
</style>