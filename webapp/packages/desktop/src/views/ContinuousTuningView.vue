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
const modelQuery = ref(""), activeModel = ref("");
const defaults = () => ({ sensitivity: 1, otps_cap: 1.5, circuit_threshold: .1, recovery_threshold: .2, circuit_error_rate: .3, recovery_error_rate: .1, silent_minutes: 5, probe_interval_seconds: 5, probe_count: 10, soft_start_multiplier: .2, window_minutes: 15, min_samples: 20, sparse_lookback_minutes: 360 });
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
const activeRows = computed(() => bases.value.filter(x => x.model_name === activeModel.value).sort((a, b) => b.base_priority - a.base_priority || b.base_weight - a.base_weight || a.channel_id - b.channel_id));
const stateMap = computed(() => new Map(states.value.map(x => [`${x.channel_id}:${x.model_name}`, x])));
const stateFor = (row: ChannelBaseValue) => stateMap.value.get(`${row.channel_id}:${row.model_name}`);
const recentEvents = computed(() => events.value.filter(x => ["weight_observed", "weight_write", "manual_takeover", "auto_paused", "circuit_opened", "probe_started", "probe_failed", "circuit_recovered"].includes(x.rule)));
const counts = computed(() => models.value.reduce((v, model) => { v[policy.dispatch_modes[model] || "off"]++; return v; }, { off: 0, observe: 0, auto: 0 }));
const abnormal = computed(() => states.value.filter(x => x.phase !== "normal" || x.paused_reason).length);
const modelAbnormal = (model: string) => bases.value.filter(row => row.model_name === model && stateFor(row) && (stateFor(row)?.phase !== "normal" || stateFor(row)?.paused_reason)).length;
const factor = (value?: number) => Number(value ?? 1).toFixed(3);
const seconds = (value?: number) => value ? `${value.toFixed(2)}s` : "—";
const percent = (value?: number) => value == null ? "—" : `${(value * 100).toFixed(1)}%`;
const comparisonClass = (value?: number, baseline = 1) => value == null || value === baseline ? "" : value > baseline ? "positive" : "negative";
const weightClass = (row: ChannelBaseValue) => comparisonClass(stateFor(row)?.proposed_weight, row.base_weight);
const modelMode = (model: string) => policy.dispatch_modes[model] || "off";
const modeText = (model: string) => ({ off: "已关闭", observe: "只观察", auto: "自动执行" }[modelMode(model)]);
const modeType = (model: string) => modelMode(model) === "auto" ? "success" : modelMode(model) === "observe" ? "warning" : "info";
const phaseText = (s?: TuningContinuousState) => !s ? "等待首次评估" : s.paused_reason === "manual_override" ? "人工修改后已暂停" : s.paused_reason ? "安全保护已暂停" : s.phase === "circuit" ? `已熔断，下次检测 ${s.next_probe_at ? formatTime(s.next_probe_at) : "待定"}` : s.phase === "probing" ? `恢复检测 ${s.probe_attempts || 0}/${policy.continuous.probe_count}` : s.phase === "soft_start" ? "恢复中（低权重运行）" : "运行正常";
const phaseType = (s?: TuningContinuousState) => s?.phase === "circuit" ? "danger" : s?.phase === "probing" || s?.phase === "soft_start" || s?.paused_reason ? "warning" : "success";
const eventName = (rule: string) => ({ weight_observed: "观察到权重变化", weight_write: "自动调整权重", manual_takeover: "检测到人工修改", auto_paused: "安全保护暂停", circuit_opened: "渠道熔断", probe_started: "开始恢复检测", probe_failed: "恢复检测未通过", circuit_recovered: "渠道恢复" } as Record<string, string>)[rule] || rule;
const eventCount = (days: number, rule: string) => events.value.filter(x => x.rule === rule && new Date(x.created_at).getTime() >= Date.now() - days * 86400000).length;
const sampleText = (row: ChannelBaseValue) => `${stateFor(row)?.last_observed_requests ?? 0}/${policy.continuous.min_samples}`;
const evaluationText = (row: ChannelBaseValue) => {
  const state = stateFor(row), requests = state?.last_observed_requests ?? 0;
  if ((row.models?.length ?? 1) > 1 || state?.paused_reason === "mixed_channel") return "多模型渠道，安全暂停";
  if (modelMode(row.model_name) === "off") return "模型已关闭";
  if (requests < policy.continuous.min_samples) return `样本不足 ${requests}/${policy.continuous.min_samples}`;
  if (!state?.metric_ready) return "缺少完整 TTFT 数据";
  return state.baseline_ready ? "已参与本轮计算" : "合格渠道不足 2 个";
};
const lastEvaluationAt = computed(() => activeRows.value.map(row => stateFor(row)?.updated_at).filter((value): value is string => !!value).sort().at(-1));

async function load() {
  await filters.loadInstances(); if (!siteID.value) return;
  loading.value = true;
  try {
    const [p, b, r] = await Promise.all([dashboard.tuningPolicy(siteID.value), dashboard.tuningBaseValues(siteID.value), dashboard.tuningRecommendations(siteID.value, 300)]);
    mode.value = p.mode; Object.assign(policy, p.policy); policy.continuous ||= defaults(); policy.dispatch_modes ||= {};
    bases.value = b.items ?? []; events.value = r.items ?? []; for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    if (!models.value.includes(activeModel.value)) activeModel.value = models.value[0] || "";
    try { states.value = (await dashboard.tuningContinuousStates(siteID.value)).items ?? []; } catch { states.value = []; }
    dirty.value = false;
  } finally { loading.value = false; }
}
let refreshTimer: ReturnType<typeof setInterval> | undefined;
async function refreshRuntime() {
  if (!siteID.value || loading.value) return;
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
  } catch { /* Keep the last good state; the next poll retries. */ }
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
    dirty.value = false;
    ElMessage.success("基础值已从 new-api 更新并保存");
  } finally { saving.value = false; }
}
async function save() { saving.value = true; try { bases.value = (await dashboard.saveTuningBaseValues(siteID.value, bases.value)).items ?? []; await dashboard.saveTuningPolicy(siteID.value, policy, mode.value); ElMessage.success("设置已保存，将在下一分钟生效"); await load(); } finally { saving.value = false; } }
watch(() => filters.site_id, () => void load());
onMounted(() => { void load(); refreshTimer = setInterval(() => void refreshRuntime(), 30000); });
onBeforeUnmount(() => { if (refreshTimer) clearInterval(refreshTimer); });
</script>

<template><AppShell title="调权中心"><div v-loading="loading" class="page">
  <el-tabs v-model="activeTab" class="tabs">
    <el-tab-pane label="运行概览" name="overview">
      <el-card shadow="never" class="workspace-card"><template #header><div class="head"><div class="title-line"><b>模型与渠道</b><small>每个模型单独选择关闭、只观察或自动执行</small><div class="inline-metrics"><span>自动 <b>{{ counts.auto }}</b></span><span>观察 <b>{{ counts.observe }}</b></span><span>关闭 <b>{{ counts.off }}</b></span><span :class="{ danger: abnormal }">关注 <b>{{ abnormal }}</b></span></div></div><div class="tools"><el-button @click="helpOpen=true">使用说明</el-button><el-button :loading="saving" @click="sync('weight')">初始化/刷新基础值</el-button><el-button v-if="dirty" type="primary" :loading="saving" @click="save">保存更改</el-button></div></div></template>
        <el-empty v-if="!models.length" description="还没有渠道基础值"><el-button type="primary" @click="sync('weight')">立即从 new-api 读取</el-button></el-empty>
        <div v-else class="model-workspace">
          <aside class="model-nav"><el-input v-model="modelQuery" clearable placeholder="搜索模型"/><div class="model-list"><button v-for="model in visibleModels" :key="model" :class="{active:activeModel===model}" @click="activeModel=model"><span><b>{{ model }}</b><small>{{ bases.filter(x=>x.model_name===model).length }} 个渠道</small></span><span class="model-status"><el-badge v-if="modelAbnormal(model)" :value="modelAbnormal(model)" type="danger"/><el-tag :type="modeType(model)" effect="plain" size="small">{{ modeText(model) }}</el-tag></span></button><el-empty v-if="!visibleModels.length" :image-size="48" description="没有匹配模型"/></div></aside>
          <section class="model-detail"><div class="model-head"><div><b>{{ activeModel }}</b><small>{{ activeRows.length }} 个渠道</small><small v-if="lastEvaluationAt">最近评估 {{ formatTime(lastEvaluationAt) }} · 每 30 秒自动刷新</small></div><el-radio-group v-if="activeModel" v-model="policy.dispatch_modes[activeModel]" size="small" @change="dirty=true"><el-radio-button value="off">关闭</el-radio-button><el-radio-button value="observe">只观察</el-radio-button><el-radio-button value="auto">自动执行</el-radio-button></el-radio-group></div>
            <el-collapse class="evidence-collapse"><el-collapse-item title="查看本轮原始指标与同模型中位数" name="evidence"><div class="evidence-grid"><div v-for="row in activeRows" :key="`${row.channel_id}:${row.model_name}`" class="evidence-row"><b>{{ row.channel_name }}</b><span>TTFT {{ seconds(stateFor(row)?.metric_ttft_p50) }}/{{ seconds(stateFor(row)?.metric_ttft_p90) }}/{{ seconds(stateFor(row)?.metric_ttft_p95) }} → 中位数 {{ seconds(stateFor(row)?.baseline_ttft_p50) }}/{{ seconds(stateFor(row)?.baseline_ttft_p90) }}/{{ seconds(stateFor(row)?.baseline_ttft_p95) }}</span><span>缓存 {{ stateFor(row)?.cache_ready ? `${percent(stateFor(row)?.metric_cache)} → ${percent(stateFor(row)?.baseline_cache)}` : "证据不足，不参与" }}</span><span>输出 {{ stateFor(row)?.otps_ready ? `${stateFor(row)?.metric_otps.toFixed(1)} → ${stateFor(row)?.baseline_otps.toFixed(1)} OTPS` : "证据不足，不参与" }}</span><span>平滑渠道错误率 {{ percent(stateFor(row)?.smoothed_error_rate) }}</span></div></div></el-collapse-item></el-collapse>
            <el-table :data="activeRows" size="small" height="calc(100vh - 230px)"><el-table-column prop="channel_id" label="ID" width="76"/><el-table-column prop="channel_name" label="渠道" min-width="170"/><el-table-column prop="group_name" label="分组" min-width="110"><template #default="{row}">{{ row.group_name || "—" }}</template></el-table-column><el-table-column label="评估状态" min-width="180"><template #default="{row}"><div class="evaluation"><span>{{ evaluationText(row) }}</span><small>窗口样本 {{ sampleText(row) }}</small></div></template></el-table-column><el-table-column label="评估系数" min-width="220"><template #default="{row}"><span class="factors"><span :class="comparisonClass(stateFor(row)?.k_speed)">速度 {{ factor(stateFor(row)?.k_speed) }}</span> · <span :class="comparisonClass(stateFor(row)?.k_cache)">缓存 {{ factor(stateFor(row)?.k_cache) }}</span> · <span :class="comparisonClass(stateFor(row)?.k_otps)">输出 {{ factor(stateFor(row)?.k_otps) }}</span> · <span :class="comparisonClass(stateFor(row)?.k_error)">错误 {{ factor(stateFor(row)?.k_error) }}</span></span></template></el-table-column><el-table-column label="基础权重" width="122"><template #default="{row}"><el-input-number v-model="row.base_weight" :min="0" size="small" controls-position="right" @change="dirty=true"/></template></el-table-column><el-table-column width="105"><template #header><span class="column-help">计算权重<el-popover trigger="click" width="460"><template #reference><button class="help" aria-label="查看计算权重说明">i</button></template><div class="calc-details"><div class="calc-title"><b>计算权重说明</b><code>round(基础权重 × 综合倍率)</code></div><p class="formula">综合倍率 = clamp(速度 × 缓存 × 输出 × 错误，0.500，1.500)</p><dl><div><dt>速度</dt><dd>比较该渠道与同模型渠道的 TTFT P50/P90/P95，按 50%/30%/20% 加权；越快系数越高。<small>来源：Agent 采集 new-api 日志的首字耗时，汇总到 metric_1m。</small></dd></div><div><dt>缓存</dt><dd>渠道缓存命中率相对同模型中位数换算；证据不足时不参与计算。<small>来源：new-api 日志中的缓存 token ÷提示 token。</small></dd></div><div><dt>输出</dt><dd>渠道 OTPS 相对同模型中位数换算；证据不足时不参与计算。<small>来源：成功流式请求的输出 token ÷生成耗时。</small></dd></div><div><dt>错误</dt><dd>分钟渠道错误率经 EWMA 平滑后换算；用户自身错误不处罚渠道。<small>来源：new-api 请求状态，由配置的用户错误码规则分类。</small></dd></div></dl><p class="calc-note">至少需要 2 个达到最少请求数且有完整 TTFT 数据的同模型渠道；模型关闭或数据不足时倍率保持 1.000。</p></div></el-popover></span></template><template #default="{row}"><span :class="weightClass(row)">{{ stateFor(row)?.proposed_weight ?? "—" }}</span></template></el-table-column><el-table-column prop="current_weight" label="当前权重" width="100"/><el-table-column label="基础优先级" width="122"><template #default="{row}"><el-input-number v-model="row.base_priority" :min="0" size="small" controls-position="right" @change="dirty=true"/></template></el-table-column><el-table-column prop="current_priority" label="线上优先级" width="110"/></el-table>
          </section>
        </div>
      </el-card>
    </el-tab-pane>
    <el-tab-pane label="变更记录" name="events"><div class="summary compact"><div><span>近 7 天自动调权</span><b>{{ eventCount(7,'weight_write') }}</b></div><div><span>近 7 天熔断</span><b>{{ eventCount(7,'circuit_opened') }}</b></div><div><span>近 7 天恢复</span><b>{{ eventCount(7,'circuit_recovered') }}</b></div><div><span>近 7 天人工接管</span><b>{{ eventCount(7,'manual_takeover') }}</b></div></div><el-card shadow="never"><template #header><div class="head"><div><b>系统做过什么</b><small>自动写入、暂停、熔断和恢复都会记录</small></div></div></template><el-timeline v-if="recentEvents.length"><el-timeline-item v-for="item in recentEvents" :key="item.id" :timestamp="formatTime(item.created_at)" placement="top"><el-tag :type="item.rule==='weight_write'?'success':'warning'">{{ eventName(item.rule) }}</el-tag><strong class="channel">{{ item.channel_name }}</strong><span>权重 {{ item.current_weight }} → {{ item.proposed_weight }}</span></el-timeline-item></el-timeline><el-empty v-else description="暂无调权记录"/></el-card></el-tab-pane>
    <el-tab-pane label="规则设置" name="settings"><el-card shadow="never"><template #header><div class="head"><div><b>系统如何计算权重</b><small>通常保持默认值即可</small></div></div></template>
      <div class="flow"><div><i>1</i><b>同模型比较</b><span>只比较相同模型的渠道</span></div><div><i>2</i><b>综合评分</b><span>速度、缓存、输出与错误</span></div><div><i>3</i><b>计算权重</b><span>基础权重 × 综合倍率</span></div><div><i>4</i><b>安全执行</b><span>自动执行才写入线上</span></div></div>
      <el-form label-position="top"><div class="params"><el-form-item label="调整灵敏度"><el-input-number v-model="policy.continuous.sensitivity" :min=".1" :max="5" :step=".1" @change="dirty=true"/><small>放大或缩小渠道相对差异；1 为标准</small></el-form-item><el-form-item label="输出加权上限"><el-input-number v-model="policy.continuous.otps_cap" :min="1" :max="3" :step=".1" @change="dirty=true"/><small>限制 OTPS 输出系数，最终综合倍率仍封顶 1.5</small></el-form-item><el-form-item label="评估窗口（分钟）"><el-input-number v-model="policy.continuous.window_minutes" :min="1" @change="dirty=true"/><small>每次计算使用最近多少分钟的指标</small></el-form-item><el-form-item label="每渠道最少请求数"><el-input-number v-model="policy.continuous.min_samples" :min="1" @change="dirty=true"/><small>低于此数量不参与本轮比较和调权</small></el-form-item></div></el-form>
      <el-collapse><el-collapse-item title="高级安全设置：熔断与自动恢复" name="safety"><p class="safety">性能差异只会降权；平滑渠道错误率达到阈值才熔断。自动模式静默后主动探测，观察模式使用真实流量被动恢复。</p><div class="params"><el-form-item label="熔断错误率"><el-input-number v-model="policy.continuous.circuit_error_rate" :min=".01" :max="1" :step=".01" @change="dirty=true"/><small>平滑渠道错误率达到此值才停止分流</small></el-form-item><el-form-item label="被动恢复错误率"><el-input-number v-model="policy.continuous.recovery_error_rate" :min="0" :max=".99" :step=".01" @change="dirty=true"/><small>观察模式降到此值后解除模拟熔断</small></el-form-item><el-form-item label="探针恢复阈值"><el-input-number v-model="policy.continuous.recovery_threshold" :min=".01" :max="1" :step=".01" @change="dirty=true"/><small>自动模式探针成功率×探针速度达到此值才恢复</small></el-form-item><el-form-item label="熔断静默期（分钟）"><el-input-number v-model="policy.continuous.silent_minutes" :min="1" @change="dirty=true"/><small>自动模式熔断后等待多久再开始探测</small></el-form-item><el-form-item label="探测间隔（秒）"><el-input-number v-model="policy.continuous.probe_interval_seconds" :min="1" @change="dirty=true"/><small>连续探测请求之间的等待时间</small></el-form-item><el-form-item label="探测次数"><el-input-number v-model="policy.continuous.probe_count" :min="1" @change="dirty=true"/><small>一次恢复判断发送多少次请求</small></el-form-item><el-form-item label="恢复初始倍率"><el-input-number v-model="policy.continuous.soft_start_multiplier" :min=".01" :max="1" :step=".05" @change="dirty=true"/><small>恢复首轮使用基础权重的比例</small></el-form-item></div></el-collapse-item></el-collapse>
    </el-card></el-tab-pane>
  </el-tabs>
  <el-drawer v-model="helpOpen" title="调权中心使用说明" size="560px" class="tuning-help">
    <div class="help-guide">
      <section><h3>这个功能做什么</h3><p>系统每分钟比较同一模型下的可用渠道，根据响应速度、缓存表现、输出速度和错误情况计算渠道权重。不同模型互不影响。</p></section>
      <section><h3>第一次怎么用</h3><ol><li>点击“初始化/刷新基础值”，读取当前渠道权重和优先级并自动保存。</li><li>选择一个模型，将模式设为“只观察”。</li><li>观察计算权重和变更记录，确认结果符合预期。</li><li>确认稳定后，再将需要的模型切换为“自动执行”。</li></ol></section>
      <section><h3>三种模式</h3><dl><div><dt>关闭</dt><dd>不参与评估，不修改当前权重。</dd></div><div><dt>只观察</dt><dd>计算结果并展示，但不会写入渠道。</dd></div><div><dt>自动执行</dt><dd>计算完成后，通过 Agent 将结果写入渠道。</dd></div></dl></section>
      <section><h3>表格怎么看</h3><dl><div><dt>基础权重</dt><dd>自动计算的基准，由初始化读取，也可以手动调整。</dd></div><div><dt>计算权重</dt><dd>基础权重乘以综合倍率后的结果；列名旁的 i 可查看公式和数据来源。</dd></div><div><dt>当前权重</dt><dd>渠道现在实际生效的权重。自动执行成功后通常会与计算权重一致。</dd></div><div><dt>基础优先级</dt><dd>熔断恢复时用于还原渠道优先级的基准。</dd></div><div><dt>线上优先级</dt><dd>渠道现在实际生效的优先级。</dd></div></dl></section>
      <section><h3>权重如何计算</h3><ol><li>系统合并评估窗口内的 TTFT 直方图；只有达到最少请求数且 TTFT 完整的渠道才参与。</li><li>同模型至少两个合格渠道组成比较基线，基准使用中位数，避免异常渠道拉偏。</li><li>速度系数限制为 0.750～1.250；缓存为 0.900～1.100；输出为 0.800～1.200。缓存或输出证据不足时不参与。</li><li>可靠性按平滑渠道错误率换算，不按请求数量重复指数惩罚；正常综合倍率限制为 0.500～1.500。</li><li>计算权重 = round(基础权重 × 综合倍率)。性能较差只降权，只有可靠性达到熔断条件才归零。</li></ol><p class="help-note">“只观察”只展示计算结果；“自动执行”才会把计算权重写入线上。</p></section>
      <section><h3>如何降权、熔断和恢复</h3><ol><li><b>普通降权：</b>渠道相对同模型中位数更慢、缓存或输出表现更差时，相应系数低于 1。</li><li><b>熔断：</b>平滑渠道错误率达到“熔断错误率”时，计算权重和优先级置为 0；性能指标本身不会触发熔断。</li><li><b>观察恢复：</b>观察模式没有真实断流，线上真实请求即被动探针；累计达到探测次数后按该轮错误率判定，降到被动恢复线即解除模拟熔断，未达标则重新累计。</li><li><b>自动恢复：</b>自动模式静默结束后发送主动探针，结合成功率和响应速度判断恢复。</li><li><b>渐进恢复：</b>探针达标后先按恢复初始倍率低权重启动，再回到正常评估。</li></ol></section>
      <section><h3>规则参数怎么用</h3><dl class="parameter-guide">
        <div><dt>灵敏度</dt><dd>用于速度、缓存和输出系数的计算。1 为标准强度；调大后好坏差异被放大、升降权更积极；调小后变化更平滑。波动大的业务宜从 1 或更低开始。</dd></div>
        <div><dt>输出加权上限</dt><dd>只限制“输出速度系数”的最高值，防止高输出速度带来过度加权；它不是最终权重上限。最终综合倍率统一限制在 0.500～1.500。</dd></div>
        <div><dt>评估窗口</dt><dd>每次评估向前统计多少分钟。调大更稳定但响应变化更慢；调小更及时但更容易受短时波动影响。</dd></div>
        <div><dt>每渠道最少请求数</dt><dd>窗口内达到该请求数才参与同模型比较。调大可提高可信度，但低流量渠道可能长期不计算；调小覆盖更广，但结果噪声更大。</dd></div>
        <div><dt>熔断错误率</dt><dd>平滑渠道错误率达到此值即熔断（性能指标不触发熔断）。调小保护更积极；调大容忍度更高。必须高于被动恢复错误率，避免反复开关。</dd></div>
        <div><dt>被动恢复错误率</dt><dd>观察模式下，一轮被动探针（数量=探测次数）的错误率降到此值即解除模拟熔断。调小验证更严格、恢复更慢。</dd></div>
        <div><dt>探针恢复阈值</dt><dd>自动模式探针的成功率×速度得分达到此值才允许恢复。调大验证更严格、恢复更慢；调小恢复更快但风险更高。</dd></div>
        <div><dt>熔断静默期</dt><dd>熔断后等待多少分钟再开始探测。调大可减少对故障渠道的打扰，但会延后恢复；调小能更快发现恢复。</dd></div>
        <div><dt>探测间隔</dt><dd>相邻恢复探测之间等待的秒数。调大更温和但验证耗时更长；调小验证更快，但会更集中地访问故障渠道。</dd></div>
        <div><dt>探测次数</dt><dd>一轮恢复判断使用的探测请求数量。调大结果更可靠但恢复更慢、探测流量更多；调小反之。</dd></div>
        <div><dt>恢复初始倍率</dt><dd>刚通过恢复检测时，以基础权重的多少比例重新接流量。数值越小恢复越谨慎；数值越大恢复越快，但再次故障的影响也更大。</dd></div>
      </dl><p class="help-note">修改规则后需点击“保存更改”，从下一次每分钟评估开始生效；不会立即重算已经完成的历史结果。</p></section>
      <section><h3>什么时候会修改线上</h3><p>只有模型处于“自动执行”且数据量达到要求时才会写入。关闭和只观察模式都不会修改当前权重。刚切换模式、写入排队或执行失败时，计算权重与当前权重可能暂时不同。</p></section>
      <section><h3>安全保护</h3><ul><li>数据不足时保持当前权重，不用不完整样本做决策。</li><li>检测到人工修改时自动暂停，优先尊重人工设置。</li><li>所有自动写入、熔断、探测和恢复都记录在“变更记录”。</li></ul></section>
      <section><h3>四项评估系数公式</h3><dl class="factor-formulas">
        <div><dt>速度系数</dt><dd><code>R = 0.5×(P50/中位P50) + 0.3×(P90/中位P90) + 0.2×(P95/中位P95)</code><br><code>速度 = clamp((1/R)^(0.35×灵敏度), 0.75, 1.25)</code></dd></div>
        <div><dt>缓存系数</dt><dd><code>缓存命中率 = 缓存Token / 提示Token</code><br><code>缓存 = clamp((本渠道命中率/同模型中位命中率)^(0.15×灵敏度), 0.9, 1.1)</code><br>窗口内提示 Token 不足 1 万或基线不足两渠道时不参与（取 1）。</dd></div>
        <div><dt>输出系数</dt><dd><code>OTPS = 输出Token / 生成耗时秒数</code><br><code>输出 = clamp((本渠道OTPS/同模型中位OTPS)^(0.25×灵敏度), 0.8, min(1.2, 输出加权上限))</code><br>样本 Token 不足 100 或基线不足两渠道时不参与（取 1）。</dd></div>
        <div><dt>错误系数</dt><dd><code>平滑错误率 = 0.3×本桶错误率 + 0.7×旧平滑值</code><br><code>错误 = 分段映射：≤1%→1；5%→0.85；15%→0.50；≥30%→0.20（线性插值）</code><br>用户自身错误不会计入渠道错误。</dd></div>
      </dl><p class="help-note">同模型至少需要两个达到最少样本数且 TTFT 完整的渠道。未形成有效基线时不计算，系数保持中性值 1.000。</p></section>
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
.evidence-collapse{margin:0 0 8px}.evidence-collapse :deep(.el-collapse-item__header){height:34px;padding:0 10px;color:#596579;font-size:12px}.evidence-grid{display:grid;gap:6px;padding:4px 10px 10px}.evidence-row{display:grid;grid-template-columns:minmax(150px,1.2fr) 2fr 1fr 1fr 1fr;gap:10px;color:#596579;font-size:12px}.evidence-row b{color:#17233b}.evidence-row span{white-space:nowrap}
</style>
