<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getSchedules, getScheduleDetail, type ScheduleDefinition, type ScheduleDetailResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import JsonTree from "../lib/JsonTree.svelte";
    import AffectedRunsView from "../lib/AffectedRunsView.svelte";
    import { currentPath, parsePath, updateParams } from "../lib/router";

    let schedules: ScheduleDefinition[] = [];
    let details: Record<string, ScheduleDetailResponse> = {};
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;

    let expandedSchedule: string | null = null;
    let affectedRunsSchedule: string | null = null;

    let unsubscribePath: (() => void) | undefined;
 
    function syncFiltersFromUrl() {
        const { params } = parsePath($currentPath);
        scheduleIdFilter = params.get("schedule_id") ?? "";
        enabledFilter = (params.get("enabled") as typeof enabledFilter) ?? "";
    }

    function commitFilters() {
        updateParams({
            schedule_id: scheduleIdFilter,
            enabled: enabledFilter,
        });
    }

    async function backfillDetail(scheduleId: string) {
        try {
            details[scheduleId] = await getScheduleDetail(scheduleId);
            details = details; // trigger reactivity, plain object mutation isn't tracked
        } catch {
            // leave unset -- row shows "..." until/unless this resolves
        }
    }

    async function loadSchedules() {
        isFetching = true;
        try {
            const res = await getSchedules();
            schedules = res.schedules;
            error = null;

            for (const s of schedules) {
                backfillDetail(s.ScheduleId);
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
            syncFiltersFromUrl();
        });
    
        loadSchedules();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadSchedules();
        }, 10000);
    });
    
    onDestroy(() => {
        unsubscribePath?.();
        clearInterval(refreshTimer);
    });

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: schedules.length === 0 });

    function nextExpected(s: ScheduleDefinition): string {
        const d = details[s.ScheduleId];
        const iso = d?.next_expected_at ?? s.NextRunAt;
        if (!iso) return "—";
        return new Date(iso).toLocaleString();
    }

    function resultDistribution(scheduleId: string): string {
        const runs = details[scheduleId]?.recent_runs ?? [];
        if (runs.length === 0) return "…";
        const succeeded = runs.filter(r => r.status === "Succeeded").length;
        const failed = runs.filter(r => r.status === "Failed").length;
        const other = runs.length - succeeded - failed;
        return `${succeeded} ok / ${failed} failed${other > 0 ? ` / ${other} other` : ""}`;
    }

    function missedDelayedCount(scheduleId: string): number {
        const annotations = details[scheduleId]?.annotations ?? [];
        return annotations.filter(a => a.Type.includes("MISSED") || a.Type.includes("DELAYED")).length;
    }

    function latestDrift(scheduleId: string): { creation: number | null; start: number | null } {
        const runs = details[scheduleId]?.recent_runs ?? [];
        if (runs.length === 0) return { creation: null, start: null };
        const latest = runs[0];
        return { creation: latest.creation_drift_seconds, start: latest.start_drift_seconds };
    }

    function toggleExpanded(scheduleId: string) {
        expandedSchedule = expandedSchedule === scheduleId ? null : scheduleId;
    }

    let scheduleIdFilter = "";
    let enabledFilter: "" | "enabled" | "disabled" = "";

    $: visibleSchedules = schedules.filter(s => {
        if (scheduleIdFilter && !s.ScheduleId.toLowerCase().includes(scheduleIdFilter.toLowerCase())) return false;
        if (enabledFilter === "enabled" && !s.Enabled) return false;
        if (enabledFilter === "disabled" && s.Enabled) return false;
        return true;
    });
</script>

<div class="filter-bar">
    <label>
        Schedule ID:
        <input type="text" bind:value={scheduleIdFilter} placeholder="e.g. TEST-SCHED" on:change={commitFilters} />
    </label>
    <label>
        Status:
        <select bind:value={enabledFilter} on:change={commitFilters}>
            <option value="">All</option>
            <option value="enabled">Enabled</option>
            <option value="disabled">Disabled</option>
        </select>
    </label>
</div>

<div class="schedule-table">
    <div class="schedule-row header">
        <span>Schedule</span>
        <span>Enabled</span>
        <span>Next Expected</span>
        <span>Recent Occurrences</span>
        <span>Creation Drift</span>
        <span>Start Drift</span>
        <span>Missed/Delayed</span>
        <span>Result Distribution</span>
        <span>Associated Runs</span>
    </div>

    {#if state === "READY" || state === "REFRESHING"}
        {#each visibleSchedules as s (s.ScheduleId)}
            {@const drift = latestDrift(s.ScheduleId)}
            <div class="schedule-row clickable" on:click={() => toggleExpanded(s.ScheduleId)} role="button" tabindex="0" on:keydown={(e) => e.key === "Enter" && toggleExpanded(s.ScheduleId)}>
                <span class="schedule-id">{s.ScheduleId}</span>
                <span class="badge" class:enabled={s.Enabled}>{s.Enabled ? "Enabled" : "Disabled"}</span>
                <span>{nextExpected(s)}</span>
                <span>{details[s.ScheduleId]?.recent_runs.length ?? "…"}</span>
                <span>{drift.creation !== null ? `${drift.creation.toFixed(0)}s` : "…"}</span>
                <span>{drift.start !== null ? `${drift.start.toFixed(0)}s` : (details[s.ScheduleId] ? "n/a" : "…")}</span>
                <span>{details[s.ScheduleId] ? missedDelayedCount(s.ScheduleId) : "…"}</span>
                <span class="distribution">{resultDistribution(s.ScheduleId)}</span>
                <span>
                    <button on:click|stopPropagation={() => affectedRunsSchedule = s.ScheduleId}>View →</button>
                </span>
            </div>

            {#if expandedSchedule === s.ScheduleId}
                <div class="schedule-detail">
                    {#if details[s.ScheduleId]}
                        <h4>Recent Runs ({details[s.ScheduleId].recent_runs.length})</h4>
                        <JsonTree value={details[s.ScheduleId].recent_runs} />

                        <h4>Annotations ({details[s.ScheduleId].annotations.length})</h4>
                        <JsonTree value={details[s.ScheduleId].annotations} />
                    {:else}
                        <p class="status">Loading detail...</p>
                    {/if}
                </div>
            {/if}
        {/each}
    {:else}
        <DataStateBanner {state} message={errorMessage(error)} />
    {/if}
</div>

{#if affectedRunsSchedule}
    <AffectedRunsView scheduleId={affectedRunsSchedule} onClose={() => affectedRunsSchedule = null} />
{/if}

<style>
    .schedule-table { margin-top: 12px; border: 1px solid #ddd; border-radius: 8px; overflow-x: auto; }
    .schedule-row {
        display: grid;
        grid-template-columns: 1.2fr 0.8fr 1.3fr 1fr 1fr 1fr 1fr 1.3fr 0.8fr;
        gap: 12px;
        padding: 10px 16px;
        border-top: 1px solid #ddd;
        font-size: 13px;
        align-items: center;
    }
    .schedule-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .schedule-row.clickable { cursor: pointer; }
    .schedule-row.clickable:hover { background: #fafafa; }
    .schedule-id { font-family: monospace; }
    .distribution { font-size: 12px; }

    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; background: #fee2e2; color: #991b1b; width: fit-content; }
    .badge.enabled { background: #dcfce7; color: #166534; }

    .status { padding: 16px; text-align: center; color: #666; }

    .schedule-detail { padding: 16px 24px; background: #fafafa; border-top: 1px solid #eee; font-size: 13px; }
    .schedule-detail h4 { font-size: 13px; margin: 12px 0 8px; }

    .schedule-row button { padding: 4px 10px; border: 1px solid #ccc; border-radius: 4px; background: white; cursor: pointer; font-size: 12px; }

    .filter-bar { display: flex; gap: 16px; align-items: flex-end; flex-wrap: wrap; padding: 12px 0; }
    .filter-bar label { display: flex; flex-direction: column; font-size: 12px; gap: 4px; }
    .filter-bar input, .filter-bar select { padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; }
</style>