<script lang="ts">
    import { startRefresh } from "./refresh";
    import { monitoringRefreshTick } from "./liveRefreshStores";
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getMonitoringHealth, type MonitoringHealthResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import { formatAge } from "../lib/activity";
    import DataStateBanner from "../lib/DataStateBanner.svelte";

    // RFC-010 §24: the live pipeline monitors itself. GET /monitoring/health
    // returns these under "live_pipeline". Typed here (not in api.ts) so this
    // card is one self-contained file.
    interface LivePipelineStats {
        active_connections: number;
        connections_total: number;
        disconnections_total: number;
        events_published_total: number;
        events_delivered_total: number;
        slow_clients_disconnected_total: number;
        replays_total: number;
        events_replayed_total: number;
        resyncs_total: number;
        max_client_buffer_depth: number;
        client_buffer_capacity: number;
        last_delivery_lag_ms: number;
        max_delivery_lag_ms: number;
        last_published_at?: string;
        last_heartbeat_at?: string;
        // RFC-005 §21 monitoring the live pipeline
        events_recorded_total: number;
        live_change_backlog: number;
        event_write_failures_total: number;
        last_publish_lag_ms: number;
        max_publish_lag_ms: number;
    }

    let stats: LivePipelineStats | null = null;
    let error: unknown = null;
    let stopRefresh: (() => void) | undefined;
    let hasLoadedOnce = false;
    let isFetching = false;

    async function load() {
        isFetching = true;
        try {
            const data = (await getMonitoringHealth()) as MonitoringHealthResponse & {
                live_pipeline?: LivePipelineStats;
            };
            stats = data.live_pipeline ?? null;
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
        stopRefresh = startRefresh(load, { ticks: [monitoringRefreshTick], gaugeEveryMs: 10000 });
    });

    onDestroy(() => stopRefresh?.());

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: stats === null });

    function ago(iso: string | undefined): string {
        if (!iso) return "never";
        return `${formatAge((Date.now() - Date.parse(iso)) / 1000)} ago`;
    }

    $: rows = stats
        ? [
              ["Active connections", String(stats.active_connections)],
              ["Connects / disconnects (total)", `${stats.connections_total} / ${stats.disconnections_total}`],
              ["Events published", String(stats.events_published_total)],
              ["Events delivered to clients", String(stats.events_delivered_total)],
              ["Last publish", ago(stats.last_published_at)],
              ["Delivery lag (last / max)", `${stats.last_delivery_lag_ms} ms / ${stats.max_delivery_lag_ms} ms`],
              ["Publish lag (last / max)", `${stats.last_publish_lag_ms} ms / ${stats.max_publish_lag_ms} ms`],
              ["Live-change backlog (recorded, not yet published)", String(stats.live_change_backlog)],
              ["Event write failures", String(stats.event_write_failures_total)],
              ["Deepest client buffer", `${stats.max_client_buffer_depth} of ${stats.client_buffer_capacity}`],
              ["Slow clients disconnected", String(stats.slow_clients_disconnected_total)],
              ["Replays served (events sent)", `${stats.replays_total} (${stats.events_replayed_total})`],
              ["Resyncs sent", String(stats.resyncs_total)],
              [
                  "Last keepalive ping",
                  stats.active_connections === 0 && !stats.last_heartbeat_at
                      ? "no clients connected yet"
                      : ago(stats.last_heartbeat_at),
              ],
          ]
        : [];
</script>

<h4>Live Pipeline</h4>

{#if state === "READY" || state === "REFRESHING"}
    <div class="pipeline">
        {#each rows as [label, value] (label)}
            <div class="row"><span class="label">{label}</span><span>{value}</span></div>
        {/each}
    </div>
    <p class="note">Counters are held in memory and reset when the backend restarts.</p>
{:else if state === "EMPTY"}
    <p class="note">The backend did not report live pipeline statistics.</p>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    h4 { font-size: 13px; margin: 20px 0 8px; color: #555; }
    .pipeline { border: 1px solid #ddd; border-radius: 8px; overflow: hidden; max-width: 560px; }
    .row { display: flex; justify-content: space-between; gap: 16px; padding: 8px 16px; border-top: 1px solid #eee; font-size: 13px; }
    .row:first-child { border-top: none; }
    .label { color: #666; }
    .note { font-size: 11px; color: #999; margin: 6px 0 0; }
</style>