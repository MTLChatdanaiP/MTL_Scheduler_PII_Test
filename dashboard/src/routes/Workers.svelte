<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import WorkerHealthCards from "../lib/WorkerHealthCards.svelte";
    import WorkerList from "../lib/WorkerList.svelte";
    import { startLiveRefreshTrigger } from "../lib/liveRefreshTrigger";
    import { workerRefreshTick } from "../lib/liveRefreshStores";

    // RFC-009 S17 / RFC-010 S22: ONE live connection for this whole page.
    // Both WorkerHealthCards and WorkerList react to workerRefreshTick
    // instead of each opening their own connection to /live/activity.
    let stopLiveRefresh: (() => void) | undefined;

    onMount(() => {
        stopLiveRefresh = startLiveRefreshTrigger({
            prefixes: ["worker."],
            scopes: ["workers"], // RFC-010 §15: the server sends only worker events, not the whole firehose
            onMatch: () => workerRefreshTick.update((n) => n + 1),
            fallback: {}, // RFC-010 §27: poll (visibly, boundedly) if the live stream goes away
        });
    });

    onDestroy(() => stopLiveRefresh?.());
</script>

<WorkerHealthCards />
<WorkerList />