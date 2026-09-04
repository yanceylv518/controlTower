import type { ApiClient } from "../client";

// API types intentionally retain snake_case so every field maps one-to-one to the frozen contract.
export interface MetricSummary {
  request_count: number;
  success_count: number;
  error_count: number;
  success_rate: number | null;
  error_rate: number | null;
  tpm: number;
  avg_use_time: number | null;
  p95_use_time: number | null;
}
export interface Overview {
  instance_count: number;
  recent_1m: MetricSummary;
  runtime: {
    latest_server_metrics: unknown[];
    health: { up_count: number; down_count: number; latest: unknown[] };
    docker: { running_count: number; stopped_count: number; latest: unknown[] };
  };
}
export interface MetricItem {
  instance_id: string;
  instance_name: string;
  bucket_time: string;
  dimension_type: string;
  dimension_key: string;
  display_key: string;
  display_name: string;
  request_count: number;
  success_count: number;
  error_count: number;
  success_rate: number | null;
  error_rate: number | null;
  tpm: number;
  prompt_tokens: number;
  completion_tokens: number;
  quota: number;
  avg_use_time: number | null;
  p95_use_time: number | null;
  p50_use_time?: number | null;
  p99_use_time?: number | null;
  stream_rate: number | null;
  cache_token_rate: number | null;
  big_input_count: number | null;
  big_input_cache_hits: number | null;
  cache_hit_rate: number | null;
  ttft_count: number | null;
  ttft_avg_ms: number | null;
  ttft_p50_ms: number | null;
  ttft_p90_ms: number | null;
  ttft_p95_ms: number | null;
  otps: number | null;
  otps_sample_tokens: number;
}
export interface AlertItem {
  id: string;
  instance_id: string;
  instance_name: string;
  display_key: string;
  dimension_type: string;
  dimension_key: string;
  rule_key: string;
  severity: string;
  status: string;
  title: string;
  summary: string;
  seen_at: string;
  first_seen_at: string;
  last_seen_at: string;
  resolved_at?: string;
  silence_until?: string;
}
export interface AlertEvent {
  id: number;
  alert_id: string;
  event_type: string;
  actor: string;
  note: string;
  created_at: string;
}
export interface DashboardOKResponse {
  ok: boolean;
}
export interface SystemSettingItem { value: string; source: "db" | "env" | "default"; default: string }
export interface SystemSettingsResponse { items: Record<string, SystemSettingItem> }
export interface BalanceAlertUserSetting { instance_id: string; user_id: number; enabled: boolean; updated_at: string; updated_by: string }
export interface AlertActionInput {
  id: string;
  action: string;
  note?: string;
  silence_minutes?: number;
}
export interface ListResponse<T> {
  items: T[];
}
export interface InstanceItem {
  instance_id: string;
  site_id: string;
  name: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  agents: Array<{
    id: string;
    version: string;
    last_seen_at: string;
    backlog_estimate: number;
    online: boolean;
  }>;
  logs_readonly_configured?: boolean;
  control_configured?: boolean;
  control_api_url?: string;
  control_admin_user_id?: number;
}
export interface ChannelSnapshot {
  id: string;
  instance_id: string;
  instance_name: string;
  channel_id: number;
  channel_name: string;
  status: string;
  weight: number;
  models_text: string;
  group_name: string | null;
  priority: number | null;
  captured_at: string;
}
export interface LogSample {
  instance_id: string;
  sample_kind: string;
  source_log_id: number;
  created_at: string;
  log_type: string;
  user_id: number;
  username: string;
  channel_id: number;
  model_name: string;
  token_id: number;
  token_name: string;
  total_tokens: number;
  quota: number;
  use_time: number;
  request_id: string;
  upstream_request_id: string;
  error_summary: string;
}
export interface AgentItem {
  id: string;
  instance_id: string;
  version: string;
  last_seen_at: string;
  last_sequence: number;
  last_log_id: number;
  source_latest_log_id: number;
  backlog_estimate: number;
  status: string;
  report_delay_ms: number;
  online: boolean;
  seconds_since_seen: number;
}
export interface ServerMetricItem {
  instance_id: string;
  collected_at: string;
  cpu_percent: number;
  memory_used_percent: number;
  disk_used_percent: number;
  network_rx_bytes_per_second: number;
  network_tx_bytes_per_second: number;
  load_1m: number;
}
export interface HealthCheckItem {
  instance_id: string;
  checked_at: string;
  target: string;
  status: string;
  http_status_code: number;
  latency_ms: number;
  error_summary: string;
}
export interface DockerStatusItem {
  instance_id: string;
  collected_at: string;
  container_name: string;
  status: string;
  running: boolean;
}
export interface UsageItem {
  dimension_type: string;
  dimension_key: string;
  display_key: string;
  display_name: string;
  request_count: number;
  total_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  quota: number;
}
export interface NotificationChannelItem {
  id: string;
  channel_type: string;
  name: string;
  webhook_url_masked: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
  has_secret: boolean;
}
export interface NotificationChannelInput {
  id: string;
  channel_type: "webhook" | "dingtalk" | "wecom";
  name: string;
  webhook_url: string;
  enabled: boolean;
  secret?: string;
}
export interface NotificationDeliveryItem {
  id: string;
  alert_id: string;
  channel_id: string;
  status: string;
  attempted_at: string;
  next_attempt_at: string;
  attempts: number;
  status_code: number;
  error_summary: string;
}
export interface InstanceCreateResponse {
  instance_id: string;
  site_id: string;
  name: string;
  token: string;
}
export interface InstanceUpdateInput {
  site_id?: string;
  name?: string;
  enabled?: boolean;
  control_api_url?: string;
  control_api_token?: string;
  control_admin_user_id?: number;
}
export interface InstanceTokenResponse {
  token: string;
  grace_until?: string;
}
export interface ChannelCommandInput {
  instance_id: string;
  confirm: true;
  status?: number;
  weight?: number;
  priority?: number;
}
export interface ChannelCommandItem {
  id: string;
  instance_id: string;
  instance_name: string;
  channel_id: number;
  status: string;
  payload: Record<string, number>;
  created_by: string;
  error_summary?: string;
  created_at: string;
}
export interface OperationAuditItem {
  instance_id: string;
  instance_name: string;
  operation_type: string;
  target_type: string;
  target_id: string;
  actor_id: string;
  after_summary: string;
  created_at: string;
}
export interface NginxTimingBucket {
  bucket_at: string;
  request_count: number;
  upstream_count: number;
  status_4xx: number;
  status_5xx: number;
  status_504: number;
  rt_p50: number;
  rt_p95: number;
  rt_max: number;
  uht_p50: number;
  uht_p95: number;
  uht_max: number;
  transfer_p50: number;
  transfer_p95: number;
  transfer_max: number;
  bytes_total: number;
  slow_count: number;
  slow_ttft_count: number;
  slow_transfer_count: number;
}
export interface NginxTimingSummary {
  total_requests: number;
  status_5xx: number;
  status_504: number;
  slow_count: number;
  slow_ttft_count: number;
  slow_transfer_count: number;
  slow_ttft_percent: number;
  slow_transfer_percent: number;
}
export interface NginxTimingResponse {
  items: NginxTimingBucket[];
  summary: NginxTimingSummary;
}
export interface NginxSlowSample {
  id: number;
  occurred_at: string;
  path: string;
  status: number;
  rt: number;
  uht: number;
  urt: number;
  bytes: number;
  request_id: string;
  match_status: "matched" | "unmatched" | "multiple";
  match_count: number;
  user_id: number;
  user_name: string;
  channel_id: number;
  channel_name: string;
  model_name: string;
  token_name: string;
}

export interface TuningSchedulingParams {
  window_minutes: number; min_samples: number;
  sparse_min_samples: number; sparse_lookback_minutes: number;
}
export interface TuningContinuousDispatchParams {
  sensitivity: number;
  speed_exponent: number;
  speed_p50_weight: number;
  speed_p90_weight: number;
  speed_p95_weight: number;
  speed_min_factor: number;
  speed_max_factor: number;
  cache_exponent: number;
  cache_min_factor: number;
  cache_max_factor: number;
  otps_exponent: number;
  otps_min_factor: number;
  otps_max_factor: number;
  error_healthy_rate: number;
  error_degraded_rate: number;
  error_poor_rate: number;
  error_floor_rate: number;
  error_degraded_factor: number;
  error_poor_factor: number;
  error_min_factor: number;
  combined_min_factor: number;
  combined_max_factor: number;
  circuit_threshold: number;
  recovery_threshold: number;
  circuit_error_rate: number;
  recovery_error_rate: number;
  silent_minutes: number;
  probe_interval_seconds: number;
  probe_count: number;
  soft_start_multiplier: number;
  window_minutes: number;
  min_samples: number;
  sparse_lookback_minutes: number;
  fast_circuit_enabled: boolean;
  fast_circuit_min_samples: number;
  fast_circuit_error_rate: number;
}
export interface TuningPolicy {
  scheduling: TuningSchedulingParams;
  continuous: TuningContinuousDispatchParams;
  dispatch_modes: Record<string, "off" | "observe" | "auto">;
}
export interface ChannelBaseValue {
  instance_id: string; channel_id: number; channel_name: string; model_name: string;
  group_name: string;
  base_weight: number; base_priority: number; current_weight: number; current_priority: number;
  max_rpm: number; max_tpm: number;
  snapshot_at?: string; models?: string[]; updated_at?: string; updated_by?: string;
}
export interface TuningPolicyResponse {
  instance_id: string; site_id: string; policy: TuningPolicy; mode: "observe" | "confirm" | "auto";
  isDefault?: boolean; updated_at?: string; updated_by?: string;
}
export interface TuningRecommendation {
  id: string; instance_id: string; channel_id: number; channel_name: string;
  created_at: string; rule: string; evidence: Record<string, unknown>;
  current_weight: number; proposed_weight: number;
  current_priority?: number; proposed_priority?: number; mode_at_creation: string;
  status: string; command_id?: string; outcome?: Record<string, unknown>;
  outcome_at?: string; hit?: boolean; acted_by?: string; acted_at?: string;
}
export interface TuningReport {
  total: number; by_rule: Record<string, number>;
}
export interface TuningContinuousState {
  instance_id: string;
  channel_id: number;
  channel_name: string;
  model_name: string;
  base_weight: number;
  base_priority: number;
  k_speed: number;
  k_cache: number;
  k_otps: number;
  k_error: number;
  multiplier: number;
  proposed_weight: number;
  last_written_weight?: number;
  last_write_at?: string;
  last_observed_requests: number;
  last_observed_errors: number;
  metric_rpm: number;
  metric_tpm: number;
  capacity_limited: boolean;
  metric_ready: boolean;
  baseline_ready: boolean;
  metric_ttft_p50: number;
  metric_ttft_p90: number;
  metric_ttft_p95: number;
  baseline_ttft_p50: number;
  baseline_ttft_p90: number;
  baseline_ttft_p95: number;
  metric_cache: number;
  baseline_cache: number;
  cache_ready: boolean;
  metric_otps: number;
  baseline_otps: number;
  otps_ready: boolean;
  smoothed_error_rate: number;
  paused_reason?: string;
  phase: "normal" | "circuit" | "probing" | "soft_start";
  circuit_opened_at?: string;
  next_probe_at?: string;
  probe_command_id?: string;
  probe_attempts: number;
  probe_successes: number;
  probe_duration_sum: number;
  original_priority?: number;
  soft_start_pending: boolean;
  write_failure_streak?: number;
  last_write_failure_at?: string;
  last_write_error?: string;
  updated_at: string;
}

const query = (
  values: Record<string, string | number | boolean | undefined>,
) => {
  const params = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") params.set(key, String(value));
  });
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
};

export interface BillingUserSummary {
  user_id: number;
  username: string;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_tokens: number;
  cache_write_tokens: number;
  abnormal_rows: number;
  mismatch_rows?: number;
  abnormal_amount: string;
  quota: number;
  amount: string;
  balance: number;
  unpriced_models: string[];
  price_sources: string[];
}
export interface BillingSummaryResponse {
  items: BillingUserSummary[];
  total: number;
  page: number;
  page_size: number;
  summary: BillingUserSummary & { users: number };
  data_through: string;
  data_from?: string;
  data_to?: string;
  generation_job?: BillingJob | null;
  currency?: BillingCurrencyDisplay;
}
export interface BillingCurrencyDisplay { type: string; symbol: string; exchange_rate: string }
export interface BillingDetailItem {
  day: string;
  model_name: string;
  group_name: string;
  tier_from: number;
  request_count: number;
  prompt_tokens: number;
  completion_tokens: number;
  cache_tokens: number;
  cache_write_tokens: number;
  cache_write_5m_tokens: number;
  cache_write_1h_tokens: number;
  abnormal_rows: number;
  abnormal_amount: string;
  quota: number;
  amount: string;
  input_price: string;
  output_price: string;
  cache_price: string;
  cache_write_price: string;
  price_source: "ct" | "newapi" | "";
  unpriced: boolean;
}
export interface BillingDetailResponse {
  items: BillingDetailItem[];
  user_id: number;
  month: string;
  data_through: string;
}
export interface BillingPriceItem {
  instance_id: string;
  model_name: string;
  effective_from: string;
  tier_from: number;
  input_price: string;
  output_price: string;
  cache_price: string;
  cache_write_price: string;
}
export interface BillingGroupRatioItem {
  instance_id: string;
  group_name: string;
  ratio: string;
}
export interface BillingModelItem {
  model_name: string;
  max_context_tokens: number;
  input_price: string;
  output_price: string;
  cache_price: string;
  cache_write_price: string;
  cache_write_price_configured: boolean;
  effective_from: string;
  price_source: "ct" | "newapi" | "";
}
export interface BillingJob {
  id: string;
  bill_no?: string;
  instance_id: string;
  job_type?: string;
  user_id?: number;
  user_name?: string;
  exclude_zero_output?: boolean;
  upstream_id?: number;
  upstream_name?: string;
  range_from?: string;
  range_to?: string;
  updated_at?: string;
  status: "pending" | "running" | "publishing" | "complete" | "failed";
  total_steps: number;
  completed_steps: number;
  abnormal_rows: number;
  billed_rows?: number;
  output_days?: number;
  output_latest_day?: string;
  error_message?: string;
  requested_by?: string;
  created_at?: string;
}
export interface BillingJobStep { job_id:string;step_no:number;range_from:string;range_to:string;status:"pending"|"running"|"complete"|"failed";processed_rows:number;abnormal_rows:number;attempts:number;error_message?:string }
export interface BillingDailyOverview {
  instance_id: string;
  day: string;
  user_count: number;
  request_count: number;
  anomaly_rows: number;
  file_count: number;
  amount: string;
  activated_at: string;
}
export interface BillingUserBillDay { instance_id:string;job_id:string;day:string;user_id:number;username:string;model_name:string;request_count:number;prompt_tokens:number;completion_tokens:number;cache_read_tokens:number;cache_write_tokens:number;anomaly_rows:number;anomaly_amount:string;amount:string;activated_at:string }
export interface BillingUserTokenBillDay { instance_id:string;job_id:string;day:string;user_id:number;username:string;token_id:number;token_name:string;model_name:string;request_count:number;prompt_tokens:number;completion_tokens:number;cache_read_tokens:number;cache_write_tokens:number;anomaly_rows:number;anomaly_amount:string;amount:string;activated_at:string }
export interface BillingUserSetting {
  instance_id: string;
  user_id: number;
  use_tiered_pricing: boolean;
}
export interface BillingChannelSummary { channel_id:number;channel_name:string;request_count:number;abnormal_rows:number;abnormal_amount?:string;prompt_tokens:number;completion_tokens:number;cache_tokens:number;cache_write_tokens?:number;quota:number;amount:string;discount:string;discounted_amount:string;unpriced_models:string[] }
export interface BillingCoverage { expected_days:number;available_days:number;missing_days:string[];status:"complete"|"partial"|"missing"|"unknown" }
export interface BillingUpstreamTotals { request_count:number;prompt_tokens:number;completion_tokens:number;cache_tokens:number;cache_write_tokens:number;quota:number;amount:string;abnormal_rows:number;abnormal_amount:string }
export interface BillingUpstreamMember { channel_id:number;channel_name:string;model_name:string;historical:boolean;bill_days:string[];totals:BillingUpstreamTotals }
export interface BillingUpstreamGroup { upstream_fp:string;display_name:string;base_url:string;member_count:number;members:BillingUpstreamMember[];totals:BillingUpstreamTotals;bill_days:string[] }
export interface BillingUpstreamDetail { day:string;model_name:string;group_name:string;tier_from:number;request_count:number;prompt_tokens:number;completion_tokens:number;cache_tokens:number;cache_write_tokens:number;quota:number;amount:string;unpriced:boolean;abnormal_rows:number;abnormal_amount:string }
export interface BillingChannelRequestDetail { created_at:string;request_id:string;username:string;token_name:string;model_name:string;prompt_tokens:number;completion_tokens:number;cache_read_tokens:number;cache_write_tokens:number;input_price:string;output_price:string;cache_read_price:string;cache_write_price:string;amount:string;abnormal:boolean;reasons:string }
export interface BillingUpstreamChannel { channel_id:number;channel_name:string }
export interface BillingReadonlyChannel { channel_id:number;channel_name:string;status:number;models:string }
export interface BillingUpstream { id:number;instance_id:string;name:string;enabled:boolean;remark:string;channels:BillingUpstreamChannel[];created_at?:string;updated_at?:string;updated_by?:string }
export interface BillingDiscountRule { id:number;instance_id:string;discount_type:"upstream_channel";subject_id:number;subject_name?:string;channel_id:number;channel_name?:string;model_name:string;discount:string;effective_from:string;effective_to?:string;remark:string;created_at?:string;updated_at?:string;updated_by?:string }
export interface BillingTokenSummary { token_id:number;token_name:string;request_count:number;abnormal_rows:number;abnormal_amount:string;prompt_tokens:number;completion_tokens:number;cache_tokens:number;cache_write_tokens:number;quota:number;billing_amount:string }
export interface BillingReconciliationBreakdown { anomaly: string; cache_write_policy: string; residual: string }
export interface BillingReconciliationRow {
  user_id: number;
  username: string;
  day?: string;
  model_name?: string;
  group_name?: string;
  request_count: number;
  abnormal_rows: number;
  billing_amount: string;
  actual_amount: string;
  diff_amount: string;
  diff_rate: string;
  fallback_priced: boolean;
  classification: "anomaly" | "cache_write_policy" | "residual";
  breakdown: BillingReconciliationBreakdown;
}
export interface BillingReconciliationResponse {
  items: BillingReconciliationRow[];
  totals: { billing_amount: string; actual_amount: string; diff_amount: string; breakdown: BillingReconciliationBreakdown };
  job: BillingJob;
  range_from: string;
  range_to: string;
  currency?: BillingCurrencyDisplay;
}
export interface BillingRequestReconciliation {
  log_id: number;
  request_id: string;
  created_at: string;
  actual_amount: string;
  rebuilt_amount: string;
  billing_amount: string;
  diff_amount: string;
  input_diff: string;
  output_diff: string;
  cache_read_diff: string;
  cache_write_diff: string;
  group_diff: string;
  unexplained: boolean;
  fallback_priced: boolean;
}
export interface BillingRequestReconciliationResponse {
  items: BillingRequestReconciliation[];
  scanned: number;
  matched: number;
  truncated: boolean;
  rebuild_residual: string;
  component_diffs: { input: string; output: string; cache_read: string; cache_write: string; group: string };
  uninformative: boolean;
}
export interface BillingVerificationResult {
  day: string;
  user_id: number;
  username: string;
  model_name: string;
  group_name: string;
  source_rows: number;
  verified_normal_rows: number;
  billed_normal_rows: number;
  verified_abnormal_rows: number;
  billed_abnormal_rows: number;
  source_quota: number;
  verified_normal_quota: number;
  billed_normal_quota: number;
  verified_abnormal_quota: number;
  billed_abnormal_quota: number;
  status: "matched" | "mismatch";
}
export interface BillingVerificationSummary {
  source_rows: number;
  verified_normal_rows: number;
  billed_normal_rows: number;
  verified_abnormal_rows: number;
  billed_abnormal_rows: number;
  source_quota: number;
  verified_normal_quota: number;
  billed_normal_quota: number;
  verified_abnormal_quota: number;
  billed_abnormal_quota: number;
  matched_rows: number;
  mismatched_rows: number;
}
export interface BillingVerificationResponse {
  job?: BillingJob | null;
  items: BillingVerificationResult[];
  summary: BillingVerificationSummary;
  total: number;
  page: number;
  page_size: number;
}

export const dashboardApi = (client: ApiClient) => ({
  instances: () =>
    client.request<ListResponse<InstanceItem>>("/api/dashboard/instances"),
  overview: (instance_id?: string, site?: string) =>
    client.request<Overview>(
      `/api/dashboard/overview${query({ instance_id, site })}`,
    ),
  metrics: (
    params: {
      instance_id?: string;
      site?: string;
      window?: string;
      latest?: boolean;
      dimension_type?: string;
      dimension_key?: string;
    } = {},
  ) =>
    client.request<ListResponse<MetricItem>>(
      `/api/dashboard/metrics${query(params)}`,
    ),
  metricHistory: (params: {
    instance_id?: string;
    site?: string;
    window: string;
    dimension_type: string;
    dimension_key?: string;
    dimension_key_prefix?: string;
    hours: number;
    aggregate?: boolean;
  }) =>
    client.request<ListResponse<MetricItem>>(
      `/api/dashboard/metric-history${query(params)}`,
    ),
  alerts: (
    params: {
      instance_id?: string;
      status?: string;
      severity?: string;
      active_only?: boolean;
      limit?: number;
      offset?: number;
    } = {},
  ) =>
    client.request<ListResponse<AlertItem>>(
      `/api/dashboard/alerts${query(params)}`,
    ),
  alertEvents: (id: string, limit = 100) =>
    client.request<ListResponse<AlertEvent>>(
      `/api/dashboard/alerts/${encodeURIComponent(id)}/events${query({ limit })}`,
    ),
  cleanupAlerts: (statuses: string[]) =>
    client.request<{ deleted: number }>("/api/dashboard/alerts/cleanup", {
      method: "POST",
      body: JSON.stringify({ statuses }),
    }),
  alertAction: (input: AlertActionInput) =>
    client.request<DashboardOKResponse>("/api/dashboard/alerts/action", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  channelSnapshots: (
    params: {
      instance_id?: string;
      latest_only?: boolean;
      limit?: number;
    } = {},
  ) =>
    client.request<ListResponse<ChannelSnapshot>>(
      `/api/dashboard/channel-snapshots${query(params)}`,
    ),
  logSamples: (
    params: {
      instance_id?: string;
      site?: string;
      sample_kind?: string;
      model_name?: string;
      user_id?: string;
      request_id?: string;
      limit?: number;
      offset?: number;
    } = {},
  ) =>
    client.request<ListResponse<LogSample>>(
      `/api/dashboard/log-samples${query(params)}`,
    ),
  agents: (params: { instance_id?: string; limit?: number } = {}) =>
    client.request<ListResponse<AgentItem>>(
      `/api/dashboard/agents${query(params)}`,
    ),
  serverMetrics: (
    params: {
      instance_id?: string;
      start_time?: string;
      end_time?: string;
      limit?: number;
    } = {},
  ) =>
    client.request<ListResponse<ServerMetricItem>>(
      `/api/dashboard/server-metrics${query(params)}`,
    ),
  healthChecks: (params: { instance_id?: string; limit?: number } = {}) =>
    client.request<ListResponse<HealthCheckItem>>(
      `/api/dashboard/health-checks${query(params)}`,
    ),
  dockerStatuses: (params: { instance_id?: string; limit?: number } = {}) =>
    client.request<ListResponse<DockerStatusItem>>(
      `/api/dashboard/docker-statuses${query(params)}`,
    ),
  usage: (hours: number, instance_id?: string) =>
    client.request<ListResponse<UsageItem>>(
      `/api/dashboard/usage${query({ hours, instance_id })}`,
    ),
  notificationChannels: () =>
    client.request<ListResponse<NotificationChannelItem>>(
      "/api/dashboard/notification-channels",
    ),
  saveNotificationChannel: (input: NotificationChannelInput) =>
    client.request<ListResponse<NotificationChannelItem>>(
      "/api/dashboard/notification-channels",
      { method: "POST", body: JSON.stringify(input) },
    ),
  notificationDeliveries: (
    params: {
      alert_id?: string;
      channel_id?: string;
      status?: string;
      limit?: number;
      offset?: number;
    } = {},
  ) =>
    client.request<ListResponse<NotificationDeliveryItem>>(
      `/api/dashboard/notification-deliveries${query(params)}`,
    ),
  resendDelivery: (id: string) =>
    client.request<DashboardOKResponse>(
      `/api/dashboard/notification-deliveries/${encodeURIComponent(id)}/resend`,
      { method: "POST" },
    ),
  createInstance: (input: { instance_id: string; site_id?: string; name: string }) =>
    client.request<InstanceCreateResponse>("/api/dashboard/instances", {
      method: "POST",
      body: JSON.stringify(input),
    }),
  updateInstance: (id: string, input: InstanceUpdateInput) =>
    client.request<InstanceItem>(
      `/api/dashboard/instances/${encodeURIComponent(id)}`,
      { method: "PUT", body: JSON.stringify(input) },
    ),
  rotateInstanceToken: (id: string) =>
    client.request<InstanceTokenResponse>(
      `/api/dashboard/instances/${encodeURIComponent(id)}/rotate-token`,
      { method: "POST" },
    ),
  createChannelCommand: (channelID: number, input: ChannelCommandInput) =>
    client.request<ChannelCommandItem>(
      `/api/dashboard/channels/${channelID}/commands`,
      { method: "POST", body: JSON.stringify(input) },
    ),
  channelCommands: (
    params: {
      instance_id?: string;
      status?: string;
      limit?: number;
      offset?: number;
    } = {},
  ) =>
    client.request<ListResponse<ChannelCommandItem>>(
      `/api/dashboard/channel-commands${query(params)}`,
    ),
  operationAudits: (
    params: { instance_id?: string; limit?: number; offset?: number } = {},
  ) =>
    client.request<ListResponse<OperationAuditItem>>(
      `/api/dashboard/operation-audits${query(params)}`,
    ),
  settings: () => client.request<SystemSettingsResponse>("/api/dashboard/settings"),
  saveSettings: (values: Record<string, string>) => client.request<SystemSettingsResponse>("/api/dashboard/settings", { method: "PUT", body: JSON.stringify({ values }) }),
  balanceAlertUsers: (instance_id: string) => client.request<ListResponse<BalanceAlertUserSetting>>(`/api/dashboard/balance-alert-users${query({ instance_id })}`),
  saveBalanceAlertUser: (instance_id: string, user_id: number, enabled: boolean) => client.request<BalanceAlertUserSetting>(`/api/dashboard/balance-alert-users${query({ instance_id })}`, { method: "PUT", body: JSON.stringify({ user_id, enabled }) }),
  nginxTiming: (params: { instance_id: string; hours: number }) =>
    client.request<NginxTimingResponse>(
      `/api/dashboard/nginx-timing${query(params)}`,
    ),
  nginxSlowSamples: (params: {
    instance_id: string;
    hours: number;
    limit?: number;
    offset?: number;
    user_id?: string;
    channel_id?: string;
    model_name?: string;
    match_status?: string;
   request_id?: string }) =>
    client.request<ListResponse<NginxSlowSample>>(
      `/api/dashboard/nginx-timing/slow-samples${query(params)}`,
    ),
  tuningPolicy: (site_id: string) =>
    client.request<TuningPolicyResponse>(`/api/dashboard/tuning/policy${query({ site_id })}`),
  saveTuningPolicy: (site_id: string, policy: TuningPolicy, mode: "observe" | "confirm" | "auto", preflight_command_id?: string) =>
    client.request<TuningPolicyResponse>(`/api/dashboard/tuning/policy${query({ site_id })}`, { method: "PUT", body: JSON.stringify({ policy, mode, preflight_command_id }) }),
  startTuningPreflight: (site_id: string, channel_id: number) =>
    client.request<{ command_id: string; status: string; error?: string }>(`/api/dashboard/tuning/preflight${query({ site_id })}`, { method: "POST", body: JSON.stringify({ channel_id }) }),
  tuningPreflight: (site_id: string, command_id: string) =>
    client.request<{ command_id: string; status: string; error?: string }>(`/api/dashboard/tuning/preflight${query({ site_id, command_id })}`),
  tuningRecommendations: (site_id: string, limit = 100, rule?: string) =>
    client.request<ListResponse<TuningRecommendation>>(`/api/dashboard/tuning/recommendations${query({ site_id, limit, rule })}`),
  tuningReport: (site_id: string, days: 7 | 30) =>
    client.request<TuningReport>(`/api/dashboard/tuning/report${query({ site_id, days })}`),
  tuningBaseValues: (site_id: string, model?: string) =>
    client.request<ListResponse<ChannelBaseValue>>(`/api/dashboard/tuning/base-values${query({ site_id, model })}`),
  tuningContinuousStates: (site_id: string) =>
    client.request<ListResponse<TuningContinuousState>>(`/api/dashboard/tuning/continuous-states${query({ site_id })}`),
  tuningCurrentRates: (site_id: string) =>
    client.request<{ items: { channel_id: number; rpm: number; tpm: number }[]; as_of: string; window_seconds: number }>(`/api/dashboard/tuning/continuous-states${query({ site_id, rates_only: 1 })}`),
  saveTuningBaseValues: (site_id: string, items: ChannelBaseValue[]) =>
    client.request<ListResponse<ChannelBaseValue>>(`/api/dashboard/tuning/base-values${query({ site_id })}`, { method: "PUT", body: JSON.stringify({ items }) }),
  syncTuningBaseValues: (site_id: string, models: string[]) =>
    client.request<ListResponse<ChannelBaseValue>>(`/api/dashboard/tuning/base-values/sync${query({ site_id })}`, { method: "POST", body: JSON.stringify({ models }) }),
  billingSummary: (params: { instance_id: string; month?: string; from?: string; to?: string; job_id?: string; covered?: 1; page?: number; page_size?: number; search?: string }) =>
    client.request<BillingSummaryResponse>(`/api/dashboard/billing/summary${query(params)}`),
  billingReconciliation: (params: { instance_id: string; from: string; to: string; job_id?: string; user_id?: number }) =>
    client.request<BillingReconciliationResponse>(`/api/dashboard/billing/reconciliation${query(params)}`),
  billingReconciliationRequests: (params: { instance_id: string; from: string; to: string; job_id: string; user_id: number; day: string; model_name: string }) =>
    client.request<BillingRequestReconciliationResponse>(`/api/dashboard/billing/reconciliation/requests${query(params)}`),
  billingVerification: (params: { source_job_id: string; job_id?: string; page?: number; page_size?: number; mismatches_only?: boolean }) =>
    client.request<BillingVerificationResponse>(`/api/dashboard/billing/verification${query(params)}`),
  startBillingVerification: (source_job_id: string) =>
    client.request<{ accepted: boolean; reused: boolean; job: BillingJob }>("/api/dashboard/billing/verification", { method: "POST", body: JSON.stringify({ source_job_id }) }),
  billingDetail: (params: { instance_id: string; user_id: number; month?: string; from?: string; to?: string; job_id?: string }) =>
    client.request<BillingDetailResponse>(`/api/dashboard/billing/detail${query(params)}`),
  generateBilling: (input: { instance_id: string; from: string; to: string; force?: boolean; scope?: "all" | "channel" | "user" | "upstream"; user_id?: number; upstream_id?: number }) =>
    client.request<{ accepted: boolean; reused: boolean; job: BillingJob }>("/api/dashboard/billing/backfill", { method: "POST", body: JSON.stringify(input) }),
  createBillingStatement: (input:{instance_id:string;statement_type:"user"|"upstream";user_id?:number;upstream_id?:number;from:string;to:string;exclude_zero_output?:boolean}) =>
    client.request<{accepted:boolean;job:BillingJob}>("/api/dashboard/billing/statements",{method:"POST",body:JSON.stringify(input)}),
  billingStatementResult: (id:string) => client.request<{job:BillingJob;total_orders:number;normal_orders:number;billable_orders:number;anomaly_total:number;reconciliation_total:number;review_required:boolean;count_balanced:boolean;model_summary:Record<string,unknown>[];daily_summary:Record<string,unknown>[];token_summary:Record<string,unknown>[];anomalies:Record<string,unknown>[];reconciliation:Record<string,unknown>[]}>(`/api/dashboard/billing/statements/result${query({id})}`),
  deleteBillingStatement: (id:string) => client.request<{deleted:boolean;id:string}>(`/api/dashboard/billing/statements/result${query({id})}`,{method:"DELETE"}),
  billingJob: (id: string) => client.request<BillingJob>(`/api/dashboard/billing/jobs${query({ id })}`),
  billingJobSteps: (id: string) => client.request<{ items: BillingJobStep[] }>(`/api/dashboard/billing/jobs/steps${query({ id })}`),
  cancelBillingJob: (id: string) => client.request<BillingJob>(`/api/dashboard/billing/jobs${query({ id })}`, { method: "DELETE" }),
  deleteFailedBillingJob: (id: string) => client.request<{ deleted: boolean; id: string }>(`/api/dashboard/billing/jobs${query({ id })}`, { method: "DELETE" }),
  billingJobs: (params: { instance_id?: string; status?: BillingJob["status"]; limit?: number } = {}) =>
    client.request<{ items: BillingJob[] }>(`/api/dashboard/billing/jobs${query(params)}`),
  billingOverview: (params: { instance_id?: string; month?: string } = {}) =>
    client.request<{ items: BillingDailyOverview[]; from: string; to: string }>(`/api/dashboard/billing/overview${query(params)}`),
  billingUserDays: (params: { instance_id: string; from?: string; through?: string; user_id?: number; search?: string }) =>
    client.request<{ items: BillingUserBillDay[]; period: string; from: string; through: string; coverage: BillingCoverage }>(`/api/dashboard/billing/user-days${query(params)}`),
  billingUserTokenDays: (params:{instance_id:string;user_id:number;from:string;through:string}) => client.request<{items:BillingUserTokenBillDay[]}>(`/api/dashboard/billing/user-token-days${query(params)}`),
  billingUserSettings: (instance_id: string) => client.request<{ items: Record<string, BillingUserSetting> }>(`/api/dashboard/billing/user-settings${query({ instance_id })}`),
  saveBillingUserSetting: (input: BillingUserSetting) => client.request<BillingUserSetting>("/api/dashboard/billing/user-settings", { method: "PUT", body: JSON.stringify(input) }),
  billingChannels:(params:{instance_id:string;from?:string;through?:string;to?:string;month?:string;channel_id?:number;job_id?:string})=>client.request<{items:BillingChannelSummary[];details:BillingDetailItem[];period:string;warning?:string;currency?:BillingCurrencyDisplay;coverage?:BillingCoverage}>(`/api/dashboard/billing/channels${query(params)}`),
  billingUpstreamChannels:(params:{instance_id:string;from:string;to?:string;through?:string;job_id?:string})=>client.request<{items:BillingUpstreamGroup[];coverage?:BillingCoverage;configured_upstreams:number;unmapped_channels:number;unmapped_current_channel_ids:number[];historical_channel_ids:number[]}>(`/api/dashboard/billing/upstream-channels${query(params)}`),
  billingUpstreamDetail:(params:{instance_id:string;fp:string;from:string;to?:string;through?:string;channel_id?:number;job_id?:string})=>client.request<{group:BillingUpstreamGroup;details:BillingUpstreamDetail[]}>(`/api/dashboard/billing/upstream-channels/detail${query(params)}`),
  billingChannelRequestDetails:(params:{instance_id:string;fp:string;channel_id:number;from:string;through:string})=>client.request<{items:BillingChannelRequestDetail[]}>(`/api/dashboard/billing/upstream-channels/requests${query(params)}`),
  billingUpstreams:(instance_id:string)=>client.request<{items:BillingUpstream[];channels:BillingReadonlyChannel[]}>(`/api/dashboard/billing/upstreams${query({instance_id})}`),
  saveBillingUpstream:(input:BillingUpstream)=>client.request<BillingUpstream>("/api/dashboard/billing/upstreams",{method:input.id?"PUT":"POST",body:JSON.stringify(input)}),
  deleteBillingUpstream:(instance_id:string,id:number)=>client.request<{deleted:boolean}>(`/api/dashboard/billing/upstreams${query({instance_id,id})}`,{method:"DELETE"}),
  billingDiscounts:(instance_id:string,type?:BillingDiscountRule["discount_type"])=>client.request<{items:BillingDiscountRule[]}>(`/api/dashboard/billing/discounts${query({instance_id,type})}`),
  saveBillingDiscount:(input:BillingDiscountRule)=>client.request<BillingDiscountRule>("/api/dashboard/billing/discounts",{method:input.id?"PUT":"POST",body:JSON.stringify(input)}),
  deleteBillingDiscount:(instance_id:string,id:number)=>client.request<{deleted:boolean}>(`/api/dashboard/billing/discounts${query({instance_id,id})}`,{method:"DELETE"}),
  billingTokens:(params:{instance_id:string;user_id:number;from:string;to:string;job_id?:string})=>client.request<{items:BillingTokenSummary[];token_data_missing:boolean;job:BillingJob}>(`/api/dashboard/billing/tokens${query(params)}`),
  billingTokenDaily:(params:{instance_id:string;user_id:number;token_id:number;from:string;to:string;job_id?:string})=>client.request<{items:BillingDetailItem[];token_id:number;job:BillingJob}>(`/api/dashboard/billing/tokens/daily${query(params)}`),
  saveBillingChannelDiscount:(input:{instance_id:string;channel_id:number;discount:string})=>client.request("/api/dashboard/billing/channels",{method:"PUT",body:JSON.stringify(input)}),
  billingPrices: (instance_id: string) =>
    client.request<ListResponse<BillingPriceItem>>(`/api/dashboard/billing/prices${query({ instance_id })}`),
  billingModels: (instance_id: string) =>
    client.request<{ items: BillingModelItem[]; warning?: string }>(`/api/dashboard/billing/models${query({ instance_id })}`),
  syncBillingModels: (instance_id: string) =>
    client.request<{ models: number; prices_changed: number }>(`/api/dashboard/billing/models${query({ instance_id })}`, { method: "POST" }),
  saveBillingModel: (input: { instance_id: string; model_name: string; max_context_tokens: number }) =>
    client.request(`/api/dashboard/billing/models`, { method: "PUT", body: JSON.stringify(input) }),
  billingGroupRatios: (instance_id: string) =>
    client.request<ListResponse<BillingGroupRatioItem>>(`/api/dashboard/billing/group-ratios${query({ instance_id })}`),
  importBillingPrices: (instance_id: string) =>
    client.request<{ items: BillingPriceItem[]; quota_per_unit: string }>(`/api/dashboard/billing/import-prices${query({ instance_id })}`),
  saveBillingPrice: (input: { instance_id: string; model_name: string; effective_from: string; tiers: Array<{ tier_from: number; input_price: string; output_price: string; cache_price: string; cache_write_price: string }> }) =>
    client.request<ListResponse<BillingPriceItem>>("/api/dashboard/billing/prices", { method: "PUT", body: JSON.stringify(input) }),
  saveBillingGroupRatio: (input: { instance_id: string; group_name: string; ratio: string }) =>
    client.request<BillingGroupRatioItem>("/api/dashboard/billing/group-ratios", { method: "PUT", body: JSON.stringify(input) }),
});
