<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getMonitoringHealth, type MonitoringHealthResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";

    let data: MonitoringHealthResponse | null = null;
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;

    async function load() {
        isFetching = true;
        try {
            data = await getMonitoringHealth();
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
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) load();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });
</script>

{#if state === "READY" || state === "REFRESHING"}
    <h4>Per-Subsystem Freshness</h4>
    <div class="mon-table">
        <div class="mon-row header">
            <span>Subsystem</span>
            <span>Last Successful Observation</span>
            <span>Lag</span>
        </div>
        {#each data?.subsystems ?? [] as s}
            <div class="mon-row">
                <span>{s.subsystem}</span>
                <span>{s.available && s.last_observed_at ? new Date(s.last_observed_at).toLocaleString() : "—"}</span>
                <span class:stale-text={!s.available}>
                    {s.available ? `${s.lag_seconds!.toFixed(0)}s` : s.unavailable_reason}
                </span>
            </div>
        {/each}
    </div>

    <h4>Anomaly-Detection Sweep</h4>
    <div class="mon-detail-grid">
        <div><span class="label">Status:</span> <span class="badge" class:degraded={data?.sweep_status !== "COMPLETE"}>{data?.sweep_status}</span></div>
        <div><span class="label">Failed checks this cycle:</span> {data?.sweep_failed_checks} <small>(count only, no per-check breakdown -- see known gaps)</small></div>
        <div><span class="label">Sampled at:</span> {data?.sweep_sampled_at ? new Date(data.sweep_sampled_at).toLocaleString() : "—"}</div>
    </div>

    <h4>PII Policy</h4>
    <div class="mon-detail-grid">
        <div><span class="label">Active policy:</span> {data?.active_policy_name} v{data?.active_policy_version}</div>
        <div><span class="label">Checksum:</span> <span class="checksum">{data?.active_policy_checksum}</span></div>
        <div><span class="label">Last reload:</span>
            <span class="badge" class:degraded={data?.last_reload_result !== "SUCCESS"}>{data?.last_reload_result}</span>
            {data?.last_reload_at ? new Date(data.last_reload_at).toLocaleString() : ""}
        </div>
        {#if data?.last_reload_failure_reason}
            <div class="failure-reason"><span class="label">Failure reason:</span> {data.last_reload_failure_reason}</div>
        {/if}
        <div><span class="label">Scanner policy-version drift:</span> {data?.scanner_drift}</div>
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    h4 { font-size: 13px; margin: 20px 0 8px; color: #555; }

    .mon-table { border: 1px solid #ddd; border-radius: 8px; overflow: hidden; }
    .mon-row { display: grid; grid-template-columns: 1.2fr 1.5fr 1fr; gap: 12px; padding: 10px 16px; border-top: 1px solid #ddd; font-size: 13px; align-items: center; }
    .mon-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .stale-text { color: #991b1b; font-size: 12px; }

    .mon-detail-grid { display: flex; flex-direction: column; gap: 8px; font-size: 13px; padding: 12px 16px; border: 1px solid #ddd; border-radius: 8px; }
    .label { color: #888; margin-right: 6px; }
    .checksum { font-family: monospace; font-size: 11px; }
    .failure-reason { color: #991b1b; }

    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; background: #dcfce7; color: #166534; }
    .badge.degraded { background: #fee2e2; color: #991b1b; }
</style>
