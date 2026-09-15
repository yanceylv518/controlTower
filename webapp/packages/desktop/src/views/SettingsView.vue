<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { ApiError, type SystemSettingItem } from "@ct/shared";
import { ElMessage } from "element-plus";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import VoiceAlertsSettings from "../components/VoiceAlertsSettings.vue";

import { usePrefsStore } from "../stores/prefs";

const prefs = usePrefsStore();
const loading = ref(false);
const saving = ref(false);
const activeTab = ref('voice');
const items = ref<Record<string, SystemSettingItem>>({});
const values = reactive<Record<string, string | number>>({});
type Field = readonly [string, string, number, number];
const sections: ReadonlyArray<{ title: string; note: string; fields: readonly Field[] }> = [
  {
    title: "TTFT 图表阈值",
    note: "用于客户、渠道、模型监控图表的等高分段轴；P50 必须小于 P90，P90 必须小于 P95",
    fields: [
      ["CT_TTFT_P50_THRESHOLD_SECONDS", "P50 阈值（秒）", 0.1, 600],
      ["CT_TTFT_P90_THRESHOLD_SECONDS", "P90 阈值（秒）", 0.1, 600],
      ["CT_TTFT_P95_THRESHOLD_SECONDS", "P95 阈值（秒）", 0.1, 600],
    ],
  },
  {
    title: "数据保留",
    note: "超过保留期的数据由每日清理任务删除，修改后下一轮生效",
    fields: [
      ["CT_RETENTION_DETAIL_DAYS", "明细数据（天）", 1, 365],
      ["CT_RETENTION_METRIC5M_DAYS", "5 分钟指标（天）", 1, 365],
      ["CT_RETENTION_RUNTIME_DAYS", "运行状态（天）", 1, 365],
      ["CT_RETENTION_HEALTH_HOURS", "健康检查（小时）", 1, 168],
      ["CT_RETENTION_ALERTS_DAYS", "告警（天）", 1, 365],
    ],
  },
  {
    title: "系统告警",
    note: "实例离线及 CPU、内存、磁盘阈值；警告阈值必须小于严重阈值",
    fields: [
      ["CT_OFFLINE_ALERT_SECONDS", "实例离线（秒）", 1, 86400],
      ["CT_CPU_WARN_PERCENT", "CPU 警告（%）", 1, 100],
      ["CT_CPU_CRIT_PERCENT", "CPU 严重（%）", 1, 100],
      ["CT_MEMORY_WARN_PERCENT", "内存警告（%）", 1, 100],
      ["CT_MEMORY_CRIT_PERCENT", "内存严重（%）", 1, 100],
      ["CT_DISK_WARN_PERCENT", "磁盘警告（%）", 1, 100],
      ["CT_DISK_CRIT_PERCENT", "磁盘严重（%）", 1, 100],
    ],
  },
  {
    title: "请求告警",
    note: "错误率和响应耗时阈值；警告阈值必须小于严重阈值",
    fields: [
      ["CT_ERROR_RATE_WARN_PERCENT", "错误率警告（%）", 1, 100],
      ["CT_ERROR_RATE_CRIT_PERCENT", "错误率严重（%）", 1, 100],
      ["CT_P95_WARN_SECONDS", "P95 警告（秒）", 0.5, 600],
      ["CT_P95_CRIT_SECONDS", "P95 严重（秒）", 0.5, 600],
    ],
  },
  {
    title: "余额告警",
    note: "按最近一段时间的用户消费速度预测余额可用天数；严重天数必须小于警告天数",
    fields: [
      ["CT_BALANCE_LOOKBACK_HOURS", "消费统计窗口（小时）", 24, 168],
      ["CT_BALANCE_WARN_DAYS", "预计可用警告（天）", 0.25, 90],
      ["CT_BALANCE_CRIT_DAYS", "预计可用严重（天）", 0.25, 90],
      ["CT_BALANCE_MIN_REQUESTS", "最小请求样本数", 1, 100000],
    ],
  },
];

const sourceLabels: Record<string, string> = {
  db: "已修改",
  env: "环境变量",
  default: "默认",
};
const editableKeys = new Set(sections.flatMap(section => section.fields.map(field => field[0])));
const sectionOrder = ["余额告警", "系统告警", "请求告警", "TTFT 图表阈值", "数据保留"];
const displaySections = [...sections].sort((a, b) => sectionOrder.indexOf(a.title) - sectionOrder.indexOf(b.title));
const displayColumns = [displaySections.filter((_, i) => i % 2 === 0), displaySections.filter((_, i) => i % 2 === 1)];
async function load() {
  loading.value = true;
  try {
    const response = await dashboard.settings();
    items.value = response.items;
    Object.entries(response.items).filter(([key]) => editableKeys.has(key)).forEach(
      ([key, item]) =>
        (values[key] =
          Number(item.value)),
    );
  } finally {
    loading.value = false;
  }
}
async function save() {
  saving.value = true;
  try {
    const payload = Object.fromEntries(
      Object.entries(values).map(([key, value]) => [key, String(value)]),
    );
    const response = await dashboard.saveSettings(payload);
    items.value = response.items;
    await prefs.load(true);
    ElMessage.success("设置已保存，将在下一轮任务中生效");
  } catch (e) {
    ElMessage.error(
      e instanceof ApiError && e.status === 400
        ? "设置校验失败，请检查阈值范围与 warn/crit 大小关系"
        : "保存失败",
    );
  } finally {
    saving.value = false;
  }
}
onMounted(load);
</script>
<template>
  <AppShell title="设置">
    <template #tools>
      <el-radio-group v-model="activeTab" size="small" aria-label="设置分类">
        <el-radio-button value="voice">电话预警</el-radio-button>
        <el-radio-button value="system">系统与监控</el-radio-button>
      </el-radio-group>
      <el-button v-if="activeTab === 'system'" type="primary" :loading="saving" :disabled="loading" @click="save"
        >保存系统设置</el-button
      >
    </template>
    <div class="settings-page">
      <VoiceAlertsSettings v-show="activeTab === 'voice'" />
      <div v-show="activeTab === 'system'" v-loading="loading" class="system-settings-grid">
       <div v-for="(column, index) in displayColumns" :key="index" class="settings-column">
        <section
        v-for="section in column"
        :key="section.title"
        class="panel sub-panel"
      >
        <h2>{{ section.title }}</h2>
        <p class="sub-note">{{ section.note }}</p>
        <div class="field-grid">
          <div v-for="field in section.fields" :key="field[0]" class="field-item">
            <label>{{ field[1] }}</label>
            <el-input-number
              v-model="values[field[0]] as number"
              :min="field[2]"
              :max="field[3]"
              :step="field[2] < 1 ? field[2] : 1"
              controls-position="right"
              size="small"
            />
            <span class="field-meta">
              <span
                :class="[
                  'source-pill',
                  items[field[0]]?.source === 'db' ? 'db' : '',
                ]"
                >{{ sourceLabels[items[field[0]]?.source] || "—" }}</span
              >
              默认 {{ items[field[0]]?.default }}
            </span>
          </div>

        </div>
        </section>
       </div>
      </div>
    </div>
  </AppShell>
</template>

<style scoped>
.settings-page { display: grid; gap: 12px; }
.system-settings-grid { display: grid; grid-template-columns: repeat(2,minmax(0,1fr)); gap: 12px; align-items: start; }
.system-settings-grid .settings-column { display: grid; gap: 12px; min-width: 0; }
.system-settings-grid .sub-panel { margin: 0; }
.system-settings-grid .field-grid { display: grid; grid-template-columns: minmax(0,1fr); gap: 0; }
.system-settings-grid .field-item { display: grid; grid-template-columns: minmax(120px,1fr) 112px 100px; gap: 8px; align-items: center; min-width: 0; padding: 8px 0; border-top: 1px solid var(--ct-line); }
.system-settings-grid .field-item :deep(.el-input-number) { width: 100%; }
.system-settings-grid .field-meta { justify-content: flex-end; }
@media(max-width:1000px) { .system-settings-grid { grid-template-columns: 1fr; } }
@media(max-width:600px) { .system-settings-grid .field-grid { grid-template-columns: 1fr; } .system-settings-grid .sub-panel { padding: 18px; } }
</style>
