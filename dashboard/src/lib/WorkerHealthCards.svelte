<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getWorkers, type WorkerListItem } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";

    // Two thresholds, not one -- is_stale alone is a 2-state signal
    // (fresh/stale), but the RFC wants 3 (online/degraded/offline). A worker
    // just past the stale threshold is "degraded" (still probably alive,
    // heartbeat is late); one well past it is "offline" (assume it's gone).
    const DEGRADED_HEARTBEAT_SECONDS = 60;
    const OFFLINE_HEARTBEAT_SECONDS = 300;

    let workers: WorkerListItem[] = [];
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;

    function heartbeatAgeSeconds(w: WorkerListItem): number {
        return (Date.now() - new Date(w.LastHeartbeat).getTime()) / 1000;
    }

    function workerStatus(w: WorkerListItem): "online" | "degraded" | "offline" {
        const age = heartbeatAgeSeconds(w);
        if (age > OFFLINE_HEARTBEAT_SECONDS) return "offline";
        if (age > DEGRADED_HEARTBEAT_SECONDS) return "degraded";
        return "online";
    }

    async function loadWorkers() {
        isFetching = true;
        try {
            const res = await getWorkers();
            workers = res.workers;
            console.log("workers loaded:", workers.length, workers);
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
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadWorkers();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });

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
