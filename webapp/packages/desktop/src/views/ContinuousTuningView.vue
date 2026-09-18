<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { ArrowLeft, ArrowRight } from "@element-plus/icons-vue";
import { ApiError, type ChannelBaseValue, type TuningChannel, type TuningContinuousState, type TuningPolicy, type TuningRecommendation } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import TuningInfo from "../components/TuningInfo.vue";
import TuningCapacityMetric from "../components/TuningCapacityMetric.vue";
import ChannelGroupEditor from "../components/ChannelGroupEditor.vue";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";
import { hiddenChannelGroupCount, MAX_VISIBLE_CHANNEL_GROUPS, normalizeChannelGroups, splitChannelGroups, visibleChannelGroups } from "../utils/channelGroup";

const filters = useFiltersStore();
const loading = ref(false), saving = ref(false), dirty = ref(false), activeTab = ref("overview"), helpOpen = ref(false);
const mode = ref<"observe" | "confirm" | "auto">("observe");
const bases = ref<ChannelBaseValue[]>([]), states = ref<TuningContinuousState[]>([]), events = ref<TuningRecommendation[]>([]);
const channels = ref<TuningChannel[]>([]);
let channelDirectoryGeneration = 0;
const groupManagerOpen = ref(false);
const groupDialogOpen = ref(false), groupSaving = ref(false), groupConfirmed = ref(false);
const editingChannel = ref<TuningChannel | null>(null), groupDraft = ref<string[]>([]);
const pendingGroups = ref(new Map<number, string>());
const groupErrors = ref(new Map<number, string>());
const groupPollTimers = new Map<number, ReturnType<typeof setTimeout>>();
const groupPollTokens = new Map<number, number>();
const statesSite = ref("");
const savedBases = ref<ChannelBaseValue[]>([]), savedPolicy = ref<TuningPolicy | null>(null), savedMode = ref<"observe" | "confirm" | "auto">("observe");
const modelQuery = ref(""), activeModel = ref("");
const manualNavCollapsed = ref(false);
const narrowNavExpanded = ref(false);
const pageElement = ref<HTMLElement | null>(null);
const compactLayout = ref(false);
const detailElement = ref<HTMLElement | null>(null);
const detailWidth = ref(900);
// Balance the whole table; keep usable minimums inside the table on narrow screens.
const tableColumns = computed(() => {
  const width = Math.max(760, detailWidth.value - 2);
  const channel = Math.round(width * .25);
  const groups = Math.round(width * .14);
  const factor = Math.floor(width * .07);
  const input = Math.floor(width * .08);
  const number = input;
  return { channel, groups, factor, input, number, priority: width - channel - groups - factor * 4 - input - number * 2 };
});
let detailObserver: ResizeObserver | undefined;
watch(detailElement, (element) => {
  detailObserver?.disconnect();
  if (!element) return;
  detailWidth.value = element.clientWidth;
  detailObserver = new ResizeObserver(([entry]) => {
    if (entry && entry.contentRect.width > 0) detailWidth.value = entry.contentRect.width;
  });
  detailObserver.observe(element);
}, { flush: 'post' });
onBeforeUnmount(() => detailObserver?.disconnect());
const modelNavCollapsed = computed({
  get: () => compactLayout.value ? !narrowNavExpanded.value : manualNavCollapsed.value,
  set: (collapsed: boolean) => {
    if (compactLayout.value) narrowNavExpanded.value = !collapsed;
    else manualNavCollapsed.value = collapsed;
  },
});
watch(compactLayout, () => { narrowNavExpanded.value = false; });
let layoutObserver: ResizeObserver | undefined;
onMounted(() => {
  if (!pageElement.value) return;
  layoutObserver = new ResizeObserver(([entry]) => {
    if (entry) compactLayout.value = entry.contentRect.width < 1200;
  });
  layoutObserver.observe(pageElement.value);
});
onBeforeUnmount(() => layoutObserver?.disconnect());
const eventModelFilter = ref(""), eventRuleFilter = ref(""), eventChannelQuery = ref("");
const eventPage = ref(1), eventPageSize = ref(20);
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T;
const wait = (milliseconds: number) => new Promise(resolve => setTimeout(resolve, milliseconds));
const defaults = () => ({ sensitivity: 1, speed_exponent: .35, speed_p50_weight: .5, speed_p90_weight: .3, speed_p95_weight: .2, speed_min_factor: .75, speed_max_factor: 1.25, cache_exponent: .15, cache_min_factor: .9, cache_max_factor: 1.1, otps_exponent: .25, otps_min_factor: .8, otps_max_factor: 1.2, error_healthy_rate: .01, error_degraded_rate: .05, error_poor_rate: .15, error_floor_rate: .3, error_degraded_factor: .85, error_poor_factor: .5, error_min_factor: .2, combined_min_factor: .5, combined_max_factor: 1.5, circuit_threshold: .1, recovery_threshold: .2, circuit_error_rate: .3, recovery_error_rate: .1, silent_minutes: 5, probe_interval_seconds: 5, probe_count: 10, soft_start_multiplier: .2, window_minutes: 15, min_samples: 20, sparse_lookback_minutes: 360, fast_circuit_enabled: true, fast_circuit_min_samples: 50, fast_circuit_error_rate: .5 });
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
const priorityGroupStarts = computed(() => {
  const starts = new Map<string, { priority: number; count: number; first: boolean }>();
  const rows = activeRows.value;
  for (let index = 0; index < rows.length; index++) {
    const row = rows[index];
    if (index > 0 && rows[index - 1].current_priority === row.current_priority) continue;
    let end = index + 1;
    while (end < rows.length && rows[end].current_priority === row.current_priority) end++;
    starts.set(channelRowKey(row), { priority: row.current_priority, count: end - index, first: index === 0 });
  }
  return starts;
});
const priorityRowClass = ({ row }: { row: ChannelBaseValue }) => {
  const group = priorityGroupStarts.value.get(channelRowKey(row));
  return group && !group.first ? 'priority-group-start' : '';
};

// 总览表可能先于全渠道目录完成加载；缺少目录行时用基础值补齐编辑器上下文。
const channelDirectoryByID = computed(() => new Map(channels.value.map(row => [row.channel_id, row])));
const groupEditorRowFor = (row: ChannelBaseValue): TuningChannel => channelDirectoryByID.value.get(row.channel_id) ?? ({
  channel_id: row.channel_id,
  channel_name: row.channel_name,
  status: "unknown",
  weight: row.current_weight,
  priority: row.current_priority,
  models: row.models?.length ? row.models : [row.model_name],
  group_name: row.group_name || "",
});
const groupOptions = computed(() => [...new Set(channels.value.flatMap(row => splitChannelGroups(row.group_name)))].sort((a, b) => a.localeCompare(b)));
const groupCellTitle = (value: string | null | undefined) => {
  const groups = splitChannelGroups(value);
  return groups.length > MAX_VISIBLE_CHANNEL_GROUPS ? `点击编辑分组（完整分组：${groups.join("、")}）` : "点击编辑分组";
};
const stateMap = computed(() => new Map((statesSite.value === siteID.value ? states.value : []).map(x => [`${x.channel_id}:${x.model_name}`, x])));
const stateFor = (row: ChannelBaseValue) => stateMap.value.get(`${row.channel_id}:${row.model_name}`);
const acceptStates = (site: string, items: TuningContinuousState[]) => {
  if (!items.length && statesSite.value === site && states.value.length) {
    refreshError.value = "评估状态暂时为空，正在显示上次成功结果";
    return;
  }
  states.value = items;
  statesSite.value = site;
  refreshError.value = "";
};
const validEvent = (item: TuningRecommendation) => item.rule !== "circuit_recovered" || item.proposed_weight > 0;
const recentEvents = computed(() => events.value.filter(x => validEvent(x) && ["weight_observed", "weight_write", "manual_takeover", "auto_paused", "circuit_opened", "probe_started", "probe_failed", "circuit_recovered"].includes(x.rule)));
const eventModel = (item: TuningRecommendation) => String(item.evidence?.model ?? bases.value.find(row => row.channel_id === item.channel_id)?.model_name ?? "");
const filteredEvents = computed(() => {
  const selectedModel = eventModelFilter.value === "__current__" ? activeModel.value : eventModelFilter.value;
  const channelQuery = eventChannelQuery.value.trim().toLowerCase();
  return recentEvents.value.filter(item =>
    (!eventDateRange.value || (new Date(item.created_at).getTime() >= new Date(`${eventDateRange.value[0]}T00:00:00`).getTime() && new Date(item.created_at).getTime() <= new Date(`${eventDateRange.value[1]}T23:59:59.999`).getTime()))
    && (!selectedModel || eventModel(item) === selectedModel)
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
  if (state.speed_stats_version !== 1) return "等待新口径速度评估；当前缓存结果尚未排除重试，请等待下一轮评估。";
  const lines: string[] = [];
  const direct = state.speed_sample_count ?? 0, retried = state.speed_retry_count ?? 0;
  const unknown = (state.speed_unknown_count ?? 0) + (state.speed_legacy_count ?? 0);
  const total = direct + retried + unknown;
  lines.push(`速度样本：未重试 ${direct}；重试排除 ${retried}；无法确认 ${unknown}。${total ? `重试排除占比 ${(retried / total * 100).toFixed(1)}%。` : "等待新口径样本。"}监控 TTFT 保留全部原有样本。`);
  if (state.speed_stats_version === 1 && state.metric_ready && state.baseline_ready && state.baseline_ttft_p50 > 0 && state.baseline_ttft_p90 > 0 && state.baseline_ttft_p95 > 0) {
    const ratio = policy.continuous.speed_p50_weight * state.metric_ttft_p50 / state.baseline_ttft_p50 + policy.continuous.speed_p90_weight * state.metric_ttft_p90 / state.baseline_ttft_p90 + policy.continuous.speed_p95_weight * state.metric_ttft_p95 / state.baseline_ttft_p95;
    const raw = Math.pow(1 / ratio, policy.continuous.speed_exponent * policy.continuous.sensitivity);
    lines.push(`速度 ${displayedSpeedFactor(row) === null ? "—" : factor(displayedSpeedFactor(row)!)}\nR = ${percent(policy.continuous.speed_p50_weight)}×(${state.metric_ttft_p50.toFixed(3)}/${state.baseline_ttft_p50.toFixed(3)}) + ${percent(policy.continuous.speed_p90_weight)}×(${state.metric_ttft_p90.toFixed(3)}/${state.baseline_ttft_p90.toFixed(3)}) + ${percent(policy.continuous.speed_p95_weight)}×(${state.metric_ttft_p95.toFixed(3)}/${state.baseline_ttft_p95.toFixed(3)}) = ${ratio.toFixed(4)}\nclamp((1/R)^(${policy.continuous.speed_exponent}×${policy.continuous.sensitivity}), ${policy.continuous.speed_min_factor}, ${policy.continuous.speed_max_factor}) = ${clampNumber(raw, policy.continuous.speed_min_factor, policy.continuous.speed_max_factor).toFixed(3)}`);
  } else {
    lines.push(`速度 ${displayedSpeedFactor(row) === null ? "—" : factor(displayedSpeedFactor(row)!)}\n${state.otps_ready && state.otps_stats_version === 1 && state.last_observed_requests >= policy.continuous.min_samples ? "TTFT 样本或基线不足，使用本轮平均输出速度系数；OTPS 在综合倍率中参与两次，不沿用历史 TTFT。" : "本轮数据不足，不重新调整权重，也不使用历史 TTFT 系数。"}`);
  }
  if (state.cache_ready && state.baseline_cache > 0) {
    const ratio = state.metric_cache / state.baseline_cache;
    const raw = Math.pow(ratio, policy.continuous.cache_exponent * policy.continuous.sensitivity);
    lines.push(`缓存 ${factor(state.k_cache)}\n大输入缓存 Token 比率 ${percent(state.metric_cache)} ÷ 同模型平均数 ${percent(state.baseline_cache)} = ${ratio.toFixed(4)}\nclamp(比值^(${policy.continuous.cache_exponent}×${policy.continuous.sensitivity}), ${policy.continuous.cache_min_factor}, ${policy.continuous.cache_max_factor}) = ${clampNumber(raw, policy.continuous.cache_min_factor, policy.continuous.cache_max_factor).toFixed(3)}\n口径与监控页面一致：仅统计输入大于 512 Token 的成功请求，缓存读取 Token 总数 ÷ 提示 Token 总数。`);
  } else {
    lines.push(`缓存 ${factor(state.k_cache)}\n回退为 1.000：窗口内大输入请求的提示 Token 不足 10000，或同模型缓存基线不足。`);
  }
  lines.push(`输出样本：未重试 ${state.otps_sample_count ?? 0}；重试排除 ${state.otps_retry_count ?? 0}；无法确认 ${state.otps_unknown_count ?? 0}。流式和非流式均按输出 Token 总数 ÷ 请求总耗时计算，监控保留重试。`);
  if (state.otps_stats_version !== 1) {
    lines.push("输出：等待新口径输出样本，旧缓存系数不能解释为请求总耗时口径。");
  } else if (state.otps_ready && state.baseline_otps > 0) {
    const ratio = state.metric_otps / state.baseline_otps;
    const raw = Math.pow(ratio, policy.continuous.otps_exponent * policy.continuous.sensitivity);
    lines.push(`输出 ${factor(state.k_otps)}\nOTPS ${state.metric_otps.toFixed(2)} ÷ 同模型平均数 ${state.baseline_otps.toFixed(2)} = ${ratio.toFixed(4)}\nclamp(比值^(${policy.continuous.otps_exponent}×${policy.continuous.sensitivity}), ${policy.continuous.otps_min_factor}, ${policy.continuous.otps_max_factor}) = ${clampNumber(raw, policy.continuous.otps_min_factor, policy.continuous.otps_max_factor).toFixed(3)}`);
  } else {
    lines.push(`输出 ${factor(state.k_otps)}\n回退为 1.000：未重试有效请求不足 ${policy.continuous.min_samples} 条、输出 Token 不足 100，或同模型合格渠道不足 2 个。`);
  }
  lines.push(`错误 ${factor(state.k_error)}\n平滑渠道错误率 ${(state.smoothed_error_rate * 100).toFixed(2)}%；按 ≤${percent(policy.continuous.error_healthy_rate)}→1.000、${percent(policy.continuous.error_degraded_rate)}→${factor(policy.continuous.error_degraded_factor)}、${percent(policy.continuous.error_poor_rate)}→${factor(policy.continuous.error_poor_factor)}、≥${percent(policy.continuous.error_floor_rate)}→${factor(policy.continuous.error_min_factor)} 分段线性换算。用户自身错误不处罚渠道。`);
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
const sampleText = (row: ChannelBaseValue) => { const state = stateFor(row); return state ? `${state.last_observed_requests}/${(savedPolicy.value?.continuous ?? policy.continuous).min_samples}` : "—"; };
const rateText = (value?: number) => value == null ? "—" : Math.round(value).toLocaleString("zh-CN");
const currentRates = ref(new Map<number, { rpm: number; tpm: number }>());
const ratesReady = ref(false), ratesError = ref("");
const ratesAsOf = ref(""), ratesWindowStart = ref("");
const ratesDelay = ref(0);
let ratesLoading = false;
const currentRateFor = (row: ChannelBaseValue) => ratesReady.value ? (currentRates.value.get(row.channel_id) ?? {rpm: 0, tpm: 0}) : undefined;
const currentlyLimited = (row: ChannelBaseValue) => {
  const rate = currentRateFor(row);
  return !!rate && ((row.max_rpm > 0 && rate.rpm >= row.max_rpm) || (row.max_tpm > 0 && rate.tpm >= row.max_tpm));
};
async function refreshCurrentRates() {
  if (!siteID.value || ratesLoading) return;
  const site = siteID.value;
  ratesLoading = true;
  try {
    const result = await dashboard.tuningCurrentRates(site);
    if (site !== siteID.value) return;
    currentRates.value = new Map(result.items.map(rate => [rate.channel_id, rate]));
    ratesAsOf.value = result.as_of;
    ratesWindowStart.value = result.window_start;
    ratesDelay.value = result.delay_seconds;
    ratesReady.value = true; ratesError.value = "";
  } catch {
    if (site !== siteID.value) return;
    ratesReady.value = false;
    ratesError.value = "实时负载暂不可用：请确认 Agent 已升级且上报正常";
  } finally { ratesLoading = false; }
}
const evaluationText = (row: ChannelBaseValue) => {
  const state = stateFor(row), requests = state?.last_observed_requests ?? 0;
  if ((row.models?.length ?? 1) > 1 || state?.paused_reason === "mixed_channel") return "多模型渠道，安全暂停";
  if (modelMode(row.model_name) === "off") return "模型已关闭";
  if (row.base_weight <= 0) return "基础权重为 0，未参与调权";
  if (!state) return "等待评估数据";
  if (state?.paused_reason || (state?.phase && state.phase !== "normal")) return phaseText(state);
  const minimum = (savedPolicy.value?.continuous ?? policy.continuous).min_samples;
  if (requests < minimum) return `窗口样本不足 ${requests}/${minimum}，本轮不调权`;
  if (state.speed_stats_version !== 1) return "等待新口径速度评估";
  if (!state?.metric_ready) return state.otps_ready && state.otps_stats_version === 1 ? "TTFT 样本不足，使用本轮输出系数" : `TTFT 样本不足 ${state.speed_sample_count ?? 0}/${policy.continuous.min_samples}，输出不可用，本轮不调权`;
  return state.baseline_ready ? "已参与本轮计算" : state.otps_ready && state.otps_stats_version === 1 ? "TTFT 基线不足，使用本轮输出系数" : "TTFT 与输出基线不足，本轮不调权";
};
const displayedSpeedFactor = (row: ChannelBaseValue): number | null => {
  const state = stateFor(row), params = savedPolicy.value?.continuous ?? policy.continuous;
  if (!state || modelMode(row.model_name) === 'off' || row.base_weight <= 0 || state.phase !== 'normal' || state.paused_reason || (row.models?.length ?? 1) > 1) return null;
  if (state.speed_stats_version !== 1 || state.last_observed_requests < params.min_samples) return null;
  const ttftReady = state.metric_ready && state.baseline_ready;
  if (!ttftReady && !(state.otps_ready && state.otps_stats_version === 1)) return null;
  // New-version fallback must match this round's output factor, never an older TTFT value.
  if (!ttftReady && state.k_speed !== state.k_otps) return null;
  return Number.isFinite(state.k_speed) ? state.k_speed : null;
};
const speedLabel = (row: ChannelBaseValue) => {
  const state = stateFor(row);
  return displayedSpeedFactor(row) !== null && state && (!state.metric_ready || !state.baseline_ready) ? "速度（输出替代）" : "速度";
};
const coefficientColumns = [{key:'speed',label:'速度'}, {key:'cache',label:'缓存'}, {key:'otps',label:'输出'}, {key:'error',label:'错误'}] as const;
const coefficientCell = (row: ChannelBaseValue, key: 'speed' | 'cache' | 'otps' | 'error') => {
  const state = stateFor(row);
  if (!state) return {value:null, status:'等待评估', detail:'尚无评估数据'};
  const value = key === 'speed' ? displayedSpeedFactor(row) : state[`k_${key}`];
  const result = (status: string, detail = status) => ({value: Number.isFinite(value) ? value : null, status, detail});
  const params = savedPolicy.value?.continuous ?? policy.continuous;
  if (modelMode(row.model_name) === 'off' || row.base_weight <= 0 || state.phase !== 'normal' || state.paused_reason || (row.models?.length ?? 1)>1) return result('未参与', evaluationText(row));
  if (key === 'error') return result('平滑错误率', `平滑错误率 ${percent(state.smoothed_error_rate)}；错误系数独立于速度样本判断`);
  if (state.last_observed_requests < params.min_samples) return result(key === 'speed' ? '' : '保留值', `窗口样本 ${state.last_observed_requests}/${params.min_samples}，本轮不重新计算性能系数`);
  if (key === 'speed') {
    if (value !== null) return result(speedLabel(row).includes('替代') ? '输出替代' : 'TTFT 有效');
    if (state.speed_stats_version !== 1) return result('等待新口径');
    return result(!state.metric_ready ? 'TTFT 样本不足' : !state.baseline_ready ? '基线不足' : '系数不可用', evaluationText(row));
  }
  if (key === 'cache') return result(state.cache_ready && state.baseline_cache > 0 ? '有效' : value === 1 ? '中性回退' : '保留值', state.cache_ready && state.baseline_cache > 0 ? '缓存样本与基线有效' : '缓存样本或基线不足');
  if (state.otps_stats_version !== 1) return result('口径待确认', '接口缺少新版输出统计标记，暂无法确认当前系数的统计口径');
  return result(state.otps_ready ? '有效' : value === 1 ? '中性回退' : '保留值', state.otps_ready ? '输出样本与基线有效' : '输出样本或基线不足');
};
const channelQuery = ref(""), channelStatusFilter = ref("");
const eventDateRange = ref<[string, string] | null>(null);
const settingsSection = ref("basic"), helpSection = ref("calculation");
const limitReason = (row: ChannelBaseValue) => {
  const rate = currentRateFor(row);
  if (!rate) return row.max_rpm > 0 || row.max_tpm > 0 ? "实时负载不可用，有容量上限的渠道暂停上调" : "";
  const exceeded = [row.max_rpm > 0 && rate.rpm >= row.max_rpm ? "RPM" : "", row.max_tpm > 0 && rate.tpm >= row.max_tpm ? "TPM" : ""].filter(Boolean);
  return exceeded.length ? `${exceeded.join(" / ")} 已达到上限，仅暂停上调，仍允许下调` : "";
};
const rowStatus = (row: ChannelBaseValue) => {
  const state = stateFor(row), text = evaluationText(row);
  if (text === "模型已关闭") return { kind: "muted", icon: "—", label: "已关闭" };
  if (state?.phase === "circuit") return { kind: "danger", icon: "", label: "熔断" };
  if (effectivePause(state) === "write_failed") return { kind: "danger", icon: "", label: "写入失败" };
  if (text.includes("暂停")) return { kind: "warning", icon: "Ⅱ", label: "已暂停" };
  if (state?.phase === "probing" || state?.phase === "soft_start") return { kind: "warning", icon: "↻", label: "恢复中" };
  if (text.startsWith("窗口样本不足")) return { kind: "muted", icon: "i", label: text.split("，")[0] };
  if (text.includes("不调权") || text.includes("未参与")) return { kind: "muted", icon: "i", label: text.includes("样本") ? "样本不足" : "未参与" };
  if (text.includes("等待")) return { kind: "muted", icon: "i", label: "待评估" };
  if (speedLabel(row).includes("替代")) return { kind: "accent", icon: "⇄", label: "" };
  return { kind: "muted", icon: "", label: "—" };
};
const overallEvaluationStatus = (row: ChannelBaseValue) => {
  const state = stateFor(row);
  const overall = !state || modelMode(row.model_name) === 'off' || row.base_weight <= 0 || state.phase !== 'normal' || !!effectivePause(state) || (row.models?.length ?? 1) > 1 || evaluationText(row).startsWith('窗口样本不足');
  return overall ? rowStatus(row).label : '';
};
const coefficientEmptyText = (row: ChannelBaseValue) => {
  const status = overallEvaluationStatus(row);
  if (status.startsWith('窗口样本不足')) return '窗口样本不足';
  return ['未参与', '已关闭', '已暂停'].includes(status) ? '未参与调权' : '';
};
const coefficientSpan = ({column}: {column: {property?: string}}) => {
  if (column.property === 'coefficient_speed') return [1,4];
  if (column.property?.startsWith('coefficient_')) return [0,0];
  return [1,1];
};
const displayedRows = computed(() => activeRows.value.filter(row => {
  const query = channelQuery.value.trim().toLowerCase();
  if (query && !`${row.channel_name} ${row.channel_id} ${row.group_name}`.toLowerCase().includes(query)) return false;
  if (channelStatusFilter.value === "limited") return !!limitReason(row);
  if (channelStatusFilter.value === "attention") return ["danger", "warning"].includes(rowStatus(row).kind) || !!limitReason(row);
  if (channelStatusFilter.value === "changed") return !!stateFor(row) && stateFor(row)!.proposed_weight !== row.current_weight;
  return true;
}));
const priorityDrafts = reactive(new Map<number, number>());
const displayedPriority = (row: ChannelBaseValue) => priorityDrafts.get(row.channel_id) ?? row.base_priority;
const editPriority = (row: ChannelBaseValue, value: number | undefined) => {
  if (value == null || !Number.isSafeInteger(value) || value < 0) return;
  priorityDrafts.set(row.channel_id, value);
  for (const item of bases.value) if (item.channel_id === row.channel_id) item.base_priority = value;
  dirty.value = true;
};
const fieldChanged = (row: ChannelBaseValue, key: "base_weight" | "base_priority" | "max_rpm" | "max_tpm") => {
  const saved = savedBases.value.find(item => channelRowKey(item) === channelRowKey(row));
  return !!saved && saved[key] !== row[key];
};
const originalBase = (row: ChannelBaseValue) => stateFor(row)?.base_weight ?? savedBases.value.find(item => channelRowKey(item) === channelRowKey(row))?.base_weight ?? row.base_weight;
const calculatedWeight = (row: ChannelBaseValue): number | null => {
  const state = stateFor(row), params = savedPolicy.value?.continuous ?? policy.continuous;
  if ((state?.last_observed_requests ?? 0) < params.min_samples || row.base_weight <= 0) return null;
  const base = originalBase(row);
  if (base <= 0) return null;
  // Display the latest coefficient snapshot as a reference; execution eligibility is separate.
  const factors = [state?.k_speed, state?.k_cache, state?.k_otps, state?.k_error].map(value => value ?? 1);
  return Math.max(1, Math.round(base * clampNumber(factors.reduce((a, b) => a * b, 1), params.combined_min_factor, params.combined_max_factor)));
};

const eventResult = (row: TuningRecommendation) => ({ succeeded: "执行成功", failed: "执行失败", pending: "等待执行", expired: "已过期", recorded: "已记录", approved: "已批准", rejected: "已拒绝" } as Record<string, string>)[row.status] || row.status || "—";
const eventResultClass = (row: TuningRecommendation) => row.status === "failed" || row.status === "expired" ? "danger" : row.status === "pending" ? "warning" : "muted";
const resetEventFilters = () => { eventModelFilter.value = ""; eventRuleFilter.value = ""; eventChannelQuery.value = ""; eventDateRange.value = null; eventPage.value = 1; };
const lastEvaluationAt = computed(() => activeRows.value.map(row => stateFor(row)?.updated_at).filter((value): value is string => !!value).sort().at(-1));
const refreshNow = ref(Date.now());
const evaluationAgeMinutes = computed(() => lastEvaluationAt.value ? Math.max(0, (refreshNow.value - new Date(lastEvaluationAt.value).getTime()) / 60000) : 0);
const evaluationStalled = computed(() => !!lastEvaluationAt.value && evaluationAgeMinutes.value >= 3);
const replacePolicy = (value: TuningPolicy) => {
  for (const key of Object.keys(policy)) delete (policy as unknown as Record<string, unknown>)[key];
  Object.assign(policy, clone(value));
};
const captureSavedState = () => {
  priorityDrafts.clear();
  savedBases.value = clone(bases.value);
  savedPolicy.value = clone(policy);
  savedMode.value = mode.value;
};
const cancelChanges = (notify = true) => {
  if (!savedPolicy.value) return;
  priorityDrafts.clear();
  bases.value = clone(savedBases.value);
  replacePolicy(savedPolicy.value);
  mode.value = savedMode.value;
  dirty.value = false;
  if (notify) ElMessage.info("已取消未保存的更改");
};
const selectModel = (model: string) => {
  if (model === activeModel.value) return;
  activeModel.value = model;
};

let loadingSite = "", loadGeneration = 0;
async function load(syncOnline = false) {
  await filters.loadInstances(); if (!siteID.value) return;
  const site = siteID.value;
  if (loading.value && loadingSite === site) return;
  loadingSite = site;
  const generation = ++loadGeneration;
  runtimeRefreshGeneration++;
  loading.value = true;
  const isCurrentLoad = () => generation === loadGeneration && site === siteID.value;
  // Only cached page reads own the loading overlay. Live synchronization
  // continues independently and is merged after the overlay is released.
  const synced = syncOnline ? syncChannels(site, false) : Promise.resolve(false);
  try {
    const [p, b, r] = await Promise.all([dashboard.tuningPolicy(site), dashboard.tuningBaseValues(site), dashboard.tuningRecommendations(site, 300)]);
    if (!isCurrentLoad()) return;
    mode.value = p.mode; Object.assign(policy, p.policy); policy.continuous = Object.assign(defaults(), p.policy.continuous || {}); policy.continuous.max_increase_percent ??= 10; policy.dispatch_modes ||= {};
    bases.value = b.items ?? []; events.value = r.items ?? []; for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    if (!models.value.includes(activeModel.value)) activeModel.value = models.value[0] || "";
    void loadChannelDirectory(site);
    try {
      const result = await dashboard.tuningContinuousStates(site);
      if (!isCurrentLoad()) return;
      acceptStates(site, result.items ?? []);
    } catch (error) {
      if (!isCurrentLoad()) return;
      refreshError.value = error instanceof Error ? error.message : "评估状态暂不可用";
    }
    dirty.value = false; captureSavedState();
  } finally { if (generation === loadGeneration) loading.value = false; }
  try {
    if (await synced) {
      if (!isCurrentLoad() || saving.value) return;
      const rows = await dashboard.tuningBaseValues(site);
      if (!isCurrentLoad() || saving.value) return;
      mergeOnlineRows(rows.items ?? []);
    }
  } catch (error) {
    if (isCurrentLoad()) refreshError.value = error instanceof Error ? error.message : "刷新失败";
  }
}
const channelSyncError = ref("");
const directControlConfigured = ref<boolean | undefined>();
const isAgentOnlySite = (error: unknown) => error instanceof ApiError && error.code === "direct_control_not_configured";
// Pull the latest channels from new-api. Returns true when a fresh snapshot was
// stored, false when the site is Agent-managed (nothing to refresh) or the call
// failed; failures surface in the banner, Agent-only sites only when explicit.
async function syncChannels(site: string, explicit: boolean): Promise<boolean> {
  try {
    await dashboard.refreshTuningChannels(site);
    if (site !== siteID.value) return false;
    directControlConfigured.value = true; channelSyncError.value = "";
    return true;
  } catch (error) {
    if (site !== siteID.value) return false;
    if (isAgentOnlySite(error)) {
      directControlConfigured.value = false; channelSyncError.value = "";
      if (explicit) ElMessage.info(String((error as ApiError).details.message || "该站点未配置 new-api 直连，渠道信息由 Agent 定期采集"));
      return false;
    }
    channelSyncError.value = error instanceof ApiError && typeof error.details.error === "string" ? error.details.error : (error instanceof Error ? error.message : "渠道同步失败");
    if (explicit) ElMessage.error(channelSyncError.value);
    return false;
  }
}
// 分组编辑使用全渠道目录，不能复用只服务调权引擎的基础值列表。
async function loadChannelDirectory(site: string) {
  const generation = ++channelDirectoryGeneration;
  if (!site) {
    channels.value = [];
    return;
  }
  try {
    const result = await dashboard.tuningChannels(site);
    if (site !== siteID.value || generation !== channelDirectoryGeneration) return;
    channels.value = result.items ?? [];
    const pending = new Map(pendingGroups.value);
    const errors = new Map(groupErrors.value);
    for (const row of channels.value) {
      if (pending.get(row.channel_id) === row.group_name) {
        pending.delete(row.channel_id);
        errors.delete(row.channel_id);
        stopGroupPoll(row.channel_id);
      }
    }
    pendingGroups.value = pending;
    groupErrors.value = errors;
  } catch {
    if (site !== siteID.value || generation !== channelDirectoryGeneration) return;
    channels.value = [];
  }
}
// 轮询 Agent 命令的终态，避免失败命令长期伪装成“等待执行”；每个渠道
// 最多轮询 45 次，站点切换或页面销毁时由 cancelGroupPolls 统一取消。
function stopGroupPoll(channelID: number) {
  const timer = groupPollTimers.get(channelID);
  if (timer) clearTimeout(timer);
  groupPollTimers.delete(channelID);
  groupPollTokens.set(channelID, (groupPollTokens.get(channelID) || 0) + 1);
}
function cancelGroupPolls() {
  for (const timer of groupPollTimers.values()) clearTimeout(timer);
  groupPollTimers.clear();
  groupPollTokens.clear();
}
function setGroupError(channelID: number, message: string) {
  const errors = new Map(groupErrors.value);
  errors.set(channelID, message);
  groupErrors.value = errors;
}
async function pollGroupCommand(site: string, channelID: number, instanceID: string, commandID: string, expectedGroup: string, token: number, attempt = 0): Promise<void> {
  if (site !== siteID.value || groupPollTokens.get(channelID) !== token) return;
  try {
    const result = await dashboard.channelCommands({ instance_id: instanceID, limit: 100 });
    if (site !== siteID.value || groupPollTokens.get(channelID) !== token) return;
    const command = result.items.find(item => item.id === commandID);
    if (command?.status === "succeeded") {
      applyGroupLocally(channelID, expectedGroup);
      stopGroupPoll(channelID);
      const pending = new Map(pendingGroups.value);
      pending.delete(channelID);
      pendingGroups.value = pending;
      ElMessage.success("渠道分组已由 Agent 执行");
      return;
    }
    if (command && ["failed", "expired"].includes(command.status)) {
      stopGroupPoll(channelID);
      const pending = new Map(pendingGroups.value);
      pending.delete(channelID);
      pendingGroups.value = pending;
      setGroupError(channelID, command.error_summary || (command.status === "expired" ? "Agent 命令已过期" : "Agent 执行失败"));
      ElMessage.error(`渠道分组更新失败：${command.error_summary || command.status}`);
      return;
    }
  } catch {
    // 短暂查询失败不改变命令状态，下一轮继续确认终态。
  }
  if (attempt >= 45) {
    stopGroupPoll(channelID);
    setGroupError(channelID, "命令状态查询超时，请查看命令记录");
    ElMessage.warning("渠道分组仍未确认，请查看命令记录");
    return;
  }
  groupPollTimers.set(channelID, setTimeout(() => void pollGroupCommand(site, channelID, instanceID, commandID, expectedGroup, token, attempt + 1), 2000));
}
function applyGroupLocally(channelID: number, group: string) {
  channels.value = channels.value.map(row => row.channel_id === channelID ? { ...row, group_name: group } : row);
  bases.value = bases.value.map(row => row.channel_id === channelID ? { ...row, group_name: group } : row);
  savedBases.value = savedBases.value.map(row => row.channel_id === channelID ? { ...row, group_name: group } : row);
}
function openGroupEditor(row: TuningChannel) {
  stopGroupPoll(row.channel_id);
  const errors = new Map(groupErrors.value);
  errors.delete(row.channel_id);
  groupErrors.value = errors;
  editingChannel.value = row;
  groupDraft.value = splitChannelGroups(row.group_name);
  groupConfirmed.value = false;
  groupDialogOpen.value = true;
}
function saveGroupSelection(groups: string[]) {
  groupDraft.value = groups;
  groupConfirmed.value = true;
  void saveGroup();
}
async function saveGroup() {
  const row = editingChannel.value;
  if (!row || !groupConfirmed.value || groupSaving.value) return;
  let group: string;
  try {
    group = normalizeChannelGroups(groupDraft.value);
  } catch (error) {
    ElMessage.warning(error instanceof Error ? error.message : "分组格式无效");
    return;
  }
  groupSaving.value = true;
  const groupSite = siteID.value;
  try {
    const result = await dashboard.saveTuningChannelGroup(groupSite, row.channel_id, group);
    if (groupSite !== siteID.value) return;
    if (result.status === "succeeded") {
      stopGroupPoll(row.channel_id);
      applyGroupLocally(row.channel_id, result.group);
      const pending = new Map(pendingGroups.value);
      pending.delete(row.channel_id);
      pendingGroups.value = pending;
      ElMessage.success("渠道分组已更新");
    } else if (result.status === "pending") {
      stopGroupPoll(row.channel_id);
      pendingGroups.value = new Map(pendingGroups.value).set(row.channel_id, result.group);
      const errors = new Map(groupErrors.value);
      errors.delete(row.channel_id);
      groupErrors.value = errors;
      const token = (groupPollTokens.get(row.channel_id) || 0) + 1;
      groupPollTokens.set(row.channel_id, token);
      void pollGroupCommand(siteID.value, row.channel_id, result.instance_id, result.command_id, result.group, token);
      ElMessage.info("分组变更已下发，等待 Agent 执行");
    } else {
      stopGroupPoll(row.channel_id);
      setGroupError(row.channel_id, `分组变更状态：${result.status || "未知"}`);
      ElMessage.warning(`分组变更状态：${result.status || "未知"}`);
    }
    groupDialogOpen.value = false;
  } catch (error) {
    if (error instanceof ApiError && error.code === "group_not_found") {
      ElMessage.error("当前 Server 尚不支持新增分组名称，请升级后重试");
    } else {
      ElMessage.error(error instanceof Error ? error.message : "分组更新失败");
    }
  } finally {
    groupSaving.value = false;
  }
}
let refreshTimer: ReturnType<typeof setInterval> | undefined;
let ratesTimer: ReturnType<typeof setInterval> | undefined;
const refreshError = ref("");
let runtimeRefreshGeneration = 0;
let runtimeSettledGeneration = 0;
const channelsRefreshing = ref(false);
async function refreshChannelsNow() {
  if (!siteID.value || channelsRefreshing.value || saving.value) return;
  channelsRefreshing.value = true;
  try {
    if (await syncChannels(siteID.value, true)) {
      await refreshRuntime();
      ElMessage.success("渠道信息已同步");
    }
  } finally { channelsRefreshing.value = false; }
}

function mergeOnlineRows(refreshed: ChannelBaseValue[]) {
  const local = new Map(bases.value.map(row => [`${row.channel_id}:${row.model_name}`, row]));
  const merged = refreshed.map(row => {
    const edited = local.get(`${row.channel_id}:${row.model_name}`);
    // An earlier periodic request can finish after a write notification.
    if (edited && new Date(edited.snapshot_at || 0).getTime() > new Date(row.snapshot_at || 0).getTime()) {
      row = { ...row, current_weight: edited.current_weight, current_priority: edited.current_priority, snapshot_at: edited.snapshot_at };
    }
    return row;
  });
  // The saved baseline always tracks the server: cancelling unsaved edits must
  // restore the latest persisted base values and keep the online columns that
  // arrived while editing, not roll them back to the last load.
  savedBases.value = clone(merged);
  bases.value = merged.map(row => {
    const edited = local.get(`${row.channel_id}:${row.model_name}`);
    return dirty.value && edited ? { ...row, base_weight: edited.base_weight, base_priority: edited.base_priority, max_rpm: edited.max_rpm, max_tpm: edited.max_tpm } : row;
  });
  for (const model of models.value) policy.dispatch_modes[model] ||= "off";
  if (!models.value.includes(activeModel.value)) activeModel.value = models.value[0] || "";
}
let changesAbort: AbortController | undefined;
async function watchChannelChanges() {
  changesAbort?.abort();
  const abort = new AbortController(); changesAbort = abort;
  const site = siteID.value;
  if (!site) return;
  let revision = "";
  while (!abort.signal.aborted) {
    try {
      const result = await dashboard.tuningChannelChanges(site, revision, abort.signal);
      if (abort.signal.aborted || site !== siteID.value) return;
      if (result.revision !== revision) {
        while ((loading.value || saving.value) && !abort.signal.aborted) await wait(200);
        if (abort.signal.aborted) return;
        const rows = await dashboard.tuningBaseValues(site);
        if (abort.signal.aborted || site !== siteID.value) return;
        if (loading.value || saving.value) { await wait(200); continue; }
        mergeOnlineRows(rows.items ?? []);
        void loadChannelDirectory(site);
        revision = result.revision;
      }
    } catch {
      if (abort.signal.aborted) return;
      await wait(2000);
    }
  }
}
async function refreshRuntime() {
  if (!siteID.value || loading.value) return;
  const site = siteID.value;
  const generation = ++runtimeRefreshGeneration;
  const loadAtStart = loadGeneration;
  // A newer pending poll must not invalidate completed data: otherwise
  // responses slower than the 30-second polling interval never reach the UI.
  const canApply = () => generation > runtimeSettledGeneration && loadAtStart === loadGeneration && site === siteID.value && !loading.value && !saving.value;
  refreshNow.value = Date.now();
  try {
    const [s, r, b] = await Promise.all([dashboard.tuningContinuousStates(site), dashboard.tuningRecommendations(site, 300), dashboard.tuningBaseValues(site)]);
    if (!canApply()) return;
    runtimeSettledGeneration = generation;
    acceptStates(site, s.items ?? []); events.value = r.items ?? [];
    mergeOnlineRows(b.items ?? []);
    void loadChannelDirectory(site);
    for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    if (!models.value.includes(activeModel.value)) activeModel.value = models.value[0] || "";
    if (!dirty.value) captureSavedState();
  } catch (error) {
    if (canApply()) {
      runtimeSettledGeneration = generation;
      refreshError.value = error instanceof Error ? error.message : "刷新失败";
    }
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
    // Agent-managed sites fall back to the Agent-collected snapshot; only a
    // failed live refresh aborts, so stale values are never saved as current.
    try { await dashboard.refreshTuningChannels(siteID.value); }
    catch (error) { if (!isAgentOnlySite(error)) throw error; }
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
    ElMessage.success(preflightCommandID ? "控制能力验证通过，自动模式已启用" : "设置已保存，执行结果将实时同步");
    await load();
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : "保存失败");
  } finally { saving.value = false; }
}
watch(() => filters.site_id, () => { groupDialogOpen.value = false; groupManagerOpen.value = false; cancelGroupPolls(); channels.value = []; pendingGroups.value = new Map(); groupErrors.value = new Map(); channelDirectoryGeneration++; void load(true); void watchChannelChanges(); });
watch(siteID, () => { ratesReady.value = false; currentRates.value.clear(); void refreshCurrentRates(); });
watch([eventModelFilter, eventRuleFilter, eventChannelQuery, eventDateRange, activeModel], () => { eventPage.value = 1; });
onMounted(() => { void load(true); void watchChannelChanges(); void refreshCurrentRates(); refreshTimer = setInterval(() => void refreshRuntime(), 30000); ratesTimer = setInterval(() => { if (!document.hidden) void refreshCurrentRates(); }, 5000); });
onBeforeUnmount(() => { loadGeneration++; changesAbort?.abort(); cancelGroupPolls(); if (refreshTimer) clearInterval(refreshTimer); if (ratesTimer) clearInterval(ratesTimer); });
</script>

<template><AppShell title="调权中心" class="tuning-shell"><template #tools><div class="tuning-header-actions"><span v-if="dirty" class="unsaved-status" role="status">有未保存更改</span><el-button v-if="dirty" :disabled="saving" @click="cancelChanges()">取消更改</el-button><el-button type="primary" :disabled="!dirty || loading || !siteID" :loading="saving" @click="save">保存更改</el-button></div></template><div ref="pageElement" v-loading="loading" class="page" :class="{'events-page':activeTab==='events', 'compact-layout':compactLayout}">
  <el-alert v-if="channelSyncError" :title="channelSyncError" type="warning" :closable="false" />
  <div class="tuning-tabs-shell" :class="{'has-overview-actions':activeTab==='overview'}">
    <div v-if="activeTab==='overview'" class="tools overview-actions"><el-button :loading="channelsRefreshing" :disabled="saving" @click="refreshChannelsNow">刷新渠道信息</el-button><el-button :disabled="!siteID" @click="groupManagerOpen = true">分组组合</el-button><el-dropdown trigger="click"><el-button :disabled="saving">更多 ···</el-button><template #dropdown><el-dropdown-menu><el-dropdown-item @click="helpOpen=true">使用说明</el-dropdown-item><el-dropdown-item divided :disabled="saving || !siteID" @click="sync('weight')">初始化/刷新基础值</el-dropdown-item></el-dropdown-menu></template></el-dropdown></div>
  <el-tabs v-model="activeTab" class="tabs">
    <el-tab-pane label="运行概览" name="overview">
      <el-card shadow="never" class="workspace-card">
        <el-empty v-if="!models.length" description="还没有渠道基础值"><el-button type="primary" @click="sync('weight')">立即从 new-api 读取</el-button></el-empty>
        <div v-else class="model-workspace" :class="{'nav-collapsed':modelNavCollapsed}">
          <aside class="model-nav">
            <div class="model-nav-tools"><el-input v-if="!modelNavCollapsed" v-model="modelQuery" clearable placeholder="搜索模型" aria-label="搜索模型"/><button type="button" class="model-nav-toggle" :aria-label="modelNavCollapsed ? '展开模型列表' : '收起模型列表'" :title="modelNavCollapsed ? '展开模型列表' : '收起模型列表'" :aria-expanded="!modelNavCollapsed" @click="modelNavCollapsed=!modelNavCollapsed"><el-icon aria-hidden="true"><ArrowRight v-if="modelNavCollapsed"/><ArrowLeft v-else/></el-icon></button></div>
            <div v-if="!modelNavCollapsed" class="model-mode-summary" aria-label="模型运行统计"><span>自动 <b>{{ counts.auto }}</b></span><span>观察 <b>{{ counts.observe }}</b></span><span>关闭 <b>{{ counts.off }}</b></span></div>
            <button v-if="modelNavCollapsed" class="model-nav-rail" type="button" :title="'当前模型：' + activeModel" aria-label="展开模型列表" @click="modelNavCollapsed=false">模型</button>
            <div v-show="!modelNavCollapsed" class="model-list"><button v-for="model in visibleModels" :key="model" :class="{active:activeModel===model}" :title="model + ' · ' + modeText(model)" :aria-label="model + '，' + modeText(model)" @click="selectModel(model)"><span><b>{{ model }}</b><span class="model-secondary"><small>{{ bases.filter(x=>x.model_name===model).length }} 个渠道</small><span class="model-mode-text" :class="modelMode(model)">{{ modelMode(model) === 'auto' ? '自动' : modelMode(model) === 'observe' ? '观察' : '关闭' }}</span></span></span></button><el-empty v-if="!visibleModels.length" :image-size="48" description="没有匹配模型"/></div>
          </aside>
          <section ref="detailElement" class="model-detail"><div class="model-head"><div><b class="active-model-name">{{ activeModel }}</b><span class="active-model-status" :class="modelMode(activeModel)">{{ modeText(activeModel) }}</span><small>{{ activeRows.length }} 个渠道</small><TuningInfo label="实时负载统计"><p>负载更新：{{ ratesAsOf ? formatTime(ratesAsOf) : '—' }}</p><p>已覆盖的 60 秒负载，每 5 秒刷新；Agent 保持 30 秒采集。</p><p>统计区间：{{ ratesWindowStart ? formatTime(ratesWindowStart) : '—' }} 至 {{ ratesAsOf ? formatTime(ratesAsOf) : '—' }}（不含结束秒）</p><p>数据延迟 {{ ratesDelay }} 秒。容量输入 0 表示不限制。</p></TuningInfo><small v-if="refreshError" class="evaluation-time stale">刷新失败：{{ refreshError }}</small><small v-else-if="evaluationStalled" class="evaluation-time stale">评估已停滞：最后成功于 {{ formatTime(lastEvaluationAt!) }}</small><small v-else-if="lastEvaluationAt" class="evaluation-time">最近评估 {{ formatTime(lastEvaluationAt) }} · 每 30 秒自动刷新</small></div><el-radio-group v-if="activeModel" v-model="policy.dispatch_modes[activeModel]" size="small" @change="dirty=true"><el-radio-button value="off">关闭</el-radio-button><el-radio-button value="observe">只观察</el-radio-button><el-radio-button value="auto">自动执行</el-radio-button></el-radio-group></div>
            <el-alert v-if="ratesError" :title="ratesError" type="warning" :closable="false"/>
            <el-table class="channel-table" scrollbar-always-on :span-method="coefficientSpan" :data="activeRows" :row-key="channelRowKey" :row-class-name="priorityRowClass" size="small" height="100%" empty-text="没有匹配渠道">
              <el-table-column label="渠道" :width="tableColumns.channel" align="left" fixed><template #default="{row}">

                <div class="channel-heading"><span class="channel-key">#{{ row.channel_id }} ·</span><b class="channel-name" :title="row.channel_name">{{ row.channel_name }}</b></div>
                <div class="channel-meta"><TuningCapacityMetric metric="TPM" :channel="row.channel_name" v-model="row.max_tpm" :current="currentRateFor(row)?.tpm" :modified="fieldChanged(row,'max_tpm')" @change="dirty=true"/><TuningCapacityMetric metric="RPM" :channel="row.channel_name" v-model="row.max_rpm" :current="currentRateFor(row)?.rpm" :modified="fieldChanged(row,'max_rpm')" @change="dirty=true"/></div>
                <small v-if="pendingGroups.has(row.channel_id) && !groupErrors.has(row.channel_id)" class="warning">分组等待执行</small><small v-if="groupErrors.has(row.channel_id)" class="danger" :title="groupErrors.get(row.channel_id)">分组执行失败</small>
              </template></el-table-column>
              <el-table-column label="分组" :width="tableColumns.groups" align="center"><template #default="{row}"><button type="button" class="group-cell-trigger" :aria-label="'编辑 ' + row.channel_name + ' 的分组'" :title="groupCellTitle(row.group_name)" @click="openGroupEditor(groupEditorRowFor(row))"><span class="group-tags"><span v-for="group in splitChannelGroups(row.group_name)" :key="group">{{ group }}</span><span v-if="!splitChannelGroups(row.group_name).length">设置分组</span></span></button></template></el-table-column>
              <el-table-column label="评估系数" align="center">
                <el-table-column v-for="metric in coefficientColumns" :key="metric.key" :prop="'coefficient_' + metric.key" :label="metric.label" :width="tableColumns.factor" align="center" class-name="coefficient-merged"><template #default="{row}">
                  <div v-if="metric.key === 'speed'" class="coefficient-group">
                    <div v-if="!coefficientEmptyText(row)" class="coefficient-values"><div v-for="item in coefficientColumns" :key="item.key" class="coefficient-cell" :title="coefficientCell(row, item.key).detail"><b v-if="item.key !== 'speed' || coefficientCell(row, item.key).value != null || !coefficientCell(row, item.key).status" :class="{'factor-up':(coefficientCell(row, item.key).value ?? 1)>1,'factor-down':(coefficientCell(row, item.key).value ?? 1)<1}">{{ coefficientCell(row, item.key).value == null ? '—' : factor(coefficientCell(row, item.key).value!) }}<el-tooltip v-if="item.key === 'speed' && coefficientCell(row, item.key).status === '输出替代'" content="TTFT 样本或基线不足，本轮使用输出速度系数替代。" placement="top"><span class="output-fallback-icon" tabindex="0" role="img" aria-label="输出替代：TTFT 样本或基线不足，本轮使用输出速度系数"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3 5h10m-3-3 3 3-3 3M13 11H3m3-3-3 3 3 3"/></svg></span></el-tooltip></b><small v-if="item.key === 'speed' && !overallEvaluationStatus(row) && coefficientCell(row, item.key).status && coefficientCell(row, item.key).status !== 'TTFT 有效' && coefficientCell(row, item.key).status !== '输出替代'">{{ coefficientCell(row, item.key).status }}</small></div></div>
                    <div v-if="overallEvaluationStatus(row)" class="coefficient-overall" :class="[rowStatus(row).kind, {'only-status':!!coefficientEmptyText(row)}]" :title="evaluationText(row)">{{ coefficientEmptyText(row) || overallEvaluationStatus(row) }}</div>
                    <span class="coefficient-samples" :title="'本轮评估窗口请求数；最低要求 ' + (savedPolicy?.continuous.min_samples ?? policy.continuous.min_samples) + ' 条'">样本 <b>{{ sampleText(row) }}</b></span>
                  </div>
                </template></el-table-column>
              </el-table-column>
              <el-table-column label="权重" align="center">
                <el-table-column label="基础" :width="tableColumns.input" align="center"><template #default="{row}"><el-input-number v-model="row.base_weight" :class="{modified:fieldChanged(row,'base_weight')}" :aria-label="row.channel_name + ' 基础权重'" :min="0" :controls="false" size="small" @change="dirty=true"/></template></el-table-column>
                <el-table-column label="计算" :width="tableColumns.number" align="center"><template #default="{row}"><div class="weight-calculated"><TuningInfo :trigger-text="String(calculatedWeight(row) ?? '—')" :class="{accent: calculatedWeight(row) !== null && calculatedWeight(row) !== row.current_weight}" :key="channelRowKey(row)" :label="'本轮计算 · ' + row.channel_name" :width="500">
                    <template v-if="stateFor(row)"><p class="calculation-formula">{{ originalBase(row) }} × {{ factor(stateFor(row)?.k_speed) }} × {{ factor(stateFor(row)?.k_otps) }} × {{ factor(stateFor(row)?.k_cache) }} × {{ factor(stateFor(row)?.k_error) }}</p><p class="formula-caption">基础 × {{ speedLabel(row) }} × 输出 × 缓存 × 错误</p><p>{{ evaluationText(row) }}</p><p v-if="displayedSpeedFactor(row) === null && calculatedWeight(row) !== null" class="formula-caption">参考值使用接口最近保留的系数；速度当前不可用于本轮评估，此处不代表本轮重新测得。</p><p v-if="speedLabel(row).includes('替代')">TTFT 不足，使用本轮 OTPS；输出系数参与两次。</p><div class="evidence-pairs"><span>窗口样本 <b>{{ sampleText(row) }}</b></span><span>TTFT 有效 <b>{{ stateFor(row)?.speed_sample_count ?? 0 }}</b></span><span>输出有效 <b>{{ stateFor(row)?.otps_sample_count ?? 0 }}</b></span><span>重试排除 <b>{{ stateFor(row)?.speed_retry_count ?? 0 }}</b></span></div><p>公式目标：<b>{{ calculatedWeight(row) ?? '—' }}</b><small>（按最近返回系数与已保存倍率边界推算；仅窗口不足或基础权重为0时不展示，不代表会执行）</small></p><p>安全限制后拟执行：<b>{{ stateFor(row)?.proposed_weight }}</b> · 当前：{{ row.current_weight }}</p><p v-if="fieldChanged(row, 'base_weight')" class="warning">基础值有未保存修改；本轮结果仍对应上次评估。</p><p v-if="limitReason(row)" class="limit-note">{{ limitReason(row) }}</p><details class="full-evidence"><summary>完整指标与计算依据</summary><pre class="factor-explanation">{{ factorExplanation(row) }}</pre></details></template><p v-else>等待首次评估，尚无计算数据。</p>
                  </TuningInfo><el-tooltip v-if="limitReason(row)" :content="limitReason(row)" placement="top"><button class="status-icon warning limit-icon" :aria-label="limitReason(row)"><svg viewBox="0 0 20 20" aria-hidden="true"><path d="M4 3h12M10 17V7m-4 4 4-4 4 4"/></svg></button></el-tooltip></div></template></el-table-column>
                <el-table-column label="线上" :width="tableColumns.number" align="center"><template #default="{row}"><span class="current-weight">{{ row.current_weight }}</span></template></el-table-column>
              </el-table-column>
              <el-table-column label="优先级" :width="tableColumns.priority" align="center"><template #default="{row}"><span :title="'调权中心保存的优先级；线上当前为 ' + row.current_priority + '。自动模式会纠正线上差异，修改后保存同步。'"><el-input-number :model-value="displayedPriority(row)" :class="{modified:priorityDrafts.has(row.channel_id)}" :aria-label="row.channel_name + ' 优先级'" :disabled="saving" :min="0" :precision="0" :controls="false" size="small" @update:model-value="editPriority(row, $event)"/></span></template></el-table-column>

            </el-table>
            <div class="channel-footer">{{ activeRows.length }} 个渠道<span>点击 TPM / RPM 编辑上限</span></div>
           </section>
         </div>
       </el-card>
       <el-dialog v-model="groupManagerOpen" class="tuning-group-dialog" title="分组组合管理" width="min(740px, calc(100vw - 32px))" append-to-body destroy-on-close :close-on-click-modal="false"><ChannelGroupEditor v-if="groupManagerOpen" :key="siteID" :site="siteID" current="" :options="groupOptions" :saving="false" manage-only @cancel="groupManagerOpen = false" /></el-dialog>
       <el-dialog v-model="groupDialogOpen" title="调整渠道分组" width="min(740px, calc(100vw - 32px))" class="tuning-group-dialog" append-to-body destroy-on-close :close-on-click-modal="false">
         <template v-if="editingChannel">
           <div class="group-editor-context"><div class="group-channel-heading"><b>{{ editingChannel.channel_name }}</b><span class="group-channel-id">#{{ editingChannel.channel_id }}</span></div><div class="group-current-line"><span>当前分组</span><span>{{ splitChannelGroups(editingChannel.group_name).join(' · ') || '未设置' }}</span></div></div>
           <ChannelGroupEditor v-if="groupDialogOpen" :key="`${siteID}:${editingChannel.channel_id}`" :site="siteID" :current="editingChannel.group_name || ''" :options="groupOptions" :saving="groupSaving" @save="saveGroupSelection" @cancel="groupDialogOpen = false" />
         </template>
       </el-dialog>
     </el-tab-pane>
    <el-tab-pane label="变更记录" name="events">
      <div class="summary compact"><div><span>近 7 天自动调权</span><b>{{ eventCount(7,'weight_write') }}</b></div><div><span>近 7 天熔断</span><b>{{ eventCount(7,'circuit_opened') }}</b></div><div><span>近 7 天恢复</span><b>{{ eventCount(7,'circuit_recovered') }}</b></div><div><span>近 7 天人工接管</span><b>{{ eventCount(7,'manual_takeover') }}</b></div></div>
      <el-card shadow="never" class="event-history-card">
        <div class="event-toolbar"><div class="event-filters">
            <el-select v-model="eventModelFilter" placeholder="模型" style="width:220px"><el-option label="全部模型" value=""/><el-option :label="`当前模型${activeModel ? `（${activeModel}）` : ''}`" value="__current__"/><el-option v-for="model in models" :key="model" :label="model" :value="model"/></el-select>
            <el-select v-model="eventRuleFilter" clearable placeholder="全部事件" style="width:170px"><el-option v-for="rule in eventRuleOptions" :key="rule" :label="eventName(rule)" :value="rule"/></el-select>
            <el-input v-model="eventChannelQuery" clearable placeholder="搜索渠道名称或 ID" style="width:240px"/>
            <el-date-picker v-model="eventDateRange" type="daterange" value-format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" @change="eventPage=1"/><el-button @click="resetEventFilters">重置</el-button>
          </div><small class="event-scope">筛选范围：最近加载的 {{ events.length }} 条记录（最多 300 条）</small></div>
        <div v-if="filteredEvents.length" class="event-table-wrap"><el-table :data="pagedEvents" size="small" height="100%">
          <el-table-column label="时间" width="170"><template #default="{row}">{{ formatTime(row.created_at) }}</template></el-table-column>
          <el-table-column label="模型与渠道" min-width="260"><template #default="{row}"><b>{{ eventModel(row) || '—' }}</b><small class="event-channel" :title="row.channel_name">{{ row.channel_name }} · #{{ row.channel_id }}</small></template></el-table-column>
          <el-table-column label="事件" width="145"><template #default="{row}"><span :class="{danger:row.rule==='circuit_opened'}">{{ eventName(row.rule) }}</span></template></el-table-column>
          <el-table-column label="变更内容" min-width="160"><template #default="{row}">权重 <b>{{ row.current_weight }} → {{ row.proposed_weight }}</b><small v-if="row.current_priority != null && row.proposed_priority != null && row.current_priority !== row.proposed_priority" class="channel-id">优先级 {{ row.current_priority }} → {{ row.proposed_priority }}</small></template></el-table-column>
          <el-table-column label="执行状态" width="115"><template #default="{row}"><span :class="eventResultClass(row)">{{ eventResult(row) }}</span></template></el-table-column>
          <el-table-column label="详情" width="80"><template #default="{row}"><TuningInfo label="变更详情"><div class="event-detail"><p><b>{{ eventName(row.rule) }}</b> · {{ eventModel(row) }}</p><p>{{ row.channel_name }} · #{{ row.channel_id }}</p><p>权重 {{ row.current_weight }} → {{ row.proposed_weight }}</p><p>模式：{{ row.mode_at_creation==='auto'?'自动':row.mode_at_creation==='observe'?'观察':'关闭' }}</p><p>状态：{{ eventResult(row) }}</p><p>记录时间：{{ formatTime(row.created_at) }}</p><p v-if="row.outcome_at">结果时间：{{ formatTime(row.outcome_at) }}</p><details><summary>记录依据</summary><pre class="factor-explanation">{{ JSON.stringify(row.evidence, null, 2) }}</pre><pre v-if="row.outcome" class="factor-explanation">{{ JSON.stringify(row.outcome, null, 2) }}</pre></details></div></TuningInfo></template></el-table-column>
        </el-table></div>
        <div v-if="filteredEvents.length" class="event-footer"><el-pagination v-model:current-page="eventPage" v-model:page-size="eventPageSize" layout="total, sizes, prev, pager, next" :page-sizes="[20,50,100]" :total="filteredEvents.length"/></div>
        <el-empty v-else description="当前筛选条件下暂无记录"/>
      </el-card>
    </el-tab-pane>
<el-tab-pane label="规则设置" name="settings"><el-card shadow="never" class="settings-card"><template #header><div class="head"><div><b>规则设置</b><small>当前站点 · {{ siteID }}</small></div><el-button @click="helpOpen=true">使用说明</el-button></div></template><el-tabs v-model="settingsSection" class="settings-sections"><el-tab-pane label="基础评估" name="basic"><div class="settings-intro"><div><h3>基础评估</h3><p>样本与评估周期，决定每轮使用的数据范围。</p></div><TuningInfo label="系数如何参与计算"><p>速度 × 输出 × 缓存 × 错误</p><p>TTFT 不足：输出² × 缓存 × 错误</p><p>最终受综合倍率、容量保护及单次上调上限约束。</p></TuningInfo></div><el-form label-position="top" class="settings-form"><div class="params"><el-form-item label="评估窗口（分钟）"><el-input-number v-model="policy.continuous.window_minutes" :min="1" @change="dirty=true"/><small>每次计算使用最近多少分钟的指标</small></el-form-item><el-form-item label="每渠道最少请求数"><el-input-number v-model="policy.continuous.min_samples" :min="1" @change="dirty=true"/><small>低于此数量不参与本轮性能比较，错误历史仍参与可靠性计算</small></el-form-item><el-form-item label="单次上调上限（%）"><el-input-number v-model="policy.continuous.max_increase_percent" :min="1" :max="100" :step="1" :precision="0" @change="dirty=true"/><small>自动模式每轮最多按当前有效权重上调该比例（至少允许 +1），默认 10%；下调不受限制</small></el-form-item><el-form-item label="调整灵敏度 S"><el-input-number v-model="policy.continuous.sensitivity" :min=".1" :max="5" :step=".1" @change="dirty=true"/><small>放大或缩小渠道相对差异；1 为标准</small></el-form-item></div></el-form></el-tab-pane><el-tab-pane label="性能系数" name="performance"><div class="settings-intro"><div><h3>性能系数</h3><p>TTFT 不足时复用本轮输出系数；缓存保留原逻辑。</p></div><TuningInfo label="系数如何参与计算"><p>速度 × 输出 × 缓存 × 错误</p><p>TTFT 不足：输出² × 缓存 × 错误</p><p>最终受综合倍率、容量保护及单次上调上限约束。</p></TuningInfo></div><el-form label-position="top" class="settings-form"><div class="factor-matrix"><div class="factor-matrix-head"><span>指标</span><span>影响指数</span><span>系数下限</span><span>系数上限</span></div><div class="factor-matrix-row"><b>TTFT 速度</b><el-form-item label="速度影响指数 αs"><el-input-number v-model="policy.continuous.speed_exponent" :min=".01" :max="2" :step=".05" :precision="2" @change="dirty=true"/><small>速度差异进入幂运算的强度；默认 0.35</small></el-form-item><el-form-item label="速度系数下限 Ls"><el-input-number v-model="policy.continuous.speed_min_factor" :min=".01" :max="1" :step=".05" :precision="2" @change="dirty=true"/><small>慢渠道速度系数最低值；默认 0.75</small></el-form-item><el-form-item label="速度系数上限 Us"><el-input-number v-model="policy.continuous.speed_max_factor" :min="1" :max="3" :step=".05" :precision="2" @change="dirty=true"/><small>快渠道速度系数最高值；默认 1.25</small></el-form-item></div><div class="factor-matrix-row"><b>平均输出速度</b><el-form-item label="输出影响指数 αo"><el-input-number v-model="policy.continuous.otps_exponent" :min=".01" :max="2" :step=".05" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="输出系数下限 Lo"><el-input-number v-model="policy.continuous.otps_min_factor" :min=".01" :max="1" :step=".05" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="输出系数上限 Uo"><el-input-number v-model="policy.continuous.otps_max_factor" :min="1" :max="3" :step=".05" :precision="2" @change="dirty=true"/></el-form-item></div><div class="factor-matrix-row"><b>缓存</b><el-form-item label="缓存影响指数 αc"><el-input-number v-model="policy.continuous.cache_exponent" :min=".01" :max="2" :step=".05" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="缓存系数下限 Lc"><el-input-number v-model="policy.continuous.cache_min_factor" :min=".01" :max="1" :step=".05" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="缓存系数上限 Uc"><el-input-number v-model="policy.continuous.cache_max_factor" :min="1" :max="3" :step=".05" :precision="2" @change="dirty=true"/></el-form-item></div></div><h4 class="parameter-heading">综合倍率边界</h4><div class="params two-columns"><el-form-item label="综合倍率下限 Lm"><el-input-number v-model="policy.continuous.combined_min_factor" :min=".01" :max="1" :step=".05" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="综合倍率上限 Um"><el-input-number v-model="policy.continuous.combined_max_factor" :min="1" :max="5" :step=".05" :precision="2" @change="dirty=true"/></el-form-item></div><h4 class="parameter-heading">TTFT 分位权重</h4><div class="params"><el-form-item label="P50 占比 w50"><el-input-number v-model="policy.continuous.speed_p50_weight" :min="0" :max="1" :step=".05" :precision="2" @change="dirty=true"/><small>默认 0.50；三个占比之和必须为 1</small></el-form-item><el-form-item label="P90 占比 w90"><el-input-number v-model="policy.continuous.speed_p90_weight" :min="0" :max="1" :step=".05" :precision="2" @change="dirty=true"/><small>默认 0.30；三个占比之和必须为 1</small></el-form-item><el-form-item label="P95 占比 w95"><el-input-number v-model="policy.continuous.speed_p95_weight" :min="0" :max="1" :step=".05" :precision="2" @change="dirty=true"/><small>默认 0.20；三个占比之和必须为 1</small></el-form-item></div></el-form></el-tab-pane><el-tab-pane label="错误率曲线" name="errors"><div class="settings-intro"><div><h3>错误率曲线</h3><p>错误率节点与惩罚系数之间采用线性插值。</p></div><TuningInfo label="系数如何参与计算"><p>速度 × 输出 × 缓存 × 错误</p><p>TTFT 不足：输出² × 缓存 × 错误</p><p>最终受综合倍率、容量保护及单次上调上限约束。</p></TuningInfo></div><el-form label-position="top" class="settings-form"><div class="params"><el-form-item label="健康错误率节点 E1"><el-input-number v-model="policy.continuous.error_healthy_rate" :min="0" :max="1" :step=".01" :precision="2" @change="dirty=true"/><small>此错误率以内系数为 1</small></el-form-item><el-form-item label="轻度错误率节点 E2"><el-input-number v-model="policy.continuous.error_degraded_rate" :min="0" :max="1" :step=".01" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="轻度错误系数 K2"><el-input-number v-model="policy.continuous.error_degraded_factor" :min=".01" :max=".99" :step=".05" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="严重错误率节点 E3"><el-input-number v-model="policy.continuous.error_poor_rate" :min="0" :max="1" :step=".01" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="严重错误系数 K3"><el-input-number v-model="policy.continuous.error_poor_factor" :min=".01" :max=".99" :step=".05" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="封底错误率节点 E4"><el-input-number v-model="policy.continuous.error_floor_rate" :min="0" :max="1" :step=".01" :precision="2" @change="dirty=true"/></el-form-item><el-form-item label="错误系数下限 Kmin"><el-input-number v-model="policy.continuous.error_min_factor" :min=".01" :max=".99" :step=".05" :precision="2" @change="dirty=true"/></el-form-item></div></el-form></el-tab-pane><el-tab-pane label="熔断与恢复" name="safety"><div class="settings-intro"><div><h3>熔断与恢复</h3><p>自动模式静默后主动探测，观察模式使用真实流量恢复。</p></div><TuningInfo label="系数如何参与计算"><p>速度 × 输出 × 缓存 × 错误</p><p>TTFT 不足：输出² × 缓存 × 错误</p><p>最终受综合倍率、容量保护及单次上调上限约束。</p></TuningInfo></div><el-form label-position="top" class="settings-form"><div class="params"><el-form-item label="启用批次快速熔断"><el-switch v-model="policy.continuous.fast_circuit_enabled" @change="dirty=true"/><small>直接检查每次 Agent 上报的渠道增量，不等待分钟桶稳定</small></el-form-item><el-form-item label="快速熔断最少请求数"><el-input-number v-model="policy.continuous.fast_circuit_min_samples" :min="1" :max="100000" @change="dirty=true"/><small>单次上报达到该请求数后才判断，默认 50</small></el-form-item><el-form-item label="快速熔断错误率"><el-input-number v-model="policy.continuous.fast_circuit_error_rate" :min=".01" :max="1" :step=".05" :precision="2" @change="dirty=true"/><small>非用户错误率达到阈值立即熔断，默认 50%</small></el-form-item><el-form-item label="熔断错误率"><el-input-number v-model="policy.continuous.circuit_error_rate" :min=".01" :max="1" :step=".01" @change="dirty=true"/><small>平滑渠道错误率达到此值才停止分流</small></el-form-item><el-form-item label="被动恢复错误率"><el-input-number v-model="policy.continuous.recovery_error_rate" :min="0" :max=".99" :step=".01" @change="dirty=true"/><small>观察模式降到此值后解除模拟熔断</small></el-form-item><el-form-item label="探针恢复阈值"><el-input-number v-model="policy.continuous.recovery_threshold" :min=".01" :max="1" :step=".01" @change="dirty=true"/><small>自动模式探针成功率×探针速度达到此值才恢复</small></el-form-item><el-form-item label="熔断静默期（分钟）"><el-input-number v-model="policy.continuous.silent_minutes" :min="1" @change="dirty=true"/><small>自动模式熔断后等待多久再开始探测</small></el-form-item><el-form-item label="探测间隔（秒）"><el-input-number v-model="policy.continuous.probe_interval_seconds" :min="1" @change="dirty=true"/><small>连续探测请求之间的等待时间</small></el-form-item><el-form-item label="探测次数"><el-input-number v-model="policy.continuous.probe_count" :min="1" @change="dirty=true"/><small>一次恢复判断发送多少次请求</small></el-form-item><el-form-item label="恢复初始倍率"><el-input-number v-model="policy.continuous.soft_start_multiplier" :min=".01" :max="1" :step=".05" @change="dirty=true"/><small>恢复首轮使用基础权重的比例</small></el-form-item></div></el-form></el-tab-pane></el-tabs></el-card></el-tab-pane>
  </el-tabs>
  </div>

  <el-drawer v-model="helpOpen" title="调权中心使用说明" size="min(860px, 92vw)" class="tuning-help">
<el-tabs v-model="helpSection"><el-tab-pane label="权重与参数" name="calculation"><div class="help-guide"><section><h3>完整计算流程</h3><ol><li>按模型分组，只比较提供同一个模型的渠道；多模型渠道为避免互相影响，不参与自动调权。</li><li>读取最近“评估窗口”内的请求，形成每个渠道的 TTFT、缓存命中、OTPS 和错误率指标。</li><li>用至少 2 个合格渠道计算同模型平均基线，再得到速度、缓存、输出、错误四个系数。</li><li>先计算原始目标：<code>Wtarget = round(Wbase × clamp(Ks × Kc × Ko × Ke, Lm, Um))</code>。</li><li>自动模式再应用容量保护和单次上调限制，得到页面展示并写入 new-api 的本轮权重；下调不受单次比例限制。</li></ol><p class="help-warning">权重下方的“拟执行”是安全限制后的本轮执行值，不一定等于基础权重直接乘四个可见系数。</p></section><section><h3>第一次使用</h3><ol><li>点击“初始化/刷新基础值”，读取当前线上权重和优先级。</li><li>先选择“只观察”，确认计算结果合理。</li><li>再切换为“自动执行”并保存；系统会先验证 new-api 控制链路。</li></ol></section><section><h3>指标与统计口径</h3><div class="help-table"><div><b>指标</b><b>定义、来源与参与条件</b></div><div><strong>TTFT P50/P90/P95</strong><span>首字节或首 Token 响应耗时的第 50、90、95 百分位，单位秒，来自 Agent 采集的 new-api 请求日志。P50 代表典型延迟，P90/P95 体现慢请求尾部。仅使用成功流式请求中确认未重试的 TTFT；有效样本达到“每渠道最少请求数”，且三个分位值都大于 0，才参与速度比较。重试和无法确认的样本不用于调权速度，监控统计不变。</span></div><div><strong>同模型平均数</strong><span>对同一模型下所有合格渠道的对应指标做算术平均；速度基线至少需要 2 个未重试样本合格渠道；不足时复用本轮有效输出系数，OTPS 参与两次，不沿用历史速度系数。输出独立建立同模型基线，不依赖 TTFT 是否存在。</span></div><div><strong>大输入缓存命中率 C</strong><span><code>缓存读取 Token 总数 ÷ 提示 Token 总数</code>。只统计成功请求且输入大于 512 Token；当前渠道累计提示 Token 至少 10,000，并且至少 2 个渠道有足够缓存证据时才参与。</span></div><div><strong>OTPS</strong><span><code>成功请求输出 Token 总数 ÷ 请求总耗时总秒数</code>，包含流式和非流式，不扣除首字等待；排除 fallback、同渠道重试与无法确认的请求。未重试有效请求达到“每渠道最少请求数”、输出 Token 至少 100，且至少 2 个渠道满足条件时参与，不依赖 TTFT。监控保留重试样本。</span></div><div><strong>渠道错误率 E</strong><span><code>(总错误数 − 用户自身错误数) ÷ 请求数</code>。用户参数、余额等归类为用户侧的错误不会处罚渠道；渠道错误按完整分钟桶进入 EWMA。</span></div><div><strong>平滑错误率</strong><span><code>Ema(new) = 0.3 × E本分钟 + 0.7 × Ema(old)</code>。最近约 90 秒的未稳定分钟桶暂不折入，避免半桶数据让错误率剧烈跳变。</span></div></div></section><section><h3>四项评估系数</h3><dl class="formula-list"><div><dt>速度系数 Ks</dt><dd><code>R = w50×TTFT50/平均TTFT50 + w90×TTFT90/平均TTFT90 + w95×TTFT95/平均TTFT95</code><code>Ks = clamp((1/R)^(αs×S), Ls, Us)</code><span>w50、w90、w95 合计必须为 1；αs 是速度影响指数，S 是全局敏感度。渠道越快，R 越小、Ks 越大。TTFT 样本或基线不足时 Ks=Ko，直接复用本轮输出系数，不再单独应用 TTFT 系数上下限。</span></dd></div><div><dt>缓存系数 Kc</dt><dd><code>Kc = clamp((C/Cavg)^(αc×S), Lc, Uc)</code><span>C 是本渠道大输入缓存命中率，Cavg 是同模型平均值；αc 控制缓存差异影响强度。证据不足时 Kc=1。</span></dd></div><div><dt>输出系数 Ko</dt><dd><code>Ko = clamp((OTPS/OTPSavg)^(αo×S), Lo, Uo)</code><span>αo 控制输出速度差异影响强度；输出越快 Ko 越大。证据不足时 Ko=1。</span></dd></div><div><dt>错误系数 Ke</dt><dd><code>E≤E1 → 1；E1~E2 → 1 到 K2；E2~E3 → K2 到 K3；E3~E4 → K3 到 Kmin；E≥E4 → Kmin</code><span>区间内采用线性插值。E1/E2/E3/E4 是健康、轻度、严重和封底错误率节点，K2/K3/Kmin 是对应惩罚系数。</span></dd></div></dl></section><section><h3>参数符号说明</h3><div class="help-table compact"><div><b>参数</b><b>作用</b></div><div><strong>S 敏感度</strong><span>同时放大或减弱速度、缓存、输出三项差异；越大越激进，不直接改变错误系数。</span></div><div><strong>αs / αc / αo</strong><span>对应速度、缓存、输出的指数。等于 1 按原始比例响应；小于 1 压缩差异；大于 1 放大差异。</span></div><div><strong>Ls/Us、Lc/Uc、Lo/Uo</strong><span>单项系数上下限，防止某一个指标独自把权重推得过高或过低。</span></div><div><strong>Lm / Um</strong><span>四项相乘后的综合倍率上下限。即使单项乘积超出范围，原始目标也只按该范围计算。</span></div><div><strong>评估窗口</strong><span>性能指标使用的最近分钟数。窗口越大越稳定但反应越慢；窗口越小越灵敏但更容易波动。</span></div><div><strong>最少请求数</strong><span>速度采用未重试有效 TTFT 样本数；TTFT 不足时使用本轮有效输出系数；窗口总请求不足时保持已执行权重。缓存和错误规则不变。</span></div></div></section><section><h3>三个权重与刷新时序</h3><dl><div><dt>基础权重 Wbase</dt><dd>长期计算基准，可手动修改；不是当前线上权重。</dd></div><div><dt>计算权重</dt><dd>页面展示的是经过容量和上调限制后的本轮执行值。评估系数反映原始评分，因此二者不一定能直接相乘对应。</dd></div><div><dt>当前权重 Wcurrent</dt><dd>new-api 已确认的线上权重。评估、写入和渠道快照更新时间不同，短时间内可能看到计算权重与当前权重相同，下一轮才继续爬升。</dd></div></dl></section><section><h3>自动模式</h3><p>每分钟重新计算。只要本轮执行权重与上次成功写入值不同，就写入 new-api；没有变化则不重复写。如果线上权重被人工或其他系统修改，系统会在确认外部变化后按当前规则重新计算并写回。</p></section><section><h3>保留的安全保护</h3><dl><div><dt>熔断</dt><dd>渠道错误率达到阈值且样本足够时，只将权重置为 0，优先级保持调权中心保存值。</dd></div><div><dt>恢复</dt><dd>静默期后主动探测；通过后先以低权重恢复，再回到正常计算。</dd></div><div><dt>多模型渠道</dt><dd>一个渠道同时服务多个模型时不自动调权，避免模型之间互相影响。</dd></div><div><dt>写入失败</dt><dd>连续失败 3 次后暂停每分钟写入，改为每 10 分钟重试，成功后自动恢复。</dd></div></dl></section><section><h3>表格与记录</h3><p>“状态”列显示样本不足、熔断、恢复中或写入失败等原因；“变更记录”保存每次自动写入、熔断和恢复。计算公式可悬停或点击“计算”数值查看。</p></section></div></el-tab-pane><el-tab-pane label="容量与执行" name="capacity"><div class="help-guide"><section><h3>从原始目标到本轮执行权重</h3><ol><li><code>M = clamp(Ks × Kc × Ko × Ke, Lm, Um)</code></li><li><code>Wtarget = max(1, round(Wbase × M))</code>；基础权重为 0 时渠道不参与调权。</li><li>若当前 RPM 或 TPM 达到所配置上限，且 Wtarget 高于当前有效权重，则本轮保持当前权重；下降仍然允许。0 表示该项不限制。</li><li>自动模式上调时：<code>Wmax = max(Wcurrent + 1, floor(Wcurrent × (1 + P/100)))</code>，最终取 <code>min(Wtarget, Wmax)</code>。P 是“单次上调上限”；至少允许 +1，所以下降后的低权重会逐轮恢复。</li><li>只观察模式不写 new-api；自动模式只在整数执行权重变化时写入。</li></ol></section><section><h3>容量、熔断与恢复参数</h3><dl class="formula-list"><div><dt>最大 RPM / TPM</dt><dd>使用当前滚动速率判断。任一非零上限被达到后只禁止上调，不影响保持和下调；实时速率不可用时，为安全起见，有配置上限的渠道同样禁止上调。</dd></div><div><dt>快速熔断</dt><dd>直接检查每次 Agent 上报增量：非用户错误率达到“快速熔断错误率”，且本批请求数达到门槛时立即熔断，不等待稳定分钟桶。</dd></div><div><dt>常规熔断</dt><dd>平滑错误率达到“熔断错误率”且样本充分时，只将权重置 0，优先级保持调权中心保存值。错误系数节点负责渐进降权，熔断阈值负责彻底停流，两者不是同一参数。</dd></div><div><dt>探针恢复阈值</dt><dd><code>探针成功率 × 探针速度得分 ≥ 恢复阈值</code>才通过。静默期决定熔断后等待多久，探测次数与间隔决定一次恢复检测的规模。</dd></div><div><dt>恢复初始倍率</dt><dd>探针通过后先以<code>基础权重 × 恢复初始倍率</code>软启动，下一轮再回到正常公式和单次上调限制。</dd></div></dl></section></div></el-tab-pane></el-tabs>
  </el-drawer>
</div></AppShell></template>

<style scoped>
.tuning-shell :deep(.content){padding:8px}

.page{--tuning-ink:var(--ct-ink);--tuning-muted:var(--ct-ink-3);color:var(--tuning-ink);display:grid;gap:14px;padding-bottom:0}
.tabs>.el-tabs__header{margin:0}
.head,.title-line,.tools,.model-head,.channel-toolbar,.channel-filters{display:flex;align-items:center;gap:12px}
.head,.model-head,.channel-toolbar{justify-content:space-between}
.head>div:first-child{display:flex;align-items:center;gap:8px}
.head b{font-size:14px}
.head small{color:var(--tuning-muted)}
.tools{gap:8px;flex-wrap:wrap}
.tools :deep(.el-button+.el-button){margin-left:0}
.inline-metrics{display:flex;align-items:center;margin-left:8px;color:var(--ct-ink-2);font-size:12px}
.inline-metrics b{margin-left:3px;color:var(--ct-ink);font-size:14px}
.workspace-card :deep(.el-card__header){padding:8px 12px}
.workspace-card :deep(.el-card__body){padding:8px}
.model-workspace{display:grid;grid-template-columns:220px minmax(0,1fr);height:calc(100vh - 176px);min-height:420px;border:1px solid var(--ct-line);border-radius:8px;overflow:hidden}
.model-nav{padding:10px 8px;border-right:1px solid var(--ct-line);background:var(--ct-surface-2);min-height:0;display:flex;flex-direction:column}
.model-list{display:grid;grid-template-columns:minmax(0,1fr);align-content:start;gap:5px;overflow-x:hidden;overflow-y:auto;margin-top:10px;min-height:0;min-width:0}
.model-list>button{display:flex;width:100%;align-items:center;justify-content:space-between;gap:8px;padding:8px;border:1px solid transparent;background:transparent;text-align:left;cursor:pointer;border-radius:5px;color:var(--tuning-ink)}
.model-list>button:hover{background:var(--ct-surface)}
.model-list>button.active{border-color:var(--ct-line);background:var(--ct-accent-weak);color:var(--ct-accent)}
.model-list b{display:block;font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.model-list small{display:block;color:var(--ct-ink-3);font-size:12px;margin-top:3px}
.model-secondary{display:flex;align-items:baseline;justify-content:space-between;gap:8px;margin-top:3px}.model-secondary small{margin-top:0}.model-mode-text{font-size:11px;color:var(--ct-ink-3);font-weight:400}.model-mode-text.auto{color:var(--ct-ok)}.model-mode-text.observe{color:var(--ct-warn)}
.model-detail{display:flex;flex-direction:column;min-width:0;min-height:0}
.model-head{min-height:34px;padding:10px 12px;background:var(--ct-surface-2);border-bottom:1px solid var(--ct-line);flex-wrap:wrap}
.model-head>div:first-child{display:flex;align-items:center;flex-wrap:wrap;gap:10px}
.model-head b{font-size:14px}
.model-head small{font-size:11px;color:var(--ct-ink-3)}
.model-head .stale{color:var(--ct-crit)}
.channel-toolbar{padding:8px 10px;gap:10px;flex-wrap:wrap}
.channel-filters{gap:8px}
.channel-filters :deep(.el-input){width:190px}
.channel-filters :deep(.el-select){width:125px}
.load-time{display:flex;align-items:center;gap:5px;font-size:11px;color:var(--ct-ink-3)}
.channel-table{flex:1;min-height:0;width:100%;font-variant-numeric:tabular-nums}
.channel-table :deep(th.el-table__cell),.event-history-card :deep(th.el-table__cell){background:var(--ct-surface);color:var(--ct-ink-3);font-weight:500;height:38px;border-bottom:1px solid var(--ct-line)}
.channel-table :deep(td.el-table__cell){padding:8px 0;border-bottom-color:var(--ct-line)}
.channel-table :deep(.cell){padding:0 10px}
.channel-name{line-height:20px;display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;font-size:12px;color:var(--ct-ink-2);font-weight:400}
.channel-heading{display:flex;align-items:baseline;gap:6px;min-width:0;line-height:20px}.channel-heading .channel-name{min-width:0}.channel-key{flex-shrink:0;font-size:12px;line-height:20px;color:var(--ct-ink-3)}
.channel-meta{display:flex;align-items:center;gap:10px;white-space:nowrap;font-size:11px;color:var(--ct-ink-3);margin-top:3px}
.group-cell-trigger{width:100%;max-width:100%;min-width:0;border:1px solid transparent;border-radius:4px;padding:0 3px;background:transparent;cursor:pointer;color:var(--ct-ink-3)}
.group-cell-trigger:hover{background:var(--ct-accent-weak);border-color:var(--ct-line)}
.group-cell-trigger:focus-visible{outline:2px solid var(--ct-accent)}
.group-tags{line-height:18px;display:flex;flex-wrap:wrap;justify-content:center;gap:4px;font-size:11px;text-align:center}
.group-tags>span{max-width:100%;overflow-wrap:anywhere;white-space:normal;padding:0 4px;background:var(--ct-accent-weak);border:1px solid var(--ct-line);color:var(--ct-accent);border-radius:3px}
.current-weight{display:block;font-size:14px;line-height:20px;font-weight:500;color:var(--ct-ink-2)}
.weight-next{display:flex;align-items:center;gap:5px;white-space:nowrap;color:var(--ct-ink-3);font-size:11px;margin-top:0;line-height:18px}
.channel-table :deep(.el-input-number){width:76px}
.channel-table :deep(.el-input__wrapper){box-shadow:0 0 0 1px #dfe7f3 inset;border-radius:4px;background:var(--ct-surface);padding:0 7px}
.channel-table :deep(.el-input__wrapper:hover){box-shadow:0 0 0 1px #99b5ea inset}
.channel-table :deep(.el-input__inner){text-align:center;font-variant-numeric:tabular-nums;color:var(--ct-ink)}
.channel-table :deep(.modified .el-input__wrapper){box-shadow:0 0 0 1px #dcac4b inset;background:var(--ct-warn-weak)}
.priority-cell{display:flex;align-items:center;gap:8px;font-size:11px;color:var(--ct-ink-3);white-space:nowrap}
.priority-cell b{color:var(--ct-ink-2);font-weight:500}
.priority-cell label{display:flex;align-items:center;gap:5px}
.priority-cell :deep(.el-input-number){width:57px}
.capacity-cell{display:flex;align-items:center;justify-content:flex-start;gap:5px;font-size:12px;white-space:nowrap}
.capacity-cell :deep(.el-input-number){width:70px}
.capacity-cell.tpm :deep(.el-input-number){width:88px}
.separator{color:var(--ct-ink-3)}
.header-sub{font-size:10px;color:var(--ct-ink-3);font-weight:400;margin-left:3px}
.unlimited{display:block;text-align:right;color:var(--ct-ink-3);font-size:10px;line-height:14px}
.row-status{display:flex;align-items:center;gap:7px;flex-wrap:wrap}
.status-text,.status-icon{border:0;background:transparent;cursor:help;padding:0;font:inherit}
.status-text{display:inline-flex;align-items:center;gap:5px;font-size:11px}
.status-icon{display:inline-grid;place-items:center;min-width:16px;height:16px;font-size:13px;font-weight:600}
.limit-icon svg{width:19px;height:19px;fill:none;stroke:currentColor;stroke-width:1.7;stroke-linecap:round;stroke-linejoin:round}
.danger,.negative{color:var(--ct-crit)}
.warning{color:var(--ct-warn)}
.accent,.positive{color:var(--ct-accent)}
.muted{color:var(--ct-ink-3)}
.channel-footer{display:flex;justify-content:space-between;gap:8px;padding:8px 10px;font-size:11px;color:var(--ct-ink-3);border-top:1px solid var(--ct-line)}
.calculation-formula{font-size:17px;font-weight:650;color:var(--ct-ink);font-variant-numeric:tabular-nums;margin:8px 0}
.formula-caption{color:var(--ct-ink-3);font-size:11px}
.evidence-pairs{display:grid;grid-template-columns:1fr 1fr;gap:8px;border-block:1px solid var(--ct-line);padding:12px 0;font-size:12px}
.evidence-pairs b{margin-left:5px;color:var(--ct-ink)}
.limit-note{padding:8px 10px;background:var(--ct-warn-weak);color:var(--ct-warn);border-radius:5px}
.full-evidence summary,.event-detail summary{color:var(--ct-accent);cursor:pointer;font-size:12px}
.factor-explanation{white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.7 ui-monospace,Consolas,monospace;color:var(--ct-ink-2);margin:12px 0}
.summary{display:flex;gap:0;padding:18px 24px;background:var(--ct-surface);border:1px solid var(--ct-line);border-radius:7px;margin:0 0 16px}
.summary>div{display:flex;align-items:center;gap:14px;padding:0 28px;border-right:1px solid var(--ct-line)}
.summary>div:first-child{padding-left:0}
.summary>div:last-child{border-right:0}
.summary span{font-size:12px;color:var(--ct-ink-3)}
.summary b{font-size:23px;color:var(--ct-accent)}
.event-history-card :deep(.el-card__body){padding:18px 20px}
.event-scope{display:block;margin-top:10px;color:var(--ct-ink-3);font-size:11px}
.event-toolbar{margin-bottom:18px}
.event-filters{display:flex;gap:10px;flex-wrap:wrap;align-items:center}
.event-filters :deep(.el-date-editor){max-width:300px;flex-grow:0}
.event-table-wrap{height:calc(100vh - 375px);min-height:330px}
.event-channel{display:block;color:var(--ct-ink-3);font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.channel-id{display:block;color:var(--ct-ink-3);font-size:11px}
.event-history-card :deep(td.el-table__cell){padding:8px 0;font-variant-numeric:tabular-nums;border-bottom-color:var(--ct-line)}
.event-footer{display:flex;justify-content:flex-end;padding-top:16px}
.settings-card :deep(.el-card__body){padding:16px 20px}
.factor-matrix{max-width:1120px;border:1px solid var(--ct-line);border-radius:6px;overflow:hidden}
.factor-matrix-head,.factor-matrix-row{display:grid;grid-template-columns:150px repeat(3,minmax(0,1fr));align-items:start;gap:16px;padding:10px 12px}
.factor-matrix-head{background:var(--ct-surface-2);color:var(--ct-ink-3);font-size:12px}
.factor-matrix-row{border-top:1px solid var(--ct-line)}
.factor-matrix-row>b{font-size:13px;font-weight:500;padding-top:9px;color:var(--ct-ink-2)}
.factor-matrix-row :deep(.el-form-item){margin:0;min-width:0}
.factor-matrix-row :deep(.el-form-item__label){position:absolute;width:1px;height:1px;overflow:hidden;clip-path:inset(50%)}
.factor-matrix-row :deep(.el-input-number){width:100%}
.factor-matrix-row small{display:none}
.parameter-heading{font-size:13px;color:var(--ct-ink-2);margin:26px 0 16px}
.settings-card .two-columns{grid-template-columns:repeat(2,minmax(0,1fr));max-width:760px}
.settings-card .settings-form{max-width:none}
.settings-card .params{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:0 16px;max-width:1200px}
.settings-intro{display:flex;justify-content:space-between;align-items:center;margin:8px 0 16px}
.settings-intro h3{font-size:14px;margin:0 0 7px}
.settings-intro p{margin:0;color:var(--ct-ink-3);font-size:12px}
.params{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px 32px;max-width:1200px}
.params :deep(.el-form-item__content){display:flex;align-items:flex-start;flex-direction:column}
.params :deep(.el-form-item__label){font-size:13px;color:var(--ct-ink-2);margin-bottom:8px}
.params :deep(.el-input-number){width:100%;max-width:300px}
.params small{font-size:11px;color:var(--ct-ink-3);line-height:1.65;margin-top:7px;max-width:300px}
.tuning-save-bar{position:sticky;bottom:14px;z-index:12;display:flex;align-items:center;justify-content:space-between;gap:16px;margin-top:16px;padding:14px 20px;border:1px solid var(--ct-line);border-radius:7px;background:var(--ct-surface);box-shadow:0 5px 22px #20365814;font-size:13px}
.tuning-save-bar i{display:inline-block;width:7px;height:7px;border-radius:50%;background:var(--ct-warning-solid);margin-right:8px}
.save-context{color:var(--ct-ink-3);font-size:11px;margin-left:14px}
.group-editor-context{margin-bottom:10px;padding:12px 14px;border:1px solid var(--ct-line);border-radius:8px;background:var(--ct-surface-2)}
.group-channel-heading{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.group-channel-heading b{color:var(--ct-ink);font-size:14px;font-weight:600;overflow-wrap:anywhere}.group-channel-id{color:var(--ct-ink-2);font-size:11px;border:1px solid var(--ct-line);background:var(--ct-surface);border-radius:4px;padding:1px 6px;font-variant-numeric:tabular-nums}
.group-current-line{display:grid;grid-template-columns:56px 1fr;gap:12px;font-size:12px;line-height:1.8;color:var(--ct-ink-2);margin-top:10px;overflow-wrap:anywhere}.group-current-line>span:first-child{color:var(--ct-ink-3)}
.group-preview{padding:14px 16px;background:var(--ct-accent-weak);border-radius:6px;margin:6px 0 16px;font-size:12px;overflow-wrap:anywhere}
.group-preview>b{display:block;margin-bottom:12px}
.group-preview>div{display:grid;grid-template-columns:65px 1fr;gap:10px;margin:7px 0}
.group-preview span{color:var(--ct-ink-3)}

.tuning-help :deep(.el-drawer__header){margin-bottom:12px;color:var(--ct-ink)}
.tuning-help :deep(.el-drawer__body){padding-top:0}

@media(max-width:1250px){.model-workspace{grid-template-columns:220px minmax(0,1fr)}
.head{flex-wrap:wrap}
.model-head{gap:10px}
.params{grid-template-columns:repeat(2,minmax(0,1fr))}
.summary>div{padding:0 14px}
.summary span{font-size:11px}
.save-context{display:none}}
@media(max-width:760px){.factor-matrix-head,.factor-matrix-row{grid-template-columns:100px repeat(3,minmax(0,1fr));gap:10px;padding:12px}
.model-workspace{grid-template-columns:150px minmax(0,1fr);min-height:520px}
.inline-metrics{gap:8px}
.tools{gap:4px}
.summary{flex-wrap:wrap;gap:14px}
.summary>div{border:0;padding:0}
.params{grid-template-columns:1fr}
.channel-footer span{display:none}
.event-filters :deep(.el-date-editor){max-width:100%}
.tuning-save-bar{flex-wrap:wrap}
.model-head>div:first-child small:last-child{flex-basis:100%}}

.help-guide{display:grid;gap:22px;color:var(--ct-ink-2)}
.help-guide section{padding-bottom:18px;border-bottom:1px solid var(--ct-line)}
.help-guide section:last-child{border-bottom:0}
.help-guide h3{margin:0 0 9px;color:var(--ct-ink)}
.help-guide p,.help-guide li,.help-guide dd{line-height:1.7}
.help-guide p,.help-guide ol,.help-guide ul,.help-guide dl{margin:0}
.help-guide ol,.help-guide ul{padding-left:22px}
.help-guide dl{display:grid;gap:8px}
.help-guide dl>div{display:grid;grid-template-columns:90px 1fr;gap:12px}
.help-guide dt{font-weight:600;color:var(--ct-accent)}
.help-guide dd{margin:0}
.help-guide .parameter-guide{gap:0;border:1px solid var(--ct-line);border-radius:8px;overflow:hidden}
.help-guide .parameter-guide>div{grid-template-columns:118px 1fr;padding:10px 12px;border-bottom:1px solid var(--ct-line)}
.help-guide .parameter-guide>div:last-child{border-bottom:0}
.help-guide .parameter-guide dt{color:var(--ct-ink)}
.help-guide .help-note{margin-top:10px;padding:9px 11px;border-radius:6px;background:var(--ct-surface-2);color:var(--ct-ink-2);font-size:13px}
.help-guide code{display:inline-block;padding:2px 5px;border-radius:4px;background:var(--ct-accent-weak);color:var(--ct-accent);font-family:Consolas,monospace;white-space:normal}
.help-guide .help-warning{margin-top:10px;padding:9px 11px;border-radius:6px;background:var(--ct-warn-weak);color:var(--ct-warn)}
.help-table{display:grid;border:1px solid var(--ct-line);border-radius:7px;overflow:hidden}
.help-table>div{display:grid;grid-template-columns:180px 1fr}
.help-table>div+div{border-top:1px solid var(--ct-line)}
.help-table b,.help-table strong,.help-table span{padding:9px 11px}
.help-table strong{color:var(--ct-ink);background:var(--ct-surface-2)}
.help-table span{border-left:1px solid var(--ct-line);color:var(--ct-ink-2);line-height:1.65}
.help-table.compact>div{grid-template-columns:160px 1fr}
.help-guide .formula-list>div{grid-template-columns:140px 1fr;padding:8px 0;border-bottom:1px dashed var(--ct-line)}
.help-guide .formula-list>div:last-child{border-bottom:0}
.formula-list dd{display:grid;gap:6px}
.formula-list code{width:fit-content}
@media(max-width:680px){.help-table>div,.help-table.compact>div{grid-template-columns:1fr}
.help-table span{border-left:0;border-top:1px solid var(--ct-line)}
.help-guide dl>div,.help-guide .formula-list>div{grid-template-columns:1fr;gap:4px}}


.tabs :deep(.el-tabs__header){margin:0;padding:0 16px;border:1px solid var(--ct-line);border-radius:8px;background:var(--ct-surface)}
.settings-sections :deep(.el-tabs__header){padding:0;border:0;border-radius:0}
.workspace-card .head{min-height:32px}
.inline-metrics span{padding:0 10px;border-left:1px solid var(--ct-line);white-space:nowrap}
.model-list>button>span:first-child{min-width:0;flex:1}
.model-status{flex-shrink:0}
.capacity-cell :deep(.el-input){width:70px;flex:none}
.capacity-cell.tpm :deep(.el-input){width:92px}
.channel-table :deep(.el-input-number){max-width:100%}
.model-nav-tools{display:flex;gap:5px;align-items:center}.model-nav-tools :deep(.el-input){min-width:0}
.model-nav-toggle{display:grid;place-items:center;flex:0 0 24px;width:24px;height:32px;padding:0;border:1px solid var(--ct-line);border-radius:4px;background:var(--ct-surface);color:var(--ct-ink-3);font-size:12px;line-height:1;cursor:pointer}
.model-nav-toggle:hover,.model-nav-rail:hover{background:var(--ct-accent-weak);color:var(--ct-accent)}.model-nav-toggle:focus-visible,.model-nav-rail:focus-visible{outline:2px solid var(--ct-line)}
.model-workspace.nav-collapsed{grid-template-columns:36px minmax(0,1fr)}.nav-collapsed .model-nav{padding:10px 5px}.model-nav-rail{border:0;background:transparent;color:var(--ct-ink-3);writing-mode:vertical-rl;letter-spacing:4px;padding:12px 5px;cursor:pointer;font-size:12px}
.evaluation-factors{display:grid;grid-template-columns:1fr 1fr;gap:2px 10px;font-size:12px;line-height:20px;color:var(--ct-ink-3);font-variant-numeric:tabular-nums}.evaluation-factors span{white-space:nowrap}.evaluation-factors b{font-weight:500;color:var(--ct-ink-2)}.evaluation-factors .factor-up{color:var(--ct-ok)}.evaluation-factors .factor-down{color:var(--ct-crit)}
.weight-calculated{display:flex;align-items:center;justify-content:center;gap:6px;font-variant-numeric:tabular-nums}.channel-table .current-weight{font-weight:400}
.speed-source{display:block;font-size:10px;color:var(--ct-ink-3);line-height:14px}
.coefficient-cell{display:flex;flex-direction:column;gap:2px;line-height:20px;font-size:12px;font-variant-numeric:tabular-nums}.coefficient-cell b{font-weight:500;color:var(--ct-ink-2)}.coefficient-cell small{font-size:10px;line-height:16px;color:var(--ct-ink-3);white-space:normal}.coefficient-cell .factor-up{color:var(--ct-ok)}.coefficient-cell .factor-down{color:var(--ct-crit)}
.channel-table :deep(td.coefficient-merged .cell){padding:0}.coefficient-values{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));align-items:start}.coefficient-values .coefficient-cell{padding:0 8px}.coefficient-overall{margin-top:5px;padding:0 10px;font-size:11px;line-height:18px;text-align:center}
.coefficient-overall.only-status{margin-top:0;text-align:center}
/* Respond to available page width, including changes to the application sidebar. */
/* The global desktop body minimum must not force this page beyond the viewport. */
:global(body:has(.tuning-shell)){min-width:0}
.tuning-shell :deep(.workspace),.tuning-shell :deep(.content){min-width:0}
.page{min-width:0;grid-template-columns:minmax(0,1fr)}
.page :deep(.el-tabs__content),.workspace-card,.model-nav{min-width:0}
.model-nav{overflow:hidden}
.compact-layout .model-workspace:not(.nav-collapsed){grid-template-columns:170px minmax(0,1fr)}
.compact-layout .head,.compact-layout .model-head{flex-wrap:wrap;gap:8px}
.compact-layout .head>div:first-child{flex-wrap:wrap}
.compact-layout .model-head>div:first-child{min-width:0;flex-wrap:wrap}
.compact-layout .channel-table :deep(.cell){padding-left:6px;padding-right:6px}
.compact-layout .channel-table :deep(td.coefficient-merged .cell){padding:0}
.compact-layout .coefficient-values .coefficient-cell{padding:0 4px}
.compact-layout .channel-table :deep(.el-input-number){width:64px}
.compact-layout .model-workspace{height:calc(100dvh - 220px);min-height:360px}
.compact-layout .channel-meta{flex-wrap:wrap;column-gap:8px;row-gap:2px}
/* Keep horizontal overflow inside the table and reserve space for its scrollbar. */
.model-head{flex-wrap:wrap;gap:8px}
.model-head>div:first-child{min-width:0;flex-wrap:wrap}
.channel-table :deep(.cell){padding-left:6px;padding-right:6px}
.channel-table :deep(.el-input-number){width:100%;max-width:68px}
.channel-table :deep(.el-scrollbar__bar.is-horizontal){height:8px;bottom:2px}
.channel-table :deep(.el-scrollbar__thumb){background:var(--ct-line-strong)}
.channel-table :deep(.el-table__body-wrapper .el-scrollbar__view){padding-bottom:10px}
.channel-meta{flex-wrap:wrap;column-gap:8px;row-gap:2px}

/* Tuning workspace: clear hierarchy without a spreadsheet-style grid. */
.workspace-card :deep(.el-card__header){padding:14px 16px}
.workspace-card :deep(.el-card__body){padding:10px}
.model-workspace{grid-template-columns:204px minmax(0,1fr);border:0;border-radius:8px;height:calc(100dvh - 198px)}
.model-nav{padding:12px 10px;background:var(--ct-surface-2)}
.model-list{gap:4px;margin-top:12px}
.model-list>button{min-width:0;box-sizing:border-box;position:relative;padding:11px 10px;border-radius:6px}
.model-list>button.active{border-color:transparent;box-shadow:inset 3px 0 var(--ct-accent)}
.model-list b{font-weight:600}
.model-mode-text{display:inline-flex;align-items:center;gap:5px}
.model-mode-text:before,.active-model-status:before{content:'';width:5px;height:5px;border-radius:50%;background:currentColor;flex-shrink:0}
.model-head{padding:16px 18px;min-height:76px;box-sizing:border-box;background:var(--ct-surface)}
.model-head>div:first-child{column-gap:10px;row-gap:6px;flex:1}
.model-head .active-model-name{font-size:20px;line-height:28px;font-weight:650;overflow-wrap:anywhere}
.active-model-status{display:inline-flex;align-items:center;gap:6px;font-size:11px;color:var(--ct-ink-3)}
.active-model-status.auto{color:var(--ct-ok)}.active-model-status.observe{color:var(--ct-warn)}
.model-head .evaluation-time{flex-basis:100%;font-size:11px;line-height:18px}
.channel-table :deep(th.el-table__cell){height:32px;background:var(--ct-surface-2);border-right:0!important;color:var(--ct-ink-2)}
.channel-table :deep(td.el-table__cell){height:64px;box-sizing:border-box;padding:10px 0;border-right:0!important}
.channel-table :deep(.el-table__border-left-patch){display:none}
.channel-table :deep(.cell){padding-left:6px;padding-right:6px}
.channel-table :deep(td:first-child .cell){padding-left:12px;padding-right:8px}
.channel-table :deep(td.coefficient-merged .cell){padding:0}
.channel-name{font-weight:500;color:var(--ct-ink);font-size:12px}
.channel-key{font-size:11px}
.channel-meta{margin-top:5px;gap:5px 12px;line-height:18px}
.coefficient-samples{display:block;margin-top:4px;text-align:center;white-space:nowrap;font-size:11px;line-height:18px;color:var(--ct-ink-3)}.coefficient-samples b{font-weight:500;color:var(--ct-ink-2)}
.group-tags{gap:4px;line-height:18px}.group-tags>span{background:var(--ct-surface-2);color:var(--ct-ink-2);border-color:transparent;border-radius:4px;padding:1px 5px}
.channel-table :deep(.el-input-number){width:100%;max-width:76px}
.channel-table :deep(.el-input__wrapper){min-height:28px;box-shadow:0 0 0 1px var(--ct-line) inset;background:var(--ct-surface-2);border-radius:5px}
.channel-table :deep(.el-input__wrapper:hover){box-shadow:0 0 0 1px var(--ct-line-strong) inset}
.channel-table :deep(.el-input__wrapper.is-focus){box-shadow:0 0 0 1px var(--ct-accent) inset;background:var(--ct-surface)}
.channel-table :deep(.modified .el-input__wrapper){box-shadow:0 0 0 1px var(--ct-warn) inset;background:var(--ct-warn-weak)}
.coefficient-cell{font-size:12px;line-height:22px}.coefficient-cell small{font-size:11px;line-height:18px}
.coefficient-overall.only-status{display:block;margin:0 12px;padding:0;background:transparent}
.channel-footer{padding:10px 14px;background:var(--ct-surface);border-top:1px solid var(--ct-line)}
@media(max-width:760px){.model-head{padding:12px}.model-head .active-model-name{font-size:17px}}

.tuning-header-actions{display:flex;align-items:center;gap:8px}.tuning-header-actions .el-button+.el-button{margin-left:0}.unsaved-status{font-size:12px;color:var(--ct-warn)}
@media(max-width:760px){.unsaved-status{display:none}}
.tuning-shell :deep(.topbar-tools){order:1}.tuning-shell :deep(.topbar .user){order:2}
.compact-layout .model-head{min-height:68px;padding:12px}.compact-layout .model-head .active-model-name{font-size:18px}

.channel-table :deep(tr.priority-group-start>td.el-table__cell){padding-top:18px}

/* Compact overview: tabs and actions share a row. */
.tuning-tabs-shell{position:relative;min-width:0;container-type:inline-size}
.overview-actions{position:absolute;right:12px;top:6px;z-index:3;gap:8px;flex-wrap:nowrap}
.tuning-tabs-shell .tabs :deep(>.el-tabs__header){min-height:44px;border-radius:8px 8px 0 0;box-sizing:border-box}
.has-overview-actions .tabs :deep(>.el-tabs__header){padding-right:350px}
.workspace-card{border-top:0;border-radius:0 0 8px 8px}
.workspace-card :deep(.el-card__body){padding:0}
.model-workspace,.compact-layout .model-workspace{height:calc(100dvh - 118px);min-height:360px;border-radius:0 0 8px 8px}
.model-mode-summary{display:flex;align-items:center;gap:10px;margin:10px 0 2px;font-size:11px;line-height:20px;color:var(--ct-ink-3);white-space:nowrap}
.model-mode-summary b{font-weight:500;color:var(--ct-ink-2)}
.model-head,.compact-layout .model-head{min-height:54px;padding:10px 16px;gap:10px}
.model-head .active-model-name,.compact-layout .model-head .active-model-name{font-size:18px;line-height:26px}
.model-head .evaluation-time{flex-basis:auto;padding-left:10px;border-left:1px solid var(--ct-line);white-space:normal}
.model-head>div:first-child{flex:1 1 460px;gap:6px 10px}
.model-head :deep(.el-radio-group){flex-shrink:0;margin-left:auto}
@container(max-width:680px){
 .overview-actions{position:static;justify-content:flex-end;padding:8px 10px;background:var(--ct-surface);border:1px solid var(--ct-line);border-bottom:0;border-radius:8px 8px 0 0;flex-wrap:wrap}
 .has-overview-actions .tabs :deep(>.el-tabs__header){padding-right:16px;border-radius:0}
 .model-workspace,.compact-layout .model-workspace{height:calc(100dvh - 168px)}
 .model-head .evaluation-time{flex-basis:100%;padding-left:0;border-left:0}
}


.coefficient-cell>b{display:inline-flex;align-items:center;justify-content:center;gap:3px;white-space:nowrap}
.output-fallback-icon{display:inline-flex;align-items:center;color:var(--ct-ink-3);cursor:help;flex-shrink:0}
.output-fallback-icon svg{width:12px;height:12px;fill:none;stroke:currentColor;stroke-width:1.3;stroke-linecap:round;stroke-linejoin:round}
.output-fallback-icon:focus-visible{outline:1px solid var(--ct-accent);outline-offset:2px;border-radius:2px}
</style>
