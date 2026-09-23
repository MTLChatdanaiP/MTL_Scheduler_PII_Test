<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getSchedules, getScheduleDetail, getAlerts, type ScheduleDefinition, type ScheduleDetailResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";

    // A start is "late" if the run actually started noticeably after its
    // expected time. 30s is a starting guess, not a value confirmed anywhere
    // -- adjust if it doesn't match what your rules.json treats as late.
    const LATE_START_THRESHOLD_SECONDS = 30;

    let schedules: ScheduleDefinition[] = [];
    let details: Record<string, ScheduleDetailResponse> = {};
    let missedCount = 0;
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;

    async function loadSchedules() {
        isFetching = true;
        try {
            const res = await getSchedules();
            schedules = res.schedules;
            error = null;

            const detailResults = await Promise.all(
                schedules.map(s => getScheduleDetail(s.ScheduleId).catch(() => null))
            );
            for (let i = 0; i < schedules.length; i++) {
                const d = detailResults[i];
                if (d) details[schedules[i].ScheduleId] = d;
            }

            // REAL number, not derived client-side -- same countByStatus
            // pattern as Run Health's lost/stuck cards.
            const alertRes = await getAlerts(`?alert_type=SCHEDULE_MISSED&status=OPEN&limit=1&offset=0`);
            missedCount = alertRes.page.total ?? 0;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        loadSchedules();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadSchedules();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });

    $: allRecentRuns = Object.values(details).flatMap(d => d.recent_runs);

    $: lateStartsCount = allRecentRuns.filter(
        r => r.start_drift_seconds !== null && r.start_drift_seconds > LATE_START_THRESHOLD_SECONDS
    ).length;

    $: recentFailuresCount = allRecentRuns.filter(r => r.status === "Failed").length;

    $: worstStartDrift = allRecentRuns.reduce((max, r) => {
        return r.start_drift_seconds !== null && r.start_drift_seconds > max ? r.start_drift_seconds : max;
    }, 0);
</script>

<h3>Schedule Health</h3>

{#if state === "READY" || state === "REFRESHING"}
    <div class="cards">
        <div class="card">
            <span>Late Starts</span>
            <strong>{lateStartsCount}</strong>
            <small>start drift over {LATE_START_THRESHOLD_SECONDS}s, across recent runs</small>
        </div>

        <div class="card">
            <span>Missed Schedules</span>
            <strong>{missedCount}</strong>
        </div>

        <div class="card">
            <span>Worst Schedule Drift</span>
            <strong>{worstStartDrift.toFixed(0)}s</strong>
        </div>

        <div class="card">
            <span>Recent Recurring-Run Failures</span>
            <strong>{recentFailuresCount}</strong>
        </div>
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 28px; display: block; }
    .card small { color: #888; font-size: 11px; }
</style>