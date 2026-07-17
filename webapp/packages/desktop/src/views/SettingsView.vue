<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import { ApiError, type SystemSettingItem } from "@ct/shared";
import { ElMessage } from "element-plus";
import { auth, dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import { useAuthStore } from "../stores/auth";
import { useFiltersStore } from "../stores/filters";

const store = useAuthStore();
const filters = useFiltersStore();
const loading = ref(false);
const saving = ref(false);
const items = ref<Record<string, SystemSettingItem>>({});
const values = reactive<Record<string, string | number>>({});
const password = reactive({
  old_password: "",
  new_password: "",
  confirm_password: "",
});

type Field = readonly [string, string, number, number];
const sections: ReadonlyArray<{ title: string; note: string; fields: readonly Field[] }> = [
  {
    title: "数据保留",
    note: "超过保留期的数据由每日清理任务删除，修改后下一轮生效",
    fields: [
      ["CT_RETENTION_DETAIL_DAYS", "明细数据（天）", 1, 365],
      ["CT_RETENTION_METRIC5M_DAYS", "5 分钟指标（天）", 1, 365],
      ["CT_RETENTION_RUNTIME_DAYS", "运行状态（天）", 1, 365],
      ["CT_RETENTION_ALERTS_DAYS", "告警（天）", 1, 365],
    ],
  },
  {
    title: "告警阈值",
    note: "warn 触发 warning 级、crit 触发 critical 级；warn 必须小于 crit",
    fields: [
      ["CT_OFFLINE_ALERT_SECONDS", "实例离线（秒）", 1, 86400],
      ["CT_CPU_WARN_PERCENT", "CPU 警告（%）", 1, 100],
      ["CT_CPU_CRIT_PERCENT", "CPU 严重（%）", 1, 100],
      ["CT_MEMORY_WARN_PERCENT", "内存警告（%）", 1, 100],
      ["CT_MEMORY_CRIT_PERCENT", "内存严重（%）", 1, 100],
      ["CT_DISK_WARN_PERCENT", "磁盘警告（%）", 1, 100],
      ["CT_DISK_CRIT_PERCENT", "磁盘严重（%）", 1, 100],
      ["CT_ERROR_RATE_WARN_PERCENT", "错误率警告（%）", 1, 100],
      ["CT_ERROR_RATE_CRIT_PERCENT", "错误率严重（%）", 1, 100],
      ["CT_P95_WARN_SECONDS", "P95 警告（秒）", 0.5, 600],
      ["CT_P95_CRIT_SECONDS", "P95 严重（秒）", 0.5, 600],
    ],
  },
];

const sourceLabels: Record<string, string> = {
  db: "已修改",
  env: "环境变量",
  default: "默认",
};
async function load() {
  loading.value = true;
  try {
    await filters.loadInstances();
    const response = await dashboard.settings();
    items.value = response.items;
    Object.entries(response.items).forEach(
      ([key, item]) =>
        (values[key] =
          key === "CT_NOTIFICATIONS_ENABLED" ||
          key === "CT_CURRENCY_SYMBOL" ||
          key === "CT_DEFAULT_INSTANCE_ID"
            ? item.value
            : Number(item.value)),
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
    await filters.loadInstances(true);
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
async function changePassword() {
  if (
    password.new_password.length < 8 ||
    password.new_password !== password.confirm_password
  ) {
    ElMessage.error("请确认新密码至少 8 位且两次输入一致");
    return;
  }
  await auth.changePassword(password.old_password, password.new_password);
  ElMessage.success("密码修改成功");
}
onMounted(load);
</script>
<template>
  <AppShell title="设置">
    <template #tools>
      <el-button type="primary" :loading="saving" @click="save"
        >保存系统设置</el-button
      >
    </template>
    <div v-loading="loading" class="settings-layout">
      <section
        v-for="section in sections"
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
              :step="field[2] === 0.5 ? 0.5 : 1"
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
      <section class="panel sub-panel">
        <h2>通知</h2>
        <p class="sub-note">关闭后 Server 侧告警不再向任何通知渠道投递</p>
        <div class="field-grid">
          <div class="field-item">
            <label>告警通知总开关</label>
            <el-switch
              v-model="values.CT_NOTIFICATIONS_ENABLED"
              active-value="true"
              inactive-value="false"
            />
            <span class="field-meta">
              <span
                :class="[
                  'source-pill',
                  items.CT_NOTIFICATIONS_ENABLED?.source === 'db' ? 'db' : '',
                ]"
                >{{
                  sourceLabels[items.CT_NOTIFICATIONS_ENABLED?.source] || "—"
                }}</span
              >
            </span>
          </div>
        </div>
      </section>
      <section class="panel sub-panel">
        <h2>默认实例</h2>
        <p class="sub-note">
          登录或刷新后，各监控页面优先展示此实例。顶部实例选择器仍可临时切换为其他实例或全部实例。
        </p>
        <div class="field-grid">
          <div class="field-item">
            <label>默认展示实例</label>
            <el-select
              v-model="values.CT_DEFAULT_INSTANCE_ID as string"
              size="small"
              clearable
              placeholder="请选择默认实例"
              style="width: 240px"
            >
              <el-option
                v-for="item in filters.instances.filter(
                  (entry) => entry.enabled,
                )"
                :key="item.instance_id"
                :label="item.name || item.instance_id"
                :value="item.instance_id"
              />
            </el-select>
            <span class="field-meta">删除或禁用后自动回退到第一个可用实例</span>
          </div>
        </div>
      </section>
      <section class="panel sub-panel">
        <h2>显示</h2>
        <p class="sub-note">
          Quota 将按「金额 = quota ÷ 换算率」显示为货币；符号跟随 new-api 站点定价（不做汇率换算）
        </p>
        <div class="field-grid">
          <div class="field-item">
            <label>Quota 换算率（每 1 货币单位）</label>
            <el-input-number
              v-model="values.CT_QUOTA_PER_UNIT as number"
              :min="1"
              :max="1000000000"
              :step="1000"
              controls-position="right"
              size="small"
            />
            <span class="field-meta">
              <span
                :class="[
                  'source-pill',
                  items.CT_QUOTA_PER_UNIT?.source === 'db' ? 'db' : '',
                ]"
                >{{ sourceLabels[items.CT_QUOTA_PER_UNIT?.source] || "—" }}</span
              >
              默认 {{ items.CT_QUOTA_PER_UNIT?.default }}
            </span>
          </div>
          <div class="field-item">
            <label>货币符号</label>
            <el-input
              v-model="values.CT_CURRENCY_SYMBOL as string"
              size="small"
              maxlength="4"
              style="width: 90px"
            />
            <span class="field-meta">
              <span
                :class="[
                  'source-pill',
                  items.CT_CURRENCY_SYMBOL?.source === 'db' ? 'db' : '',
                ]"
                >{{ sourceLabels[items.CT_CURRENCY_SYMBOL?.source] || "—" }}</span
              >
              默认 {{ items.CT_CURRENCY_SYMBOL?.default }}
            </span>
          </div>
        </div>
      </section>
      <section class="panel sub-panel">
        <h2>账户</h2>
        <p class="sub-note">
          当前用户 {{ store.user?.username }} · 角色 {{ store.user?.role }}
        </p>
        <div class="field-grid">
          <div class="field-item">
            <label>旧密码</label>
            <el-input
              v-model="password.old_password"
              type="password"
              show-password
              size="small"
            />
          </div>
          <div class="field-item">
            <label>新密码</label>
            <el-input
              v-model="password.new_password"
              type="password"
              show-password
              size="small"
            />
          </div>
          <div class="field-item">
            <label>确认新密码</label>
            <el-input
              v-model="password.confirm_password"
              type="password"
              show-password
              size="small"
            />
          </div>
          <div class="field-item field-action">
            <el-button @click="changePassword">修改密码</el-button>
          </div>
        </div>
      </section>
    </div>
  </AppShell>
</template>
