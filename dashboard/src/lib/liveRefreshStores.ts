import { writable } from "svelte/store";

// RFC-009 §17 / RFC-010 §22: one connection per PAGE, not one per component.
// A page (Workers.svelte, Queues.svelte) starts exactly one
// startLiveRefreshTrigger and bumps the matching store below on every
// matching event. Components on that page subscribe to the store instead of
// each opening their own connection to the same feed.
//
// A plain incrementing number, not the event itself: components don't need
// to know WHAT changed, only THAT something in their category did, so they
// know to reload. Starts at 0 so the first real bump (1) is always a change
// components can react to with a simple "$: if ($tick) reload()".
export const workerRefreshTick = writable(0);
export const queueRefreshTick = writable(0);

// RFC-010 §22: the Overview page's own aggregate numbers depend on several
// event families at once (runs, alerts, queues, workers, schedules, PII), so it
// gets one broad tick rather than one store per card. Cards that already have a
// dedicated store (workers, queues) keep using theirs -- Overview bumps those
// too, so a card works the same whichever page it is on.
export const overviewRefreshTick = writable(0);