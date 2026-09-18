import { defineStore } from 'pinia'
import { ApiError } from '@ct/shared'
import { client } from '../api'

export const menuGroups = [
  { title: '总览', items: [['/', '总览']] },
  { title: '监控分析', items: [['/customers', '客户监控'], ['/channels', '渠道监控'], ['/models', '模型监控'], ['/runtime', '系统状态']] },
  { title: '数据查询', items: [['/usage', '用量统计'], ['/readonly-users', '用户管理'], ['/readonly-logs', '使用日志'], ['/container-logs', '容器日志']] },
  { title: '账单管理', items: [['/billing', '用户账单'], ['/billing/channels', '上游账单'], ['/billing/tasks', '账单任务'], ['/billing/discounts', '渠道折扣']] },
  { title: '系统管理', items: [['/tuning', '调权中心'], ['/alerts', '告警中心'], ['/notifications', '通知设置'], ['/instances', '实例管理'], ['/log-archives', '日志归档'], ['/access-users', '账号管理'], ['/models/manage', '模型广场'], ['/billing/upstreams', '上游管理'], ['/settings', '系统设置'], ['/audits', '操作审计']] },
] as const
type Response = { items: Record<string, boolean> }
let pending: Promise<void> | null = null
export const useMenuVisibilityStore = defineStore('menuVisibility', {
  state: () => ({ items: {} as Record<string, boolean>, ready: false, supported: true, loading: false, error: '', revision: 0 }),
  getters: { visible: state => (path: string) => state.ready && state.items[path] !== false },
  actions: {
    async load() {
      if (pending) return pending
      this.loading = true
      const revision = this.revision
      pending = (async () => {
      try {
        const result = await client.request<Response>('/api/dashboard/menu-visibility')
        if (revision !== this.revision) return
        this.items = result.items; this.ready = true; this.supported = true; this.error = ''
      } catch (error) {
        if (revision !== this.revision) return
        if (error instanceof ApiError && error.status === 404) {
          this.items = {}; this.ready = true; this.supported = false
          this.error = '当前后端尚未支持菜单显示设置，请升级 Server 后使用。'
        } else { this.error = '菜单配置加载失败，请重试。' }
      } finally { this.loading = false }
      })()
      try { await pending } finally { pending = null }
    },
    async save(items: Record<string, boolean>) {
      const result = await client.request<Response>('/api/dashboard/menu-visibility', { method: 'PUT', body: JSON.stringify({ items }) })
      this.revision++
      this.items = result.items; this.ready = true; this.error = ''
    },
  },
})
