<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { type ChannelBaseValue, type TuningContinuousState, type TuningPolicy, type TuningRecommendation } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";

const filters = useFiltersStore();
const loading = ref(false), saving = ref(false), dirty = ref(false), activeTab = ref("overview"), helpOpen = ref(false);
const mode = ref<"observe" | "confirm" | "auto">("observe");
const bases = ref<ChannelBaseValue[]>([]), states = ref<TuningContinuousState[]>([]), events = ref<TuningRecommendation[]>([]);
const savedBases = ref<ChannelBaseValue[]>([]), savedPolicy = ref<TuningPolicy | null>(null), savedMode = ref<"observe" | "confirm" | "auto">("observe");
const modelQuery = ref(""), activeModel = ref("");
const eventModelFilter = ref(""), eventRuleFilter = ref(""), eventChannelQuery = ref("");
const eventPage = ref(1), eventPageSize = ref(20);
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T;
const wait = (milliseconds: number) => new Promise(resolve => setTimeout(resolve, milliseconds));
const defaults = () => ({ sensitivity: 1, otps_cap: 1.5, circuit_threshold: .1, recovery_threshold: .2, circuit_error_rate: .3, recovery_error_rate: .1, silent_minutes: 5, probe_interval_seconds: 5, probe_count: 10, soft_start_multiplier: .2, window_minutes: 15, min_samples: 20, sparse_lookback_minutes: 360, write_deadband_percent: 5, min_write_interval_minutes: 5 });
const policy = reactive<TuningPolicy>({ scheduling: { window_minutes: 15, min_samples: 20, sparse_min_samples: 10, sparse_lookback_minutes: 360 }, continuous: defaults(), dispatch_modes: {} });
const siteID = computed(() => filters.site_id || "");
const modelChannelCount = (model: string) => bases.value.filter(x => x.model_name === model).length;
const models = computed(() => {
  const modeOrder = { auto: 0, observe: 1, off: 2 } as const;
  return [...new Set(bases.value.map(x => x.model_name))].sort((a, b) =>
    modeOrder[policy.dispatch_modes[a] || "off"] - modeOrder[policy.dispatch_modes[b] || "off"]
    || modelChannelCount(b) - modelChannelCount(a)
    || a.localeCompare(b),
  );
});
const visibleModels = computed(() => models.value.filter(model => model.toLowerCase().includes(modelQuery.value.trim().toLowerCase())));
const activeRows = computed(() => bases.value.filter(x => x.model_name === activeModel.value).sort((a, b) => b.current_priority - a.current_priority || b.current_weight - a.current_weight || a.channel_id - b.channel_id));
const channelRowKey = (row: ChannelBaseValue) => `${row.channel_id}:${row.model_name}`;
const stateMap = computed(() => new Map(states.value.map(x => [`${x.channel_id}:${x.model_name}`, x])));
const stateFor = (row: ChannelBaseValue) => stateMap.value.get(`${row.channel_id}:${row.model_name}`);
const validEvent = (item: TuningRecommendation) => item.rule !== "circuit_recovered" || item.proposed_weight > 0;
const recentEvents = computed(() => events.value.filter(x => validEvent(x) && ["weight_observed", "weight_write", "manual_takeover", "auto_paused", "circuit_opened", "probe_started", "probe_failed", "circuit_recovered"].includes(x.rule)));
const eventModel = (item: TuningRecommendation) => String(item.evidence?.model ?? bases.value.find(row => row.channel_id === item.channel_id)?.model_name ?? "");
const filteredEvents = computed(() => {
  const selectedModel = eventModelFilter.value === "__current__" ? activeModel.value : eventModelFilter.value;
  const channelQuery = eventChannelQuery.value.trim().toLowerCase();
  return recentEvents.value.filter(item =>
    (!selectedModel || eventModel(item) === selectedModel)
    && (!eventRuleFilter.value || item.rule === eventRuleFilter.value)
    && (!channelQuery || item.channel_name.toLowerCase().includes(channelQuery) || String(item.channel_id).includes(channelQuery)),
  );
});
const eventRuleOptions = computed(() => [...new Set(recentEvents.value.map(item => item.rule))]);
const pagedEvents = computed(() => filteredEvents.value.slice((eventPage.value - 1) * eventPageSize.value, eventPage.value * eventPageSize.value));
const counts = computed(() => models.value.reduce((v, model) => { v[policy.dispatch_modes[model] || "off"]++; return v; }, { off: 0, observe: 0, auto: 0 }));
const factor = (value?: number) => Number(value ?? 1).toFixed(3);
const clampNumber = (value: number, low: number, high: number) => Math.min(high, Math.max(low, value));
const factorExplanation = (row: ChannelBaseValue) => {
  const state = stateFor(row);
  if (!state) return "尚未完成首次评估，没有可解释的计算数据。";
  if (row.base_weight <= 0) return "基础权重为 0，该渠道不参与调权；所有性能系数保持中性值 1.000。";
  const lines: string[] = [];
  if (state.metric_ready && state.baseline_ready && state.baseline_ttft_p50 > 0 && state.baseline_ttft_p90 > 0 && state.baseline_ttft_p95 > 0) {
    const ratio = .5 * state.metric_ttft_p50 / state.baseline_ttft_p50 + .3 * state.metric_ttft_p90 / state.baseline_ttft_p90 + .2 * state.metric_ttft_p95 / state.baseline_ttft_p95;
    const raw = Math.pow(1 / ratio, .35 * policy.continuous.sensitivity);
    lines.push(`速度 ${factor(state.k_speed)}\nR = 50%×(${state.metric_ttft_p50.toFixed(3)}/${state.baseline_ttft_p50.toFixed(3)}) + 30%×(${state.metric_ttft_p90.toFixed(3)}/${state.baseline_ttft_p90.toFixed(3)}) + 20%×(${state.metric_ttft_p95.toFixed(3)}/${state.baseline_ttft_p95.toFixed(3)}) = ${ratio.toFixed(4)}\nclamp((1/R)^(0.35×${policy.continuous.sensitivity}), 0.75, 1.25) = ${clampNumber(raw, .75, 1.25).toFixed(3)}`);
  } else {
    lines.push(`速度 ${factor(state.k_speed)}\n回退为 1.000：${!state.metric_ready ? "请求数不足或 TTFT P50/P90/P95 不完整" : "同模型合格渠道不足 2 个，无法形成中位数基线"}。`);
  }
  if (state.cache_ready && state.baseline_cache > 0) {
    const ratio = state.metric_cache / state.baseline_cache;
    const raw = Math.pow(ratio, .15 * policy.continuous.sensitivity);
    lines.push(`缓存 ${factor(state.k_cache)}\n命中率 ${percent(state.metric_cache)} ÷ 同模型中位数 ${percent(state.baseline_cache)} = ${ratio.toFixed(4)}\nclamp(比值^(0.15×${policy.continuous.sensitivity}), 0.90, 1.10) = ${clampNumber(raw, .9, 1.1).toFixed(3)}`);
  } else {
    lines.push(`缓存 ${factor(state.k_cache)}\n回退为 1.000：窗口提示 Token 不足 10000，或同模型缓存基线不足。`);
  }
  if (state.otps_ready && state.baseline_otps > 0) {
    const ratio = state.metric_otps / state.baseline_otps;
    const upper = Math.min(1.2, policy.continuous.otps_cap);
    const raw = Math.pow(ratio, .25 * policy.continuous.sensitivity);
    lines.push(`输出 ${factor(state.k_otps)}\nOTPS ${state.metric_otps.toFixed(2)} ÷ 同模型中位数 ${state.baseline_otps.toFixed(2)} = ${ratio.toFixed(4)}\nclamp(比值^(0.25×${policy.continuous.sensitivity}), 0.80, ${upper.toFixed(2)}) = ${clampNumber(raw, .8, upper).toFixed(3)}`);
  } else {
    lines.push(`输出 ${factor(state.k_otps)}\n回退为 1.000：窗口输出 Token 不足 100，或同模型 OTPS 基线不足。`);
  }
  lines.push(`错误 ${factor(state.k_error)}\n平滑渠道错误率 ${(state.smoothed_error_rate * 100).toFixed(2)}%；按 ≤1%→1.000、5%→0.850、15%→0.500、≥30%→0.200 分段线性换算。用户自身错误不处罚渠道。`);
  return lines.join("\n\n");
};
const seconds = (value?: number) => value ? `${value.toFixed(2)}s` : "—";
const percent = (value?: number) => value == null ? "—" : `${(value * 100).toFixed(1)}%`;
const comparisonClass = (value?: number, baseline = 1) => value == null || value === baseline ? "" : value > baseline ? "positive" : "negative";
const weightClass = (row: ChannelBaseValue) => comparisonClass(stateFor(row)?.proposed_weight, row.base_weight);
const modelMode = (model: string) => policy.dispatch_modes[model] || "off";
const modeText = (model: string) => ({ off: "已关闭", observe: "只观察", auto: "自动执行" }[modelMode(model)]);
const modeType = (model: string) => modelMode(model) === "auto" ? "success" : modelMode(model) === "observe" ? "warning" : "info";
const effectivePause = (s?: TuningContinuousState) => s?.paused_reason === "manual_override" ? "" : s?.paused_reason || "";
const phaseText = (s?: TuningContinuousState) => !s ? "等待首次评估" : effectivePause(s) === "write_failed" ? `写入 new-api 失败已暂停，每10分钟自动重试${s.last_write_error ? `：${s.last_write_error}` : ""}` : effectivePause(s) ? "安全保护已暂停" : s.phase === "circuit" ? `已熔断，下次检测 ${s.next_probe_at ? formatTime(s.next_probe_at) : "待定"}` : s.phase === "probing" ? `恢复检测 ${s.probe_attempts || 0}/${policy.continuous.probe_count}` : s.phase === "soft_start" ? "恢复中（低权重运行）" : "运行正常";
const phaseType = (s?: TuningContinuousState) => s?.phase === "circuit" ? "danger" : s?.phase === "probing" || s?.phase === "soft_start" || effectivePause(s) ? "warning" : "success";
const eventName = (rule: string) => ({ weight_observed: "观察到权重变化", weight_write: "自动调整权重", manual_takeover: "检测到人工修改", auto_paused: "安全保护暂停", circuit_opened: "渠道熔断", probe_started: "开始恢复检测", probe_failed: "恢复检测未通过", circuit_recovered: "渠道恢复" } as Record<string, string>)[rule] || rule;
const eventCount = (days: number, rule: string) => events.value.filter(x => validEvent(x) && x.rule === rule && new Date(x.created_at).getTime() >= Date.now() - days * 86400000).length;
const sampleText = (row: ChannelBaseValue) => `${stateFor(row)?.last_observed_requests ?? 0}/${policy.continuous.min_samples}`;
const evaluationText = (row: ChannelBaseValue) => {
  const state = stateFor(row), requests = state?.last_observed_requests ?? 0;
  if ((row.models?.length ?? 1) > 1 || state?.paused_reason === "mixed_channel") return "多模型渠道，安全暂停";
  if (modelMode(row.model_name) === "off") return "模型已关闭";
  if (row.base_weight <= 0) return "基础权重为 0，未参与调权";
  if (state?.paused_reason || (state?.phase && state.phase !== "normal")) return phaseText(state);
  if (requests < policy.continuous.min_samples) return `样本不足 ${requests}/${policy.continuous.min_samples}`;
  if (!state?.metric_ready) return "缺少完整 TTFT 数据";
  return state.baseline_ready ? "已参与本轮计算" : "合格渠道不足 2 个";
};
const lastEvaluationAt = computed(() => activeRows.value.map(row => stateFor(row)?.updated_at).filter((value): value is string => !!value).sort().at(-1));
const refreshNow = ref(Date.now());
const evaluationAgeMinutes = computed(() => lastEvaluationAt.value ? Math.max(0, (refreshNow.value - new Date(lastEvaluationAt.value).getTime()) / 60000) : 0);
const evaluationStalled = computed(() => !!lastEvaluationAt.value && evaluationAgeMinutes.value >= 3);
const replacePolicy = (value: TuningPolicy) => {
  for (const key of Object.keys(policy)) delete (policy as unknown as Record<string, unknown>)[key];
  Object.assign(policy, clone(value));
};
const captureSavedState = () => {
  savedBases.value = clone(bases.value);
  savedPolicy.value = clone(policy);
  savedMode.value = mode.value;
};
const cancelChanges = (notify = true) => {
  if (!savedPolicy.value) return;
  bases.value = clone(savedBases.value);
  replacePolicy(savedPolicy.value);
  mode.value = savedMode.value;
  dirty.value = false;
  if (notify) ElMessage.info("已取消未保存的更改");
};
const selectModel = (model: string) => {
  if (model === activeModel.value) return;
  if (dirty.value) cancelChanges(false);
  activeModel.value = model;
};

async function load() {
  await filters.loadInstances(); if (!siteID.value) return;
  loading.value = true;
  try {
    const [p, b, r] = await Promise.all([dashboard.tuningPolicy(siteID.value), dashboard.tuningBaseValues(siteID.value), dashboard.tuningRecommendations(siteID.value, 300)]);
    mode.value = p.mode; Object.assign(policy, p.policy); policy.continuous ||= defaults(); policy.dispatch_modes ||= {};
    bases.value = b.items ?? []; events.value = r.items ?? []; for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    if (!models.value.includes(activeModel.value)) activeModel.value = models.value[0] || "";
    try { states.value = (await dashboard.tuningContinuousStates(siteID.value)).items ?? []; } catch { states.value = []; }
    dirty.value = false; captureSavedState();
  } finally { loading.value = false; }
}
let refreshTimer: ReturnType<typeof setInterval> | undefined;
const refreshError = ref("");
async function refreshRuntime() {
  if (!siteID.value || loading.value) return;
  refreshNow.value = Date.now();
  try {
    const [s, r, b] = await Promise.all([dashboard.tuningContinuousStates(siteID.value), dashboard.tuningRecommendations(siteID.value, 300), dashboard.tuningBaseValues(siteID.value)]);
    states.value = s.items ?? []; events.value = r.items ?? [];
    const refreshed = b.items ?? [];
    if (dirty.value) {
      const local = new Map(bases.value.map(row => [`${row.channel_id}:${row.model_name}`, row]));
      bases.value = refreshed.map(row => {
        const edited = local.get(`${row.channel_id}:${row.model_name}`);
        return edited ? { ...row, base_weight: edited.base_weight, base_priority: edited.base_priority } : row;
      });
    } else {
      bases.value = refreshed;
    }
    for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    if (!models.value.includes(activeModel.value)) activeModel.value = models.value[0] || "";
    if (!dirty.value) captureSavedState();
    refreshError.value = "";
  } catch (error) {
    refreshError.value = error instanceof Error ? error.message : "刷新失败";
  }
}
async function sync(kind: "weight" | "priority") {
  // Refresh overwrites base values with the CURRENT online values and saves
  // immediately. With auto dispatch running, online weights are computed
  // results (base x multiplier) - silently adopting them as the new anchor
  // would compound the multiplier on itself round after round.
  if (bases.value.length) {
    await ElMessageBox.confirm(
      "刷新会用线上当前值覆盖基础值并立即保存。若自动调度已运行，线上权重是计算结果——覆盖后它将成为新的调整基准。确认继续？",
      "覆盖基础值",
      { type: "warning", confirmButtonText: "覆盖并保存" },
    );
  }
  saving.value = true;
  try {
    const rows = (await dashboard.syncTuningBaseValues(siteID.value, models.value)).items ?? [];
    const index = new Map(bases.value.map(x => [`${x.channel_id}:${x.model_name}`, x]));
    for (const row of rows) { const old = index.get(`${row.channel_id}:${row.model_name}`); if (!old) bases.value.push(row); else if (kind === "weight") old.base_weight = row.current_weight; else old.base_priority = row.current_priority; }
    for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    if (!activeModel.value) activeModel.value = models.value[0] || "";
    bases.value = (await dashboard.saveTuningBaseValues(siteID.value, bases.value)).items ?? [];
    dirty.value = false; captureSavedState();
    ElMessage.success("基础值已从 new-api 更新并保存");
  } finally { saving.value = false; }
}
async function runAutoPreflight() {
  const newlyAutoModels = Object.entries(policy.dispatch_modes)
    .filter(([model, nextMode]) => nextMode === "auto" && savedPolicy.value?.dispatch_modes?.[model] !== "auto")
    .map(([model]) => model);
  if (!newlyAutoModels.length) return "";
  const channel = bases.value.find(row => newlyAutoModels.includes(row.model_name) && row.channel_id > 0);
  if (!channel) throw new Error("自动模式预检失败：当前模型没有可验证的渠道");
  ElMessage.info("正在验证与 new-api 的控制链路，请稍候…");
  const started = await dashboard.startTuningPreflight(siteID.value, channel.channel_id);
  // Direct-control sites verify synchronously: the POST response is already
  // terminal. Only agent-queue sites need the polling loop below.
  const settle = (status: string, error?: string) => {
    if (status === "succeeded") return started.command_id;
    if (["failed", "expired"].includes(status)) {
      throw new Error(`自动模式预检失败：${error || (status === "expired" ? "Agent 未及时领取验证命令" : "new-api 控制命令执行失败")}`);
    }
    return "";
  };
  const immediate = settle(started.status, started.error);
  if (immediate) return immediate;
  for (let attempt = 0; attempt < 45; attempt++) {
    const result = await dashboard.tuningPreflight(siteID.value, started.command_id);
    const settled = settle(result.status, result.error);
    if (settled) return settled;
    await wait(1000);
  }
  throw new Error("自动模式预检超时：请确认 Agent 在线且上报周期正常");
}
async function save() {
  saving.value = true;
  try {
    const preflightCommandID = await runAutoPreflight();
    await dashboard.saveTuningPolicy(siteID.value, policy, mode.value, preflightCommandID || undefined);
    bases.value = (await dashboard.saveTuningBaseValues(siteID.value, bases.value)).items ?? [];
    ElMessage.success(preflightCommandID ? "控制能力验证通过，自动模式已启用" : "设置已保存，将在下一分钟生效");
    await load();
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "保存失败");
  } finally { saving.value = false; }
}
watch(() => filters.site_id, () => void load());
watch([eventModelFilter, eventRuleFilter, eventChannelQuery, activeModel], () => { eventPage.value = 1; });
onMounted(() => { void load(); refreshTimer = setInterval(() => void refreshRuntime(), 30000); });
onBeforeUnmount(() => { if (refreshTimer) clearInterval(refreshTimer); });
</script>

<template><AppShell title="调权中心"><div v-loading="loading" class="page" :class="{'events-page':activeTab==='events'}">
  <el-tabs v-model="activeTab" class="tabs">
    <el-tab-pane label="运行概览" name="overview">
      <el-card shadow="never" class="workspace-card"><template #header><div class="head"><div class="title-line"><b>模型与渠道</b><small>每个模型单独选择关闭、只观察或自动执行</small><div class="inline-metrics"><span>自动 <b>{{ counts.auto }}</b></span><span>观察 <b>{{ counts.observe }}</b></span><span>关闭 <b>{{ counts.off }}</b></span></div></div><div class="tools"><el-button @click="helpOpen=true">使用说明</el-button><el-button :loading="saving" @click="sync('weight')">初始化/刷新基础值</el-button><el-button v-if="dirty" :disabled="saving" @click="cancelChanges()">取消更改</el-button><el-button v-if="dirty" type="primary" :loading="saving" @click="save">保存更改</el-button></div></div></template>
        <el-empty v-if="!models.length" description="还没有渠道基础值"><el-button type="primary" @click="sync('weight')">立即从 new-api 读取</el-button></el-empty>
        <div v-else class="model-workspace">
          <aside class="model-nav"><el-input v-model="modelQuery" clearable placeholder="搜索模型"/><div class="model-list"><button v-for="model in visibleModels" :key="model" :class="{active:activeModel===model}" @click="selectModel(model)"><span><b>{{ model }}</b><small>{{ bases.filter(x=>x.model_name===model).length }} 个渠道</small></span><span class="model-status"><el-tag :type="modeType(model)" effect="plain" size="small">{{ modeText(model) }}</el-tag></span></button><el-empty v-if="!visibleModels.length" :image-size="48" description="没有匹配模型"/></div></aside>
          <section class="model-detail"><div class="model-head"><div><b>{{ activeModel }}</b><small>{{ activeRows.length }} 个渠道</small><small v-if="refreshError" class="stale">刷新失败：{{ refreshError }}</small><small v-else-if="evaluationStalled" class="stale">评估已停滞：最后成功于 {{ formatTime(lastEvaluationAt!) }}</small><small v-else-if="lastEvaluationAt">最近评估 {{ formatTime(lastEvaluationAt) }} · 每 30 秒自动刷新</small></div><el-radio-group v-if="activeModel" v-model="policy.dispatch_modes[activeModel]" size="small" @change="dirty=true"><el-radio-button value="off">关闭</el-radio-button><el-radio-button value="observe">只观察</el-radio-button><el-radio-button value="auto">自动执行</el-radio-button></el-radio-group></div>
            <el-collapse class="evidence-collapse"><el-collapse-item title="查看本轮原始指标与同模型中位数" name="evidence"><div class="evidence-grid"><div v-for="row in activeRows" :key="`${row.channel_id}:${row.model_name}`" class="evidence-row"><b>{{ row.channel_name }}</b><span>TTFT {{ seconds(stateFor(row)?.metric_ttft_p50) }}/{{ seconds(stateFor(row)?.metric_ttft_p90) }}/{{ seconds(stateFor(row)?.metric_ttft_p95) }} → 中位数 {{ seconds(stateFor(row)?.baseline_ttft_p50) }}/{{ seconds(stateFor(row)?.baseline_ttft_p90) }}/{{ seconds(stateFor(row)?.baseline_ttft_p95) }}</span><span>缓存 {{ stateFor(row)?.cache_ready ? `${percent(stateFor(row)?.metric_cache)} → ${percent(stateFor(row)?.baseline_cache)}` : "证据不足，不参与" }}</span><span>输出 {{ stateFor(row)?.otps_ready ? `${stateFor(row)?.metric_otps.toFixed(1)} → ${stateFor(row)?.baseline_otps.toFixed(1)} OTPS` : "证据不足，不参与" }}</span><span>平滑渠道错误率 {{ percent(stateFor(row)?.smoothed_error_rate) }}</span></div></div></el-collapse-item></el-collapse>
            <el-table :data="activeRows" :row-key="channelRowKey" size="small" height="calc(100vh - 230px)"><el-table-column prop="channel_id" label="ID" width="76"/><el-table-column prop="channel_name" label="渠道" min-width="170"/><el-table-column prop="group_name" label="分组" min-width="110"><template #default="{row}">{{ row.group_name || "—" }}</template></el-table-column><el-table-column label="评估状态" min-width="180"><template #default="{row}"><div class="evaluation"><span>{{ evaluationText(row) }}</span><small>窗口样本 {{ sampleText(row) }}</small></div></template></el-table-column><el-table-column label="评估系数" min-width="220"><template #default="{row}"><el-popover trigger="hover" placement="top-start" :width="520" popper-class="factor-explain-popover"><template #reference><span class="factors explainable"><span :class="comparisonClass(stateFor(row)?.k_speed)">速度 {{ factor(stateFor(row)?.k_speed) }}</span> · <span :class="comparisonClass(stateFor(row)?.k_cache)">缓存 {{ factor(stateFor(row)?.k_cache) }}</span> · <span :class="comparisonClass(stateFor(row)?.k_otps)">输出 {{ factor(stateFor(row)?.k_otps) }}</span> · <span :class="comparisonClass(stateFor(row)?.k_error)">错误 {{ factor(stateFor(row)?.k_error) }}</span></span></template><pre class="factor-explanation">{{ factorExplanation(row) }}</pre></el-popover></template></el-table-column><el-table-column label="基础权重" width="122"><template #default="{row}"><el-input-number v-model="row.base_weight" :min="0" size="small" controls-position="right" @change="dirty=true"/></template></el-table-column><el-table-column width="105"><template #header><span class="column-help">计算权重<el-popover trigger="click" width="460"><template #reference><button class="help" aria-label="查看计算权重说明">i</button></template><div class="calc-details"><div class="calc-title"><b>计算权重说明</b><code>round(基础权重 × 综合倍率)</code></div><p class="formula">综合倍率 = clamp(速度 × 缓存 × 输出 × 错误，0.500，1.500)</p><dl><div><dt>速度</dt><dd>比较该渠道与同模型渠道的 TTFT P50/P90/P95，按 50%/30%/20% 加权；越快系数越高。<small>来源：Agent 采集 new-api 日志的首字耗时，汇总到 metric_1m。</small></dd></div><div><dt>缓存</dt><dd>渠道缓存命中率相对同模型中位数换算；证据不足时不参与计算。<small>来源：new-api 日志中的缓存 token ÷提示 token。</small></dd></div><div><dt>输出</dt><dd>渠道 OTPS 相对同模型中位数换算；证据不足时不参与计算。<small>来源：成功流式请求的输出 token ÷生成耗时。</small></dd></div><div><dt>错误</dt><dd>分钟渠道错误率经 EWMA 平滑后换算；用户自身错误不处罚渠道。<small>来源：new-api 请求状态，由配置的用户错误码规则分类。</small></dd></div></dl><p class="calc-note">至少需要 2 个达到最少请求数且有完整 TTFT 数据的同模型渠道；模型关闭或数据不足时倍率保持 1.000。</p></div></el-popover></span></template><template #default="{row}"><span :class="weightClass(row)">{{ stateFor(row)?.proposed_weight ?? "—" }}</span></template></el-table-column><el-table-column prop="current_weight" label="当前权重" width="100"/><el-table-column label="基础优先级" width="122"><template #default="{row}"><el-input-number v-model="row.base_priority" :min="0" size="small" controls-position="right" @change="dirty=true"/></template></el-table-column><el-table-column prop="current_priority" label="线上优先级" width="110"/></el-table>
          </section>
        </div>
      </el-card>
    </el-tab-pane>
    <el-tab-pane label="变更记录" name="events">
      <div class="summary compact"><div><span>近 7 天自动调权</span><b>{{ eventCount(7,'weight_write') }}</b></div><div><span>近 7 天熔断</span><b>{{ eventCount(7,'circuit_opened') }}</b></div><div><span>近 7 天恢复</span><b>{{ eventCount(7,'circuit_recovered') }}</b></div><div><span>近 7 天人工接管</span><b>{{ eventCount(7,'manual_takeover') }}</b></div></div>
      <el-card shadow="never" class="event-history-card">
        <div class="event-toolbar"><div class="event-filters">
            <el-select v-model="eventModelFilter" placeholder="模型" style="width:220px"><el-option label="全部模型" value=""/><el-option :label="`当前模型${activeModel ? `（${activeModel}）` : ''}`" value="__current__"/><el-option v-for="model in models" :key="model" :label="model" :value="model"/></el-select>
            <el-select v-model="eventRuleFilter" clearable placeholder="全部事件" style="width:170px"><el-option v-for="rule in eventRuleOptions" :key="rule" :label="eventName(rule)" :value="rule"/></el-select>
            <el-input v-model="eventChannelQuery" clearable placeholder="搜索渠道名称或 ID" style="width:240px"/>
          </div></div>
        <div v-if="filteredEvents.length" class="event-table-wrap"><el-table :data="pagedEvents" :fit="false" size="small" height="100%">
          <el-table-column label="时间" width="170"><template #default="{row}">{{ formatTime(row.created_at) }}</template></el-table-column>
          <el-table-column label="模型" width="210" show-overflow-tooltip><template #default="{row}">{{ eventModel(row) || '—' }}</template></el-table-column>
          <el-table-column prop="channel_name" label="渠道" width="360" show-overflow-tooltip><template #default="{row}"><b>{{ row.channel_name }}</b><small class="channel-id">ID {{ row.channel_id }}</small></template></el-table-column>
          <el-table-column label="事件" width="145"><template #default="{row}"><el-tag :type="row.rule==='weight_write'?'success':row.rule==='circuit_opened'?'danger':'warning'">{{ eventName(row.rule) }}</el-tag></template></el-table-column>
          <el-table-column label="权重变化" width="125"><template #default="{row}">{{ row.current_weight }} → {{ row.proposed_weight }}</template></el-table-column>
          <el-table-column label="模式" width="90"><template #default="{row}">{{ row.mode_at_creation==='auto'?'自动':row.mode_at_creation==='observe'?'观察':'关闭' }}</template></el-table-column>
        </el-table></div>
        <div v-if="filteredEvents.length" class="event-footer"><el-pagination v-model:current-page="eventPage" v-model:page-size="eventPageSize" layout="sizes, prev, pager, next" :page-sizes="[20,50,100]" :total="filteredEvents.length"/></div>
        <el-empty v-else description="当前筛选条件下暂无记录"/>
      </el-card>
    </el-tab-pane>
    <el-tab-pane label="规则设置" name="settings"><el-card shadow="never"><template #header><div class="head"><div><b>系统如何计算权重</b><small>通常保持默认值即可</small></div></div></template>
      <div class="flow"><div><i>1</i><b>同模型比较</b><span>只比较相同模型的渠道</span></div><div><i>2</i><b>综合评分</b><span>速度、缓存、输出与错误</span></div><div><i>3</i><b>计算权重</b><span>基础权重 × 综合倍率</span></div><div><i>4</i><b>安全执行</b><span>自动执行才写入线上</span></div></div>
      <el-form label-position="top"><div class="params"><el-form-item label="调整灵敏度"><el-input-number v-model="policy.continuous.sensitivity" :min=".1" :max="5" :step=".1" @change="dirty=true"/><small>放大或缩小渠道相对差异；1 为标准</small></el-form-item><el-form-item label="输出加权上限"><el-input-number v-model="policy.continuous.otps_cap" :min="1" :max="3" :step=".1" @change="dirty=true"/><small>限制 OTPS 输出系数，最终综合倍率仍封顶 1.5</small></el-form-item><el-form-item label="评估窗口（分钟）"><el-input-number v-model="policy.continuous.window_minutes" :min="1" @change="dirty=true"/><small>每次计算使用最近多少分钟的指标</small></el-form-item><el-form-item label="每渠道最少请求数"><el-input-number v-model="policy.continuous.min_samples" :min="1" @change="dirty=true"/><small>低于此数量不参与本轮性能比较，错误历史仍参与可靠性计算</small></el-form-item></div></el-form>
      <el-collapse><el-collapse-item title="高级安全设置：熔断与自动恢复" name="safety"><p class="safety">性能差异只会降权；平滑渠道错误率达到阈值才熔断。自动模式静默后主动探测，观察模式使用真实流量被动恢复。</p><div class="params"><el-form-item label="熔断错误率"><el-input-number v-model="policy.continuous.circuit_error_rate" :min=".01" :max="1" :step=".01" @change="dirty=true"/><small>平滑渠道错误率达到此值才停止分流</small></el-form-item><el-form-item label="被动恢复错误率"><el-input-number v-model="policy.continuous.recovery_error_rate" :min="0" :max=".99" :step=".01" @change="dirty=true"/><small>观察模式降到此值后解除模拟熔断</small></el-form-item><el-form-item label="探针恢复阈值"><el-input-number v-model="policy.continuous.recovery_threshold" :min=".01" :max="1" :step=".01" @change="dirty=true"/><small>自动模式探针成功率×探针速度达到此值才恢复</small></el-form-item><el-form-item label="熔断静默期（分钟）"><el-input-number v-model="policy.continuous.silent_minutes" :min="1" @change="dirty=true"/><small>自动模式熔断后等待多久再开始探测</small></el-form-item><el-form-item label="探测间隔（秒）"><el-input-number v-model="policy.continuous.probe_interval_seconds" :min="1" @change="dirty=true"/><small>连续探测请求之间的等待时间</small></el-form-item><el-form-item label="探测次数"><el-input-number v-model="policy.continuous.probe_count" :min="1" @change="dirty=true"/><small>一次恢复判断发送多少次请求</small></el-form-item><el-form-item label="恢复初始倍率"><el-input-number v-model="policy.continuous.soft_start_multiplier" :min=".01" :max="1" :step=".05" @change="dirty=true"/><small>恢复首轮使用基础权重的比例</small></el-form-item></div></el-collapse-item></el-collapse>
    </el-card></el-tab-pane>
  </el-tabs>
  <el-drawer v-model="helpOpen" title="调权中心使用说明" size="560px" class="tuning-help">
    <div class="help-guide">
      <section><h3>最简单的理解</h3><ol><li>系统每分钟计算一次渠道权重。</li><li>关闭模式不计算；只观察模式只展示；自动执行模式会写入 new-api。</li><li>自动模式下，整数计算权重只要变化就立即写入；没有变化就不重复写。</li></ol></section>
      <section><h3>第一次使用</h3><ol><li>点击“初始化/刷新基础值”，读取当前线上权重和优先级。</li><li>先选择“只观察”，确认计算结果合理。</li><li>再切换为“自动执行”并保存；系统会先验证 new-api 控制链路。</li></ol></section>
      <section><h3>三个权重</h3><dl><div><dt>基础权重</dt><dd>计算基准，可手动修改。</dd></div><div><dt>计算权重</dt><dd>基础权重乘以速度、缓存、输出和错误四项系数后的整数结果。</dd></div><div><dt>当前权重</dt><dd>new-api 当前实际权重；渠道快照刷新后会与最近写入结果一致。</dd></div></dl></section>
      <section><h3>自动模式</h3><p>每分钟重新计算。只要计算权重与上次成功写入值不同，就写入 new-api。不存在写入死区，也不等待最小写入间隔；如果线上权重被人工或其他系统修改，不会暂停调权，下一轮会重新写回当前计算权重。</p></section>
      <section><h3>保留的安全保护</h3><dl><div><dt>熔断</dt><dd>渠道错误率达到阈值且样本足够时，将权重和优先级置为 0。</dd></div><div><dt>恢复</dt><dd>静默期后主动探测；通过后先以低权重恢复，再回到正常计算。</dd></div><div><dt>多模型渠道</dt><dd>一个渠道同时服务多个模型时不自动调权，避免模型之间互相影响。</dd></div><div><dt>写入失败</dt><dd>连续失败 3 次后暂停每分钟写入，改为每 10 分钟重试，成功后自动恢复。</dd></div></dl></section>
      <section><h3>表格与记录</h3><p>“评估状态”直接显示样本不足、熔断、恢复中或写入失败等原因；“变更记录”保存每次自动写入、熔断和恢复。计算公式可点击“计算权重”旁的 i 查看。</p></section>
    </div>
  </el-drawer>
</div></AppShell></template>

<style scoped>
.page{display:grid;gap:14px;padding-bottom:70px}.hero{display:flex;justify-content:space-between;align-items:center;gap:20px;padding:20px 24px;border:1px solid #dfe7f3;border-radius:10px;background:linear-gradient(120deg,#fff,#f3f7ff)}.hero span,.hero p,.head small,.hero-state small,.summary small,.proposal small,.model-head small,.model-list small{color:#8491a5}.hero h2{margin:4px 0}.hero p{margin:0}.hero-state{display:flex;align-items:center;gap:12px;min-width:300px;padding:12px 16px;border-radius:8px;background:#fff}.hero-state div{display:flex;flex-direction:column;gap:4px}.hero-state i{width:10px;height:10px;border-radius:50%;background:#f3a326;box-shadow:0 0 0 5px #fdf1db}.hero-state i.active{background:#21a675;box-shadow:0 0 0 5px #dff5ec}.tabs :deep(.el-tabs__header){margin:0;padding:0 16px;border:1px solid #e1e8f2;border-radius:8px;background:#fff}.summary{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:14px 0}.summary>div{display:flex;flex-direction:column;gap:6px;padding:16px 18px;border:1px solid #e1e8f2;border-radius:8px;background:#fff}.summary b{font-size:26px}.summary .danger b{color:#d84a4a}.summary.compact b{font-size:22px}.head,.model-head,.tools{display:flex;align-items:center;justify-content:space-between;gap:12px}.title-line{display:flex;align-items:center;gap:8px}.title-line small{margin-left:4px}.help{display:grid;place-items:center;width:18px;height:18px;padding:0;border:1px solid #aeb9ca;border-radius:50%;background:#fff;color:#718096;font-size:12px;font-weight:700;cursor:help}.help-copy{margin:8px 0 0;color:#596579;line-height:1.6}.model-workspace{display:grid;grid-template-columns:260px minmax(0,1fr);min-height:560px;border:1px solid #e2e8f2;border-radius:8px;overflow:hidden}.model-nav{padding:12px;border-right:1px solid #e2e8f2;background:#f7f9fc}.model-list{display:grid;gap:5px;margin-top:10px;max-height:510px;overflow:auto}.model-list>button{display:flex;align-items:center;justify-content:space-between;gap:8px;width:100%;padding:10px;border:1px solid transparent;border-radius:7px;background:transparent;text-align:left;cursor:pointer}.model-list>button:hover{background:#fff}.model-list>button.active{border-color:#b9cdfb;background:#eaf1ff;color:#245eea}.model-list>button>span:first-child{display:flex;min-width:0;flex-direction:column;gap:3px}.model-list b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.model-status{display:flex;align-items:center;gap:6px}.model-detail{min-width:0;background:#fff}.model-head{min-height:34px;padding:10px 12px;background:#f7f9fc}.model-head>div{display:flex;align-items:center;gap:10px}.proposal{display:flex;align-items:center;gap:6px;flex-wrap:wrap}.proposal>b{font-size:18px;color:#245eea}.proposal button{padding:0;border:0;background:none;color:#4e73b8;cursor:pointer}.proposal small{flex-basis:100%}.factors p{display:flex;justify-content:space-between}.channel{margin:0 12px}.flow{display:grid;grid-template-columns:repeat(4,1fr);gap:10px;margin-bottom:20px}.flow>div{display:grid;grid-template-columns:30px 1fr;gap:3px 8px;padding:14px;border:1px solid #dfe7f3;border-radius:8px;background:#f8faff}.flow i{grid-row:1/3;display:grid;place-items:center;width:28px;height:28px;border-radius:50%;background:#3168e8;color:#fff;font-style:normal}.flow span,.params small{font-size:12px;color:#657086}.params{display:grid;grid-template-columns:repeat(5,minmax(150px,1fr));gap:0 16px}.params :deep(.el-form-item__content){display:flex;flex-direction:column;align-items:flex-start}.safety{padding:10px 12px;border-radius:6px;background:#fff7e8;color:#76551b}.save{position:fixed;z-index:20;right:24px;bottom:18px;display:flex;align-items:center;gap:18px;padding:10px 12px 10px 18px;border:1px solid #f0d59b;border-radius:8px;background:#fffaf0;box-shadow:0 8px 28px #17233b24}@media(max-width:1200px){.summary,.flow{grid-template-columns:1fr 1fr}.params{grid-template-columns:repeat(3,1fr)}.hero{align-items:flex-start;flex-direction:column}.hero-state{width:100%}.model-workspace{grid-template-columns:220px minmax(720px,1fr);overflow:auto}}@media(max-width:760px){.model-workspace{display:block}.model-nav{border-right:0;border-bottom:1px solid #e2e8f2}.model-list{display:flex;max-height:none;overflow:auto}.model-list>button{min-width:190px}.title-line small{display:none}}
.primary-metrics{display:flex;gap:0;margin:10px 0}.primary-metrics>div{flex-direction:row;align-items:center;gap:8px;padding:5px 16px;border:0;border-right:1px solid #e1e8f2;border-radius:0;background:transparent}.primary-metrics>div:first-child{padding-left:4px}.primary-metrics>div:last-child{border-right:0}.primary-metrics b{font-size:17px}.primary-metrics span{color:#68758a}
.page{padding-bottom:0}.model-workspace{height:calc(100vh - 256px);min-height:420px}.model-list{max-height:calc(100vh - 330px)}
.workspace-card :deep(.el-card__header){padding:8px 12px}.workspace-card :deep(.el-card__body){padding:8px}.workspace-card .head{min-height:32px}
.model-workspace{grid-template-columns:300px minmax(0,1fr)}.model-list{overflow-x:hidden;overflow-y:auto}.model-list>button>span:first-child{flex:1}.model-status{flex:0 0 auto}@media(max-width:760px){.model-list{overflow-x:auto;overflow-y:hidden}}
.inline-metrics{display:flex;align-items:center;margin-left:8px;color:#68758a}.inline-metrics span{padding:0 10px;border-left:1px solid #dfe6f0;font-size:12px;white-space:nowrap}.inline-metrics b{margin-left:3px;font-size:14px;color:#17233b}.inline-metrics .danger b{color:#d84a4a}@media(max-width:1100px){.title-line>small{display:none}.inline-metrics{margin-left:0}}
.model-workspace{height:calc(100vh - 176px)}.model-list{max-height:calc(100vh - 250px)}
.proposal .result{font-size:18px;font-weight:700;color:#245eea}
.column-help{display:inline-flex;align-items:center;gap:5px}.column-help .help{width:16px;height:16px;font-size:11px}
:global(.calc-details){color:#3d4859}:global(.calc-title){display:flex;align-items:center;justify-content:space-between;gap:16px}:global(.calc-title code){padding:5px 8px;border-radius:5px;background:#f2f5fa;color:#245eea}:global(.calc-details .formula){margin:10px 0;padding:8px 10px;border-radius:6px;background:#f4f7fc;font-weight:600}:global(.calc-details dl){display:grid;gap:9px;margin:0}:global(.calc-details dl>div){display:grid;grid-template-columns:92px 1fr;gap:10px;padding-top:9px;border-top:1px solid #edf0f5}:global(.calc-details dt){display:flex;justify-content:space-between;font-weight:600}:global(.calc-details dt b){color:#245eea}:global(.calc-details dd){margin:0;line-height:1.45}:global(.calc-details dd small){display:block;margin-top:3px;color:#8491a5}:global(.calc-note){margin:10px 0 0;color:#7b5c24;font-size:12px}
.help-guide{display:grid;gap:22px;color:#3d4859}.help-guide section{padding-bottom:18px;border-bottom:1px solid #e8edf4}.help-guide section:last-child{border-bottom:0}.help-guide h3{margin:0 0 9px;color:#17233b}.help-guide p,.help-guide li,.help-guide dd{line-height:1.7}.help-guide p,.help-guide ol,.help-guide ul,.help-guide dl{margin:0}.help-guide ol,.help-guide ul{padding-left:22px}.help-guide dl{display:grid;gap:8px}.help-guide dl>div{display:grid;grid-template-columns:90px 1fr;gap:12px}.help-guide dt{font-weight:600;color:#245eea}.help-guide dd{margin:0}
.help-guide .parameter-guide{gap:0;border:1px solid #e5eaf2;border-radius:8px;overflow:hidden}.help-guide .parameter-guide>div{grid-template-columns:118px 1fr;padding:10px 12px;border-bottom:1px solid #edf1f6}.help-guide .parameter-guide>div:last-child{border-bottom:0}.help-guide .parameter-guide dt{color:#253858}.help-guide .help-note{margin-top:10px;padding:9px 11px;border-radius:6px;background:#f5f7fa;color:#606b7d;font-size:13px}
.evaluation{display:flex;flex-direction:column;gap:2px}.evaluation small{color:#8491a5}.factors{color:#596579;font-size:12px;white-space:normal;line-height:1.75}.positive{color:#21a675;font-weight:600}.negative{color:#d84a4a;font-weight:600}
.model-head .stale{color:#d84a4a;font-weight:600}
.factors.explainable{cursor:help;border-bottom:1px dashed #aeb9ca}:global(.factor-explanation){margin:0;white-space:pre-wrap;color:#3d4859;font:12px/1.55 ui-monospace,SFMono-Regular,Consolas,monospace}:global(.factor-explain-popover){max-width:min(520px,calc(100vw - 32px))}
.evidence-collapse{margin:0 0 8px}.evidence-collapse :deep(.el-collapse-item__header){height:34px;padding:0 10px;color:#596579;font-size:12px}.evidence-grid{display:grid;gap:6px;padding:4px 10px 10px}.evidence-row{display:grid;grid-template-columns:minmax(150px,1.2fr) 2fr 1fr 1fr 1fr;gap:10px;color:#596579;font-size:12px}.evidence-row b{color:#17233b}.evidence-row span{white-space:nowrap}
.event-toolbar{position:absolute;z-index:2;top:20px;right:20px;left:20px;display:flex;flex-wrap:wrap;align-items:center;gap:12px 16px}.event-filters{display:flex;flex-wrap:wrap;gap:10px}.channel-id{display:block;color:#8491a5;font-weight:400}.event-footer{position:absolute;z-index:3;right:20px;bottom:8px;left:20px;display:flex;height:44px;align-items:center;justify-content:flex-end;background:#fff}
.page.events-page{height:calc(100vh - 88px);overflow:hidden}.events-page .tabs{height:100%}.events-page .tabs :deep(.el-tabs__content){height:calc(100% - 42px);overflow:hidden}.events-page .tabs :deep(.el-tab-pane){display:flex;height:100%;min-height:0;flex-direction:column;overflow:hidden}.events-page .event-history-card{min-height:0;flex:1}.events-page .event-history-card :deep(.el-card__body){position:relative;height:100%;min-height:0;padding:0}.event-table-wrap{position:absolute;top:64px;right:20px;bottom:60px;left:20px;min-height:0;overflow:hidden}.event-table-wrap :deep(.el-table){min-height:0}.event-history-card :deep(.el-empty){position:absolute;inset:64px 20px 20px}
</style>
