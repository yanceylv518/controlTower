<script setup lang="ts">
import { computed } from "vue";

type PathPart = string | number;
type ChangeType = "added" | "removed" | "changed" | "same" | "context";

interface SnapshotValue {
  path: PathPart[];
  value: unknown;
}

interface SnapshotDiffRow {
  path: PathPart[];
  before: unknown;
  after: unknown;
  beforePresent: boolean;
  afterPresent: boolean;
  change: ChangeType;
}

const props = defineProps<{ before: string; after: string }>();
const AUDIT_SNAPSHOT_SCROLL_THRESHOLD = 8;

const fieldLabels: Record<string, string> = {
  actor_name: "操作者名称",
  actor_id: "操作人",
  actor_role: "操作者角色",
  actor_user_id: "操作者用户 ID",
  actor_username: "操作者用户名",
  account: "账号信息",
  action: "操作",
  auth_method: "认证方式",
  cache_price: "缓存价格",
  cache_read_price: "缓存读取价格",
  cache_write_price: "缓存写入价格",
  api_url: "API 地址",
  applied: "是否已应用",
  archive_format_version: "归档格式版本",
  auto_prepare: "自动准备",
  body: "请求体",
  base_priority: "基础优先级",
  base_url: "服务地址",
  base_weight: "基础权重",
  before_group: "变更前分组",
  channel_id: "渠道 ID",
  channel_name: "渠道名称",
  channel_type: "渠道类型",
  channels: "关联渠道",
  client_ip: "客户端 IP",
  command_id: "命令 ID",
  config: "配置",
  confirmed_at: "确认时间",
  confirmed_by: "确认人",
  correlation_id: "关联 ID",
  coverage_from: "数据覆盖起始日期",
  coverage_policy: "覆盖策略",
  coverage_revision: "覆盖策略版本",
  created_at: "创建时间",
  current_priority: "当前优先级",
  current_weight: "当前权重",
  dataset_id: "数据集 ID",
  description: "说明",
  display_name: "显示名称",
  direct: "直连执行",
  discount: "折扣",
  enabled: "启用状态",
  effective_from: "生效时间",
  error: "错误",
  error_code: "错误代码",
  error_summary: "错误摘要",
  evidence: "依据",
  evidence_json: "依据详情",
  expires_at: "过期时间",
  full_history: "完整历史日志",
  group: "分组",
  group_name: "分组名称",
  group_ratio: "分组倍率",
  groups: "分组列表",
  http_method: "请求方法",
  http_status: "HTTP 状态",
  id: "ID",
  input: "输入",
  instance_id: "实例 ID",
  instance_name: "实例名称",
  input_price: "输入价格",
  interval_seconds: "间隔时间（秒）",
  limit: "上限",
  max_rpm: "每分钟请求上限",
  max_tpm: "每分钟令牌上限",
  mode: "模式",
  model: "模型",
  model_name: "模型名称",
  name: "名称",
  operation: "操作",
  operation_type: "操作类型",
  ip: "IP 地址",
  offset: "偏移量",
  output_price: "输出价格",
  password_updated: "密码已更新",
  permissions: "权限",
  phone: "联系电话",
  policy: "策略",
  priority: "优先级",
  proposed_priority: "调整后优先级",
  proposed_weight: "调整后权重",
  probe_count: "探测次数",
  probe_interval_seconds: "探测间隔（秒）",
  query_json: "查询条件",
  quota: "额度",
  query: "查询参数",
  region: "区域",
  remark: "备注",
  request_count: "请求数",
  request: "请求内容",
  request_id: "请求 ID",
  registration: "归档注册信息",
  reconcile_date: "对账日期",
  reconcile_id: "对账 ID",
  retry_count: "重试次数",
  revision: "版本号",
  role: "角色",
  route: "路由",
  running: "运行状态",
  rule: "规则",
  scope_site: "站点范围",
  scope_user_ids: "用户范围",
  schema_fingerprint: "结构指纹",
  site_id: "站点 ID",
  site_name: "站点名称",
  source_fingerprint: "数据源指纹",
  source_component: "来源组件",
  source_retained_from: "源数据保留起始日期",
  state: "状态",
  status: "状态",
  timeout_seconds: "超时时间（秒）",
  target_id: "目标 ID",
  target_type: "目标类型",
  token_name: "令牌名称",
  trigger_type: "触发方式",
  updated_at: "更新时间",
  version: "版本号",
  user_id: "用户 ID",
  username: "用户名",
  values: "配置项",
  value: "值",
  weight: "权重",
  window_minutes: "窗口时长（分钟）",
};

const hasBefore = computed(() => props.before.trim().length > 0);
const hasAfter = computed(() => props.after.trim().length > 0);
const canCompare = computed(() => hasBefore.value && hasAfter.value);

function parseSnapshot(raw: string): unknown {
  if (!raw.trim()) return undefined;
  try {
    const value: unknown = JSON.parse(raw);
    if (value && typeof value === "object" && !Array.isArray(value)) {
      const envelope = value as Record<string, unknown>;
      // 账号审计的变更后快照包含操作者外层信息，应与变更前的账号字段直接比较。
      if ("actor_username" in envelope && envelope.account && typeof envelope.account === "object" && !Array.isArray(envelope.account)) return envelope.account;
    }
    return value;
  } catch {
    return raw;
  }
}

function addValue(values: Map<string, SnapshotValue>, path: PathPart[], value: unknown) {
  const resolvedPath = path.length ? path : ["内容"];
  values.set(JSON.stringify(resolvedPath), { path: resolvedPath, value });
}

function flattenSnapshot(raw: string) {
  const values = new Map<string, SnapshotValue>();
  const parsed = parseSnapshot(raw);
  if (parsed === undefined) return values;

  const pending: SnapshotValue[] = [{ path: [], value: parsed }];
  while (pending.length) {
    const current = pending.pop();
    if (!current) continue;

    if (Array.isArray(current.value)) {
      if (!current.value.length) {
        addValue(values, current.path, current.value);
      } else {
        for (let index = current.value.length - 1; index >= 0; index -= 1) {
          pending.push({ path: [...current.path, index], value: current.value[index] });
        }
      }
      continue;
    }

    if (typeof current.value === "object" && current.value !== null) {
      const entries = Object.entries(current.value as Record<string, unknown>);
      if (!entries.length) {
        addValue(values, current.path, current.value);
      } else {
        for (let index = entries.length - 1; index >= 0; index -= 1) {
          const [key, value] = entries[index];
          pending.push({ path: [...current.path, key], value });
        }
      }
      continue;
    }

    addValue(values, current.path, current.value);
  }
  return values;
}

function sameValue(left: unknown, right: unknown) {
  return Object.is(left, right) || JSON.stringify(left) === JSON.stringify(right);
}

const rows = computed<SnapshotDiffRow[]>(() => {
  const before = flattenSnapshot(props.before);
  const after = flattenSnapshot(props.after);
  const paths = new Set([...before.keys(), ...after.keys()]);

  const diff = [...paths].map((key) => {
    const oldValue = before.get(key);
    const newValue = after.get(key);
    const beforePresent = before.has(key);
    const afterPresent = after.has(key);
    let change: ChangeType = "context";

    if (canCompare.value) {
      if (!beforePresent) change = "added";
      else if (!afterPresent) change = "removed";
      else change = sameValue(oldValue?.value, newValue?.value) ? "same" : "changed";
    }

    return {
      path: newValue?.path || oldValue?.path || [],
      before: oldValue?.value,
      after: newValue?.value,
      beforePresent,
      afterPresent,
      change,
    };
  });

  return canCompare.value ? diff.filter((row) => row.change !== "same") : diff;
});

const changedCount = computed(() => rows.value.filter((row) => row.change !== "same" && row.change !== "context").length);
const requiresScroll = computed(() => rows.value.length > AUDIT_SNAPSHOT_SCROLL_THRESHOLD);

function fieldLabel(path: PathPart[]) {
  return path.reduce((label, part) => {
    if (typeof part === "number") return `${label}[${part}]`;
    const segment = fieldLabels[part] || `字段：${part}`;
    return label ? `${label} / ${segment}` : segment;
  }, "");
}

function changeLabel(change: ChangeType) {
  if (change === "added") return "新增";
  if (change === "removed") return "删除";
  if (change === "changed") return "修改";
  return "";
}

function displayValue(value: unknown, present: boolean, path: PathPart[] = []) {
  if (!present) return "—";
  if (value === null) return "空值";
  if (typeof value === "boolean") return value ? "是" : "否";
  if (Array.isArray(value) && !value.length) return "空列表";
  if (typeof value === "object" && value !== null && !Object.keys(value).length) return "空对象";
  if (typeof value === "string") {
    if (value === "[redacted]") return "已隐藏敏感信息";
    const enums: Record<string, Record<string, string>> = {
      role: { admin: "管理员", viewer: "查看账号" },
      actor_role: { admin: "管理员", viewer: "查看账号" },
      mode: { observe: "仅观察", auto: "自动调权", manual: "手动调权" },
      status: { succeeded: "成功", success: "成功", failed: "失败", submitted: "已提交", pending: "等待执行", expired: "已过期" },
    };
    return enums[String(path[path.length - 1])]?.[value] || value || "空字符串";
  }
  return String(value);
}
</script>

<template>
  <div v-if="rows.length" class="audit-snapshot-grid" :class="{ 'is-context': !canCompare }" role="table" :aria-label="canCompare ? '变更前后字段对比' : '操作内容字段'">
    <div class="audit-snapshot-header" role="row">
      <span role="columnheader">字段</span>
      <span v-if="canCompare" role="columnheader">变更前</span>
      <span role="columnheader">{{ canCompare ? '变更后' : '记录内容' }}</span>
    </div>
    <div
      class="audit-snapshot-rows"
      :class="{ 'is-scrollable': requiresScroll }"
      role="rowgroup"
      :tabindex="requiresScroll ? 0 : undefined"
      :aria-label="requiresScroll ? '字段变更列表，可滚动查看' : undefined"
    >
      <div v-for="row in rows" :key="JSON.stringify(row.path)" class="audit-snapshot-row" :class="`is-${row.change}`" role="row">
        <span class="audit-snapshot-field" role="cell">
          {{ fieldLabel(row.path) }}
          <small v-if="changeLabel(row.change)" class="audit-change-mark" :class="`mark-${row.change}`">{{ changeLabel(row.change) }}</small>
        </span>
        <span v-if="canCompare" class="audit-snapshot-value snapshot-before" data-label="变更前" role="cell">{{ displayValue(row.before, row.beforePresent, row.path) }}</span>
        <span class="audit-snapshot-value snapshot-after" :data-label="canCompare ? '变更后' : '记录内容'" role="cell">{{ displayValue(hasAfter ? row.after : row.before, hasAfter ? row.afterPresent : row.beforePresent, row.path) }}</span>
      </div>
    </div>
    <div v-if="canCompare" class="audit-snapshot-count">{{ changedCount }} 项字段变更</div>
    <div v-else class="audit-snapshot-count">共 {{ rows.length }} 项记录内容；此记录未提供前后对比</div>
  </div>
  <p v-else-if="canCompare" class="audit-snapshot-empty">前后快照一致，没有变更字段</p>
  <p v-else class="audit-snapshot-empty">未记录变更快照</p>
</template>

<style scoped>
.audit-snapshot-grid {
  overflow: hidden;
  border: 1px solid var(--ct-line);
  border-radius: 6px;
  background: var(--ct-surface);
}
.audit-snapshot-rows.is-scrollable {
  max-height: 280px;
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
}
.audit-snapshot-rows.is-scrollable:focus-visible {
  outline: 2px solid var(--ct-accent);
  outline-offset: -2px;
}
.audit-snapshot-header,
.audit-snapshot-row {
  display: grid;
  grid-template-columns: minmax(140px, 0.85fr) minmax(0, 1fr) minmax(0, 1fr);
  gap: 10px;
  align-items: start;
  padding: 8px 10px;
}
.audit-snapshot-header {
  background: var(--ct-surface-2);
  color: var(--ct-ink-3);
  font-size: 11px;
  border-bottom: 1px solid var(--ct-line);
}
.is-context .audit-snapshot-header,
.is-context .audit-snapshot-row { grid-template-columns: minmax(140px, 0.85fr) minmax(0, 2fr); }
.audit-snapshot-row + .audit-snapshot-row { border-top: 1px solid var(--ct-line); }
.audit-snapshot-row:hover { background: var(--ct-surface-2); }
.audit-snapshot-field,
.audit-snapshot-value { min-width: 0; overflow-wrap: anywhere; }
.audit-snapshot-field { color: var(--ct-ink-2); font-size: 12px; line-height: 1.5; }
.audit-snapshot-value { color: var(--ct-ink); font-size: 12px; line-height: 1.5; white-space: pre-wrap; }
.snapshot-before { color: var(--ct-ink-2); }
.is-added .snapshot-after { color: var(--ct-ok); font-weight: 600; }
.is-removed .snapshot-before { color: var(--ct-crit); font-weight: 600; }
.is-changed .snapshot-after { color: var(--ct-accent); font-weight: 600; }
.audit-change-mark {
  display: inline-block;
  margin-left: 6px;
  padding: 0 4px;
  border-radius: 3px;
  font-size: 10px;
  line-height: 16px;
  vertical-align: 1px;
}
.mark-added { background: var(--ct-ok-weak); color: var(--ct-ok); }
.mark-removed { background: var(--ct-crit-weak); color: var(--ct-crit); }
.mark-changed { background: var(--ct-accent-weak); color: var(--ct-accent); }
.audit-snapshot-count { padding: 6px 10px; border-top: 1px solid var(--ct-line); color: var(--ct-ink-3); font-size: 11px; }
.audit-snapshot-empty { margin: 0; padding: 12px; color: var(--ct-ink-3); font-size: 12px; }
@media (max-width: 900px) {
  .audit-snapshot-header { display: none; }
  .is-context .audit-snapshot-row { grid-template-columns: minmax(0, 1fr); }
  .audit-snapshot-row { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px 10px; }
  .audit-snapshot-field { grid-column: 1 / -1; }
  .audit-snapshot-value::before { content: attr(data-label); display: block; margin-bottom: 2px; color: var(--ct-ink-3); font-size: 10px; }
}
</style>
