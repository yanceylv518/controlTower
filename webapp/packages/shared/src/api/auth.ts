import type { ApiClient } from '../client'

export interface CurrentUser { id?: number; username: string; role: string; scope_site: string; scope_user_ids: number[]; enabled: boolean; display_name?: string; permissions?: string[]; permission_preset_id?: number }
export interface ScopedUser extends CurrentUser { id: number }
export interface PermissionOption { key: string; label: string; description: string }
export interface OKResponse { ok: boolean }
export interface PermissionPreset { id: number; name: string; description: string; permissions: string[]; version: number; created_by: string; updated_by: string; created_at: string; updated_at: string; bound_accounts?: number; synced_accounts?: number }
export interface PermissionPresetInput { name: string; description: string; permissions: string[]; version?: number }
export interface PermissionPresetList { items: PermissionPreset[]; max_presets: number; max_apply_accounts: number; binding_supported?: boolean }
export interface PermissionPresetApply { version: number; user_ids: number[]; mode: 'replace' | 'merge'; expected_permissions: Record<string, string[]>; expected_preset_ids?: Record<string, number> }
export interface AccountPermissionBinding { permission_preset_id?: number; permission_preset_version?: number; expected_permissions?: string[]; expected_permission_preset_id?: number }

export const authApi = (client: ApiClient) => ({
  login: (username: string, password: string) => client.request<CurrentUser>('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => client.request<OKResponse>('/api/auth/logout', { method: 'POST' }),
  me: () => client.request<CurrentUser>('/api/auth/me'),
  changePassword: (old_password: string, new_password: string) => client.request<OKResponse>('/api/auth/password', { method: 'POST', body: JSON.stringify({ old_password, new_password }) }),
  users: (signal?: AbortSignal) => client.request<{ items: ScopedUser[]; permissions: PermissionOption[] }>('/api/auth/users', { signal }),
  createUser: (body: AccountPermissionBinding & { username: string; password: string; role: string; scope_site: string; scope_user_ids: number[]; display_name?: string; permissions?: string[] }) => client.request<OKResponse>('/api/auth/users', { method: 'POST', body: JSON.stringify(body), signal: AbortSignal.timeout(15000) }),
  updateUser: (id: number, body: AccountPermissionBinding & { role: string; scope_site: string; scope_user_ids: number[]; enabled: boolean; display_name?: string; permissions?: string[] }) => client.request<OKResponse>(`/api/auth/users/${id}`, { method: 'PUT', body: JSON.stringify(body), signal: AbortSignal.timeout(15000) }),
  resetPassword: (id: number, password: string) => client.request<OKResponse>(`/api/auth/users/${id}/password`, { method: 'POST', body: JSON.stringify({ password }), signal: AbortSignal.timeout(15000) }),
  permissionPresets: (signal?: AbortSignal) => client.request<PermissionPresetList>('/api/auth/permission-presets', { signal }),
  createPermissionPreset: (body: PermissionPresetInput) => client.request<PermissionPreset>('/api/auth/permission-presets', { method: 'POST', body: JSON.stringify(body), signal: AbortSignal.timeout(15000) }),
  updatePermissionPreset: (id: number, body: PermissionPresetInput) => client.request<PermissionPreset>(`/api/auth/permission-presets/${id}`, { method: 'PUT', body: JSON.stringify(body), signal: AbortSignal.timeout(15000) }),
  deletePermissionPreset: (id: number, version: number) => client.request<OKResponse>(`/api/auth/permission-presets/${id}`, { method: 'DELETE', body: JSON.stringify({ version }), signal: AbortSignal.timeout(15000) }),
  applyPermissionPreset: (id: number, body: PermissionPresetApply) => client.request<{ ok: boolean; changed: number; selected: number }>(`/api/auth/permission-presets/${id}/apply`, { method: 'POST', body: JSON.stringify(body), signal: AbortSignal.timeout(15000) }),
})
