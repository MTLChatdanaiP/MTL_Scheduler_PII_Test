<script lang="ts">
    import PriorityBanner from "../lib/PriorityBanner.svelte";
    import RunHealthCards from "../lib/RunCards.svelte";
    import QueueHealthCards from "../lib/QueueHealthCards.svelte";
    import WorkerHealthCards from "../lib/WorkerHealthCards.svelte";
    import ScheduleHealthCards from "../lib/ScheduleHealthCards.svelte";
    import AlertHealthCards from "../lib/AlertHealthCards.svelte";
    import PIIHealthCards from "../lib/PIIHealthCards.svelte";
    import MonitoringHealthCards from "../lib/MonitoringHealthCards.svelte";
    import ActivityFeed from "../lib/ActivityFeed.svelte";
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