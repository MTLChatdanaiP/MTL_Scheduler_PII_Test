<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { startLiveRefreshTrigger } from "../lib/liveRefreshTrigger";
    import { workerRefreshTick, queueRefreshTick, overviewRefreshTick } from "../lib/liveRefreshStores";
    import { drillHref, type OverviewSection } from "../lib/drillLinks";
    import PriorityBanner from "../lib/PriorityBanner.svelte";
    import RunHealthCards from "../lib/RunCards.svelte";
    import QueueHealthCards from "../lib/QueueHealthCards.svelte";
    import WorkerHealthCards from "../lib/WorkerHealthCards.svelte";
    import ScheduleHealthCards from "../lib/ScheduleHealthCards.svelte";
    import AlertHealthCards from "../lib/AlertHealthCards.svelte";
    import PIIHealthCards from "../lib/PIIHealthCards.svelte";
    import MonitoringHealthCards from "../lib/MonitoringHealthCards.svelte";
    import ComponentHealthCard from "../lib/ComponentHealthCard.svelte";
    import ActivityFeed from "../lib/ActivityFeed.svelte";
    import PipelineLagStrip from "../lib/PipelineLagStrip.svelte";

    // RFC-010 §22: ONE live connection for the whole page (not one per card),
    // bumping every tick store the cards on this page listen to. Same
    // single-connection-per-page rule Workers.svelte and Queues.svelte follow.
    //
    // RFC-010 §7/§15: it subscribes to the server's platform.summary signal ONLY -- one event per second at most, sent after a burst of
    // changes -- instead of receiving every event of nine families just to refetch the same aggregates. Each card refetches its own
    // snapshot, which is what the live path was always for.
    //
    // RFC-010 §27: fallback enabled here because Overview is the landing page --
    // the one screen most likely to be left open while a backend restarts.
    let stopTrigger: (() => void) | null = null;

    onMount(() => {
        stopTrigger = startLiveRefreshTrigger({
            prefixes: ["platform."],
            scopes: ["platform.summary"],
            onMatch: () => {
                workerRefreshTick.update((n) => n + 1);
                queueRefreshTick.update((n) => n + 1);
                overviewRefreshTick.update((n) => n + 1);
            },
            fallback: { intervalMs: 10_000, maxDurationMs: 300_000, graceMs: 5_000 },
        });
    });

    onDestroy(() => stopTrigger?.());

    // RFC-010 §22 drill-through: each aggregate links to the page that lists what it counts.
    const sections: { key: OverviewSection; label: string }[] = [
        { key: "runs", label: "Runs" },
        { key: "queues", label: "Queues" },
        { key: "workers", label: "Workers" },
        { key: "schedules", label: "Schedules" },
        { key: "alerts", label: "Open alerts" },
        { key: "pii", label: "PII" },
        { key: "monitoring", label: "Monitoring" },
        { key: "components", label: "Components" },
    ];
</script>

<h2>Overview</h2>

<PriorityBanner />
<p class="legend">Plain figures are stored facts. A dashed <span class="legend-pill">derived</span> tag marks a verdict worked out from them (health, RFC state).</p>
<PipelineLagStrip />

<nav class="drill" aria-label="Open the page behind an aggregate">
    {#each sections as s (s.key)}
        <a href={drillHref(s.key)}>{s.label} →</a>
    {/each}
</nav>

<RunHealthCards />
<QueueHealthCards />
<WorkerHealthCards />
<ScheduleHealthCards />
<AlertHealthCards />
<PIIHealthCards />
<MonitoringHealthCards />
<ComponentHealthCard />

<!-- Live Activity: connection badge, filters, real-time graph and the feed.
     One component, one live connection. Move this line up if you want it
     nearer the top of the page. -->
<ActivityFeed />

<style>
    .legend { font-size: 12px; color: #4b5563; margin: 4px 0 8px; }
    .legend-pill { padding: 0 6px; border: 1px dashed #6b7280; border-radius: 8px; font-size: 10px; }
    .drill { display: flex; flex-wrap: wrap; gap: 8px 16px; margin: 4px 0 12px; font-size: 13px; }
    .drill a { color: #1d4ed8; text-decoration: none; }
    .drill a:hover { text-decoration: underline; }
</style>
