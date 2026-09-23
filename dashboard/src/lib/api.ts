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
    WorkerId: string;
    InstanceId: string;
    Hostname: string;
    StartedAt: string;
    ConfiguredCapacity: number;
    LastHeartbeat: string;
    RunningAttempts: number;
    Capacity: number;
}

export interface WorkerDetailItem extends WorkerListItem {
    active_attempts: unknown[];
    recent_completions: unknown[];
    recent_failures: unknown[];
    alerts: Alert[];
    active_alert_count: number;
}
 
export interface WorkersListResponse {
    workers: WorkerListItem[];
    freshness: Freshness;
    live: Live;
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
    return apiFetch(`/workers/${encodeURIComponent(workerId)}`);
}

// ---------------------------------------------------------------- Schedules

export interface ScheduleDefinition {
    ScheduleId: string;
    NextRunAt: string;
    Enabled: boolean;
}
 
// tagged/snake_case, per the Go struct given directly
export interface ScheduleRunItem {
    run_id: string;
    expected_at: string;
    created_at: string;
    started_at: string | null;
    status: string;
    creation_drift_seconds: number;
    start_drift_seconds: number | null;
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
 
export interface SchedulesListResponse {
    schedules: ScheduleDefinition[];
    page: Page;
    freshness: Freshness;
    live: Live;
}
 
export interface ScheduleDetailResponse {
    schedule: ScheduleDefinition;
    recent_runs: ScheduleRunItem[];
    annotations: MonitoringAnnotationItem[];
    next_expected_at: string | null;
    freshness: Freshness;
    live: Live;
}
 
export function getSchedules(params: string = ""): Promise<SchedulesListResponse> {
    return apiFetch(`/schedules${params}`);
}
 
export function getScheduleDetail(scheduleId: string): Promise<ScheduleDetailResponse> {
    return apiFetch(`/schedules/${encodeURIComponent(scheduleId)}`);
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

export interface PIIFindingItem {
    type: string;
    detector_id: string;
    confidence: number;
    source: string;
    index: number;
    policy_action: string;
 
    run_id: string;
    field_path?: string;
    rule_id: string;
    mask_strategy?: string;
    policy_name: string;
    policy_version: number;
    policy_checksum: string;
    detected_at: string;
}

export interface PIIListResponse {
    piis: PIIFindingItem[];   // confirmed "piis", not "findings"
    page: Page;
    freshness: Freshness;
    live: Live;
}
 
export function getPIIFindings(params: string = ""): Promise<PIIListResponse> {
    return apiFetch(`/pii/findings${params}`);
}
 

// ---------------------------------------------------------------- Monitoring health

// ============================================================================
// ADD TO api.ts
// ============================================================================

export interface SubsystemFreshness {
    subsystem: string;
    last_observed_at?: string;
    lag_seconds?: number;
    available: boolean;
    unavailable_reason?: string;
}

export interface MonitoringHealthResponse {
    sweep_status: string;
    sweep_failed_checks: number;
    sweep_sampled_at: string;

    subsystems: SubsystemFreshness[];

    active_policy_name: string;
    active_policy_version: number;
    active_policy_checksum: string;

    last_reload_result: string;
    last_reload_at: string;
    last_reload_failure_reason?: string;

    scanner_drift: string;
    known_gaps: string[];

    freshness: Freshness;
    live: Live;
}

export function getMonitoringHealth(): Promise<MonitoringHealthResponse> {
    return apiFetch(`/monitoring/health`);
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