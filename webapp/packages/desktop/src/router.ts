import { createRouter, createWebHistory } from 'vue-router'
import { ApiError } from '@ct/shared'
import { useAuthStore } from './stores/auth'
import { setUnauthorizedHandler } from './api'
import LoginView from './views/LoginView.vue'
import OverviewView from './views/OverviewView.vue'
export const router = createRouter({ history: createWebHistory('/next/'), routes: [{ path: '/login', name: 'login', component: LoginView }, { path: '/', name: 'overview', component: OverviewView }] })
router.beforeEach(async (to) => { const store = useAuthStore(); if (to.name === 'login' || store.user) return true; try { await store.load(); return true } catch (error) { if (error instanceof ApiError && error.status === 401) return { name: 'login', query: { redirect: to.fullPath } }; throw error } })
setUnauthorizedHandler(() => { const store = useAuthStore(); store.user = null; if (router.currentRoute.value.name !== 'login') void router.replace({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } }) })
