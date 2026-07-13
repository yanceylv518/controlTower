import type { ApiClient } from '../client'

export interface CurrentUser { username: string; role: string }
export interface OKResponse { ok: boolean }

export const authApi = (client: ApiClient) => ({
  login: (username: string, password: string) => client.request<CurrentUser>('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => client.request<OKResponse>('/api/auth/logout', { method: 'POST' }),
  me: () => client.request<CurrentUser>('/api/auth/me'),
  changePassword: (old_password: string, new_password: string) => client.request<OKResponse>('/api/auth/password', { method: 'POST', body: JSON.stringify({ old_password, new_password }) }),
})
