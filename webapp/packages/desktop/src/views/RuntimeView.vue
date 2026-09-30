<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { siteOf, type HealthCheckItem, type ServerMetricItem } from "@ct/shared";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { loadRuntimeHistory } from "../utils/runtimeHistory";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import HoursSelect from "../components/HoursSelect.vue";
import StatusTag from "../components/StatusTag.vue";
import TrendChart, { type TrendSeries } from "../components/TrendChart.vue";
import { formatNumber, formatPercent, formatTime } from "../utils/format";

const filters = useFiltersStore();
const hours = ref(1);
const state = useAsyncData(async () => {
  const end = Date.now();
  const startTime = new Date(end - hours.value * 3600000).toISOString();
  const endTime = new Date(end).toISOString();
  await filters.loadInstances();
  const siteInstanceIDs = new Set(filters.instances
    .filter(item => item.enabled && siteOf(item) === filters.site_id)
    .map(item => item.instance_id));
  const [agents, metrics, health] = await Promise.all([
    dashboard.agents({ limit: 200 }),
    loadRuntimeHistory(dashboard.serverMetrics, [...siteInstanceIDs], startTime, endTime),
    dashboard.healthChecks({ limit: 200 }),
  ]);
  return {
    agents: agents.items.filter(item => siteInstanceIDs.has(item.instance_id)),
    metrics,
    health: health.items.filter(item => siteInstanceIDs.has(item.instance_id)),
  };
});
watch(() => [filters.site_id, hours.value], () => void state.reload());
useAutoRefresh(state.reload);
const stale = (time: string) => !Number.isFinite(Date.parse(time)) || Date.now() - Date.parse(time) > 120000;
const healthy = (item: HealthCheckItem) => item.status === "up" || item.status === "healthy";
const tone = (value: number) => value >= 90 ? "crit" : value >= 70 ? "warn" : "ok";
const resources = [
  { field: "cpu_percent", label: "CPU", color: "#2f5fe0" },
  { field: "memory_used_percent", label: "内存", color: "#b96e0c" },
  { field: "disk_used_percent", label: "磁盘", color: "#1391a5" },
] as const;
const grouped = computed(() => filters.instances
  .filter(item => item.enabled && siteOf(item) === filters.site_id)
  .map(instance => {
    const items = (state.data.value?.metrics || []).filter(item => item.instance_id === instance.instance_id)
      .sort((a, b) => a.collected_at.localeCompare(b.collected_at));
    const health = (state.data.value?.health || []).filter(item => item.instance_id === instance.instance_id)
      .sort((a, b) => b.checked_at.localeCompare(a.checked_at));
    const latest = new Map<string, HealthCheckItem>();
    for (const item of health) if (!latest.has(item.target)) latest.set(item.target, item);
    const checks = [...latest.values()].sort((a, b) => Number(healthy(a)) - Number(healthy(b)) || a.target.localeCompare(b.target));
    const abnormal = checks.filter(item => !healthy(item)).length;
    const outdated = checks.filter(item => stale(item.checked_at)).length;
    return {
      id: instance.instance_id, name: instance.name || instance.instance_id, items,
      latest: items.at(-1), health, checks, abnormal, outdated,
      agents: (state.data.value?.agents || []).filter(item => item.instance_id === instance.instance_id),
    };
  }));
const series = (items: ServerMetricItem[]): TrendSeries[] => resources.map(resource => ({
  name: resource.label, color: resource.color, unit: "%",
  data: items.map(item => [item.collected_at, item[resource.field]]),
}));
</script>

<template>
  <AppShell title="系统状态">
    <template #tools><HoursSelect v-model="hours" /></template>
    <div class="runtime-page">
      <div class="runtime-heading">
        <div><span class="runtime-eyebrow">实例运行概况</span><h2>{{ filters.site_id || '当前站点' }}</h2></div>
        <span class="runtime-count">{{ grouped.length }} 个实例 · 最近 {{ hours }} 小时趋势</span>
      </div>
      <AsyncPanel :loading="state.loading.value" :error="state.error.value" :empty="!grouped.length"
        empty-text="当前站点暂无启用的实例" @retry="state.reload">
        <div class="machine-grid">
          <section v-for="group in grouped" :key="group.id" class="panel machine-card">
            <header class="machine-head">
              <div class="machine-identity"><h2>{{ group.name }}</h2><span class="machine-id">{{ group.id }}</span></div>
              <el-tag v-if="!group.agents.length" size="small" type="info">Agent 未上报</el-tag>
              <el-tag v-else size="small" :type="group.agents.every(agent => agent.online) ? 'success' : 'danger'">
                {{ group.agents.every(agent => agent.online) ? 'Agent 在线' : 'Agent 离线' }}
              </el-tag>
            </header>
            <div class="machine-time">
              <span>采集于 {{ formatTime(group.latest?.collected_at) }}</span>
              <el-tag v-if="group.latest && stale(group.latest.collected_at)" size="small" type="warning">采样已过期</el-tag>
            </div>
            <div class="res-grid num">
              <div v-for="resource in resources" :key="resource.field" class="res-stat">
                <span class="res-label">{{ resource.label }}</span>
                <span :class="['res-value', group.latest ? tone(group.latest[resource.field]) : '']">
                  {{ group.latest ? formatPercent(group.latest[resource.field] / 100) : '—' }}
                </span>
                <span v-if="group.latest" class="res-track"><span :class="['res-fill', tone(group.latest[resource.field])]"
                  :style="{ width: `${Math.max(0, Math.min(group.latest[resource.field], 100))}%` }"></span></span>
              </div>
              <div class="res-stat">
                <el-tooltip content="最近 1 分钟运行中或等待 CPU、磁盘等 I/O 的平均任务数。不是百分比，需结合 CPU 核数判断。">
                  <span class="res-label load-label" tabindex="0">负载 · 1分钟</span>
                </el-tooltip>
                <span class="res-value">{{ group.latest?.load_1m.toFixed(2) ?? '—' }}</span>
              </div>
            </div>
            <div class="machine-trends"><TrendChart title="资源使用率" :series="series(group.items)" percent /></div>
            <div class="health-strip">
              <span>健康检查</span>
              <el-tag v-if="!group.checks.length" size="small" type="info">暂无检查</el-tag>
              <template v-else>
                <el-tag v-if="group.abnormal" size="small" type="danger">{{ group.abnormal }} 项异常</el-tag>
                <el-tag v-if="group.outdated" size="small" type="warning">{{ group.outdated }} 项已过期</el-tag>
                <el-tag v-if="!group.abnormal && !group.outdated" size="small" type="success">{{ group.checks.length }} 项正常</el-tag>
              </template>
            </div>
            <el-collapse class="runtime-details">
              <el-collapse-item title="运行详情" name="status">
                <h3>Agent</h3>
                <p v-if="!group.agents.length" class="detail-note">暂无 Agent 上报</p>
                <article v-for="agent in group.agents" :key="agent.id" class="agent-detail">
                  <div class="detail-line"><strong>{{ agent.id }}</strong><StatusTag :value="agent.online ? 'online' : 'offline'" /></div>
                  <div class="detail-line"><span>日志积压</span><span>{{ formatNumber(agent.backlog_estimate) }}</span></div>
                  <div class="detail-line"><span>上报延迟</span><span>{{ formatNumber(agent.report_delay_ms) }} ms</span></div>
                  <div class="detail-line"><span>最近心跳</span><span>{{ formatTime(agent.last_seen_at) }}</span></div>
                </article>
                <h3>最新健康检查</h3>
                <p v-if="!group.checks.length" class="detail-note">暂无检查结果</p>
                <article v-for="check in group.checks" :key="check.target" class="check-detail">
                  <div class="detail-line"><strong>{{ check.target }}</strong><StatusTag :value="healthy(check) ? 'up' : 'down'" /></div>
                  <div class="detail-line"><span>HTTP {{ check.http_status_code || '—' }} · {{ formatNumber(check.latency_ms) }} ms</span>
                    <el-tag v-if="stale(check.checked_at)" size="small" type="warning">已过期</el-tag></div>
                  <p v-if="check.error_summary" class="check-error">{{ check.error_summary }}</p>
                  <span class="detail-note">{{ formatTime(check.checked_at) }}</span>
                </article>
                <el-collapse v-if="group.health.length > group.checks.length" class="health-history">
                  <el-collapse-item :title="`检查历史（${group.health.length}）`" name="history">
                    <el-table v-mobile-cards :data="group.health" max-height="280">
                      <el-table-column label="时间" width="160"><template #default="s">{{ formatTime(s.row.checked_at) }}</template></el-table-column>
                      <el-table-column prop="target" label="目标" min-width="120" show-overflow-tooltip />
                      <el-table-column label="状态" width="80"><template #default="s"><StatusTag :value="healthy(s.row) ? 'up' : 'down'" /></template></el-table-column>
                      <el-table-column prop="latency_ms" label="延迟 ms" width="90" />
                    </el-table>
                  </el-collapse-item>
                </el-collapse>
              </el-collapse-item>
              <el-collapse-item title="原始采样（排障使用）" name="raw" class="raw-metrics">
                <el-table v-mobile-cards :data="[...group.items].reverse()" max-height="280" empty-text="暂无采样">
                  <el-table-column label="时间" width="160"><template #default="s">{{ formatTime(s.row.collected_at) }}</template></el-table-column>
                  <el-table-column v-for="resource in resources" :key="resource.field" :prop="resource.field" :label="`${resource.label} %`" min-width="80" align="right" />
                  <el-table-column prop="load_1m" label="1分钟负载" min-width="100" align="right" />
                </el-table>
              </el-collapse-item>
            </el-collapse>
          </section>
        </div>
      </AsyncPanel>
    </div>
  </AppShell>
</template>

<style scoped>
.runtime-heading { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 18px; }
.runtime-eyebrow, .runtime-count, .detail-note { color: var(--ct-ink-3); font-size: 12px; }
.runtime-heading h2 { margin: 4px 0 0; font-size: 20px; overflow-wrap: anywhere; }
.machine-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); align-items: start; gap: 12px; }
.machine-grid .machine-card { min-width: 0; min-height: 0; margin: 0; padding: 16px; }
.machine-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; margin-bottom: 8px; }
.machine-identity { min-width: 0; }
.machine-head h2 { margin: 0 0 3px; font-size: 15px; overflow-wrap: anywhere; }
.machine-id { display: block; overflow-wrap: anywhere; }
.machine-head > .el-tag { flex-shrink: 0; }
.machine-time { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin: 0 0 14px; min-height: 24px; font-size: 11px; }
.res-grid { grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 6px; margin-bottom: 14px; }
.res-stat { min-width: 0; padding: 8px; }
.res-label { letter-spacing: 0; white-space: nowrap; }
.load-label { cursor: help; text-decoration: underline dotted; text-underline-offset: 3px; }
.machine-trends { display: block; }
.machine-trends :deep(.trend-chart) { padding: 0; border: 0; box-shadow: none; background: transparent; }
.machine-trends :deep(.trend-header) { margin-bottom: 8px; }
.machine-trends :deep(.trend-chart-canvas), .machine-trends :deep(.el-empty) { height: 200px; }
.health-strip { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; padding: 12px 0; border-top: 1px solid var(--ct-line); margin-top: 10px; }
.health-strip > span:first-child { margin-right: auto; font-size: 12px; color: var(--ct-ink-2); }
.runtime-details { border-bottom: 0; }
.runtime-details :deep(.el-collapse-item__header) { height: 36px; font-size: 12px; }
.raw-metrics { margin: 0; }
.runtime-details h3 { font-size: 12px; margin: 12px 0 8px; }
.detail-line { display: flex; justify-content: space-between; align-items: flex-start; flex-wrap: wrap; gap: 4px 8px; margin-bottom: 5px; }
.detail-line strong { overflow-wrap: anywhere; min-width: 0; flex: 1; }
.agent-detail, .check-detail { padding: 8px 0; border-bottom: 1px solid var(--ct-line); }
.check-error { color: var(--ct-crit); overflow-wrap: anywhere; margin: 6px 0; }
@media (max-width: 1279px) { .machine-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 900px) { .machine-grid { grid-template-columns: minmax(0, 1fr); } }
@media (max-width: 480px) {
  .runtime-heading { align-items: flex-start; flex-direction: column; gap: 6px; }
  .res-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
