<script lang="ts">
    import MonitoringHealthCards from "../lib/MonitoringHealthCards.svelte";
    import MonitoringHealthDetail from "../lib/MonitoringHealthDetail.svelte";
    import LivePipelineCard from "../lib/LivePipelineCard.svelte";
    import FrontendHealthCard from "../lib/FrontendHealthCard.svelte";
    import PolicyDashboardCard from "../lib/PolicyDashboardCard.svelte";
    import { usePageLive } from "../lib/pageLive";
    import { monitoringRefreshTick } from "../lib/liveRefreshStores";

    // RFC-009 §17. The pipeline counters and heartbeat ages change without any event, so these cards also refresh on a short
    // safety timer (lib/refresh.ts); the live trigger makes a state change appear at once.
    usePageLive({ scope: "monitoring.health", prefixes: ["monitoring."], tick: monitoringRefreshTick });
</script>

<MonitoringHealthCards />
<MonitoringHealthDetail />
<LivePipelineCard />
<FrontendHealthCard />
<PolicyDashboardCard />
