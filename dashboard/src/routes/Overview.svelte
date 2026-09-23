<script lang="ts">
    import PriorityBanner from "../lib/PriorityBanner.svelte";
    import RunHealthCards from "../lib/RunCards.svelte";
    import QueueHealthCards from "../lib/QueueHealthCards.svelte";
    import WorkerHealthCards from "../lib/WorkerHealthCards.svelte";
    import ScheduleHealthCards from "../lib/ScheduleHealthCards.svelte";
    import AlertHealthCards from "../lib/AlertHealthCards.svelte";
    import PiiHealthCards from "../lib/PiiCards.svelte";
    import MonitoringHealthCards from "../lib/MonitoringHealthCards.svelte";
    import ActivityFeed from "../lib/LiveActivity.svelte";
</script>

<h2>Overview</h2>

<PriorityBanner />

<RunHealthCards />
<QueueHealthCards />
<WorkerHealthCards />
<ScheduleHealthCards />
<AlertHealthCards />
<PiiHealthCards />
<MonitoringHealthCards />

<ActivityFeed />