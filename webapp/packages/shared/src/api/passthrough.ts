import type { ApiClient } from '../client'

export interface ReadonlyUser { id: number; username: string; display_name: string; quota: number; used_quota: number; status: number }
export interface ReadonlyLog { id: number; user_id: number; created_at: string; type: number; username: string; model_name: string; channel_id: number; token_name: string; prompt_tokens: number; completion_tokens: number; quota: number; use_time: number; request_id: string; content_summary: string }
export interface ReadonlyResponse<T> { items: T[]; configured: boolean }
const query = (params: Record<string, string | number | undefined>) => { const out = new URLSearchParams(); Object.entries(params).forEach(([key, value]) => { if (value !== undefined && value !== '') out.set(key, String(value)) }); const text = out.toString(); return text ? `?${text}` : '' }
export const passthroughApi = (client: ApiClient) => ({
  users: (params: { site?: string; user_ids?: string; limit?: number; offset?: number }) => client.request<ReadonlyResponse<ReadonlyUser>>(`/api/dashboard/passthrough/users${query(params)}`),
  logs: (params: { site?: string; user_ids?: string; start_time?: string; end_time?: string; limit?: number; offset?: number }) => client.request<ReadonlyResponse<ReadonlyLog>>(`/api/dashboard/passthrough/logs${query(params)}`),
})
