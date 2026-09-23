<script lang="ts">
    import GlobalBar from "./lib/GlobalBar.svelte";
    import { currentPath, parsePath, navigate } from "./lib/router";

    import Overview from "./routes/Overview.svelte";
    import Runs from "./routes/Runs.svelte";
    import Queues from "./routes/Queues.svelte";
    import Workers from "./routes/Workers.svelte";
    import Schedules from "./routes/Schedules.svelte";
    import Alerts from "./routes/Alerts.svelte";
    import PII from "./routes/PII.svelte";
    import Monitoring from "./routes/Monitoring.svelte";
    import Components from "./routes/Components.svelte";
    import LiveActivity from "./routes/LiveActivity.svelte";

    const pages: PageName[] = [
        "Overview", "Runs", "Queues", "Workers",
        "Schedules", "Alerts", "PII", "Monitoring", 
        "Components", "LiveActivity",
    ];

    const pageComponents = {
        "Overview": Overview,
        "Runs": Runs,
        "Queues": Queues,
        "Workers": Workers,
        "Schedules": Schedules,
        "Alerts": Alerts,
        "PII": PII,
        "Monitoring": Monitoring,
        "Components": Components,
        "LiveActivity": LiveActivity,
    };

    type PageName = keyof typeof pageComponents;

    // router.ts's URL keys are lowercase ("alerts"), your page names are
    // capitalized ("Alerts") — this pair of maps converts between them so
    // neither side has to change its own convention.
    const toKey = (page: PageName): string => page.toLowerCase();
    const toPage = (key: string): PageName | null =>
        pages.find(p => toKey(p) === key) ?? null;

    // derived from the URL, not a local variable — this is what makes the
    // page respond to the address bar, back/forward, and pasted links
    $: activePage = toPage(parsePath($currentPath).page) ?? "Overview";
</script>

<div class="dashboard">
    <aside class="sidebar">
        <h1>MTL Scheduler</h1>

        <nav>
            {#each pages as page}
                <button
                    class:active={activePage === page}
                    onclick={() => navigate(`/${toKey(page)}`)}
                >
                    {page}
                </button>
            {/each}
        </nav>
    </aside>

    <main>
        <header>
            <h2>{activePage}</h2>
        </header>

        <GlobalBar />

        <section class="content">
            <svelte:component this={pageComponents[activePage]} />
        </section>
    </main>
</div>

<style>
    .dashboard {
        display: flex;
        min-height: 100vh;
    }

    .sidebar {
        width: 220px;
        padding: 20px;
        border-right: 1px solid #ddd;
    }

    .sidebar h1 {
        font-size: 20px;
        margin-bottom: 30px;
    }

    nav {
        display: flex;
        flex-direction: column;
        gap: 6px;
    }

    nav button {
        padding: 10px;
        text-align: left;
        background: none;
        border: none;
        cursor: pointer;
    }

    nav button.active {
        font-weight: bold;
        background: #eee;
    }

    main {
        flex: 1;
    }

    header {
        padding: 20px;
        border-bottom: 1px solid #ddd;
    }

    header h2 {
        margin-top: 0;
    }

    .content {
        padding: 20px;
    }
</style>