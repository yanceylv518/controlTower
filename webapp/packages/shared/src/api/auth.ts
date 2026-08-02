import type { ApiClient } from '../client'

export interface CurrentUser { id?: number; username: string; role: string; scope_site: string; scope_user_ids: number[]; enabled: boolean }
export interface ScopedUser extends CurrentUser { id: number }
export interface OKResponse { ok: boolean }

export const authApi = (client: ApiClient) => ({
  login: (username: string, password: string) => client.request<CurrentUser>('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => client.request<OKResponse>('/api/auth/logout', { method: 'POST' }),
  me: () => client.request<CurrentUser>('/api/auth/me'),
  changePassword: (old_password: string, new_password: string) => client.request<OKResponse>('/api/auth/password', { method: 'POST', body: JSON.stringify({ old_password, new_password }) }),
  users: () => client.request<{ items: ScopedUser[] }>('/api/auth/users'),
  createUser: (body: { username: string; password: string; role: string; scope_site: string; scope_user_ids: number[] }) => client.request<OKResponse>('/api/auth/users', { method: 'POST', body: JSON.stringify(body) }),
  updateUser: (id: number, body: { role: string; scope_site: string; scope_user_ids: number[]; enabled: boolean }) => client.request<OKResponse>(`/api/auth/users/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
})
