import type { ApiClient } from '../client'

// API types intentionally retain snake_case so every field maps one-to-one to the frozen contract.
export interface MetricSummary { request_count: number; success_count: number; error_count: number; success_rate: number | null; error_rate: number | null; tpm: number; avg_use_time: number | null; p95_use_time: number | null }
export interface Overview { instance_count: number; recent_1m: MetricSummary; runtime: { latest_server_metrics: unknown[]; health: { up_count: number; down_count: number; latest: unknown[] }; docker: { running_count: number; stopped_count: number; latest: unknown[] } } }
export interface MetricItem { instance_id: string; bucket_time: string; dimension_type: string; dimension_key: string; display_key: string; request_count: number; success_count: number; error_count: number; success_rate: number | null; error_rate: number | null; tpm: number; prompt_tokens: number; completion_tokens: number; quota: number; avg_use_time: number | null; p95_use_time: number | null; p50_use_time?: number; p99_use_time?: number; stream_rate: number | null; cache_token_rate: number | null }
export interface AlertItem { id: string; instance_id: string; rule_key: string; severity: string; status: string; title: string; summary: string; seen_at: string; first_seen_at: string; last_seen_at: string; resolved_at?: string; silence_until?: string }
export interface AlertEvent { id: number; alert_id: string; event_type: string; actor: string; note: string; created_at: string }
export interface DashboardOKResponse { ok: boolean }
export interface AlertActionInput { id: string; action: string; note?: string; silence_minutes?: number }
export interface ListResponse<T> { items: T[] }
export interface InstanceItem { instance_id: string; name: string; enabled: boolean; created_at: string; updated_at: string; agents: Array<{ id: string; version: string; last_seen_at: string; backlog_estimate: number; online: boolean }> }
export interface ChannelSnapshot { id: string; instance_id: string; channel_id: number; channel_name: string; status: string; weight: number; models_text: string; captured_at: string }
export interface LogSample { instance_id: string; sample_kind: string; source_log_id: number; created_at: string; log_type: string; user_id: number; username: string; channel_id: number; model_name: string; token_id: number; token_name: string; total_tokens: number; quota: number; use_time: number; request_id: string; upstream_request_id: string; error_summary: string }
export interface AgentItem { id: string; instance_id: string; version: string; last_seen_at: string; last_sequence: number; last_log_id: number; source_latest_log_id: number; backlog_estimate: number; status: string; report_delay_ms: number; online: boolean; seconds_since_seen: number }
export interface ServerMetricItem { instance_id: string; collected_at: string; cpu_percent: number; memory_used_percent: number; disk_used_percent: number; network_rx_bytes_per_second: number; network_tx_bytes_per_second: number; load_1m: number }
export interface HealthCheckItem { instance_id: string; checked_at: string; target: string; status: string; http_status_code: number; latency_ms: number; error_summary: string }
export interface DockerStatusItem { instance_id: string; collected_at: string; container_name: string; status: string; running: boolean }
export interface UsageItem { dimension_type: string; dimension_key: string; display_key: string; request_count: number; total_tokens: number; prompt_tokens: number; completion_tokens: number; quota: number }

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
  alerts: (params: { instance_id?: string; active_only?: boolean; limit?: number } = {}) => client.request<ListResponse<AlertItem>>(`/api/dashboard/alerts${query(params)}`),
  alertEvents: (id: string, limit = 100) => client.request<ListResponse<AlertEvent>>(`/api/dashboard/alerts/${encodeURIComponent(id)}/events${query({ limit })}`),
  alertAction: (input: AlertActionInput) => client.request<DashboardOKResponse>('/api/dashboard/alerts/action', { method: 'POST', body: JSON.stringify(input) }),
  channelSnapshots: (params: { instance_id?: string; latest_only?: boolean; limit?: number } = {}) => client.request<ListResponse<ChannelSnapshot>>(`/api/dashboard/channel-snapshots${query(params)}`),
  logSamples: (params: { instance_id?: string; sample_kind?: string; model_name?: string; user_id?: string; request_id?: string; limit?: number; offset?: number } = {}) => client.request<ListResponse<LogSample>>(`/api/dashboard/log-samples${query(params)}`),
  agents: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<AgentItem>>(`/api/dashboard/agents${query(params)}`),
  serverMetrics: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<ServerMetricItem>>(`/api/dashboard/server-metrics${query(params)}`),
  healthChecks: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<HealthCheckItem>>(`/api/dashboard/health-checks${query(params)}`),
  dockerStatuses: (params: { instance_id?: string; limit?: number } = {}) => client.request<ListResponse<DockerStatusItem>>(`/api/dashboard/docker-statuses${query(params)}`),
  usage: (hours: number) => client.request<ListResponse<UsageItem>>(`/api/dashboard/usage${query({ hours })}`),
})
