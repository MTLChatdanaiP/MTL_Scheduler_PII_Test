import { writable } from "svelte/store";
import { getParam } from "./router";

const initialTimeRange = (getParam("time_range") || "1h") as TimeRange;

export const timeRange = writable<TimeRange>(initialTimeRange);

export type TimeRange = "15m" | "1h" | "6h" | "24h" | "7d" | "custom";

export const autoRefreshEnabled = writable<boolean>(true);
export const lastRefreshedAt = writable<Date | null>(null);
export const globalSearchQuery = writable<string>("");
export const activeFilterCount = writable<number>(0);