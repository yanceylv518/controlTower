<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { type ChannelBaseValue, type TuningContinuousState, type TuningPolicy, type TuningRecommendation } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";

const filters = useFiltersStore();
const loading = ref(false), saving = ref(false), dirty = ref(false), activeTab = ref("overview");
const mode = ref<"observe" | "confirm" | "auto">("observe");
const bases = ref<ChannelBaseValue[]>([]), states = ref<TuningContinuousState[]>([]), events = ref<TuningRecommendation[]>([]);
const selectedModels = ref<string[]>([]);
const defaults = () => ({ sensitivity: 1, otps_cap: 1.5, circuit_threshold: .1, recovery_threshold: .2, silent_minutes: 5, probe_interval_seconds: 5, probe_count: 10, soft_start_multiplier: .2, window_minutes: 15, min_samples: 20, sparse_lookback_minutes: 360 });
const policy = reactive<TuningPolicy>({ scheduling: { window_minutes: 15, min_samples: 20, sparse_min_samples: 10, sparse_lookback_minutes: 360 }, continuous: defaults(), dispatch_modes: {} });
const siteID = computed(() => filters.site_id || "");
const models = computed(() => [...new Set(bases.value.map(x => x.model_name))].sort());
const groups = computed(() => models.value.map(model => ({ model, rows: bases.value.filter(x => x.model_name === model) })).filter(x => !selectedModels.value.length || selectedModels.value.includes(x.model)));
const stateMap = computed(() => new Map(states.value.map(x => [`${x.channel_id}:${x.model_name}`, x])));
const stateFor = (row: ChannelBaseValue) => stateMap.value.get(`${row.channel_id}:${row.model_name}`);
const recentEvents = computed(() => events.value.filter(x => ["weight_write", "manual_takeover", "auto_paused", "circuit_opened", "probe_started", "probe_failed", "circuit_recovered"].includes(x.rule)));
const counts = computed(() => models.value.reduce((v, model) => { v[policy.dispatch_modes[model] || "off"]++; return v; }, { off: 0, observe: 0, auto: 0 }));
const abnormal = computed(() => states.value.filter(x => x.phase !== "normal" || x.paused_reason).length);
const factor = (value?: number) => Number(value ?? 1).toFixed(3);
const modelMode = (model: string) => policy.dispatch_modes[model] || "off";
const modeText = (model: string) => ({ off: "已关闭", observe: "只观察", auto: "自动执行" }[modelMode(model)]);
const modeType = (model: string) => modelMode(model) === "auto" ? "success" : modelMode(model) === "observe" ? "warning" : "info";
const phaseText = (s?: TuningContinuousState) => !s ? "等待首次评估" : s.paused_reason === "manual_override" ? "人工修改后已暂停" : s.paused_reason ? "安全保护已暂停" : s.phase === "circuit" ? `已熔断，下次检测 ${s.next_probe_at ? formatTime(s.next_probe_at) : "待定"}` : s.phase === "probing" ? `恢复检测 ${s.probe_attempts || 0}/${policy.continuous.probe_count}` : s.phase === "soft_start" ? "恢复中（低权重运行）" : "运行正常";
const phaseType = (s?: TuningContinuousState) => s?.phase === "circuit" ? "danger" : s?.phase === "probing" || s?.phase === "soft_start" || s?.paused_reason ? "warning" : "success";
const eventName = (rule: string) => ({ weight_write: "自动调整权重", manual_takeover: "检测到人工修改", auto_paused: "安全保护暂停", circuit_opened: "渠道熔断", probe_started: "开始恢复检测", probe_failed: "恢复检测未通过", circuit_recovered: "渠道恢复" } as Record<string, string>)[rule] || rule;
const eventCount = (days: number, rule: string) => events.value.filter(x => x.rule === rule && new Date(x.created_at).getTime() >= Date.now() - days * 86400000).length;

async function load() {
  await filters.loadInstances(); if (!siteID.value) return;
  loading.value = true;
  try {
    const [p, b, r] = await Promise.all([dashboard.tuningPolicy(siteID.value), dashboard.tuningBaseValues(siteID.value), dashboard.tuningRecommendations(siteID.value, 300)]);
    mode.value = p.mode; Object.assign(policy, p.policy); policy.continuous ||= defaults(); policy.dispatch_modes ||= {};
    bases.value = b.items; events.value = r.items; for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    try { states.value = (await dashboard.tuningContinuousStates(siteID.value)).items; } catch { states.value = []; }
    dirty.value = false;
  } finally { loading.value = false; }
}
async function sync(kind: "weight" | "priority") {
  const rows = (await dashboard.syncTuningBaseValues(siteID.value, selectedModels.value.length ? selectedModels.value : models.value)).items;
  const index = new Map(bases.value.map(x => [`${x.channel_id}:${x.model_name}`, x]));
  for (const row of rows) { const old = index.get(`${row.channel_id}:${row.model_name}`); if (!old) bases.value.push(row); else if (kind === "weight") old.base_weight = row.current_weight; else old.base_priority = row.current_priority; }
  dirty.value = true; ElMessage.warning("已读取 new-api 当前值，请保存后生效");
}
async function save() { saving.value = true; try { bases.value = (await dashboard.saveTuningBaseValues(siteID.value, bases.value)).items; await dashboard.saveTuningPolicy(siteID.value, policy, mode.value); ElMessage.success("设置已保存，将在下一分钟生效"); await load(); } finally { saving.value = false; } }
watch(() => filters.site_id, () => void load()); onMounted(() => void load());
</script>

<template><AppShell title="调权中心"><div v-loading="loading" class="page">
  <section class="hero"><div><span>当前站点</span><h2>{{ siteID || "未选择站点" }}</h2><p>系统每分钟比较同一模型的可用渠道，表现越好的渠道获得越高权重。</p></div><div class="hero-state"><i :class="{ active: counts.auto }" /><div><b>{{ counts.auto ? "自动调度运行中" : counts.observe ? "当前仅观察，不会修改线上权重" : "调度尚未开启" }}</b><small>{{ counts.auto }} 个自动执行 · {{ counts.observe }} 个只观察</small></div></div></section>
  <el-tabs v-model="activeTab" class="tabs">
    <el-tab-pane label="运行概览" name="overview">
      <div class="summary"><div><span>自动执行的模型</span><b>{{ counts.auto }}</b><small>会写入 new-api</small></div><div><span>只观察的模型</span><b>{{ counts.observe }}</b><small>只展示建议</small></div><div><span>关闭的模型</span><b>{{ counts.off }}</b><small>不参与评估</small></div><div :class="{ danger: abnormal }"><span>需要关注的渠道</span><b>{{ abnormal }}</b><small>熔断、检测或暂停</small></div></div>
      <el-alert class="guide" type="info" :closable="false" show-icon title="第一次使用：建议先选“只观察”">系统会显示准备采用的权重，但不会修改线上配置；确认结果合理后，再将单个模型切换为“自动执行”。</el-alert>
      <el-card shadow="never"><template #header><div class="head"><div><b>模型与渠道</b><small>每个模型单独选择关闭、只观察或自动执行</small></div><div class="tools"><el-select v-model="selectedModels" multiple collapse-tags placeholder="筛选模型"><el-option v-for="item in models" :key="item" :label="item" :value="item" /></el-select><el-button @click="sync('weight')">从 new-api 读取当前值</el-button><el-tag v-if="dirty" type="warning">有修改未保存</el-tag></div></div></template>
        <el-empty v-if="!groups.length" description="还没有渠道基础值"><el-button type="primary" @click="sync('weight')">立即从 new-api 读取</el-button></el-empty>
        <section v-for="group in groups" :key="group.model" class="model"><div class="model-head"><div><b>{{ group.model }}</b><el-tag :type="modeType(group.model)" effect="plain">{{ modeText(group.model) }}</el-tag></div><el-radio-group v-model="policy.dispatch_modes[group.model]" size="small" @change="dirty=true"><el-radio-button value="off">关闭</el-radio-button><el-radio-button value="observe">只观察</el-radio-button><el-radio-button value="auto">自动执行</el-radio-button></el-radio-group></div>
          <el-table :data="group.rows" size="small"><el-table-column prop="channel_name" label="渠道" min-width="170"/><el-table-column label="运行状态" min-width="190"><template #default="{row}"><el-tag :type="phaseType(stateFor(row))">{{ phaseText(stateFor(row)) }}</el-tag></template></el-table-column><el-table-column label="基础权重" width="132"><template #default="{row}"><el-input-number v-model="row.base_weight" :min="1" size="small" controls-position="right" @change="dirty=true"/></template></el-table-column><el-table-column label="系统计算结果" width="165"><template #default="{row}"><div class="proposal"><b>{{ stateFor(row)?.proposed_weight ?? "—" }}</b><el-popover v-if="stateFor(row)" trigger="hover" width="250"><template #reference><button>倍率 ×{{ factor(stateFor(row)?.multiplier) }}</button></template><div class="factors"><b>权重计算明细</b><p>响应速度 <span>×{{ factor(stateFor(row)?.k_speed) }}</span></p><p>缓存表现 <span>×{{ factor(stateFor(row)?.k_cache) }}</span></p><p>输出速度 <span>×{{ factor(stateFor(row)?.k_otps) }}</span></p><p>错误情况 <span>×{{ factor(stateFor(row)?.k_error) }}</span></p></div></el-popover><small v-if="modelMode(group.model)==='observe'">预览值，未写入</small></div></template></el-table-column><el-table-column prop="current_weight" label="线上当前权重" width="130"/><el-table-column label="基础优先级" width="132"><template #default="{row}"><el-input-number v-model="row.base_priority" :min="0" size="small" controls-position="right" @change="dirty=true"/></template></el-table-column><el-table-column prop="current_priority" label="线上当前优先级" width="140"/></el-table>
        </section>
      </el-card>
    </el-tab-pane>
    <el-tab-pane label="变更记录" name="events"><div class="summary compact"><div><span>近 7 天自动调权</span><b>{{ eventCount(7,'weight_write') }}</b></div><div><span>近 7 天熔断</span><b>{{ eventCount(7,'circuit_opened') }}</b></div><div><span>近 7 天恢复</span><b>{{ eventCount(7,'circuit_recovered') }}</b></div><div><span>近 7 天人工接管</span><b>{{ eventCount(7,'manual_takeover') }}</b></div></div><el-card shadow="never"><template #header><div class="head"><div><b>系统做过什么</b><small>自动写入、暂停、熔断和恢复都会记录</small></div></div></template><el-timeline v-if="recentEvents.length"><el-timeline-item v-for="item in recentEvents" :key="item.id" :timestamp="formatTime(item.created_at)" placement="top"><el-tag :type="item.rule==='weight_write'?'success':'warning'">{{ eventName(item.rule) }}</el-tag><strong class="channel">{{ item.channel_name }}</strong><span>权重 {{ item.current_weight }} → {{ item.proposed_weight }}</span></el-timeline-item></el-timeline><el-empty v-else description="暂无调权记录"/></el-card></el-tab-pane>
    <el-tab-pane label="规则设置" name="settings"><el-card shadow="never"><template #header><div class="head"><div><b>系统如何计算权重</b><small>通常保持默认值即可</small></div></div></template>
      <div class="flow"><div><i>1</i><b>同模型比较</b><span>只比较相同模型的渠道</span></div><div><i>2</i><b>综合评分</b><span>速度、缓存、输出与错误</span></div><div><i>3</i><b>计算权重</b><span>基础权重 × 综合倍率</span></div><div><i>4</i><b>安全执行</b><span>自动执行才写入线上</span></div></div>
      <el-form label-position="top"><div class="params"><el-form-item label="调整灵敏度"><el-input-number v-model="policy.continuous.sensitivity" :min="0" :max="5" :step=".1" @change="dirty=true"/><small>越大，表现差异影响越明显</small></el-form-item><el-form-item label="最大加权倍数"><el-input-number v-model="policy.continuous.otps_cap" :min="1" :max="5" :step=".1" @change="dirty=true"/><small>限制最多放大多少倍</small></el-form-item><el-form-item label="评估窗口（分钟）"><el-input-number v-model="policy.continuous.window_minutes" :min="1" @change="dirty=true"/><small>使用最近多久的数据</small></el-form-item><el-form-item label="最少请求数"><el-input-number v-model="policy.continuous.min_samples" :min="1" @change="dirty=true"/><small>不足时不调整</small></el-form-item><el-form-item label="低流量回看（分钟）"><el-input-number v-model="policy.continuous.sparse_lookback_minutes" :min="policy.continuous.window_minutes" @change="dirty=true"/><small>流量少时扩大观察范围</small></el-form-item></div></el-form>
      <el-collapse><el-collapse-item title="高级安全设置：熔断与自动恢复" name="safety"><p class="safety">严重异常时先停止分流，等待后用少量请求检测，通过后再以低权重恢复。</p><div class="params"><el-form-item label="熔断线"><el-input-number v-model="policy.continuous.circuit_threshold" :min="0" :max="1" :step=".01" @change="dirty=true"/></el-form-item><el-form-item label="恢复线"><el-input-number v-model="policy.continuous.recovery_threshold" :min="0" :max="1" :step=".01" @change="dirty=true"/></el-form-item><el-form-item label="暂停观察（分钟）"><el-input-number v-model="policy.continuous.silent_minutes" :min="1" @change="dirty=true"/></el-form-item><el-form-item label="检测间隔（秒）"><el-input-number v-model="policy.continuous.probe_interval_seconds" :min="1" @change="dirty=true"/></el-form-item><el-form-item label="检测次数"><el-input-number v-model="policy.continuous.probe_count" :min="1" @change="dirty=true"/></el-form-item><el-form-item label="恢复初始倍率"><el-input-number v-model="policy.continuous.soft_start_multiplier" :min=".01" :max="1" :step=".05" @change="dirty=true"/></el-form-item></div></el-collapse-item></el-collapse>
    </el-card></el-tab-pane>
  </el-tabs>
  <div v-if="dirty" class="save"><span>你有尚未保存的修改</span><el-button type="primary" :loading="saving" @click="save">保存并在下一分钟生效</el-button></div>
</div></AppShell></template>

<style scoped>
.page{display:grid;gap:14px;padding-bottom:70px}.hero{display:flex;justify-content:space-between;align-items:center;gap:20px;padding:20px 24px;border:1px solid #dfe7f3;border-radius:10px;background:linear-gradient(120deg,#fff,#f3f7ff)}.hero span,.hero p,.head small,.hero-state small,.summary small,.proposal small{color:#8491a5}.hero h2{margin:4px 0}.hero p{margin:0}.hero-state{display:flex;align-items:center;gap:12px;min-width:300px;padding:12px 16px;border-radius:8px;background:#fff}.hero-state div{display:flex;flex-direction:column;gap:4px}.hero-state i{width:10px;height:10px;border-radius:50%;background:#f3a326;box-shadow:0 0 0 5px #fdf1db}.hero-state i.active{background:#21a675;box-shadow:0 0 0 5px #dff5ec}.tabs :deep(.el-tabs__header){margin:0;padding:0 16px;border:1px solid #e1e8f2;border-radius:8px;background:#fff}.summary{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin:14px 0}.summary>div{display:flex;flex-direction:column;gap:6px;padding:16px 18px;border:1px solid #e1e8f2;border-radius:8px;background:#fff}.summary b{font-size:26px}.summary .danger b{color:#d84a4a}.summary.compact b{font-size:22px}.guide{margin-bottom:14px}.head,.model-head,.tools{display:flex;align-items:center;justify-content:space-between;gap:12px}.head>div:first-child{display:flex;flex-direction:column;gap:5px}.tools .el-select{width:240px}.model{margin-top:14px;border:1px solid #e2e8f2;border-radius:8px;overflow:hidden}.model-head{padding:11px 12px;background:#f7f9fc}.model-head>div{display:flex;align-items:center;gap:10px}.proposal{display:flex;align-items:center;gap:6px;flex-wrap:wrap}.proposal>b{font-size:18px;color:#245eea}.proposal button{padding:0;border:0;background:none;color:#4e73b8;cursor:pointer}.proposal small{flex-basis:100%}.factors p{display:flex;justify-content:space-between}.channel{margin:0 12px}.flow{display:grid;grid-template-columns:repeat(4,1fr);gap:10px;margin-bottom:20px}.flow>div{display:grid;grid-template-columns:30px 1fr;gap:3px 8px;padding:14px;border:1px solid #dfe7f3;border-radius:8px;background:#f8faff}.flow i{grid-row:1/3;display:grid;place-items:center;width:28px;height:28px;border-radius:50%;background:#3168e8;color:#fff;font-style:normal}.flow span,.params small{font-size:12px;color:#657086}.params{display:grid;grid-template-columns:repeat(5,minmax(150px,1fr));gap:0 16px}.params :deep(.el-form-item__content){display:flex;flex-direction:column;align-items:flex-start}.safety{padding:10px 12px;border-radius:6px;background:#fff7e8;color:#76551b}.save{position:fixed;z-index:20;right:24px;bottom:18px;display:flex;align-items:center;gap:18px;padding:10px 12px 10px 18px;border:1px solid #f0d59b;border-radius:8px;background:#fffaf0;box-shadow:0 8px 28px #17233b24}@media(max-width:1200px){.summary,.flow{grid-template-columns:1fr 1fr}.params{grid-template-columns:repeat(3,1fr)}.hero{align-items:flex-start;flex-direction:column}.hero-state{width:100%}}
</style>
