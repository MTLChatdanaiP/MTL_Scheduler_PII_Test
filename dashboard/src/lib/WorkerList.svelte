<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getWorkers, getWorkerDetail, type WorkerListItem, type WorkerDetailResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import JsonTree from "../lib/JsonTree.svelte";
    import { currentPath, parsePath, updateParams } from "../lib/router";

    const DEGRADED_HEARTBEAT_SECONDS = 60;
    const OFFLINE_HEARTBEAT_SECONDS = 300;

    let workers: WorkerListItem[] = [];
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;

    let attemptCounts: Record<string, { completions: number; failures: number; active: number }> = {};
    let alertCounts: Record<string, number> = {};

    let expandedWorker: string | null = null;
    let detail: WorkerDetailResponse | null = null;
    let detailLoading = false;
    let detailError: string | null = null;

    let workerIdFilter = "";
    let statusFilter: "" | "online" | "degraded" | "offline" = "";
    let hostnameFilter = "";

    let unsubscribePath: (() => void) | undefined;
    
    function syncFiltersFromUrl() {
        const { params } = parsePath($currentPath);
        workerIdFilter = params.get("worker_id") ?? "";
        statusFilter = (params.get("status") as typeof statusFilter) ?? "";
        hostnameFilter = params.get("hostname") ?? "";
    }
    
    function commitFilters() {
        updateParams({
            worker_id: workerIdFilter,
            status: statusFilter,
            hostname: hostnameFilter,
        });
}

    $: visibleWorkers = workers.filter(w => {
        if (workerIdFilter && !w.WorkerId.toLowerCase().includes(workerIdFilter.toLowerCase())) return false;
        if (statusFilter && workerStatus(w) !== statusFilter) return false;
        if (hostnameFilter &&!(w.Hostname ?? "").toLowerCase().includes(hostnameFilter.toLowerCase())) {return false;}
        return true;
    });

    function heartbeatAgeSeconds(w: WorkerListItem): number {
        return (Date.now() - new Date(w.LastHeartbeat).getTime()) / 1000;
    }

    function workerStatus(w: WorkerListItem): "online" | "degraded" | "offline" {
        const age = heartbeatAgeSeconds(w);
        if (age > OFFLINE_HEARTBEAT_SECONDS) return "offline";
        if (age > DEGRADED_HEARTBEAT_SECONDS) return "degraded";
        return "online";
    }

    function utilization(w: WorkerListItem): number {
        return w.Capacity > 0 ? (w.RunningAttempts / w.Capacity) * 100 : 0;
    }

    async function backfillWorkerCounts(workerId: string) {
        try {
            const res = await getWorkerDetail(workerId);
            attemptCounts[workerId] = {
                completions: res.worker.recent_completions?.length ?? 0,
                failures: res.worker.recent_failures?.length ?? 0,
                active: res.worker.active_attempts?.length ?? 0,
            };
            alertCounts[workerId] = res.worker.active_alert_count ?? 0;
        } catch {
            // leave undefined -- table shows "..." until/unless this resolves
        }
    }

    async function loadWorkers() {
        isFetching = true;
        try {
            const res = await getWorkers();
            workers = res.workers;
            error = null;

            console.log("workers loaded:", workers.length, workers);

            for (const w of workers) {
                backfillWorkerCounts(w.WorkerId);
            }
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        syncFiltersFromUrl();
    
        unsubscribePath = currentPath.subscribe(() => {
            syncFiltersFromUrl();   // no refetch needed, visibleWorkers is reactive
        });
    
        loadWorkers();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadWorkers();
        }, 10000);
    });

    onDestroy(() => {
        unsubscribePath?.();
        clearInterval(refreshTimer);
    });

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: workers.length === 0 });

    async function toggleExpanded(workerId: string) {
        if (expandedWorker === workerId) {
            expandedWorker = null;
            return;
        }
        expandedWorker = workerId;
        detailLoading = true;
        detailError = null;
        detail = null;

        try {
            detail = await getWorkerDetail(workerId);
        } catch (e) {
            detailError = errorMessage(e);
        } finally {
            detailLoading = false;
        }
    }

    $: console.log(
        "filters",
        workerIdFilter,
        statusFilter,
        hostnameFilter,
        visibleWorkers.length
    );
</script>

<div class="filter-bar">
    <label>
        Worker ID:
        <input type="text" bind:value={workerIdFilter} placeholder="e.g. Consumer" on:change={commitFilters}/>
    </label>
    <label>
        Status:
        <select bind:value={statusFilter} on:change={commitFilters}>
            <option value="">All</option>
            <option value="online">Online</option>
            <option value="degraded">Degraded</option>
            <option value="offline">Offline</option>
        </select>
    </label>
    <label>
        Hostname:
        <input type="text" bind:value={hostnameFilter} on:change={commitFilters}/>
    </label>
</div>

<div class="worker-table">
    <div class="worker-row header">
        <span>Worker</span>
        <span>Status</span>
        <span>Last Heartbeat</span>
        <span>Version</span>
        <span>Queues</span>
        <span>Capacity</span>
        <span>Active Attempts</span>
        <span>Utilization</span>
        <span>Completions</span>
        <span>Failures</span>
        <span>Alerts</span>
        <span>Hostname</span>
    </div>

    {#if state === "READY" || state === "REFRESHING"}
        {#each visibleWorkers as w (w.WorkerId)}
            <div
                class="worker-row clickable"
                on:click={() => toggleExpanded(w.WorkerId)}
                role="button"
                tabindex="0"
                on:keydown={(e) => e.key === "Enter" && toggleExpanded(w.WorkerId)}
            >
                <span class="worker-id">{w.WorkerId}</span>
                <span class="badge" class:online={workerStatus(w) === "online"} class:degraded={workerStatus(w) === "degraded"} class:offline={workerStatus(w) === "offline"}>
                    {workerStatus(w)}
                </span>
                <span>{heartbeatAgeSeconds(w).toFixed(0)}s ago</span>
                <span class="gap">—</span>
                <span class="gap">—</span>
                <span>{w.Capacity}</span>
                <span>{attemptCounts[w.WorkerId]?.active ?? "…"}</span>
                <span>{utilization(w).toFixed(0)}%</span>
                <span>{attemptCounts[w.WorkerId]?.completions ?? "…"}</span>
                <span>{attemptCounts[w.WorkerId]?.failures ?? "…"}</span>
                <span>{alertCounts[w.WorkerId] ?? "…"}</span>
                <span>{w.Hostname || "—"}</span>
            </div>

            {#if expandedWorker === w.WorkerId}
                <div class="worker-detail">
                    {#if detailLoading}
                        <p class="status">Loading...</p>
                    {:else if detailError}
                        <p class="status error">{detailError}</p>
                    {:else if detail}
                        <JsonTree value={detail.worker} />

                        <p class="gap-note">
                            "Version" and "queues" are not shown — Worker has no
                            version field (commented out in the model) and no
                            queue association exists on any worker record.
                        </p>
                    {/if}
                </div>
            {/if}
        {/each}
    {:else}
        <DataStateBanner {state} message={errorMessage(error)} />
    {/if}

    <details>
        <summary>Debug JSON</summary>
        <pre>{JSON.stringify(workers, null, 2)}</pre>
    </details>
</div>


<style>
    .worker-table { margin-top: 12px; border: 1px solid #ddd; border-radius: 8px; overflow-x: auto; }
    .worker-row {
        display: grid;
        grid-template-columns: 1.2fr 0.8fr 1fr 0.8fr 0.8fr 0.8fr 1fr 0.9fr 1fr 0.8fr 0.7fr 1fr;
        gap: 12px;
        padding: 10px 16px;
        border-top: 1px solid #ddd;
        font-size: 13px;
        align-items: center;
    }
    .worker-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .worker-row.clickable { cursor: pointer; }
    .worker-row.clickable:hover { background: #fafafa; }
    .worker-id { font-family: monospace; }
    .gap { color: #bbb; }

    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; width: fit-content; }
    .badge.online { background: #dcfce7; color: #166534; }
    .badge.degraded { background: #fef3c7; color: #92400e; }
    .badge.offline { background: #fee2e2; color: #991b1b; }

    .status { padding: 16px; text-align: center; color: #666; }
    .status.error { color: #991b1b; }

    .worker-detail { padding: 16px 24px; background: #fafafa; border-top: 1px solid #eee; font-size: 13px; }
    .gap-note { margin-top: 12px; font-size: 11px; color: #999; }

    .filter-bar { display: flex; gap: 16px; align-items: flex-end; flex-wrap: wrap; padding: 12px 0; }
    .filter-bar label { display: flex; flex-direction: column; font-size: 12px; gap: 4px; }
    .filter-bar input, .filter-bar select { padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; }
</style>