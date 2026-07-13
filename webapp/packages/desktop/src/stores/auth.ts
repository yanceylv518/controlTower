import { defineStore } from 'pinia'
import type { CurrentUser } from '@ct/shared'
import { auth } from '../api'
export const useAuthStore = defineStore('auth', { state: () => ({ user: null as CurrentUser | null }), actions: { async load() { this.user = await auth.me(); return this.user }, async login(username: string, password: string) { this.user = await auth.login(username, password) }, async logout() { try { await auth.logout() } finally { this.user = null } } } })
