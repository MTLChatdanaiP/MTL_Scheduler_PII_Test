<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getQueues, getQueueDetail, getAlerts, type QueueHealth, type QueueDetailResponse } from "../lib/api";
    import AffectedRunsView from "../lib/AffectedRunsView.svelte";
    import JsonTree from "../lib/JsonTree.svelte";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
 
    let hasLoadedOnce = false;
    let isFetching = false;
    let error: unknown = null;

    const DEGRADED_PENDING_THRESHOLD = 20;

    let queues: QueueHealth[] = [];
    let trends: Record<string, "up" | "down" | "flat" | null> = {};
    let alertCounts: Record<string, number> = {};
    let refreshTimer: ReturnType<typeof setInterval>;

    let expandedQueue: string | null = null;
    let detail: QueueDetailResponse | null = null;
    let detailLoading = false;
    let detailError: string | null = null;

    let affectedRunsQueue: string | null = null;

    async function computeTrend(queueName: string) {
        try {
            const res = await getQueueDetail(queueName);
            if (res.history.length < 2) {
                trends[queueName] = "flat";
                return;
            }
            // history is newest-first from the backend (SampledAt DESC) —
            // compare the newest sample against the oldest one available
            const newest = res.history[0].PendingCount;
            const oldest = res.history[res.history.length - 1].PendingCount;
            trends[queueName] = newest > oldest ? "up" : newest < oldest ? "down" : "flat";
        } catch {
            trends[queueName] = null;
        }
    }

    async function computeAlertCount(queueName: string) {
        try {
            const res = await getAlerts(`?subject_type=QUEUE&subject_id=${queueName}&status=OPEN&limit=1&offset=0`);
            alertCounts[queueName] = res.page.total ?? 0;
        } catch {
            alertCounts[queueName] = 0;
        }
    }

    async function loadQueues() {
        isFetching = true;
        try {
            const res = await getQueues();
            queues = res.queues;
            error = null;
            for (const q of queues) {
                computeTrend(q.QueueName);
                computeAlertCount(q.QueueName);
            }
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }
 
    $: state = deriveState({
        hasLoadedOnce,
        isFetching,
        error,
        isEmpty: queues.length === 0,
    });

    onMount(() => {
        loadQueues();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadQueues();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));

    async function toggleExpanded(queueName: string) {
        if (expandedQueue === queueName) {
            expandedQueue = null;
            return;
        }
        expandedQueue = queueName;
        detailLoading = true;
        detailError = null;
        detail = null;

        try {
            detail = await getQueueDetail(queueName);
        } catch (e) {
            detailError = (e as Error).message;
        } finally {
            detailLoading = false;
        }
    }

    function isDegraded(q: QueueHealth): boolean {
        return q.PendingCount > DEGRADED_PENDING_THRESHOLD;
    }

    function trendArrow(t: "up" | "down" | "flat" | null | undefined): string {
        if (t === "up") return "↑";
        if (t === "down") return "↓";
        if (t === "flat") return "→";
        return "—";
    }

    function samplingAge(sampledAt: string): string {
        const seconds = Math.round((Date.now() - new Date(sampledAt).getTime()) / 1000);
        return `${seconds}s ago`;
    }
</script>

<div class="queue-table">
    <div class="queue-row header">
        <span>Queue</span>
        <span>Status</span>
        <span>Depth</span>
        <span>Pending</span>
        <span>Oldest Age</span>
        <span>Consumers</span>
        <span>Throughput</span>
        <span>Ack Rate</span>
        <span>Trend</span>
        <span>Sampled</span>
        <span>Alerts</span>
    </div>

    {#if error}
        <p class="status error">{error}</p>
    {:else}
        {#if state === "READY" || state === "REFRESHING"}
            {#each queues as q (q.QueueName)}
                <div
                    class="queue-row clickable"
                    on:click={() => toggleExpanded(q.QueueName)}
                    role="button"
                    tabindex="0"
                    on:keydown={(e) => e.key === "Enter" && toggleExpanded(q.QueueName)}
                >
                    <span class="queue-name">{q.QueueName}</span>
                    <span class="badge" class:degraded={isDegraded(q)}>
                        {isDegraded(q) ? "Degraded" : "Healthy"}
                    </span>
                    <span>{q.StreamLength}</span>
                    <span>{q.PendingCount}</span>
                    <span>{q.OldestPendingAgeSeconds}s</span>
                    <span class:zero-consumers={q.ConsumerCount === 0}>{q.ConsumerCount}</span>
                    <span class="gap">—</span>
                    <span class="gap">—</span>
                    <span>{trendArrow(trends[q.QueueName])}</span>
                    <span>{samplingAge(q.SampledAt)}</span>
                    <span>{alertCounts[q.QueueName] ?? "…"}</span>
                </div>

                {#if expandedQueue === q.QueueName}
                    <div class="queue-detail">
                        {#if detailLoading}
                            <p class="status">Loading...</p>
                        {:else if detailError}
                            <p class="status error">{detailError}</p>
                        {:else if detail}
                            <div class="detail-actions">
                                <button on:click|stopPropagation={() => affectedRunsQueue = q.QueueName}>
                                    View affected runs →
                                </button>
                            </div>

                            <h4>Sample History ({detail.history.length})</h4>
                            <JsonTree value={detail.history} />

                            <p class="gap-note">
                                Throughput and completion/ack rate are not shown — QueueHealth
                                has no field for messages processed or acknowledged, so these
                                cannot be computed from anything currently stored.
                            </p>
                        {/if}
                    </div>
                {/if}
            {/each}
        {:else}
            <DataStateBanner {state} message={errorMessage(error)} />
        {/if}
        {#if queues.length === 0}
            <p class="status">No queues found.</p>
        {/if}
    {/if}
</div>

{#if affectedRunsQueue}
    <AffectedRunsView queueName={affectedRunsQueue} onClose={() => affectedRunsQueue = null} />
{/if}

<style>
    .queue-table { margin-top: 12px; border: 1px solid #ddd; border-radius: 8px; overflow-x: auto; }
    .queue-row {
        display: grid;
        grid-template-columns: 1.2fr 1fr 0.8fr 0.8fr 1fr 1fr 1fr 1fr 0.6fr 1fr 0.8fr;
        gap: 12px;
        padding: 10px 16px;
        border-top: 1px solid #ddd;
        font-size: 13px;
        align-items: center;
    }
    .queue-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .queue-row.clickable { cursor: pointer; }
    .queue-row.clickable:hover { background: #fafafa; }
    .queue-name { font-family: monospace; }
    .gap { color: #bbb; }
    .zero-consumers { color: #991b1b; font-weight: bold; }

    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; background: #dcfce7; color: #166534; width: fit-content; }
    .badge.degraded { background: #fef3c7; color: #92400e; }

    .status { padding: 16px; text-align: center; color: #666; }
    .status.error { color: #991b1b; }

    .queue-detail { padding: 16px 24px; background: #fafafa; border-top: 1px solid #eee; font-size: 13px; }
    .detail-actions { margin-bottom: 12px; }
    .detail-actions button { padding: 6px 12px; border: 1px solid #ccc; border-radius: 4px; background: white; cursor: pointer; }
    .gap-note { margin-top: 12px; font-size: 11px; color: #999; }
</style>
