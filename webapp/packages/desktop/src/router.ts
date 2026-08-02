import { createRouter, createWebHistory } from 'vue-router'
import { ApiError } from '@ct/shared'
import { useAuthStore } from './stores/auth'
import { setUnauthorizedHandler } from './api'
import LoginView from './views/LoginView.vue'
import OverviewView from './views/OverviewView.vue'
import DimensionView from './views/DimensionView.vue'
import CustomerMonitorView from './views/CustomerMonitorView.vue'
import DimensionDetailView from './views/DimensionDetailView.vue'
import SamplesView from './views/SamplesView.vue'
import RuntimeView from './views/RuntimeView.vue'
import UsageView from './views/UsageView.vue'
import AlertsView from './views/AlertsView.vue'
import NotificationsView from './views/NotificationsView.vue'
import InstancesView from './views/InstancesSiteView.vue'
import AuditsView from './views/AuditsView.vue'
import SettingsView from './views/SettingsView.vue'
import NotFoundView from './views/NotFoundView.vue'
import LatencyView from './views/LatencyView.vue'
import TuningView from './views/ContinuousTuningView.vue'
import UsersView from './views/UsersView.vue'
import ReadonlyUsersView from './views/ReadonlyUsersView.vue'
import ReadonlyLogsView from './views/ReadonlyLogsView.vue'
import BillingView from './views/BillingView.vue'
import BillingPricingView from './views/BillingPricingView.vue'
export const router = createRouter({ history: createWebHistory('/'), routes: [
  { path: '/readonly-users', component: ReadonlyUsersView, meta: { title: '用户管理' } },
  { path: '/readonly-logs', component: ReadonlyLogsView, meta: { title: '使用日志' } },
  { path: '/billing', component: BillingView, meta: { title: '用户账单' } },
  { path: '/billing/pricing', component: BillingPricingView, meta: { title: '账单计价', adminOnly: true } },
  { path: '/customers', component: CustomerMonitorView, meta: { title: '客户监控' } },
  { path: '/login', name: 'login', component: LoginView, meta: { title: '登录' } }, { path: '/', name: 'overview', component: OverviewView, meta: { title: '运行总览' } },
  { path: '/channels', component: DimensionView, props: { kind: 'channels' }, meta: { title: '渠道监控' } }, { path: '/models', component: DimensionView, props: { kind: 'models' }, meta: { title: '模型监控' } },
  { path: '/customers/:key', component: DimensionDetailView, props: route => ({ kind: 'customers', dimensionKey: String(route.params.key) }), meta: { title: '客户详情' } },
  { path: '/channels/:key', component: DimensionDetailView, props: route => ({ kind: 'channels', dimensionKey: String(route.params.key) }), meta: { title: '渠道详情' } },
  { path: '/models/:key', component: DimensionDetailView, props: route => ({ kind: 'models', dimensionKey: String(route.params.key) }), meta: { title: '模型详情' } },
  { path: '/samples', component: SamplesView, meta: { title: '样本分析' } }, { path: '/runtime', component: RuntimeView, meta: { title: '系统状态' } }, { path: '/usage', component: UsageView, meta: { title: '用量统计' } },
  { path: '/latency', component: LatencyView, meta: { title: '延时分诊' } },
  { path: '/tuning', component: TuningView, meta: { title: '调权中心' } },
  { path: '/alerts', component: AlertsView, meta: { title: '告警中心' } }, { path: '/notifications', component: NotificationsView, meta: { title: '通知设置' } }, { path: '/instances', component: InstancesView, meta: { title: '实例管理' } }, { path: '/audits', component: AuditsView, meta: { title: '操作审计' } },
  { path: '/settings', component: SettingsView, meta: { title: '设置' } },
  { path: '/access-users', component: UsersView, meta: { title: '访问账号', adminOnly: true } },
  { path: '/:pathMatch(.*)*', component: NotFoundView, meta: { title: '页面不存在' } },
] })
router.beforeEach(async (to) => { const store = useAuthStore(); if (to.name === 'login') return true; try { if (!store.user) await store.load(); if (store.user?.role === 'viewer' && !to.path.startsWith('/customers') && to.path !== '/readonly-users' && to.path !== '/readonly-logs' && to.path !== '/billing') return '/customers'; if (to.meta.adminOnly && store.user?.role !== 'admin') return '/customers'; return true } catch (error) { if (error instanceof ApiError && error.status === 401) return { name: 'login', query: { redirect: to.fullPath } }; throw error } })
setUnauthorizedHandler(() => { const store = useAuthStore(); store.user = null; if (router.currentRoute.value.name !== 'login') void router.replace({ name: 'login', query: { redirect: router.currentRoute.value.fullPath } }) })
router.afterEach(to => { document.title = `${String(to.meta.title || 'Control Tower')} · Control Tower` })
