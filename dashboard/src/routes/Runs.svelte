<script lang="ts">
    import RunHealthCards from "../lib/RunCards.svelte";
    import RunList from "../lib/RunList.svelte";
    import { usePageLive } from "../lib/pageLive";
    import { runRefreshTick } from "../lib/liveRefreshStores";

    // RFC-009 §17: this page used to poll every 10s with no live connection.
    usePageLive({ scope: "runs", prefixes: ["task.", "run.", "attempt."], tick: runRefreshTick });
</script>

<RunHealthCards />
<RunList />
