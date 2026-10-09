<script lang="ts">
    import { startRefresh } from "./refresh";
    import { runRefreshTick } from "./liveRefreshStores";
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled, timeRange } from "../lib/stores";
    import { getRuns, getAlerts, getOverview } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import { overviewRefreshTick } from "../lib/liveRefreshStores";
 
    let hasLoadedOnce = false;
    let isFetching = false;
    let error: unknown = null;

    // CONFIRM THESE against Debug JSON before trusting the counts below —
    // these are a guess at RunListItem.CurrentStatus's vocabulary, not
    // confirmed. If wrong, every card silently shows 0, not an error.
    const STATUS_SUCCEEDED = "Completed";
    const STATUS_FAILED = "Failed";
    const STATUS_RUNNING = "Running";
    const STATUS_QUEUED = "Queued";
    const STATUS_BLOCKED = "Blocked"; // RFC-006 §11 BLOCK: stored for the record, never queued

    let counts = {
        succeeded: 0,
        failed: 0,
        running: 0,
        queued: 0,
        blocked: 0,
        retrying: 0,
    };
    let lostStuckCount: number
    let failureRate: number | null = null;

    let stopRefresh: (() => void) | undefined;

    function getTimeRangeParam(): string {
        const now = new Date();
        const ranges: Record<string, number> = {
            "15m": 15 * 60 * 1000,
            "1h": 60 * 60 * 1000,
            "6h": 6 * 60 * 60 * 1000,
            "24h": 24 * 60 * 60 * 1000,
            "7d": 7 * 24 * 60 * 60 * 1000,
        };
        const ms = ranges[$timeRange];
        return ms ? `&created_from=${new Date(now.getTime() - ms).toISOString()}` : "";
    }

    // one cheap call per status: limit=1 means almost no payload, we only
    // want page.total. This is what avoids needing RunListItem's field
    // casing to be correct at all.
    async function countByStatus(status: string): Promise<number> {
        const res = await getRuns(`?execution_state=${status}&limit=1&offset=0${getTimeRangeParam()}`);
        return res.page.total ?? 0;
    }

    // Batch 7 (RFC-010 §22): the server counts chains that are waiting on or running a retry RIGHT NOW. An older backend does not send
    // the number, so the old "runs on a retry" count stands in for it.
    async function countRetrying(): Promise<number> {
        try {
            const o = await getOverview();
            if (typeof o.retrying_chains === "number") return o.retrying_chains;
        } catch {
            /* fall through to the older count */
        }
        const res = await getRuns(`?retry_index=1&limit=1&offset=0${getTimeRangeParam()}`);
        return res.page.total ?? 0;
    }

    async function countLostStuckAlerts(): Promise<number> {
        const res = await getAlerts(`?alert_type=RUN_LOST,RUN_STUCK&status=OPEN&limit=1&offset=0${getTimeRangeParam()}`);
        return res.page.total ?? 0;
    }

    async function countOpenAlertsByType(alertType: string): Promise<number> {
        const res = await getAlerts(`?alert_type=${alertType}&status=OPEN&limit=1&offset=0${getTimeRangeParam()}`);
        return res.page.total ?? 0;
    }

    async function loadCounts() {
        isFetching = true;
        try {
            const [succeeded, failed, running, queued, blocked, retrying, runLost, runStuck] = await Promise.all([
                countByStatus(STATUS_SUCCEEDED),
                countByStatus(STATUS_FAILED),
                countByStatus(STATUS_RUNNING),
                countByStatus(STATUS_QUEUED),
                countByStatus(STATUS_BLOCKED),
                countRetrying(),
                countOpenAlertsByType("RUN_LOST"),
                countOpenAlertsByType("RUN_STUCK"),
            ]);

            counts = { succeeded, failed, running, queued, blocked, retrying };
            lostStuckCount = runLost + runStuck;

            const total = succeeded + failed;
            failureRate = total > 0 ? (failed / total) * 100 : null;

            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        loadCounts();
        stopRefresh = startRefresh(loadCounts, { ticks: [runRefreshTick], gaugeEveryMs: 30000 });
    });

    onDestroy(() => stopRefresh?.());

    $: if ($timeRange) loadCounts();

    $: state = deriveState({
        hasLoadedOnce,
        isFetching,
        error,
        isEmpty: false,   // cards always have a value, even if it's 0
    });

    // RFC-010 §22: Overview starts ONE live connection for the whole page and
    // bumps this; every card on it reloads instead of waiting for its own 10s
    // poll. Same shape WorkerHealthCards/QueueHealthCards already use. Guarded
    // on > 0 so the initial store value never triggers a duplicate first load.
    let lastOverviewTick = 0;
    $: if ($overviewRefreshTick !== lastOverviewTick) {
        lastOverviewTick = $overviewRefreshTick;
        if (lastOverviewTick > 0) loadCounts();
    }
</script>

<h3>Run Health</h3>

{#if error}
    <p>Error: {error}</p>
{:else}
    {#if state === "READY" || state === "REFRESHING"}
        <div class="cards">
            <div class="card"><span>Succeeded</span><strong>{counts.succeeded}</strong></div>
            <div class="card"><span>Failed</span><strong>{counts.failed}</strong></div>
            <div class="card"><span>Running</span><strong>{counts.running}</strong></div>
            <div class="card"><span>Waiting / Queued</span><strong>{counts.queued}</strong></div>
            <div class="card"><span>Blocked by PII policy</span><strong>{counts.blocked}</strong></div>
            <div class="card"><span>Retrying Chains</span><strong>{counts.retrying}</strong></div>
            <div class="card">
                <span>Failure Rate</span>
                <strong>{failureRate !== null ? `${failureRate.toFixed(1)}%` : "—"}</strong>
            </div>
            <div class="card">
                <span>Lost / Stuck Annotations</span>
                <strong>{lostStuckCount}</strong>
            </div>
        </div>
    {:else}
        <DataStateBanner {state} message={errorMessage(error)} />
    {/if}
{/if}

<style>
    .cards {
        display: grid;
        grid-template-columns: repeat(4, 1fr);
        gap: 16px;
    }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 28px; }
    .gap-card { background: #fafafa; }
    .gap-card small { display: block; margin-top: 8px; color: #888; font-size: 11px; }
</style>