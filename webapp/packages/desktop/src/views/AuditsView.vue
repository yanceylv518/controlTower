<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import AsyncPanel from "../components/AsyncPanel.vue";
import AuditSnapshotDiff from "../components/AuditSnapshotDiff.vue";
import CompactDateTimeRangePicker from "../components/CompactDateTimeRangePicker.vue";
import ListPager from "../components/ListPager.vue";
import { useAsyncData } from "../composables/useAsyncData";
import { useAutoRefresh } from "../composables/useAutoRefresh";
import { formatTime } from "../utils/format";
import { copyText } from "../utils/copyText";
import type { OperationAuditItem } from "@ct/shared";

const page = ref(1);
const pageSize = ref(20);
const expandedRows = ref<string[]>([]);
const auditTable = ref<{
  toggleRowExpansion: (row: OperationAuditItem, expanded?: boolean) => void;
} | null>(null);
const draftTimeRange = ref<[Date, Date] | null>(null);
const appliedTimeRange = ref<[Date, Date] | null>(null);
const draft = reactive({ q: "", actor: "", operation_type: "", status: "" });
const applied = ref({ ...draft });
const appliedTimeParams = computed(() => {
  if (!appliedTimeRange.value) return {};
  const [from, end] = appliedTimeRange.value;
  if (!Number.isFinite(from.getTime()) || !Number.isFinite(end.getTime())) return {};
  const to = new Date(end);
  to.setMilliseconds(0);
  to.setSeconds(to.getSeconds() + 1);
  return { from: from.toISOString(), to: to.toISOString() };
});
const timeRangeResetEnabled = computed(() => draftTimeRange.value !== null || appliedTimeRange.value !== null);

const state = useAsyncData(async (signal) => {
  return dashboard.operationAudits({
    q: applied.value.q.trim() || undefined,
    actor: applied.value.actor.trim() || undefined,
    actor_exact: applied.value.actor ? true : undefined,
    operation_type: applied.value.operation_type.trim() || undefined,
    status: applied.value.status || undefined,
    ...appliedTimeParams.value,
    limit: pageSize.value,
    offset: (page.value - 1) * pageSize.value,
  }, signal);
});

const items = computed(() => (state.data.value?.items || []).map(item => ({ ...item, target_display: targetLabel(item) })));
const total = computed(() => state.data.value?.total || 0);
const operationTypes = computed(() => state.data.value?.operation_types || []);
const actorOptions = ref<string[]>([]);
const actorLoading = ref(false);
const actorError = ref(false);
let actorSearchVersion = 0;
let actorSearchTimer: ReturnType<typeof setTimeout> | undefined;
let actorSearchController: AbortController | undefined;
function searchActors(value: string) {
  const version = ++actorSearchVersion;
  clearTimeout(actorSearchTimer);
  actorSearchController?.abort();
  actorLoading.value = true;
  actorError.value = false;
  actorSearchTimer = setTimeout(async () => {
    const controller = new AbortController();
    actorSearchController = controller;
    const timeout = setTimeout(() => controller.abort(), 8_000);
    try {
      const response = await dashboard.operationAudits({ actor_options: true, actor: value.trim() || undefined }, controller.signal);
      if (version === actorSearchVersion) actorOptions.value = response.actors || [];
    } catch {
      if (version === actorSearchVersion) { actorOptions.value = []; actorError.value = true; }
    } finally {
      clearTimeout(timeout);
      if (version === actorSearchVersion) actorLoading.value = false;
    }
  }, 250);
}
onBeforeUnmount(() => { ++actorSearchVersion; clearTimeout(actorSearchTimer); actorSearchController?.abort(); state.cancel(); });

watch([page, pageSize], () => { expandedRows.value = []; void state.reload(); });
useAutoRefresh((silent) => {
  if (silent && (expandedRows.value.length > 0 || page.value > 1 || state.loading.value)) return;
  return state.reload(silent);
});

function applyFilters() {
  expandedRows.value = [];
  applied.value = { ...draft };
  appliedTimeRange.value = draftTimeRange.value
    ? [new Date(draftTimeRange.value[0]), new Date(draftTimeRange.value[1])]
    : null;
  if (page.value === 1) void state.reload();
  else page.value = 1;
}

function resetTimeRange() {
  expandedRows.value = [];
  draftTimeRange.value = null;
  appliedTimeRange.value = null;
  if (page.value === 1) void state.reload();
  else page.value = 1;
}

function clearFilters() {
  Object.assign(draft, { q: "", actor: "", operation_type: "", status: "" });
  draftTimeRange.value = null;
  applyFilters();
}

function handleAuditRowClick(row: OperationAuditItem, _column: unknown, event: MouseEvent) {
  const target = event.target;
  if (target instanceof Element && target.closest(".el-table__expand-icon, button, a, input, textarea, select, [role='button'], .audit-detail")) {
    return;
  }
  auditTable.value?.toggleRowExpansion(row);
}

async function copyAuditValue(value: string) {
  if (await copyText(value)) ElMessage.success("已复制请求ID");
  else ElMessage.error("复制失败，请选中文本手动复制");
}

function detailTitle(item: OperationAuditItem) {
  if (["auth.login", "auth.logout", "auth.viewer_login"].includes(item.operation_type)) return "登录与退出信息";
  return item.before_summary.trim() && item.after_summary.trim() ? "变更对比" : "操作内容";
}

function errorLabel(value: string) {
  const labels: Record<string, string> = {
    invalid_credentials: "账号或密码验证失败",
    unauthorized: "登录已失效或尚未登录",
    forbidden: "没有执行此操作的权限",
    csrf: "请求来源校验失败",
    locked: "连续验证失败，账号暂时锁定",
    rate_limited: "操作过于频繁，请稍后重试",
    invalid_request: "请求内容不符合要求",
    request_too_large: "请求内容超过大小限制",
    audit_failed: "审计记录保存失败",
    logout_failed: "退出登录失败",
  };
  return labels[value] || value;
}

function operationLabel(value: string) {
  const labels: Record<string, string> = {
    "settings.update": "系统设置变更",
    "settings.balance_alert_user_update": "额度告警用户配置变更",
    "menu_visibility.update": "菜单可见性变更",
    "instance.update": "修改站点实例",
    "instance.delete": "删除站点实例",
    "logs.query": "查询使用日志",
    "auth.account_create": "创建管理账号",
    "auth.account_update": "修改管理账号",
    "auth.password_reset": "重置管理账号密码",
    "auth.password_change": "修改账号密码",
    "auth.login": "账号登录",
    "auth.logout": "账号退出",
    "auth.viewer_login": "查看账号登录",
    "billing.price_update": "账单价格变更",
    "billing.group_ratio_update": "分组倍率变更",
    "billing.model_metadata_update": "模型计费信息变更",
    "billing.models_sync": "同步计费模型",
    "billing.backfill": "账单数据回填",
    "billing.discount.create": "创建账单折扣",
    "billing.discount.update": "修改账单折扣",
    "billing.discount.delete": "删除账单折扣",
    "billing.upstream.create": "添加账单上游",
    "billing.upstream.update": "修改账单上游",
    "billing.upstream.delete": "删除账单上游",
    "billing.channel_setting.update": "修改渠道计费配置",
    "billing.user_setting.update": "修改用户计费配置",
    "channel.update": "渠道配置变更",
    "channel.probe": "渠道连通性测试",
    "tuning.base_update": "调权基础值变更",
    "tuning.base_priority_sync": "基础优先级同步",
    "tuning.manual_execute": "手动调权执行",
    "tuning.policy_update": "调权策略变更",
    "tuning.group_presets_update": "渠道分组预设变更",
    "tuning.group_update": "渠道分组变更",
    "tuning.weight_update": "渠道权重变更",
    "voice_alert.configure": "语音告警配置变更",
    "instance.create": "添加站点实例",
    "instance.token_rotate": "轮换站点服务令牌",
    "passthrough.users": "查询用户数据",
    "passthrough.logs": "查询使用日志",
    "passthrough.logs.stat": "查询使用日志统计",
    "passthrough.logs.count": "查询使用日志数量",
  };
  if (labels[value]) return labels[value];
  if (value.startsWith("http.")) return httpOperationLabel(value);
  return "其他操作";
}

function operationTypeOptionLabel(value: string) {
  const label = operationLabel(value);
  return label === "其他操作" ? value : label;
}

function httpOperationLabel(value: string) {
  const parts = value.split(".");
  const source = parts[1] || "";
  if (value.endsWith(".*")) return `${sourceComponentLabel(source)}接口操作`;
  const lastPart = parts[parts.length - 1];
  const methods: Record<string, string> = {
    post: "提交",
    put: "修改",
    patch: "修改",
    delete: "删除",
  };
  const action = methods[lastPart] || "执行";
  const resourceParts = parts.slice(2, methods[lastPart] ? -1 : undefined);
  if (resourceParts[0] === source) resourceParts.shift();

  const resourceLabels: Record<string, string> = {
    accounts: "管理员账号",
    account: "管理员账号",
    auth: "账号管理",
    users: "用户",
    user: "用户",
    password: "密码",
    instances: "站点实例",
    instance: "站点实例",
    tokens: "服务令牌",
    token: "服务令牌",
    roles: "角色",
    permissions: "权限",
    tuning: "调权配置",
    channels: "渠道",
    channel: "渠道",
    channel_group_presets: "渠道分组预设",
    group_presets: "分组预设",
    groups: "分组",
    models: "模型",
    model_metadata: "模型信息",
    prices: "计费价格",
    discounts: "折扣配置",
    discount: "折扣配置",
    upstreams: "上游配置",
    upstream: "上游配置",
    jobs: "任务",
    backfill: "数据回填",
    billing_daily: "账单统计",
    billing: "账单配置",
    policies: "调权策略",
    policy: "调权策略",
    preflight: "调权预检",
    settings: "系统设置",
    menu_visibility: "菜单权限",
    system_settings: "系统设置",
    balance_alerts: "额度告警",
    balance_alert_user: "额度告警用户",
    alerts: "告警规则",
    notification_channels: "通知渠道",
    notification_deliveries: "通知记录",
    container_log_tasks: "容器日志任务",
    log_archives: "日志归档",
    archive_datasets: "归档数据集",
    datasets: "数据集",
    logs: "使用日志",
    visibility: "菜单权限",
    newapi_readonly: "New API 只读数据",
  };
  const sourceLabel = sourceComponentLabel(source);
  const resourceLabel = resourceParts
    .map((part) => resourceLabels[part])
    .filter(Boolean)
    .join(" · ");
  return [sourceLabel, resourceLabel, action].filter(Boolean).join(" · ");
}

function statusMeta(value: string) {
  if (value === "succeeded" || value === "success") return { label: "成功", type: "success" as const };
  if (value === "failed") return { label: "失败", type: "danger" as const };
  if (value === "submitted" || value === "pending") return { label: "执行中", type: "warning" as const };
  if (value === "expired" || value === "timed_out") return { label: "未完成", type: "info" as const };
  return { label: value ? "其他状态" : "未知", type: "info" as const };
}

function actorLabel(item: OperationAuditItem) {
  if (item.actor_type === "system") return "系统自动";
  if (item.actor_type === "service_token") return "服务令牌";
  if (item.actor_type === "human") return item.actor_role ? actorRoleLabel(item.actor_role) : (item.actor_id && item.actor_id !== "unknown" ? "用户" : "身份未验证");
  return item.actor_type ? "其他来源" : "未知来源";
}

function actorRoleLabel(value: string) {
  const labels: Record<string, string> = {
    admin: "管理员",
    viewer: "查看账号",
  };
  return labels[value] || "其他角色";
}

function sourceComponentLabel(value: string) {
  const labels: Record<string, string> = {
    tuning: "调权中心",
    billing: "账单管理",
    identity: "账号管理",
    system: "系统管理",
    archive_datasets: "归档数据",
    archive: "归档数据",
    log_archives: "日志归档",
    alerts: "告警管理",
    notification_channels: "通知渠道",
    notification_deliveries: "通知记录",
    container_log_tasks: "容器日志",
    channels: "渠道管理",
    channel_group_presets: "渠道分组",
    control_tower: "控制台",
  };
  return value ? labels[value] || "其他模块" : "—";
}

function targetTypeLabel(value: string) {
  const labels: Record<string, string> = {
    channel: "渠道",
    ct_user: "账号",
    user: "用户",
    users: "账号",
    channels: "渠道",
    instances: "站点实例",
    notification_channels: "通知渠道",
    notification_deliveries: "通知记录",
    alerts: "告警规则",
    voice_alert: "语音告警配置",
    voice_alerts: "语音告警配置",
    log_archives: "日志归档",
    archive_datasets: "归档数据集",
    tuning: "调权配置",
    tuning_policy: "调权策略",
    tuning_group_presets: "渠道分组预设",
    system_settings: "系统设置",
    balance_alert_user: "额度告警用户",
    billing_daily: "账单统计",
    billing: "账单配置",
    newapi_readonly: "New API 只读数据",
    instance: "站点实例",
    configuration: "系统配置",
  };
  return labels[value] || "";
}

function targetLabel(item: OperationAuditItem) {
  const object = (value: unknown): Record<string, unknown> =>
    value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
  const parse = (value: string) => {
    try { return object(JSON.parse(value)); } catch { return {}; }
  };
  const after = parse(item.after_summary);
  const before = parse(item.before_summary);
  const snapshots = [after, object(after.request), before, object(before.request)];
  const text = (value: unknown) => typeof value === "string" && value.trim() ? value.trim() : "";
  const nameFrom = (...keys: string[]) => {
    for (const snapshot of snapshots) {
      for (const key of keys) {
        const value = text(snapshot[key]);
        if (value) return value;
      }
    }
    return "";
  };
  const operation = item.operation_type;
  const site = item.instance_name || item.instance_id;
  const id = item.target_id;
  if (operation === "menu_visibility.update") return "全局菜单可见性";
  if (item.target_type === "system_settings" || operation === "settings.update") return "全局系统设置";
  if (item.target_type === "tuning_policy" || operation === "tuning.policy_update") return site ? `调权策略 · ${site}` : "调权策略";
  if (item.target_type === "tuning_group_presets" || operation === "tuning.group_presets_update") return site ? `渠道分组预设 · ${site}` : "渠道分组预设";
  if (operation.startsWith("http.tuning.") && operation.includes(".channels.")) return site ? `渠道基础配置 · ${site}` : "渠道基础配置";

  let kind = targetTypeLabel(item.target_type) || sourceComponentLabel(item.source_component);
  let name = "";
  if (item.target_type === "ct_user" || operation.startsWith("auth.") || operation.startsWith("http.identity.")) {
    kind = "账号";
    for (const snapshot of snapshots) {
      const account = object(snapshot.account);
      const username = text(account.username) || text(snapshot.username);
      const displayName = text(account.display_name) || text(snapshot.display_name);
      if (username || displayName) {
        name = displayName && username && displayName !== username ? `${displayName}（${username}）` : username || displayName;
        break;
      }
    }
    if (!name && ["auth.password_change", "auth.login", "auth.logout"].includes(operation) && item.actor_id !== "unknown") name = item.actor_id;
  } else if (item.target_type === "instance" || item.target_type === "instances") {
    name = nameFrom("name", "instance_name") || (site !== id ? site : "");
  } else if (item.target_type === "channel" || item.target_type === "channels") {
    name = nameFrom("channel_name", "name");
  } else if (operation.startsWith("billing.upstream.")) {
    kind = "账单上游";
    name = nameFrom("name");
  } else if (operation.startsWith("billing.discount.")) {
    kind = "账单折扣";
    name = nameFrom("name", "remark");
  } else if (operation === "billing.channel_setting.update") {
    kind = "渠道计费配置";
    name = nameFrom("channel_name");
  } else if (operation === "billing.user_setting.update") {
    kind = "用户计费配置";
    name = nameFrom("username");
  } else {
    name = nameFrom("name", "model_name", "model");
  }
  if (name) return `${kind} · ${name}${id && id !== name && id !== "global" ? `（ID：${id}）` : ""}`;
  if (id && id !== "global") return `${kind} #${id}`;
  return kind || "未记录目标";
}

function authMethodLabel(value: string) {
  const labels: Record<string, string> = {
    session: "网页登录",
    web_session: "网页登录",
    bearer: "服务令牌",
    service_token: "服务令牌",
  };
  return value ? labels[value] || "其他认证方式" : "—";
}

function httpMethodLabel(value: string) {
  const labels: Record<string, string> = {
    GET: "读取",
    POST: "提交",
    PUT: "整体更新",
    PATCH: "修改",
    DELETE: "删除",
  };
  return value ? labels[value.toUpperCase()] || "其他请求" : "—";
}

function httpStatus(item: OperationAuditItem) {
  return item.http_status ? String(item.http_status) : "—";
}
</script>

<template>
  <AppShell class="audit-shell" title="操作审计">
    <section class="audit-panel">
      <div class="audit-toolbar">
        <CompactDateTimeRangePicker
          v-model="draftTimeRange"
          :reset-enabled="timeRangeResetEnabled"
          class="audit-time-range"
          @reset="resetTimeRange"
        />
        <el-input v-model="draft.q" clearable placeholder="目标ID / 请求ID / 错误信息" title="按目标ID、请求ID或错误信息模糊搜索；操作名称请使用操作类型筛选" class="audit-search" @keyup.enter="applyFilters" />
        <el-select v-model="draft.actor" clearable filterable remote :remote-method="searchActors" :loading="actorLoading" :no-data-text="actorError ? '加载失败，请重新搜索' : '无匹配操作人'" placeholder="搜索操作人" class="audit-actor" @visible-change="(visible: boolean) => { if (visible) searchActors(''); }">
          <el-option v-for="actor in actorOptions" :key="actor" :label="actor === 'unknown' ? '未验证身份' : actor" :value="actor" />
        </el-select>
        <el-select v-model="draft.operation_type" clearable filterable placeholder="操作类型" class="audit-operation">
          <el-option v-for="operationType in operationTypes" :key="operationType" :label="operationTypeOptionLabel(operationType)" :value="operationType" />
        </el-select>
        <el-select v-model="draft.status" clearable placeholder="结果" class="audit-select">
          <el-option label="成功" value="succeeded" />
          <el-option label="失败" value="failed" />
        </el-select>
        <el-button type="primary" :loading="state.loading.value" @click="applyFilters">查询</el-button>
        <el-button @click="clearFilters">重置</el-button>
        <span v-if="expandedRows.length || page > 1" class="audit-refresh-hint">阅读记录时暂停自动刷新</span>
        <span v-if="state.lastRefreshError.value && !state.error.value" class="audit-error" role="status">自动刷新失败，当前显示上次结果，请重新查询</span>
      </div>

      <AsyncPanel class="audit-results" :loading="state.loading.value" :error="state.error.value" @retry="state.reload">
      <el-table
        ref="auditTable"
        height="100%"
        :data="items"
        row-key="id"
        :expand-row-keys="expandedRows"
        empty-text="没有符合条件的记录，请调整筛选条件"
        class="audit-table"
        @row-click="handleAuditRowClick"
        @expand-change="(_row: OperationAuditItem, rows: OperationAuditItem[]) => { expandedRows = rows.map(row => row.id); }"
      >
        <el-table-column type="expand" width="34">
          <template #default="scope">
            <div class="audit-detail">
              <el-descriptions :column="4" border size="small">
                <el-descriptions-item label="来源模块">{{ sourceComponentLabel(scope.row.source_component) }}</el-descriptions-item>
                <el-descriptions-item label="操作者类型">{{ actorLabel(scope.row) }}</el-descriptions-item>
                <el-descriptions-item label="认证方式">{{ authMethodLabel(scope.row.auth_method) }}</el-descriptions-item>
                <el-descriptions-item label="客户端 IP">{{ scope.row.client_ip || "—" }}</el-descriptions-item>
                <el-descriptions-item label="请求类型">{{ httpMethodLabel(scope.row.http_method) }}</el-descriptions-item>
                <el-descriptions-item label="HTTP 状态">{{ httpStatus(scope.row) }}</el-descriptions-item>
                <el-descriptions-item label="请求ID">
                  <button v-if="scope.row.request_id" type="button" class="audit-copy" title="点击复制请求ID" @click="copyAuditValue(scope.row.request_id)">{{ scope.row.request_id }}</button>
                  <span v-else>未记录</span>
                </el-descriptions-item>
                <el-descriptions-item v-if="scope.row.correlation_id && scope.row.correlation_id !== scope.row.request_id" label="关联ID">{{ scope.row.correlation_id }}</el-descriptions-item>
                <el-descriptions-item label="路由" :span="4">{{ scope.row.route || "—" }}</el-descriptions-item>
                <el-descriptions-item v-if="scope.row.error_summary" label="错误摘要" :span="4">
                  <span class="audit-error" :title="scope.row.error_summary">{{ errorLabel(scope.row.error_summary) }}</span>
                </el-descriptions-item>
              </el-descriptions>
              <div class="audit-snapshots">
                <section>
                  <h4>{{ detailTitle(scope.row) }}</h4>
                  <AuditSnapshotDiff :before="scope.row.before_summary" :after="scope.row.after_summary" />
                </section>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="时间" min-width="158">
          <template #default="scope">{{ formatTime(scope.row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" min-width="190" show-overflow-tooltip>
          <template #default="scope">{{ operationLabel(scope.row.operation_type) }}</template>
        </el-table-column>
        <el-table-column label="目标" min-width="150" show-overflow-tooltip>
          <template #default="scope">{{ scope.row.target_display }}</template>
        </el-table-column>
        <el-table-column label="操作人" min-width="150" show-overflow-tooltip>
          <template #default="scope">
            <span>{{ scope.row.actor_id && scope.row.actor_id !== "unknown" ? scope.row.actor_id : "未验证身份" }}</span>
            <span class="audit-actor-type">{{ actorLabel(scope.row) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作方式" width="110">
          <template #default="scope">{{ authMethodLabel(scope.row.auth_method) }}</template>
        </el-table-column>
        <el-table-column label="结果" width="92">
          <template #default="scope">
            <el-tag size="small" :type="statusMeta(scope.row.status).type">{{ statusMeta(scope.row.status).label }}</el-tag>
          </template>
        </el-table-column>
      </el-table>

      <ListPager v-model:page="page" v-model:page-size="pageSize" :item-count="items.length" :total="total" />
      </AsyncPanel>
    </section>
  </AppShell>
</template>

<style scoped>
.audit-shell { height: 100dvh; min-height: 0; overflow: hidden; }
.audit-shell :deep(.workspace) { display: flex; height: 100dvh; min-height: 0; flex-direction: column; }
.audit-shell :deep(.topbar) { flex: 0 0 auto; }
.audit-shell :deep(.content) { display: flex; min-height: 0; flex: 1 1 auto; flex-direction: column; overflow: hidden; }
.audit-panel { display: grid; min-width: 0; min-height: 0; flex: 1 1 auto; grid-template-rows: auto minmax(0, 1fr); }
.audit-results { display: grid; min-width: 0; min-height: 0; grid-template-rows: minmax(0, 1fr) auto; }
.audit-refresh-hint { color: var(--ct-ink-3); font-size: 12px; }
.audit-copy { border: 0; padding: 0; background: none; color: var(--ct-accent); font: inherit; overflow-wrap: anywhere; text-align: left; cursor: pointer; }
.audit-copy:hover { text-decoration: underline; }
.audit-copy:focus-visible { outline: 2px solid var(--ct-accent); outline-offset: 2px; }
.audit-detail :deep(.el-descriptions__content) { overflow-wrap: anywhere; }
.audit-panel :deep(.audit-table) { width: 100%; min-height: 0; }
.audit-panel :deep(.audit-table .el-table__body tr.el-table__row) { cursor: pointer; }
.audit-toolbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}
.audit-search { width: 250px; }
.audit-actor { width: 150px; }
.audit-operation { width: 190px; }
.audit-time-range { width: 294px; min-width: 0; max-width: 100%; flex: 0 0 294px; }
.audit-select { width: 125px; }
.audit-detail { display: grid; gap: 12px; padding: 8px 16px 12px; }
.audit-snapshots { display: grid; grid-template-columns: minmax(0, 1fr); }
.audit-snapshots section { min-width: 0; }
.audit-snapshots h4 { margin: 0 0 6px; font-size: 13px; font-weight: 600; }
.audit-actor-type { display: block; color: var(--el-text-color-secondary); font-size: 12px; }
.audit-error { color: var(--el-color-danger); }
@media (max-width: 900px) {
  .audit-detail { padding: 8px 4px 12px; }
}
@media (max-width: 760px) {
  .audit-time-range { width: 100%; flex-basis: 100%; }
}
</style>
