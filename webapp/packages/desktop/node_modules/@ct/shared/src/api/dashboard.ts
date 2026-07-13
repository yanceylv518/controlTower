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

const query = (values: Record<string, string | number | boolean | undefined>) => {
  const params = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => { if (value !== undefined && value !== '') params.set(key, String(value)) })
  const encoded = params.toString()
  return encoded ? `?${encoded}` : ''
}

export const dashboardApi = (client: ApiClient) => ({
  overview: () => client.request<Overview>('/api/dashboard/overview'),
  metrics: (params: { window?: string; latest?: boolean; dimension_type?: string; dimension_key?: string } = {}) => client.request<ListResponse<MetricItem>>(`/api/dashboard/metrics${query(params)}`),
  metricHistory: (params: { window: string; dimension_type: string; dimension_key: string; hours: number }) => client.request<ListResponse<MetricItem>>(`/api/dashboard/metric-history${query(params)}`),
  alerts: (params: { active_only?: boolean; limit?: number } = {}) => client.request<ListResponse<AlertItem>>(`/api/dashboard/alerts${query(params)}`),
  alertEvents: (id: string, limit = 100) => client.request<ListResponse<AlertEvent>>(`/api/dashboard/alerts/${encodeURIComponent(id)}/events${query({ limit })}`),
  alertAction: (input: AlertActionInput) => client.request<DashboardOKResponse>('/api/dashboard/alerts/action', { method: 'POST', body: JSON.stringify(input) }),
})
