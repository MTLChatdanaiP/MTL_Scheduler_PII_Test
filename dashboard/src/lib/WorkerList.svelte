<script lang="ts">
    import SortControl from "./SortControl.svelte";
    import Interpretation from "./Interpretation.svelte";
    import { parseSort, sortBy } from "./sortView";
    import { activateOnKey } from "./a11y";
    import { startRefresh } from "./refresh";
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getWorkers, getWorkerDetail, type WorkerListItem, type WorkerDetailResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import JsonTree from "../lib/JsonTree.svelte";
    import { currentPath, parsePath, updateParams } from "../lib/router";
    import { workerRefreshTick } from "../lib/liveRefreshStores";
    import { revisionLabel, type WorkerStatus } from "../lib/componentView";
    import { workerStatusOf } from "./thresholds";


    let workers: WorkerListItem[] = [];
    let error: unknown = null;
    let stopRefresh: (() => void) | undefined;
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

    // Batch 7: ?sort= (client-side -- the workers list is small and arrives whole).
    const WORKER_SORTS = [
        { key: "worker", label: "Worker" }, { key: "status", label: "Status" }, { key: "heartbeat", label: "Last heartbeat" },
        { key: "utilization", label: "Utilization" }, { key: "hostname", label: "Hostname" },
    ];
    $: workerSort = parseSort(parsePath($currentPath).params.get("sort"), WORKER_SORTS.map((o) => o.key));
    $: sortedWorkers = sortBy(visibleWorkers, workerSort, {
        worker: (w) => w.WorkerId, status: (w) => workerStatus(w), heartbeat: (w) => new Date(w.LastHeartbeat).getTime(),
        utilization: (w) => utilization(w), hostname: (w) => w.Hostname,
    });

    $: visibleWorkers = workers.filter(w => {
        if (workerIdFilter && !w.WorkerId.toLowerCase().includes(workerIdFilter.toLowerCase())) return false;
        if (statusFilter && workerStatus(w) !== statusFilter) return false;
        if (hostnameFilter &&!(w.Hostname ?? "").toLowerCase().includes(hostnameFilter.toLowerCase())) {return false;}
        return true;
    });

    function heartbeatAgeSeconds(w: WorkerListItem): number {
        return (Date.now() - new Date(w.LastHeartbeat).getTime()) / 1000;
    }

    // RFC-005 §7: the backend derives health with the same thresholds the monitoring sweep uses, so this prefers it. The age-based
    // answer below is only the fallback for an older backend that sends none.
    function workerStatus(w: WorkerListItem): WorkerStatus {
        return workerStatusOf(w);
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
        stopRefresh = startRefresh(loadWorkers, { gaugeEveryMs: 10000 });
    });

    onDestroy(() => {
        unsubscribePath?.();
        stopRefresh?.();
    });

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: workers.length === 0 });

    // RFC-009 S17 / RFC-010 S22: one shared connection per PAGE (see Workers.svelte/Queues.svelte) bumps this; every component on the page reloads.
    let lastTick = 0;
    $: if ($workerRefreshTick !== lastTick) {
        lastTick = $workerRefreshTick;
        if (lastTick > 0) loadWorkers();
    }

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

<div class="filter-bar"><SortControl options={WORKER_SORTS} /></div>

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
        {#each sortedWorkers as w (w.WorkerId)}
            <div
                class="worker-row clickable"
                on:click={() => toggleExpanded(w.WorkerId)}
                role="button"
                tabindex="0"
                on:keydown={(e) => activateOnKey(e, () => toggleExpanded(w.WorkerId))}
            >
                <span class="worker-id">{w.WorkerId}</span>
                <span class="badge" class:online={workerStatus(w) === "online"} class:degraded={workerStatus(w) === "degraded"} class:offline={workerStatus(w) === "offline"} title={`${w.health_reason ?? ""}${w.build_revision !== undefined ? ` · build ${revisionLabel(w.build_revision)}` : ""}`.trim()}>
                    {workerStatus(w)}
                </span><Interpretation basis={w.health_reason ?? "heartbeat age against the offline thresholds"} />
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