<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import QueueHealthCards from "../lib/QueueHealthCards.svelte";
    import QueueList from "../lib/QueueList.svelte";
    import { startLiveRefreshTrigger } from "../lib/liveRefreshTrigger";
    import { queueRefreshTick } from "../lib/liveRefreshStores";

    let stopLiveRefresh: (() => void) | undefined;

    onMount(() => {
        stopLiveRefresh = startLiveRefreshTrigger({
            prefixes: ["queue."],
            scopes: ["queues"], // RFC-010 §15
            onMatch: () => queueRefreshTick.update((n) => n + 1),
            fallback: {}, // RFC-010 §27
        });
    });

    onDestroy(() => stopLiveRefresh?.());
</script>

<QueueHealthCards />
<QueueList />