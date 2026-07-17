<script setup lang="ts">
import { computed, ref, watch } from "vue";
import type { HealthCheckItem, ServerMetricItem } from "@ct/shared";
import { dashboard } from "../api";
import { useFiltersStore } from "../stores/filters";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import HoursSelect from "../components/HoursSelect.vue";
import StatusTag from "../components/StatusTag.vue";
import TrendChart, { type TrendSeries } from "../components/TrendChart.vue";
import {
  formatBytes,
  formatNumber,
  formatPercent,
  formatTime,
} from "../utils/format";

const filters = useFiltersStore();
const hours = ref(1);
const startTime = () =>
  new Date(Date.now() - hours.value * 3600000).toISOString();
const state = useAsyncData(async () => {
  const common = { instance_id: filters.instance_id || undefined, limit: 200 };
  const [agents, metrics, health, docker] = await Promise.all([
    dashboard.agents(common),
    dashboard.serverMetrics({
      ...common,
      start_time: startTime(),
      end_time: new Date().toISOString(),
    }),
    dashboard.healthChecks(common),
    dashboard.dockerStatuses(common),
  ]);
  return {
    agents: agents.items,
    metrics: metrics.items,
    health: health.items,
    docker: docker.items,
  };
});
watch(
  () => [filters.instance_id, hours.value],
  () => void state.reload(),
);
useAutoRefresh(state.reload);
const instanceName = (id: string) =>
  filters.instances.find((item) => item.instance_id === id)?.name || id;
const grouped = computed(() => {
  const groups = new Map<string, ServerMetricItem[]>();
  for (const item of state.data.value?.metrics || []) {
    const list = groups.get(item.instance_id) || [];
    list.push(item);
    groups.set(item.instance_id, list);
  }
  return [...groups].map(([id, items]) => ({
    id,
    name: instanceName(id),
    items: items.sort((a, b) => a.collected_at.localeCompare(b.collected_at)),
    latest: items[items.length - 1],
  }));
});
const latestHealth = computed(() => {
  const latest = new Map<string, HealthCheckItem>();
  for (const item of state.data.value?.health || []) {
    const key = `${item.instance_id}\u0000${item.target}`;
    const current = latest.get(key);
    if (!current || item.checked_at > current.checked_at) latest.set(key, item);
  }
  return [...latest.values()].sort((a, b) => {
    const aDown = a.status === "up" || a.status === "healthy" ? 0 : 1;
    const bDown = b.status === "up" || b.status === "healthy" ? 0 : 1;
    return bDown - aDown || a.target.localeCompare(b.target);
  });
});
const healthSummary = computed(() => {
  const normal = latestHealth.value.filter(
    (item) => item.status === "up" || item.status === "healthy",
  ).length;
  return { normal, abnormal: latestHealth.value.length - normal };
});
const stale = (time: string) => Date.now() - new Date(time).getTime() > 120000;
const tone = (value: number) =>
  value >= 90 ? "crit" : value >= 70 ? "warn" : "ok";
const percent = (value: number) => formatPercent(value / 100);
const points = (
  items: ServerMetricItem[],
  field: keyof ServerMetricItem,
  scale = 1,
) =>
  items.map(
    (item) => [item.collected_at, Number(item[field]) * scale] as [
      string,
      number,
    ],
  );
const percentSeries = (group: (typeof grouped.value)[number]): TrendSeries[] => [
  { name: "CPU", color: "#2f5fe0", data: points(group.items, "cpu_percent"), unit: "%" },
  { name: "内存", color: "#b96e0c", data: points(group.items, "memory_used_percent"), unit: "%" },
];
const diskSeries = (group: (typeof grouped.value)[number]): TrendSeries[] => [
  { name: "磁盘", color: "#1391a5", data: points(group.items, "disk_used_percent"), unit: "%" },
];
const networkSeries = (group: (typeof grouped.value)[number]): TrendSeries[] => [
  { name: "接收", color: "#2f5fe0", data: points(group.items, "network_rx_bytes_per_second", 1 / 1024), unit: " KB/s" },
  { name: "发送", color: "#178a5e", data: points(group.items, "network_tx_bytes_per_second", 1 / 1024), unit: " KB/s" },
];
</script>
<template>
  <AppShell title="系统状态">
    <template #tools><HoursSelect v-model="hours" /></template>
    <AsyncPanel
      :loading="state.loading.value"
      :error="state.error.value"
      :empty="!grouped.length"
      @retry="state.reload"
    >
      <template #empty>
        <el-empty
          description="Agent 上报系统指标后，这里会显示当前状态和趋势"
        />
      </template>
      <!-- 每实例一张机器卡：状态行 + 资源仪表 + 趋势 -->
      <section
        v-for="group in grouped"
        :key="group.id"
        class="panel machine-card"
      >
        <div class="machine-head">
          <h2>{{ group.name }}</h2>
          <el-tooltip :content="group.id"
            ><span class="machine-id">{{ group.id }}</span></el-tooltip
          >
          <StatusTag v-if="stale(group.latest.collected_at)" value="stale" />
          <span class="machine-time"
            >采集于 {{ formatTime(group.latest.collected_at) }}</span
          >
        </div>
        <div class="res-grid num">
          <div class="res-stat">
            <span class="res-label">CPU</span>
            <span :class="['res-value', tone(group.latest.cpu_percent)]">{{
              percent(group.latest.cpu_percent)
            }}</span>
            <span class="res-track"
              ><span
                :class="['res-fill', tone(group.latest.cpu_percent)]"
                :style="{ width: `${Math.min(group.latest.cpu_percent, 100)}%` }"
              ></span
            ></span>
          </div>
          <div class="res-stat">
            <span class="res-label">内存</span>
            <span
              :class="['res-value', tone(group.latest.memory_used_percent)]"
              >{{ percent(group.latest.memory_used_percent) }}</span
            >
            <span class="res-track"
              ><span
                :class="['res-fill', tone(group.latest.memory_used_percent)]"
                :style="{
                  width: `${Math.min(group.latest.memory_used_percent, 100)}%`,
                }"
              ></span
            ></span>
          </div>
          <div class="res-stat">
            <span class="res-label">磁盘</span>
            <span :class="['res-value', tone(group.latest.disk_used_percent)]">{{
              percent(group.latest.disk_used_percent)
            }}</span>
            <span class="res-track"
              ><span
                :class="['res-fill', tone(group.latest.disk_used_percent)]"
                :style="{
                  width: `${Math.min(group.latest.disk_used_percent, 100)}%`,
                }"
              ></span
            ></span>
          </div>
          <div class="res-stat">
            <span class="res-label">1m 负载</span>
            <span class="res-value">{{ group.latest.load_1m.toFixed(2) }}</span>
          </div>
          <div class="res-stat">
            <span class="res-label">网络接收</span>
            <span class="res-value"
              >{{ formatBytes(group.latest.network_rx_bytes_per_second) }}/s</span
            >
          </div>
          <div class="res-stat">
            <span class="res-label">网络发送</span>
            <span class="res-value"
              >{{ formatBytes(group.latest.network_tx_bytes_per_second) }}/s</span
            >
          </div>
        </div>
        <div class="machine-trends">
          <TrendChart title="CPU / 内存" :series="percentSeries(group)" percent />
          <TrendChart title="磁盘使用率" :series="diskSeries(group)" percent />
          <TrendChart title="网络收发速率" :series="networkSeries(group)" />
        </div>
        <el-collapse class="raw-metrics">
          <el-collapse-item title="原始采样（排障使用）" name="raw">
            <el-table :data="[...group.items].reverse()">
              <el-table-column label="时间" width="180">
                <template #default="s">{{
                  formatTime(s.row.collected_at)
                }}</template>
              </el-table-column>
              <el-table-column prop="cpu_percent" label="CPU %" align="right" />
              <el-table-column
                prop="memory_used_percent"
                label="内存 %"
                align="right"
              />
              <el-table-column
                prop="disk_used_percent"
                label="磁盘 %"
                align="right"
              />
              <el-table-column label="RX" align="right">
                <template #default="s"
                  >{{ formatBytes(s.row.network_rx_bytes_per_second) }}/s</template
                >
              </el-table-column>
              <el-table-column label="TX" align="right">
                <template #default="s"
                  >{{ formatBytes(s.row.network_tx_bytes_per_second) }}/s</template
                >
              </el-table-column>
            </el-table>
          </el-collapse-item>
        </el-collapse>
      </section>
    </AsyncPanel>
    <div v-if="state.data.value" class="support-grid">
      <section class="panel sub-panel">
        <h2>Agent</h2>
        <el-table :data="state.data.value.agents">
          <el-table-column prop="id" label="Agent" />
          <el-table-column label="状态" width="90">
            <template #default="s"
              ><StatusTag :value="s.row.online ? 'online' : 'offline'"
            /></template>
          </el-table-column>
          <el-table-column label="积压" align="right">
            <template #default="s">{{
              formatNumber(s.row.backlog_estimate)
            }}</template>
          </el-table-column>
          <el-table-column
            prop="report_delay_ms"
            label="上报延迟 ms"
            align="right"
          />
        </el-table>
      </section>
      <section class="panel sub-panel">
        <div class="support-panel-head">
          <h2>健康检查</h2>
          <div class="support-summary">
            <el-tag size="small" type="success" effect="plain">
              正常 {{ healthSummary.normal }}
            </el-tag>
            <el-tag
              v-if="healthSummary.abnormal"
              size="small"
              type="danger"
              effect="plain"
            >
              异常 {{ healthSummary.abnormal }}
            </el-tag>
          </div>
        </div>
        <p class="sub-note">每个目标仅展示最近一次检查，异常项优先。</p>
        <el-table :data="latestHealth">
          <el-table-column prop="target" label="目标" />
          <el-table-column label="状态" width="90">
            <template #default="s"><StatusTag :value="s.row.status" /></template>
          </el-table-column>
          <el-table-column prop="http_status_code" label="HTTP" width="72" align="right" />
          <el-table-column prop="latency_ms" label="延迟" width="82" align="right">
            <template #default="s">{{ s.row.latency_ms }} ms</template>
          </el-table-column>
        </el-table>
        <el-collapse
          v-if="state.data.value.health.length > latestHealth.length"
          class="health-history"
        >
          <el-collapse-item
            :title="`历史记录（${state.data.value.health.length}）`"
            name="history"
          >
            <el-table :data="state.data.value.health" max-height="320">
              <el-table-column label="时间" width="148">
                <template #default="s">{{ formatTime(s.row.checked_at) }}</template>
              </el-table-column>
              <el-table-column prop="target" label="目标" show-overflow-tooltip />
              <el-table-column label="状态" width="82">
                <template #default="s"><StatusTag :value="s.row.status" /></template>
              </el-table-column>
              <el-table-column prop="latency_ms" label="ms" width="68" align="right" />
            </el-table>
          </el-collapse-item>
        </el-collapse>
      </section>
      <section class="panel sub-panel">
        <h2>容器</h2>
        <el-table :data="state.data.value.docker">
          <el-table-column prop="container_name" label="容器" />
          <el-table-column label="状态" width="90">
            <template #default="s"
              ><StatusTag :value="s.row.running ? 'running' : 'stopped'"
            /></template>
          </el-table-column>
          <el-table-column prop="status" label="详情" show-overflow-tooltip />
        </el-table>
      </section>
    </div>
  </AppShell>
</template>
