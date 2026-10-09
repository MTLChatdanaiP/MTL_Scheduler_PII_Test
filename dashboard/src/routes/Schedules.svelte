<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import ScheduleHealthCards from "../lib/ScheduleHealthCards.svelte";
    import ScheduleList from "../lib/ScheduleList.svelte";
    import { startLiveRefreshTrigger } from "../lib/liveRefreshTrigger";
    import { scheduleRefreshTick } from "../lib/liveRefreshStores";

    // RFC-009 S17 / RFC-010 S22: ONE live connection for this whole page, like Workers and Queues. It used to have none, so
    // the occurrence ledger only changed on a 10 second poll. Schedule events announce occurrences and definition changes;
    // the task and attempt events below are what move a scheduled run from created to started to finished.
    let stopLiveRefresh: (() => void) | undefined;

    onMount(() => {
        stopLiveRefresh = startLiveRefreshTrigger({
            prefixes: ["schedule.", "task.created", "task.completed", "task.failed", "attempt.started"],
            onMatch: () => scheduleRefreshTick.update((n) => n + 1),
        });
    });

    onDestroy(() => stopLiveRefresh?.());
</script>

<ScheduleHealthCards />
<ScheduleList />