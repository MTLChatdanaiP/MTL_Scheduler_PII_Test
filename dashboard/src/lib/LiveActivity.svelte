<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getAlerts, type Alert } from "../lib/api";

    // NOT the real §8.9 multi-source feed -- that needs a new global,
    // time-windowed backend endpoint (reusing the §6 timeline converter
    // functions, dropped chain-scoping) that doesn't exist yet. This is the
    // honest, cheap stand-in: alerts already exclude heartbeat noise and raw
    // logs by nature, which covers the "no noise" requirement for free.
    let activity: Alert[] = [];
    let refreshTimer: ReturnType<typeof setInterval>;

    async function load() {
        try {
            const res = await getAlerts(`?limit=15&offset=0`);
            activity = res.alerts.sort((a, b) => new Date(b.opened_at).getTime() - new Date(a.opened_at).getTime());
        } catch {
            // silent -- this is a small supplementary widget, not worth its
            // own error banner on top of everything else on Overview
        }
    }

    onMount(() => {
        load();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) load();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));
</script>

<h4>Recent Activity <span class="caveat">(alert-based, not a full system feed)</span></h4>

<div class="feed">
    {#each activity as a (a.alert_id)}
        <div class="feed-item">
            <span class="time">{new Date(a.opened_at).toLocaleTimeString()}</span>
            <span class="badge" class:critical={a.severity === "CRITICAL"} class:warning={a.severity === "WARNING"}>{a.severity}</span>
            <span>{a.summary}</span>
        </div>
    {/each}
    {#if activity.length === 0}
        <p class="empty">No recent activity.</p>
    {/if}
</div>

<style>
    h4 { font-size: 13px; color: #555; margin: 20px 0 8px; }
    .caveat { font-weight: normal; color: #999; font-size: 11px; }

    .feed { border: 1px solid #ddd; border-radius: 8px; max-height: 300px; overflow-y: auto; }
    .feed-item { display: flex; gap: 12px; align-items: center; padding: 8px 16px; border-top: 1px solid #eee; font-size: 12px; }
    .feed-item:first-child { border-top: none; }
    .time { color: #888; min-width: 70px; }
    .empty { padding: 16px; text-align: center; color: #999; font-size: 13px; }

    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 10px; font-weight: bold; background: #eee; }
    .critical { background: #fee2e2; color: #991b1b; }
    .warning { background: #fef3c7; color: #92400e; }
</style>