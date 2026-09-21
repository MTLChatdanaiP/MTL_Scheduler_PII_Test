const BASE_URL = "http://localhost:8080";
const API_KEY = "dev-local-key-changeme";

// ---------------------------------------------------------------- Alert

export interface Alert {
    alert_id: string;
    alert_type: string;
    severity: string;
    status: string;
    opened_at: string;
    acknowledged_at: string | null;   // may be absent/null if never acked
    acknowledged_by: string | null;
    resolved_at: string | null;
    subject_type: string;
    subject_id: string;
    rule_id: string;
    rule_version: number;
    evidence: string;                  // JSON-encoded string, parse before display
    summary: string;
}
 
export interface AlertsResponse {
    alerts: Alert[];
    page: Page;
    freshness: Freshness;
    live: Live;
}

export function getAlerts(params: string = ""): Promise<AlertsResponse> {
    return apiFetch(`/alerts${params}`);
}

export function acknowledgeAlert(alertId: string): Promise<{ alert_id: string; status: string; acknowledged_by: string }> {
    return apiFetch(`/alerts/${alertId}/acknowledge`, { method: "POST" })
}

// ---------------------------------------------------------------- Runs

export interface RunListItem {
    // gorm.Model fields, also untagged, also PascalCase
    ID: number;
    CreatedAt: string;
    UpdatedAt: string;
    DeletedAt: string | null;

    // models.Task, embedded with no json tag — flattened, PascalCase.
    JobId: string;
    TaskName: string;
    TaskType: string;
    Status: string;
    FinishedAt: string;
    RunAt: string;
    ExpectedAt: string;
    ExecutionChainId: string;
    ParentRunId: string;
    RetryIndex: number;
    ScheduleId: string;
    ScanStatus: string;
    Queue: string;

    // this one DOES have a tag, per your earlier Debug JSON — confirm it's
    // not just coincidentally lowercase
    source_run_id: string;

    current_status: string;
    queued_at: string;
    started_at: string;
    completed_at: string;
    recovery_started: boolean;
    pii_finding_count: number;
    was_reclaimed: boolean;
    last_event_at: string;

    attempts: unknown[];

    AttemptCount: number;
    LatestWorker: string;
    FailureCategory: string;
    Duration: number;

    pii_findings: PIIFinding[];
    active_alert_count: number;
    schedule?: ScheduleDefinition;
    annotations?: MonitoringAnnotationItem[];
}

export interface MonitoringAnnotationItem {
    AnnotationID: string;
    Type: string;
    SubjectType: string;
    SubjectID: string;
    DerivedAt: string;
    Evidence: string;
    ResolvedAt: string | null;
}

export interface RunsListResponse {
    runs: RunListItem[];
    page: Page;
    freshness: Freshness;
    live: Live;
}

export interface RunDetailResponse {
    run: RunListItem;
    freshness: Freshness;
    live: Live;
    payload_size_bytes: number;
}

export function getRuns(params: string = ""): Promise<RunsListResponse> {
    return apiFetch(`/runs${params}`);
}

export function getRunDetail(runId: string): Promise<RunDetailResponse> {
    return apiFetch(`/runs/${runId}`);
}

// ---------------------------------------------------------------- Timelines

export interface TimelineEntry {
    occurred_at: string;
    event_type: string;
    run_id: string;
    source: "EVENT" | "ATTEMPT" | "PII" | "ALERT" | "ANNOTATION";
    detail?: Record<string, unknown>;
}

export interface TimelineResponse {
    chain_id: string;
    entries: TimelineEntry[];
    count: number;
    truncated: boolean;
}

export function getChainTimeline(chainId: string): Promise<TimelineResponse> {
    return apiFetch(`/execution-chains/${chainId}/timeline`);
}

// ---------------------------------------------------------------- Queues

export interface QueueHealth {
    ID: number;
    CreatedAt: string;
    UpdatedAt: string;
    DeletedAt: string | null;
    QueueName: string;
    StreamLength: number;
    PendingCount: number;
    OldestPendingAgeSeconds: number;
    ConsumerCount: number;
    SampledAt: string;
}
 
export interface QueuesListResponse {
    queues: QueueHealth[];
    freshness: Freshness;
    live: Live;
}
 
export interface QueueDetailResponse {
    current: QueueHealth;
    history: QueueHealth[];
    freshness: Freshness;
    live: Live;
}
 
export function getQueues(): Promise<QueuesListResponse> {
    return apiFetch(`/queues`);
}
 
export function getQueueDetail(queueName: string): Promise<QueueDetailResponse> {
    return apiFetch(`/queues/${encodeURIComponent(queueName)}`);
}

// ---------------------------------------------------------------- Workers

export interface WorkerListItem {
    worker_id: string;
    instance_id: string;
    hostname: string;
    started_at: string;
    configured_capacity: number;
    last_heartbeat: string;
    running_attempts: number;
    capacity: number;
    is_stale: boolean;
}

export interface WorkersListResponse {
    workers: WorkerListItem[];
    freshness: Freshness;
    live: Live;
}

// detail adds the extra fields from when you fixed GetWorkerDetail —
// active_attempts / recent_completions / recent_failures / alerts /
// active_alert_count. Confirm exact key casing on Attempt/Alert sub-objects
// too, since those come from different structs than WorkerListItem.
export interface WorkerDetailItem extends WorkerListItem {
    active_attempts: unknown[];
    recent_completions: unknown[];
    recent_failures: unknown[];
    alerts: Alert[];
    active_alert_count: number;
}

export interface WorkerDetailResponse {
    worker: WorkerDetailItem;
    freshness: Freshness;
    live: Live;
}

export function getWorkers(): Promise<WorkersListResponse> {
    return apiFetch(`/workers`);
}

export function getWorkerDetail(workerId: string): Promise<WorkerDetailResponse> {
    return apiFetch(`/workers/${workerId}`);
}

// ---------------------------------------------------------------- Schedules

export interface ScheduleDefinition {
    schedule_id: string;
    next_run_at: string;
    enabled: boolean;
    // there are likely more fields (name, cron expression) not confirmed —
    // check Debug JSON and add them
}

export interface ScheduleRunItem {
    run_id: string;
    expected_at: string;
    created_at: string;
    started_at: string | null;
    status: string;
    creation_drift_seconds: number;
    start_drift_seconds: number | null;
}

export interface SchedulesListResponse {
    schedules: ScheduleDefinition[];
    page: Page;
}

export interface ScheduleDetailResponse {
    schedule: ScheduleDefinition;
    recent_runs: ScheduleRunItem[];
    annotations: unknown[];
    next_expected_at: string | null;
}

export function getSchedules(params: string = ""): Promise<SchedulesListResponse> {
    return apiFetch(`/schedules${params}`);
}

export function getScheduleDetail(scheduleId: string): Promise<ScheduleDetailResponse> {
    return apiFetch(`/schedules/${scheduleId}`);
}

// ---------------------------------------------------------------- PII findings

export interface PIIFinding {
    type: string;
    detector_id: string;
    confidence: number;
    source: string;
    index: number;
    policy_action: string;
}

export interface PIIFindingsResponse {
    findings: PIIFinding[];   // confirm this key — it was "piis" before the
                                // §13 rename, double check it landed
    page: Page;
}

export function getPIIFindings(params: string = ""): Promise<PIIFindingsResponse> {
    return apiFetch(`/pii/findings${params}`);
}

// ---------------------------------------------------------------- Monitoring health

// DOESN'T EXIST YET on the backend — this is for when you build 8.7's
// GET /monitoring/health endpoint. Written now so the shape is ready.
export interface MonitoringHealth {
    status: string;   // "COMPLETE" | "DEGRADED" | "PARTIAL" | "UNKNOWN"
    failed_checks: number;
    sampled_at: string;
}

export interface MonitoringHealthResponse {
    health: MonitoringHealth;
    freshness: Freshness;
    live: Live;
}

// ---------------------------------------------------------------- Watermark/Page

export interface Page {
    limit: number;
    count: number;
    has_more: boolean;
    offset?: number;
    total?: number;
}

export interface Freshness {
    last_updated_at: string | null;
    lag_seconds: number;
}
 
export interface Live {
    watermark: number;
}

export function getMonitoringHealth(): Promise<MonitoringHealthResponse> {
    return apiFetch(`/monitoring/health`);
}

async function apiPost<T>(path: string): Promise<T> {
    const res = await fetch(BASE_URL + path, {
        method: "POST",
        headers: { "X-API-Key": API_KEY },
    });

    if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || `request failed: ${res.status}`);
    }

    return res.json();
}

async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
    const res = await fetch(BASE_URL + path, {
        ...options,
        headers: { "X-API-Key": API_KEY, ...options.headers },
    });
 
    if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new ApiError(body.error || `request failed: ${res.status}`, res.status);
    }
 
    return res.json();
}

export class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
        super(message);
        this.status = status;
    }
}