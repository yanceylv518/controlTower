export type SettlementReportTask = {waiting_for?:string;id:string;instance_id:string;from:string;to:string;status:string;overwrite:boolean;automatic:boolean;created_at:string;updated_at:string;days:{day:string;status:string;processed:number;error:string;updated_at:string}[]};
import type { ApiClient } from "../client";

export interface ChannelGroupPreset { id: string; name: string; groups: string[] }
export interface ChannelGroupPresets { items: ChannelGroupPreset[]; revision: number }

export interface ChannelScheduleConfig {
  enabled: boolean;
  days: number[];
  windows: { start: string; end: string }[];
  revision: number;
  align_now: boolean;
  expected_updated_at?: string;
}
export interface ChannelScheduleExecution {
  id: string;
  site_id: string;
  channel_id: number;
  revision: number;
  boundary: string;
  target: number;
  status: string;
  attempts: number;
  error: string;
  updated_at: string;
  expires_at?: string;
  before_status: number;
}
export interface ChannelSchedule extends ChannelScheduleConfig {
  site_id: string;
  channel_id: number;
  effective_at: string;
  updated_at: string;
  updated_by: string;
  closed_by_schedule: boolean;
  allowed: boolean;
  next_at?: string;
  next_status?: number;
  execution?: ChannelScheduleExecution;
}
export interface ChannelScheduleList {
  items: ChannelSchedule[];
  direct_configured: boolean;
  worker_enabled: boolean;
}
export interface ChannelSchedulePreview {
  allowed: boolean;
  next_at?: string;
  next_status?: number;
  effective_windows: { start_minute: number; end_minute: number }[];
}

export interface BillingGenerationDay {day:string;status:string;job_id?:string;processed:number;error?:string;updated_at?:string}
export interface BillingGenerationBlocker {kind:string;period?:string;status:string;reason:string;attempts?:number;updated_at:string}
export interface BillingGenerationProgress {blocker?:BillingGenerationBlocker;subject_name?:string;waiting_for?:string;subject_id:number;outcome:string;total_days:number;checked:number;complete:number;empty:number;pending:number;running:number;failed:number;processed:number;error?:string;last_attempt?:string;days:BillingGenerationDay[];monthly?:BillingGenerationDay}
export interface BillingGenerationTask {id:string;instance_id:string;kind:string;from:string;to:string;work_until:string;source:string;overwrite:boolean;exclude_zero_output?:boolean;subject_ids:number[];created_at:string;outcome:string;percentage:number;completed_users:number;failed_users:number;complete_days:number;empty_days:number;items?:BillingGenerationProgress[]}
export interface BillingGenerationBatch {kind?:string;instance_id:string;subject_ids:number[];from:string;to:string;overwrite?:boolean;exclude_zero_output?:boolean}
export interface BillingDetailRow {unit_price?:string;image_input_tokens?:string;image_output_tokens?:string;audio_input_tokens?:string;audio_output_tokens?:string;time:string;request_id:string;model:string;token:string;token_id:string;input:string;output:string;cache_read:string;cache_write:string;amount:string;before_amount?:string;discount?:string}
export interface BillingDetailFilter {from?:string;to?:string;model?:string;token?:string}
export interface BillingWorkspaceTotals {
  requests:number; input:number; output:number; cache_read:number; cache_write:number;
  amount:string; before_amount:string; discount?:string; empty_count:number; empty_amount:string;
  image_input_tokens?:number; image_output_tokens?:number; audio_input_tokens?:number; audio_output_tokens?:number;
}
export interface BillingWorkspaceModel extends BillingWorkspaceTotals { model:string }
export interface BillingWorkspaceBill extends BillingWorkspaceTotals {
  job:BillingJob; currency?:BillingCurrencyDisplay; models?:BillingWorkspaceModel[]; tiers?:(Omit<BillingWorkspaceTotals,"empty_count"|"empty_amount"> & {model:string;tier:string})[];
}
export interface BillingCatalogPage {
  items:(BillingWorkspaceBill & {generated_at:string})[]; total:number; subjects:number;
  counts:Record<'daily'|'monthly'|'temporary'|'zero_output',number>; page:number; page_size:number;
}
export interface BillingDetailTask {key:string;kind:'prepare'|'export';status:'running'|'complete'|'failed';processed:number;total:number;matched:number;error?:string}
export interface BillingDetailPage {preparing?:boolean;task?:BillingDetailTask;items?:BillingDetailRow[];next_cursor?:number;total?:number;currency?:string;models?:string[];tokens?:string[]}

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
export interface OverviewPoint {
  from: string; to: string; complete: boolean; received_sources: number; confirmed_at: string | null;
  metrics: (MetricSummary & { prompt_tokens: number; completion_tokens: number; p95_lower_bound: boolean }) | null;
}
export interface OverviewRequests {
  site: string; queried_at: string; sources: string[]; recent: OverviewPoint; points: OverviewPoint[]; step_seconds: number;
}
export interface OverviewRuntime {
  site: string; queried_at: string; stale_after_seconds: number;
  items: { instance_id: string; name: string; enabled: boolean; online: boolean; last_seen_at: string | null;
    health: { target: string; checked_at: string; status: string; http_status_code: number }[] | null;
    docker: { container_name: string; collected_at: string; running: boolean; status: string }[] | null;
  }[];
}
export interface OverviewAlerts {
  site: string; queried_at: string; evaluated_at: string | null; fresh: boolean; total: number; unconfirmed: number;
  items: (AlertItem & { confirmed_at: string | null })[];
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
  consume_count?: number | null;
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
  otps_duration_seconds?: number;
}
export interface AlertItem {
  site_id?: string;
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
  site_id: string;
  rule_keys: string[];
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
  site_id: string;
  rule_keys: string[];
  id: string;
  channel_type: "webhook" | "dingtalk" | "wecom";
  name: string;
  webhook_url: string;
  enabled: boolean;
  secret?: string;
}
export interface NotificationDeliveryItem {
  alert_title?: string;
  alert_summary?: string;
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
  group?: string;
}
export interface ChannelCommandItem {
  site_id: string;
  execution_kind: string;
  legacy_instance_id?: string;
  id: string;
  instance_id: string;
  instance_name: string;
  channel_id: number;
  status: string;
  payload: Record<string, number | string>;
  created_by: string;
  error_summary?: string;
  created_at: string;
}
export interface OperationAuditItem {
  site_id?: string;
  id: string;
  instance_id: string;
  instance_name: string;
  operation_type: string;
  target_type: string;
  target_id: string;
  actor_id: string;
  actor_type: string;
  actor_role: string;
  source_component: string;
  trigger_type: string;
  request_id: string;
  correlation_id: string;
  client_ip: string;
  auth_method: string;
  http_method: string;
  route: string;
  http_status: number;
  error_summary: string;
  before_summary: string;
  after_summary: string;
  status: string;
  created_at: string;
  updated_at: string;
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
	 recovery_speed_exponent: number;
  channel_history_retention_days: number;
  recovery_interval_seconds: number;
  evaluation_interval_seconds: number;
  routine_interval_seconds: number;
  routine_probe_enabled: boolean;
  routine_probe_selected_only?: boolean;
  routine_probe_channel_ids?: number[];
  probe_timeout_seconds: number;
  probe_slow_limit: number;
  routine_error_limit: number;
  probe_max_age_seconds: number;
  baseline_min_channels: number;
  output_min_tokens: number;
  cache_min_tokens: number;
  error_alpha: number;
  error_settle_seconds: number;
  error_lookback_minutes: number;
  capacity_target_utilization: number;
  capacity_release_utilization: number;
  capacity_peer_utilization: number;
  capacity_overload_seconds: number;
  capacity_recovery_seconds: number;
  capacity_feedback_seconds: number;
  capacity_max_age_seconds: number;
  capacity_sample_gap_seconds: number;
  capacity_min_weight: number;
  write_pause_attempts: number;
  write_max_attempts: number;
  write_retry_seconds: number;
  external_write_grace_seconds: number;
  recovery_round_seconds: number;
  recovery_lost_seconds: number;
  recovery_speed_min_factor: number;
  recovery_min_weight: number;
  fast_report_max_age_seconds: number;
  fast_report_future_seconds: number;
  observe_deadband_percent: number;

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
  probe_speed_exponent: number;
  probe_speed_min_factor: number;
  probe_speed_max_factor: number;
  error_healthy_rate: number;
  error_degraded_rate: number;
  error_poor_rate: number;
  error_floor_rate: number;
  error_degraded_factor: number;
  error_poor_factor: number;
  error_min_factor: number;
  combined_min_factor: number;
  combined_max_factor: number;
  max_increase_percent?: number;
  capacity_max_decrease_percent?: number;
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
export interface TuningChannel {
  channel_id: number; channel_name: string; status: string; weight: number;
  priority: number; models: string[]; group_name: string;
}
export interface TuningChannelGroupResponse {
  command_id: string; instance_id: string; channel_id: number;
  group: string; status: string; created_at: string;
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
export interface ProbeDispatchCounts {
  due: number; sent: number; global_limited: number; channel_limited: number; evidence_limited: number;
  last_due_at: string; last_sent_at?: string; last_skipped_at?: string;
}
export interface ProbeMetrics {
  due: number; sent: number; success: number; slow: number; errors: number; unknown: number; canceled: number;
  pending: number; unconfirmed: number; skipped: number; global_limited: number; channel_limited: number; evidence_limited: number;
  success_rate: number | null; mean_seconds: number | null; mean_success_seconds: number | null; max_seconds: number | null; last_started_at?: string;
}
export interface ProbeChannelStats extends ProbeMetrics { channel_id: number; channel_name: string; model_name: string }
export interface ProbeRecord {
	 round_id?: string; round_index?: number;
  id: string; instance_id: string; channel_id: number; channel_name: string; model_name: string; kind: string;
  started_at: string; deadline_at: string; finished_at?: string; outcome: string; reason: string; duration_seconds?: number;
}
export interface ProbeOverview {
  retention_days: number;
  from: string; to: string; queried_at: string; bucket_seconds: number; total: ProbeMetrics; channels: ProbeChannelStats[];
  trend: Array<ProbeMetrics & {at: string}>;
  recorder?: {started_at: string; last_seen_at: string; last_loss_at?: string; dropped_records: number; queued_records: number};
}
export interface ProbeQuery {site_id: string; from: string; to: string; channel_id?: number; model?: string; kind?: string; q?: string; outcome?: string; cursor?: string; limit?: number; round_id?: string; dispatch?: string}
export interface LifelineChannel {
 channel_id:number; channel_name:string; models:string[]; weight:number; priority:number;
 switch:string; state:string; reason:string; next:string; next_at?:string; confirmed_at?:string;
 pending:number; accelerated:number; last?:ProbeRecord; round_id?:string;current_round?:ProbeRound;
}
export interface LifelineCurrent {queried_at:string; channels:LifelineChannel[]}
export interface ProbeRound {
 execution_kind?:string; executor_instance_id?:string;
 id:string; channel_id:number; model:string; planned:number; status:string; created_at:string; updated_at:string;
 metrics?:ProbeMetrics; reported_attempts?:number; reported_successes?:number;
}
export interface ChannelLifeEvent {
  id: string; source: string; at: string; finished_at?: string; operation: string; outcome: string; actor: string;
  command_id?: string; recommendation_id?: string; details: Record<string, unknown>;
}
export interface ProbeCoverageWindow {
  from: string; to: string; as_of: string; error?: string;
  items: Record<string, { scheduled: ProbeDispatchCounts; accelerated: ProbeDispatchCounts }>;
}
export interface TuningContinuousState {
  evaluation?: {performance_evaluated?: boolean; base_weight: number; base_updated_at: string; policy_updated_at: string; evaluated_at: string; params: TuningPolicy["continuous"]};
  instance_id: string;
  channel_id: number;
  channel_name: string;
  model_name: string;
  base_weight: number;
  base_priority: number;
  k_speed: number;
  k_cache: number;
  k_otps: number;
  probe_speed?: { samples: number; mean_seconds: number; baseline_seconds: number; last_started_at: string; factor: number; ready: boolean };
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
	capacity?: {
	  initialized: boolean; active: boolean; phase: string; reason?: string; fresh: boolean;
	  max_rpm: number; max_tpm: number; utilization: number;
	  sample_at: string; over_since: string; under_since: string; applied_at: string;
	  confirmed_weight: number; raw_target: number; bound_weight: number;
	  pending_command_id?: string; pending_weight: number;
	};
  speed_sample_count?: number;
  speed_retry_count?: number;
  speed_unknown_count?: number;
  speed_legacy_count?: number;
  speed_stats_version?: number;
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
  otps_sample_count?: number;
  otps_retry_count?: number;
  otps_unknown_count?: number;
  otps_stats_version?: number;
  smoothed_error_rate: number;
  paused_reason?: string;
  phase: "normal" | "circuit" | "probing" | "soft_start";
  circuit_disabled?: boolean;
  routine_circuit_latched?: boolean;
  circuit_status_target?: number;
  circuit_status_command_id?: string;
  circuit_opened_at?: string;
  next_probe_at?: string;
  probe_command_id?: string;
  probe_attempts: number;
  probe_successes: number;
  probe_duration_sum: number;
  probe_slow_streak?: number;
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
export interface BillingMonthlyCoverage {
  ranges: {from:string;to:string}[];
  missing: {from:string;to:string}[];
  covered_days:number;
  empty_days:number;
  total_days:number;
  complete:boolean;
}
export interface BillingJob {
  monthly_coverage?: BillingMonthlyCoverage;
  id: string;
  bill_no?: string;
  instance_id: string;
  job_type?: string;
  user_id?: number;
  user_name?: string;
  exclude_zero_output?: boolean;
  zero_output_policy?: "included" | "excluded" | "mixed" | "unknown";
  pricing_source?: "newapi" | "recalculate";
  usage_version?: number;
  bill_period?: string;
  data_source?: string;
  upstream_id?: number;
  upstream_name?: string;
  range_from?: string;
  range_to?: string;
  updated_at?: string;
  status: "pending" | "running" | "publishing" | "complete" | "failed" | "no_data";
  total_steps: number;
  completed_steps: number;
  abnormal_rows: number;
  billed_rows?: number;
  mismatch_rows?: number;
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
export interface BillingUpstreamChannel { channel_id:number;channel_name:string;selected_models?:string[];association_source?:"legacy"|"auto"|"manual";matched_prefix?:string;associated_at?:string;associated_by?:string }
export interface BillingReadonlyChannel { channel_id:number;channel_name:string;status:number;models:string;base_url?:string;auto_excluded?:boolean;source_missing?:boolean }
export interface BillingUpstream {
  id:number;instance_id:string;name:string;revision?:number;channel_prefixes?:string[];suggested_prefixes?:string[];review_prefixes?:string[];
  add_channel_ids?:number[];remove_channel_ids?:number[];
  channel_transfers?:{channel_id:number;from_upstream_id:number}[];
  prefix_transfers?:{prefix:string;from_upstream_id:number}[];
  url?:string;urls?:string[];enabled:boolean;remark:string;channels:BillingUpstreamChannel[];
  sync_error?:string;created_at?:string;updated_at?:string;updated_by?:string;
}
export interface BillingUpstreamDirectory { items:BillingUpstream[];channels:BillingReadonlyChannel[];source_available?:boolean;sync_error?:string;synced_at?:string }
export interface BillingDiscountRule { id:number;instance_id:string;discount_type:"upstream_channel"|"user_model";subject_id:number;subject_name?:string;channel_id:number;channel_name?:string;model_name:string;discount:string;effective_from:string;effective_to?:string;remark:string;created_at?:string;updated_at?:string;updated_by?:string }
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
  overviewRequests: (site: string, hours: number, signal?: AbortSignal) => client.request<OverviewRequests>(`/api/dashboard/overview-snapshot${query({site, hours, section: 'requests'})}`, {signal}),
  overviewRuntime: (site: string, signal?: AbortSignal) => client.request<OverviewRuntime>(`/api/dashboard/overview-snapshot${query({site, section: 'runtime'})}`, {signal}),
  overviewAlerts: (site: string, signal?: AbortSignal) => client.request<OverviewAlerts>(`/api/dashboard/overview-snapshot${query({site, section: 'alerts'})}`, {signal}),
  siteCurrency: (site: string) => client.request<{ quota_per_unit: number; price_multiplier: number; symbol: string; type: string }>(`/api/dashboard/passthrough/currency${query({ site })}`),
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
      site?: string;
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
      offset?: number;
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
  notificationChannels: (params: { site_id?: string; unassigned?: boolean }) =>
    client.request<ListResponse<NotificationChannelItem>>(
      `/api/dashboard/notification-channels${query(params)}`,
    ),
  saveNotificationChannel: (input: NotificationChannelInput) =>
    client.request<ListResponse<NotificationChannelItem>>(
      "/api/dashboard/notification-channels",
      { method: "POST", body: JSON.stringify(input) },
    ),
  notificationDeliveries: (
    params: {
      site_id?: string;
      start_time?: string;
      end_time?: string;
      search?: string;
      alert_id?: string;
      channel_id?: string;
      status?: string;
      limit?: number;
      offset?: number;
    } = {},
  ) =>
    client.request<ListResponse<NotificationDeliveryItem> & { filters_supported?: boolean }>(
      `/api/dashboard/notification-deliveries${query(params)}`,
    ),
  resendDelivery: (id: string, site_id: string) =>
    client.request<DashboardOKResponse>(
      `/api/dashboard/notification-deliveries/${encodeURIComponent(id)}/resend${query({ site_id })}`,
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
      site_id?: string;
      channel_id?: number;
      execution_kind?: string;
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
    params: { list_only?: boolean; count_only?: boolean; before_time?: string; before_id?: string; actor_exact?: boolean; actor_options?: boolean; instance_id?: string; site_id?: string; operation_type?: string; actor?: string; status?: string; source?: string; trigger?: string; q?: string; search_mode?: "smart" | "target" | "error" | "ip"; request_id?: string; correlation_id?: string; from?: string; to?: string; limit?: number; offset?: number } = {},
    signal?: AbortSignal,
  ) =>
    client.request<ListResponse<OperationAuditItem> & { total: number; has_more?: boolean; operation_types: string[]; actors: string[] }>(
      `/api/dashboard/operation-audits${query(params)}`,
      { signal },
    ),
  tuningGroupPresets: (site_id: string) => client.request<ChannelGroupPresets>(`/api/dashboard/tuning/group-presets${query({ site_id })}`),
  saveTuningGroupPresets: (site_id: string, value: ChannelGroupPresets) => client.request<ChannelGroupPresets>(`/api/dashboard/tuning/group-presets${query({ site_id })}`, { method: "PUT", body: JSON.stringify(value) }),
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
  tuningPolicy: (site_id: string, signal?: AbortSignal) =>
    client.request<TuningPolicyResponse>(`/api/dashboard/tuning/policy${query({ site_id })}`, { signal }),
  saveTuningPolicy: (site_id: string, policy: TuningPolicy, mode: "observe" | "confirm" | "auto", preflight_command_id?: string, expected_policy?: TuningPolicy, expected_mode?: "observe" | "confirm" | "auto") =>
    client.request<TuningPolicyResponse>(`/api/dashboard/tuning/policy${query({ site_id })}`, { method: "PUT", body: JSON.stringify({ policy, mode, preflight_command_id, expected_policy, expected_mode }) }),
  startTuningPreflight: (site_id: string, channel_id: number) =>
    client.request<{ command_id: string; status: string; error?: string }>(`/api/dashboard/tuning/preflight${query({ site_id })}`, { method: "POST", body: JSON.stringify({ channel_id }) }),
  tuningPreflight: (site_id: string, command_id: string) =>
    client.request<{ command_id: string; status: string; error?: string }>(`/api/dashboard/tuning/preflight${query({ site_id, command_id })}`),
  tuningRecommendations: (site_id: string, limit = 100, rule?: string, signal?: AbortSignal) =>
    client.request<ListResponse<TuningRecommendation>>(`/api/dashboard/tuning/recommendations${query({ site_id, limit, rule })}`, { signal }),
  tuningReport: (site_id: string, days: 7 | 30) =>
    client.request<TuningReport>(`/api/dashboard/tuning/report${query({ site_id, days })}`),
  tuningBaseValues: (site_id: string, model?: string, signal?: AbortSignal) =>
    client.request<ListResponse<ChannelBaseValue>>(`/api/dashboard/tuning/base-values${query({ site_id, model })}`, { signal }),
  tuningChannels: (site_id: string, signal?: AbortSignal) =>
    client.request<ListResponse<TuningChannel>>(`/api/dashboard/tuning/channels${query({ site_id })}`, { signal }),
  saveTuningChannelStatus: (site_id: string, channel_id: number, status: 1 | 2) =>
    client.request<{command_id:string;channel_id:number;channel_status:1|2;status:string}>(`/api/dashboard/tuning/channels/${channel_id}/status${query({ site_id })}`, {method:"PUT",body:JSON.stringify({status,confirm:true})}),
  channelSchedules: (site_id:string, signal?:AbortSignal) => client.request<ChannelScheduleList>(`/api/dashboard/tuning/channel-schedules${query({site_id})}`, {signal}),
  saveChannelSchedule: (site_id:string,channel_id:number,value:ChannelScheduleConfig,signal?:AbortSignal) => client.request<{saved:boolean;revision:number;changed:boolean}>(`/api/dashboard/tuning/channel-schedules/${channel_id}${query({site_id})}`,{method:'PUT',body:JSON.stringify({...value,confirm:true}),signal}),
  previewChannelSchedule: (site_id:string,value:ChannelScheduleConfig,signal?:AbortSignal) => client.request<ChannelSchedulePreview>(`/api/dashboard/tuning/channel-schedules/preview${query({site_id,config:JSON.stringify(value)})}`,{signal}),
  deleteChannelSchedule: (site_id:string,channel_id:number,revision:number,signal?:AbortSignal,expected_updated_at?:string) => client.request<{saved:boolean}>(`/api/dashboard/tuning/channel-schedules/${channel_id}${query({site_id})}`,{method:'DELETE',body:JSON.stringify({revision,confirm:true,expected_updated_at}),signal}),
  channelScheduleExecutions: (site_id:string,channel_id:number,signal?:AbortSignal) => client.request<{items:ChannelScheduleExecution[]}>(`/api/dashboard/tuning/channel-schedules/${channel_id}/executions${query({site_id})}`,{signal}),
  tuningGroups: (site_id: string) =>
    client.request<{ items: string[] }>(`/api/dashboard/tuning/groups${query({ site_id })}`),
  saveTuningChannelGroup: (site_id: string, channel_id: number, group: string) =>
    client.request<TuningChannelGroupResponse>(`/api/dashboard/tuning/channels/${channel_id}/group${query({ site_id })}`, { method: "PUT", body: JSON.stringify({ confirm: true, group }) }),
  refreshTuningChannels: (site_id: string) =>
    client.request<{ synced: boolean }>(`/api/dashboard/tuning/channels/refresh${query({ site_id })}`, { method: "POST" }),
  tuningChannelChanges: (site_id: string, after: string, signal: AbortSignal) =>
    client.request<{ revision: string }>(`/api/dashboard/tuning/channels/changes${query({ site_id, after })}`, { signal }),
  tuningContinuousStates: (site_id: string, signal?: AbortSignal) =>
    client.request<ListResponse<TuningContinuousState> & { probe_coverage?: ProbeCoverageWindow }>(`/api/dashboard/tuning/continuous-states${query({ site_id })}`, { signal }),
  probeOverview: (params: ProbeQuery, signal?: AbortSignal) => client.request<ProbeOverview>(`/api/dashboard/tuning/probes/overview${query({...params})}`, {signal}),
  lifelineCurrent: (site_id:string, signal?:AbortSignal) => client.request<LifelineCurrent>(`/api/dashboard/tuning/probes/current${query({site_id})}`,{signal}),
  probeRounds: (params:ProbeQuery, signal?:AbortSignal) => client.request<{items:ProbeRound[];next_cursor?:string}>(`/api/dashboard/tuning/probes/rounds${query({...params})}`,{signal}),
  probeRecords: (params: ProbeQuery, signal?: AbortSignal) => client.request<{items: ProbeRecord[]; total: number; next_cursor?: string}>(`/api/dashboard/tuning/probes/records${query({...params})}`, {signal}),
  channelLifeline: (params: ProbeQuery & {source?: string}, signal?: AbortSignal) => client.request<{items: ChannelLifeEvent[]; next_cursor?: string}>(`/api/dashboard/tuning/probes/lifeline${query({...params})}`, {signal}),
  tuningCurrentRates: (site_id: string) =>
    client.request<{ items: { channel_id: number; rpm: number; tpm: number }[]; as_of: string; window_start: string; window_seconds: number; delay_seconds: number }>(`/api/dashboard/tuning/continuous-states${query({ site_id, rates_only: 1 })}`),
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
  createBillingStatement: (input:{instance_id:string;statement_type:"user"|"upstream";user_id?:number;upstream_id?:number;from:string;to:string;exclude_zero_output?:boolean;recalculate?:boolean}) =>
    client.request<{accepted:boolean;job:BillingJob}>("/api/dashboard/billing/statements",{method:"POST",body:JSON.stringify(input)}),
  billingStatementResult: (id:string, deferPrices = false, signal?: AbortSignal) => client.request<{job:BillingJob;daily_files:{day:string;filename:string}[];total_orders:number;normal_orders:number;billable_orders:number;anomaly_total:number;reconciliation_total:number;review_required:boolean;count_balanced:boolean;model_summary:Record<string,unknown>[];daily_summary:Record<string,unknown>[];token_summary:Record<string,unknown>[];anomalies:Record<string,unknown>[];reconciliation:Record<string,unknown>[]}>(`/api/dashboard/billing/statements/result${query({id, defer_prices: deferPrices ? "1" : undefined})}`, { signal }),
  billingStatementPrices: (id:string, signal?: AbortSignal) => client.request<{daily_summary:Record<string,unknown>[]}>(`/api/dashboard/billing/statements/result${query({id, section: "prices"})}`, { signal }),
  deleteBillingStatement: (id:string) => client.request<{deleted:boolean;id:string}>(`/api/dashboard/billing/statements/result${query({id})}`,{method:"DELETE"}),
  billingJob: (id: string) => client.request<BillingJob>(`/api/dashboard/billing/jobs${query({ id })}`),
  billingJobSteps: (id: string) => client.request<{ items: BillingJobStep[] }>(`/api/dashboard/billing/jobs/steps${query({ id })}`),
  cancelBillingJob: (id: string) => client.request<BillingJob>(`/api/dashboard/billing/jobs${query({ id })}`, { method: "DELETE" }),
  deleteFailedBillingJob: (id: string) => client.request<{ deleted: boolean; id: string }>(`/api/dashboard/billing/jobs${query({ id })}`, { method: "DELETE" }),
  billingJobs: (params: { instance_id?: string; status?: BillingJob["status"]; kind?: string; subject_id?: number; limit?: number } = {}) =>
    client.request<{ items: BillingJob[]; pricing_source_selection?: boolean }>(`/api/dashboard/billing/jobs${query(params)}`),
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
  billingUpstreams:(instance_id:string,signal?:AbortSignal)=>client.request<BillingUpstreamDirectory>(`/api/dashboard/billing/upstreams${query({instance_id})}`,{signal}),
  syncBillingUpstreams:(instance_id:string,restore_auto_channel_ids?:number[])=>client.request<BillingUpstreamDirectory>(`/api/dashboard/billing/upstreams${query({instance_id,action:"sync"})}`,{method:"POST",body:JSON.stringify({restore_auto_channel_ids:restore_auto_channel_ids||[]})}),
  saveBillingUpstream:(input:BillingUpstream)=>client.request<BillingUpstream>("/api/dashboard/billing/upstreams",{method:input.id?"PUT":"POST",body:JSON.stringify(input)}),
  deleteBillingUpstream:(instance_id:string,id:number,revision?:number,target_id?:number,target_revision?:number)=>client.request<{deleted:boolean;archived?:boolean;sync_error?:string}>(`/api/dashboard/billing/upstreams${query({instance_id,id,revision,target_id,target_revision})}`,{method:"DELETE"}),
  reportTasks:(instance_id:string)=>client.request<{items:SettlementReportTask[]}>(`/api/dashboard/billing/report-tasks${query({instance_id})}`),
  createReportTask:(input:{instance_id:string;from:string;to:string;overwrite:boolean})=>client.request<SettlementReportTask>('/api/dashboard/billing/report-tasks',{method:'POST',body:JSON.stringify(input)}),
  retryReportTask:(instance_id:string,id:string)=>client.request<{ok:boolean}>('/api/dashboard/billing/report-tasks?action=retry',{method:'POST',body:JSON.stringify({instance_id,id})}),
  cancelReportTask:(instance_id:string,id:string)=>client.request<{ok:boolean}>('/api/dashboard/billing/report-tasks?action=cancel',{method:'POST',body:JSON.stringify({instance_id,id})}),
  settlementReport:(params:{instance_id:string;from:string;to:string},signal?:AbortSignal)=>client.request<{items:{user_id:number;user:string;model:string;channel_id:number;channel:string;upstream:string;discount:string;requests:number;input:number;output:number;cache:number;empty:number;amount:string;cost:string;raw_amount:string;raw_cost:string;base_fallback?:number;cost_discount:string;unknown_cost:number}[];failed_requests:number|null;expected_days:number;generated_days:number;missing_days:string[];complete:boolean;currency?:BillingCurrencyDisplay}>(`/api/dashboard/billing/reports${query(params)}`,{signal}),
  billingWorkspace:(params:{instance_id:string;kind:string;subject_id:number;from:string;to:string})=>client.request<{items:BillingWorkspaceBill[];remaining_days:number}>(`/api/dashboard/billing/workspace${query(params)}`),
  billingCatalog:(params:{instance_id:string;kind:string;period:string;month?:string;q?:string;page?:number;page_size?:number},signal?:AbortSignal)=>client.request<BillingCatalogPage>(`/api/dashboard/billing/catalog${query(params)}`,{signal}),
  billingDetailPage:(id:string,params:BillingDetailFilter&{cursor?:number;retry?:number})=>client.request<BillingDetailPage>(`/api/dashboard/billing/statements/details${query({id,...params})}`),
  exportBillingDetails:(id:string,filter:BillingDetailFilter)=>client.request<BillingDetailTask>(`/api/dashboard/billing/statements/details${query({id,action:'export'})}`,{method:'POST',body:JSON.stringify(filter)}),
  billingDetailTask:(id:string,key:string)=>client.request<BillingDetailTask>(`/api/dashboard/billing/statements/details${query({id,key,action:'status'})}`),
  billingGenerationHistory:(site:string,page=1,kind="user_statement")=>client.request<{items:BillingGenerationTask[];total:number}>(`/api/dashboard/billing/generation-batch${query({instance_id:site,action:'history',page,kind})}`),
  billingGenerationHistoryDetail:(site:string,id:string,kind="user_statement")=>client.request<BillingGenerationTask>(`/api/dashboard/billing/generation-batch${query({instance_id:site,action:'history-detail',id,kind})}`),
  billingGenerationStatus:(site:string,kind="user_statement")=>client.request<{busy:boolean;targets:{instance_id:string;kind:string;subject_id:number;from:string;to:string}[];jobs:BillingJob[]}>(`/api/dashboard/billing/generation-batch${query({instance_id:site,action:'status',kind})}`),
  cancelBillingBatch:(input:BillingGenerationBatch)=>client.request<{cancelled:boolean}>('/api/dashboard/billing/generation-batch?action=cancel',{method:'POST',body:JSON.stringify(input)}),
  generateBillingBatch:(input:BillingGenerationBatch)=>client.request<{accepted:boolean;items:BillingGenerationProgress[]}>('/api/dashboard/billing/generation-batch',{method:'POST',body:JSON.stringify(input)}),
  billingBatchProgress:(input:BillingGenerationBatch)=>client.request<{items:BillingGenerationProgress[];subject_ids?:number[]}>(`/api/dashboard/billing/generation-batch${query({...input,subject_ids:input.subject_ids.join(',')})}`),
  billingGenerationPreview:(input:BillingGenerationBatch,signal?:AbortSignal)=>client.request<{items:BillingGenerationProgress[]}>(`/api/dashboard/billing/generation-batch${query({...input,action:'preview',subject_ids:input.subject_ids.join(',')})}`,{signal}),
  generateMissingBills:(input:{instance_id:string;kind:string;subject_id:number;from?:string;to?:string;exclude_zero_output?:boolean})=>client.request<{accepted:boolean;automatic:boolean;outcome:'registered'|'generating'|'already_complete'|'no_consumption'|'failed'}>('/api/dashboard/billing/generate-missing',{method:'POST',body:JSON.stringify(input)}),
  createNewStatement:(input:{instance_id:string;statement_type:string;user_id?:number;upstream_id?:number;from:string;to:string;period?:string;exclude_zero_output?:boolean})=>client.request('/api/dashboard/billing/statements',{method:'POST',body:JSON.stringify({...input,new_edition:true})}),
  billingConfiguration:()=>client.request<{data_source:string}>('/api/dashboard/billing/configuration'),
  saveBillingConfiguration:(data_source:string)=>client.request('/api/dashboard/billing/configuration',{method:'PUT',body:JSON.stringify({data_source})}),
  billingCurrentDiscounts:(instance_id:string,user_id:number)=>client.request<{items:{model_name:string;discount:string}[]}>(`/api/dashboard/billing/current-discounts${query({instance_id,user_id})}`),
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
