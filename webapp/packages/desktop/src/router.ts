import { createRouter, createWebHistory } from 'vue-router'
import { ApiError } from '@ct/shared'
import { useAuthStore } from './stores/auth'
import { setUnauthorizedHandler } from './api'
import LoginView from './views/LoginView.vue'
import OverviewView from './views/OverviewView.vue'
import DimensionView from './views/DimensionView.vue'
import SamplesView from './views/SamplesView.vue'
import RuntimeView from './views/RuntimeView.vue'
import UsageView from './views/UsageView.vue'
import AlertsView from './views/AlertsView.vue'
import NotificationsView from './views/NotificationsView.vue'
import InstancesView from './views/InstancesView.vue'
import AuditsView from './views/AuditsView.vue'
import SettingsView from './views/SettingsView.vue'
import NotFoundView from './views/NotFoundView.vue'
import LatencyView from './views/LatencyView.vue'
export const router = createRouter({ history: createWebHistory('/'), routes: [
  { path: '/login', name: 'login', component: LoginView, meta: { title: '登录' } }, { path: '/', name: 'overview', component: OverviewView, meta: { title: '运行总览' } },
  { path: '/customers', component: DimensionView, props: { kind: 'customers' }, meta: { title: '客户监控' } }, { path: '/channels', component: DimensionView, props: { kind: 'channels' }, meta: { title: '渠道监控' } }, { path: '/models', component: DimensionView, props: { kind: 'models' }, meta: { title: '模型监控' } },
  { path: '/samples', component: SamplesView, meta: { title: '样本分析' } }, { path: '/runtime', component: RuntimeView, meta: { title: '系统状态' } }, { path: '/usage', component: UsageView, meta: { title: '用量统计' } },
  { path: '/latency', component: LatencyView, meta: { title: '延时分诊' } },
  { path: '/alerts', component: AlertsView, meta: { title: '告警中心' } }, { path: '/notifications', component: NotificationsView, meta: { title: '通知设置' } }, { path: '/instances', component: InstancesView, meta: { title: '实例管理' } }, { path: '/audits', component: AuditsView, meta: { title: '操作审计' } },
  { path: '/settings', component: SettingsView, meta: { title: '设置' } }, { path: '/:pathMatch(.*)*', component: NotFoundView, meta: { title: '页面不存在' } },
] })
router.beforeEach(async (to) => { const store = useAuthStore(); if (to.name === 'login' || store.user) return true; try { await store.load(); return true } catch (error) { if (error instanceof ApiError && error.status === 401) return { name: 'login', query: { redirect: to.fullPath } }; throw error } })
setUnauthorizedHandler(() => { const store = useAuthStore(); store.user = null; if (router.currentRoute.value.name !== 'login') void router.replace({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } }) })
router.afterEach(to => { document.title = `${String(to.meta.title || 'Control Tower')} · Control Tower` })
