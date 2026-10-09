<script lang="ts">
    import { startRefresh } from "./refresh";
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getQueues, type QueueHealth } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import { queueRefreshTick } from "../lib/liveRefreshStores";
 
    let hasLoadedOnce = false;
    let isFetching = false;
    let error: unknown = null;

    // Matches the QUEUE_BACKLOG alert rule's own threshold (rules.json,
    // queue.pending_count > 20) so this card and the real alert never
    // disagree about what "degraded" means.
    import { queueDegraded } from "./thresholds";

    let queues: QueueHealth[] = [];
    let stopRefresh: (() => void) | undefined;

    function isDegraded(q: QueueHealth): boolean {
        return queueDegraded(q);
    }

    async function loadQueues() {
        isFetching = true;
        try {
            const res = await getQueues();
            queues = res.queues;
            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        loadQueues();
        stopRefresh = startRefresh(loadQueues, { gaugeEveryMs: 10000 });
    });

    onDestroy(() => {
        stopRefresh?.();
    });

    $: degradedCount = queues.filter(isDegraded).length;
    $: healthyCount = queues.length - degradedCount;
    $: totalDepth = queues.reduce((sum, q) => sum + q.StreamLength, 0);
    $: worstOldestAge = queues.reduce((max, q) => Math.max(max, q.OldestPendingAgeSeconds), 0);
    $: worstQueue = queues.find(q => q.OldestPendingAgeSeconds === worstOldestAge);
    $: noConsumerCount = queues.filter(q => q.ConsumerCount === 0).length;

    $: state = deriveState({
        hasLoadedOnce,
        isFetching,
        error,
        isEmpty: false,   // cards always have a value, even if it's 0
    });

    // RFC-009 S17 / RFC-010 S22: one shared connection per PAGE (see Workers.svelte/Queues.svelte) bumps this; every component on the page reloads.
    let lastTick = 0;
    $: if ($queueRefreshTick !== lastTick) {
        lastTick = $queueRefreshTick;
        if (lastTick > 0) loadQueues();
    }
</script>

<h3>Queue Health</h3>

{#if error}
    <p>Error: {error}</p>
{:else}
    {#if state === "READY" || state === "REFRESHING"}
        <div class="cards">
            <div class="card">
                <span>Healthy / Degraded</span>
                <strong>{healthyCount} / {degradedCount}</strong>
            </div>

            <div class="card">
                <span>Total Depth</span>
                <strong>{totalDepth}</strong>
            </div>

            <div class="card">
                <span>Oldest Pending Age</span>
                <strong>{worstOldestAge}s</strong>
                {#if worstQueue}<small>{worstQueue.QueueName}</small>{/if}
            </div>

            <div class="card gap-card">
                <span>Backlog Trend</span>
                <strong>—</strong>
                <small>per-queue trend shown in the table below (needs history,
                    not available in this summary fetch)</small>
            </div>

            <div class="card">
                <span>Queues With No Consumers</span>
                <strong>{noConsumerCount}</strong>
            </div>
        </div>
    {:else}
        <DataStateBanner {state} message={errorMessage(error)} />
    {/if}
{/if}

<style>
    .cards {
        display: grid;
        grid-template-columns: repeat(5, 1fr);
        gap: 16px;
    }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 28px; display: block; }
    .card small { color: #888; font-size: 11px; }
    .gap-card { background: #fafafa; }
</style>