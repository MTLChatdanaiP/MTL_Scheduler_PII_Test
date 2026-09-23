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

    type SubsystemFreshnessLike = { available: boolean; lag_seconds?: number } | undefined;

    function findSubsystem(name: string) {
        return data?.subsystems.find(s => s.subsystem === name);
    }

    function fmtLag(s: SubsystemFreshnessLike): string {
        if (!s || !s.available) return "unavailable";
        return `${s.lag_seconds!.toFixed(0)}s ago`;
    }
</script>

<h3>Monitoring Health</h3>

{#if state === "READY" || state === "REFRESHING"}
    {@const ingest = findSubsystem("Event Ingestion")}
    {@const projection = findSubsystem("Projection")}
    {@const redis = findSubsystem("Redis Queue Inspection")}
    {@const alertEval = findSubsystem("Alert Evaluation")}

    <div class="cards">
        <div class="card" class:stale={ingest && !ingest.available}>
            <span>Event Ingestion</span>
            <strong>{fmtLag(ingest)}</strong>
        </div>

        <div class="card" class:stale={projection && !projection.available}>
            <span>Projection Freshness</span>
            <strong>{fmtLag(projection)}</strong>
        </div>

        <div class="card" class:stale={redis && !redis.available}>
            <span>Redis Inspection Freshness</span>
            <strong>{fmtLag(redis)}</strong>
        </div>

        <div class="card unavailable">
            <span>Alert-Evaluation Freshness</span>
            <strong>—</strong>
            <small>{alertEval?.unavailable_reason}</small>
        </div>

        <div class="card gap-card">
            <span>Known Monitoring Gaps</span>
            <ul>
                {#each data?.known_gaps ?? [] as gap}
                    <li>{gap}</li>
                {/each}
            </ul>
        </div>
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 24px; display: block; }
    .card small { color: #888; font-size: 11px; }
    .card.stale { border-color: #fca5a5; background: #fef2f2; }
    .card.unavailable { background: #fafafa; }
    .gap-card { grid-column: span 3; background: #fafafa; }
    .gap-card ul { margin: 4px 0 0; padding-left: 18px; font-size: 12px; color: #666; }
    .gap-card li { margin-bottom: 4px; }
</style>
