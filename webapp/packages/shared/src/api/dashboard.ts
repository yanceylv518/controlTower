import type { ApiClient } from '../client'

// API types intentionally retain snake_case so every field maps one-to-one to the frozen contract.
export interface MetricSummary { request_count: number; success_count: number; error_count: number; success_rate: number | null; error_rate: number | null; tpm: number; avg_use_time: number | null; p95_use_time: number | null }
export interface Overview { instance_count: number; recent_1m: MetricSummary; runtime: { latest_server_metrics: unknown[]; health: { up_count: number; down_count: number; latest: unknown[] }; docker: { running_count: number; stopped_count: number; latest: unknown[] } } }
export interface MetricItem { instance_id: string; instance_name: string; bucket_time: string; dimension_type: string; dimension_key: string; display_key: string; request_count: number; success_count: number; error_count: number; success_rate: number | null; error_rate: number | null; tpm: number; prompt_tokens: number; completion_tokens: number; quota: number; avg_use_time: number | null; p95_use_time: number | null; p50_use_time?: number; p99_use_time?: number; stream_rate: number | null; cache_token_rate: number | null }
export interface AlertItem { id: string; instance_id: string; instance_name: string; display_key: string; rule_key: string; severity: string; status: string; title: string; summary: string; seen_at: string; first_seen_at: string; last_seen_at: string; resolved_at?: string; silence_until?: string }
export interface AlertEvent { id: number; alert_id: string; event_type: string; actor: string; note: string; created_at: string }
export interface DashboardOKResponse { ok: boolean }
export interface AlertActionInput { id: string; action: string; note?: string; silence_minutes?: number }
export interface ListResponse<T> { items: T[] }
export interface InstanceItem { instance_id: string; name: string; enabled: boolean; created_at: string; updated_at: string; agents: Array<{ id: string; version: string; last_seen_at: string; backlog_estimate: number; online: boolean }> }
export interface ChannelSnapshot { id: string; instance_id: string; instance_name: string; channel_id: number; channel_name: string; status: string; weight: number; models_text: string; captured_at: string }
export interface LogSample { instance_id: string; sample_kind: string; source_log_id: number; created_at: string; log_type: string; user_id: number; username: string; channel_id: number; model_name: string; token_id: number; token_name: string; total_tokens: number; quota: number; use_time: number; request_id: string; upstream_request_id: string; error_summary: string }
export interface AgentItem { id: string; instance_id: string; version: string; last_seen_at: string; last_sequence: number; last_log_id: number; source_latest_log_id: number; backlog_estimate: number; status: string; report_delay_ms: number; online: boolean; seconds_since_seen: number }
export interface ServerMetricItem { instance_id: string; collected_at: string; cpu_percent: number; memory_used_percent: number; disk_used_percent: number; network_rx_bytes_per_second: number; network_tx_bytes_per_second: number; load_1m: number }
export interface HealthCheckItem { instance_id: string; checked_at: string; target: string; status: string; http_status_code: number; latency_ms: number; error_summary: string }
export interface DockerStatusItem { instance_id: string; collected_at: string; container_name: string; status: string; running: boolean }
export interface UsageItem { dimension_type: string; dimension_key: string; display_key: string; request_count: number; total_tokens: number; prompt_tokens: number; completion_tokens: number; quota: number }
export interface NotificationChannelItem { id: string; channel_type: string; name: string; webhook_url_masked: string; enabled: boolean; created_at: string; updated_at: string; has_secret: boolean }
export interface NotificationChannelInput { id: string; channel_type: 'webhook' | 'dingtalk'; name: string; webhook_url: string; enabled: boolean; secret?: string }
export interface NotificationDeliveryItem { id: string; alert_id: string; channel_id: string; status: string; attempted_at: string; next_attempt_at: string; attempts: number; status_code: number; error_summary: string }
export interface InstanceCreateResponse { instance_id: string; name: string; token: string }
export interface InstanceUpdateInput { name?: string; enabled?: boolean }
export interface InstanceTokenResponse { token: string; grace_until?: string }
export interface ChannelCommandInput { instance_id: string; confirm: true; status?: number; weight?: number; priority?: number }
export interface ChannelCommandItem { id: string; instance_id: string; instance_name: string; channel_id: number; status: string; payload: Record<string, number>; created_by: string; error_summary?: string; created_at: string }
export interface OperationAuditItem { instance_id: string; instance_name: string; operation_type: string; target_type: string; target_id: string; actor_id: string; after_summary: string; created_at: string }
export interface NginxTimingBucket { bucket_at: string; request_count: number; upstream_count: number; status_4xx: number; status_5xx: number; status_504: number; rt_p50: number; rt_p95: number; rt_max: number; uht_p50: number; uht_p95: number; uht_max: number; transfer_p50: number; transfer_p95: number; transfer_max: number; bytes_total: number; slow_count: number; slow_ttft_count: number; slow_transfer_count: number }
export interface NginxTimingSummary { total_requests: number; status_5xx: number; status_504: number; slow_count: number; slow_ttft_count: number; slow_transfer_count: number; slow_ttft_percent: number; slow_transfer_percent: number }
export interface NginxTimingResponse { items: NginxTimingBucket[]; summary: NginxTimingSummary }
export interface NginxSlowSample { id: number; occurred_at: string; path: string; status: number; rt: number; uht: number; urt: number; bytes: number }

const query = (values: Record<string, string | number | boolean | undefined>) => {
  const params = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
  const encoded = params.toString()
  return encoded ? `?${encoded}` : ''
}

export const dashboardApi = (client: ApiClient) => ({
  instances: () => client.request<ListResponse<InstanceItem>>('/api/dashboard/instances'),
  overview: (instance_id?: string) => client.request<Overview>(`/api/dashboard/overview${query({ instance_id })}`),
  metrics: (params: { instance_id?: string; window?: string; latest?: boolean; dimension_type?: string; dimension_key?: string } = {}) => client.request<ListResponse<MetricItem>>(`/api/dashboard/metrics${query(params)}`),
  metricHistory: (params: { window: string; dimension_type: string; dimension_key: string; hours: number }) => client.request<ListResponse<MetricItem>>(`/api/dashboard/metric-history${query(params)}`),
  alerts: (params: { instance_id?: string; status?: string; severity?: string; active_only?: boolean; limit?: number; offset?: number } = {}) => client.request<ListResponse<AlertItem>>(`/api/dashboard/alerts${query(params)}`),
  alertEvents: (id: string, limit = 100) => client.request<ListResponse<AlertEvent>>(`/api/dashboard/alerts/${encodeURIComponent(id)}/events${query({ limit })}`),
  alertAction: (input: AlertActionInput) => client.request<DashboardOKResponse>('/api/dashboard/alerts/action', { method: 'POST', body: JSON.stringify(input) }),
  channelSnapshots: (params: { instance_id?: string; latest_only?: boolean; limit?: number } = {}) => client.request<ListResponse<ChannelSnapshot>>(`/api/dashboard/channel-snapshots${query(params)}`),
  logSamples: (params: { instance_id?: string; sample_kind?: string; model_name?: string; user_id?: string; request_id?: string; limit?: number; offset?: number } = {}) => client.request<ListResponse<LogSample>>(`/api/dashboard/log-samples${query(params)}`),
  agents: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<AgentItem>>(`/api/dashboard/agents${query(params)}`),
  serverMetrics: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<ServerMetricItem>>(`/api/dashboard/server-metrics${query(params)}`),
  healthChecks: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<HealthCheckItem>>(`/api/dashboard/health-checks${query(params)}`),
  dockerStatuses: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<DockerStatusItem>>(`/api/dashboard/docker-statuses${query(params)}`),
  usage: (hours: number) => client.request<ListResponse<UsageItem>>(`/api/dashboard/usage${query({ hours })}`),
  notificationChannels: () => client.request<ListResponse<NotificationChannelItem>>('/api/dashboard/notification-channels'),
  saveNotificationChannel: (input: NotificationChannelInput) => client.request<ListResponse<NotificationChannelItem>>('/api/dashboard/notification-channels', { method: 'POST', body: JSON.stringify(input) }),
  notificationDeliveries: (params: { alert_id?: string; channel_id?: string; status?: string; limit?: number; offset?: number } = {}) => client.request<ListResponse<NotificationDeliveryItem>>(`/api/dashboard/notification-deliveries${query(params)}`),
  resendDelivery: (id: string) => client.request<DashboardOKResponse>(`/api/dashboard/notification-deliveries/${encodeURIComponent(id)}/resend`, { method: 'POST' }),
  createInstance: (input: { instance_id: string; name: string }) => client.request<InstanceCreateResponse>('/api/dashboard/instances', { method: 'POST', body: JSON.stringify(input) }),
  updateInstance: (id: string, input: InstanceUpdateInput) => client.request<InstanceItem>(`/api/dashboard/instances/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify(input) }),
  rotateInstanceToken: (id: string) => client.request<InstanceTokenResponse>(`/api/dashboard/instances/${encodeURIComponent(id)}/rotate-token`, { method: 'POST' }),
  createChannelCommand: (channelID: number, input: ChannelCommandInput) => client.request<ChannelCommandItem>(`/api/dashboard/channels/${channelID}/commands`, { method: 'POST', body: JSON.stringify(input) }),
  channelCommands: (params: { instance_id?: string; status?: string; limit?: number; offset?: number } = {}) => client.request<ListResponse<ChannelCommandItem>>(`/api/dashboard/channel-commands${query(params)}`),
  operationAudits: (params: { instance_id?: string; limit?: number; offset?: number } = {}) => client.request<ListResponse<OperationAuditItem>>(`/api/dashboard/operation-audits${query(params)}`),
  nginxTiming: (params: { instance_id: string; hours: number }) => client.request<NginxTimingResponse>(`/api/dashboard/nginx-timing${query(params)}`),
  nginxSlowSamples: (params: { instance_id: string; hours: number; limit?: number }) => client.request<ListResponse<NginxSlowSample>>(`/api/dashboard/nginx-timing/slow-samples${query(params)}`),
})
