<script lang="ts">
    import PIIHealthCards from "../lib/PIIHealthCards.svelte";
    import PIIFindingsList from "../lib/PIIFindingsList.svelte";
    import { usePageLive } from "../lib/pageLive";
    import { piiRefreshTick } from "../lib/liveRefreshStores";

    // RFC-009 §17. The server answers 403 to a key without pii.findings.read; the connection badge then says NO ACCESS and the
    // slower safety refresh in lib/refresh.ts keeps the page honest.
    usePageLive({ scope: "pii.summary", prefixes: ["pii."], tick: piiRefreshTick });
</script>

<PIIHealthCards />
<PIIFindingsList />
