<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { startLiveRefreshTrigger } from "../lib/liveRefreshTrigger";
    import { workerRefreshTick, queueRefreshTick, overviewRefreshTick } from "../lib/liveRefreshStores";
    import PriorityBanner from "../lib/PriorityBanner.svelte";
    import RunHealthCards from "../lib/RunCards.svelte";
    import QueueHealthCards from "../lib/QueueHealthCards.svelte";
    import WorkerHealthCards from "../lib/WorkerHealthCards.svelte";
    import ScheduleHealthCards from "../lib/ScheduleHealthCards.svelte";
    import AlertHealthCards from "../lib/AlertHealthCards.svelte";
    import PIIHealthCards from "../lib/PIIHealthCards.svelte";
    import MonitoringHealthCards from "../lib/MonitoringHealthCards.svelte";
    import ActivityFeed from "../lib/ActivityFeed.svelte";

    // RFC-010 §22: ONE live connection for the whole page (not one per card),
    // bumping every tick store the cards on this page listen to. Same
    // single-connection-per-page rule Workers.svelte and Queues.svelte follow.
    //
    // RFC-010 §27: fallback enabled here because Overview is the landing page --
    // the one screen most likely to be left open while a backend restarts.
    let stopTrigger: (() => void) | null = null;

    onMount(() => {
        stopTrigger = startLiveRefreshTrigger({
            prefixes: ["task.", "run.", "alert.", "queue.", "worker.", "schedule.", "pii.", "monitoring.", "component."],
            onMatch: () => {
                workerRefreshTick.update((n) => n + 1);
                queueRefreshTick.update((n) => n + 1);
                overviewRefreshTick.update((n) => n + 1);
            },
            fallback: { intervalMs: 10_000, maxDurationMs: 300_000, graceMs: 5_000 },
        });
    });

    onDestroy(() => stopTrigger?.());
</script>

<h2>Overview</h2>

<PriorityBanner />

<RunHealthCards />
<QueueHealthCards />
<WorkerHealthCards />
<ScheduleHealthCards />
<AlertHealthCards />
<PIIHealthCards />
<MonitoringHealthCards />

<!-- Live Activity: connection badge, filters, real-time graph and the feed.
     One component, one live connection. Move this line up if you want it
     nearer the top of the page. -->
<ActivityFeed />