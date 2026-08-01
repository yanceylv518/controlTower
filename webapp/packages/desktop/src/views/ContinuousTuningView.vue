<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { ElMessage } from "element-plus";
import { siteOf, type ChannelBaseValue, type TuningContinuousState, type TuningPolicy, type TuningRecommendation } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";

const filters = useFiltersStore();
const loading = ref(false), saving = ref(false), dirty = ref(false);
const mode = ref<"observe" | "confirm" | "auto">("observe");
const bases = ref<ChannelBaseValue[]>([]), states = ref<TuningContinuousState[]>([]), events = ref<TuningRecommendation[]>([]);
const selectedModels = ref<string[]>([]);
const continuousDefaults = () => ({ sensitivity: 1, otps_cap: 1.5, circuit_threshold: .1, recovery_threshold: .2, silent_minutes: 5, probe_interval_seconds: 5, probe_count: 10, soft_start_multiplier: .2, window_minutes: 15, min_samples: 20, sparse_lookback_minutes: 360 });
const policy = reactive<TuningPolicy>({
  scheduling: { window_minutes: 15, min_samples: 20, sparse_min_samples: 10, sparse_lookback_minutes: 360, trial_initial_minutes: 60, trial_backoff_factor: 2, trial_max_minutes: 1440, trial_windows: 2, cooldown_minutes: 10, daily_action_limit: 6 },
  dynamic_weighting: { mode: "off", ttft_influence: .5, error_influence: .3, cache_influence: .1, otps_influence: .1, min_multiplier: .5, max_multiplier: 1.5, smoothing_alpha: .3, max_increase_per_round: .2, max_decrease_per_round: .3 },
  criteria: [], continuous: continuousDefaults(), assignments: {}, dispatch_modes: {},
});
const instanceID = computed(() => filters.instances.filter(x => x.enabled && siteOf(x) === filters.site_id).map(x => x.instance_id).sort()[0] || "");
const models = computed(() => [...new Set(bases.value.map(x => x.model_name))].sort());
const groups = computed(() => models.value.map(model => ({ model, rows: bases.value.filter(x => x.model_name === model) })));
const stateMap = computed(() => new Map(states.value.map(x => [`${x.channel_id}:${x.model_name}`, x])));
const recentEvents = computed(() => events.value.filter(x => ["weight_write", "manual_takeover", "auto_paused"].includes(x.rule)));
const stateFor = (row: ChannelBaseValue) => stateMap.value.get(`${row.channel_id}:${row.model_name}`);
const factor = (value?: number) => Number(value ?? 1).toFixed(3);
const eventName = (rule: string) => ({ weight_write: "自动写权", manual_takeover: "人工接管", auto_paused: "自动暂停" } as Record<string, string>)[rule] || rule;
const eventCount = (days: number, rule: string) => events.value.filter(x => x.rule === rule && new Date(x.created_at).getTime() >= Date.now() - days * 86400000).length;

async function load() {
  await filters.loadInstances(); if (!instanceID.value) return;
  loading.value = true;
  try {
    const [p, b, r] = await Promise.all([dashboard.tuningPolicy(instanceID.value), dashboard.tuningBaseValues(instanceID.value), dashboard.tuningRecommendations(instanceID.value, 300)]);
    mode.value = p.mode; Object.assign(policy, p.policy); policy.continuous ||= continuousDefaults(); policy.dispatch_modes ||= {};
    bases.value = b.items; events.value = r.items;
    for (const model of models.value) policy.dispatch_modes[model] ||= "off";
    try { states.value = (await dashboard.tuningContinuousStates(instanceID.value)).items; } catch { states.value = []; }
    dirty.value = false;
  } finally { loading.value = false; }
}
async function sync(kind: "weight" | "priority") {
  const rows = (await dashboard.syncTuningBaseValues(instanceID.value, selectedModels.value.length ? selectedModels.value : models.value)).items;
  const index = new Map(bases.value.map(x => [`${x.channel_id}:${x.model_name}`, x]));
  for (const row of rows) { const old = index.get(`${row.channel_id}:${row.model_name}`); if (!old) bases.value.push(row); else if (kind === "weight") old.base_weight = row.current_weight; else old.base_priority = row.current_priority; }
  dirty.value = true; ElMessage.warning("已从 new-api 回填，请保存后生效");
}
async function save() {
  saving.value = true;
  try { bases.value = (await dashboard.saveTuningBaseValues(instanceID.value, bases.value)).items; await dashboard.saveTuningPolicy(instanceID.value, policy, mode.value); ElMessage.success("连续调度配置已保存，将在下一分钟评估生效"); await load(); }
  finally { saving.value = false; }
}
watch(() => filters.site_id, () => void load()); onMounted(() => void load());
</script>

<template>
  <AppShell title="调权中心"><div v-loading="loading" class="page">
    <el-alert type="info" :closable="false" show-icon title="连续调度 v3.0">每分钟按同模型渠道相对表现计算：实时权重 = 基础权重 × 综合倍率。观察只记录建议，自动才写入 new-api。</el-alert>
    <el-card shadow="never"><template #header><div class="title"><strong>1. 渠道总览与基础值</strong><span>当前实例：{{ instanceID || "无" }}</span></div></template>
      <div class="toolbar"><el-select v-model="selectedModels" multiple collapse-tags placeholder="全部模型"><el-option v-for="model in models" :key="model" :label="model" :value="model" /></el-select><el-button @click="sync('weight')">同步当前权重</el-button><el-button @click="sync('priority')">同步当前优先级</el-button><el-tag v-if="dirty" type="warning">有未保存修改</el-tag></div>
      <el-empty v-if="!groups.length" description="暂无基础值，请先同步渠道" :image-size="52" />
      <section v-for="group in groups" :key="group.model" class="model"><div class="model-head"><strong>{{ group.model }}</strong><el-radio-group v-model="policy.dispatch_modes[group.model]" size="small" @change="dirty=true"><el-radio-button value="off">关闭</el-radio-button><el-radio-button value="observe">观察</el-radio-button><el-radio-button value="auto">自动</el-radio-button></el-radio-group></div>
        <el-table :data="group.rows" size="small"><el-table-column prop="channel_name" label="渠道" min-width="150" /><el-table-column label="当前 / 建议" width="125"><template #default="{row}">{{ row.current_weight }} / <b>{{ stateFor(row)?.proposed_weight ?? "-" }}</b></template></el-table-column><el-table-column label="基础权重" width="140"><template #default="{row}"><el-input-number v-model="row.base_weight" :min="1" size="small" @change="dirty=true" /></template></el-table-column><el-table-column label="基础优先级" width="140"><template #default="{row}"><el-input-number v-model="row.base_priority" :min="0" size="small" @change="dirty=true" /></template></el-table-column><el-table-column label="速度" width="74"><template #default="{row}">{{ factor(stateFor(row)?.k_speed) }}</template></el-table-column><el-table-column label="缓存" width="74"><template #default="{row}">{{ factor(stateFor(row)?.k_cache) }}</template></el-table-column><el-table-column label="OTPS" width="74"><template #default="{row}">{{ factor(stateFor(row)?.k_otps) }}</template></el-table-column><el-table-column label="错误" width="74"><template #default="{row}">{{ factor(stateFor(row)?.k_error) }}</template></el-table-column><el-table-column label="综合倍率" width="90"><template #default="{row}"><b>{{ factor(stateFor(row)?.multiplier) }}</b></template></el-table-column><el-table-column label="状态" width="110"><template #default="{row}"><el-tag :type="stateFor(row)?.paused_reason ? 'warning':'success'">{{ stateFor(row)?.paused_reason || "评估中" }}</el-tag></template></el-table-column></el-table>
      </section>
    </el-card>
    <el-card shadow="never"><template #header><strong>2. 连续调度参数</strong></template>
      <el-alert class="formula" type="success" :closable="false">M = K速度 × K缓存 × K_OTPS × K错误；最终权重 = 基础权重 × M。非熔断渠道最低权重为 1，倍率最高 {{ policy.continuous.otps_cap }}。</el-alert>
      <el-form label-position="top"><div class="params"><el-form-item label="灵敏度 λ"><el-input-number v-model="policy.continuous.sensitivity" :min="0" :max="5" :step=".1" @change="dirty=true" /></el-form-item><el-form-item label="倍率上限"><el-input-number v-model="policy.continuous.otps_cap" :min="1" :max="5" :step=".1" @change="dirty=true" /></el-form-item><el-form-item label="评估窗口（分钟）"><el-input-number v-model="policy.continuous.window_minutes" :min="1" @change="dirty=true" /></el-form-item><el-form-item label="最少样本"><el-input-number v-model="policy.continuous.min_samples" :min="1" @change="dirty=true" /></el-form-item><el-form-item label="稀疏回看（分钟）"><el-input-number v-model="policy.continuous.sparse_lookback_minutes" :min="policy.continuous.window_minutes" @change="dirty=true" /></el-form-item></div></el-form>
      <div class="flow"><div><b>① 同模型基线</b><span>至少两个有效渠道才比较。</span></div><div><b>② 四因子评分</b><span>速度、缓存、OTPS、错误共同作用。</span></div><div><b>③ 生成权重</b><span>乘基础权重、取整并限制上限。</span></div><div><b>④ 安全执行</b><span>仅自动模式写入；人工改权后暂停。</span></div></div>
      <el-collapse><el-collapse-item title="熔断与恢复参数（B3 生效）" name="b3"><div class="params"><el-form-item label="熔断阈值"><el-input-number v-model="policy.continuous.circuit_threshold" :min="0" :max="1" :step=".01" /></el-form-item><el-form-item label="恢复阈值"><el-input-number v-model="policy.continuous.recovery_threshold" :min="0" :max="1" :step=".01" /></el-form-item><el-form-item label="静默期（分钟）"><el-input-number v-model="policy.continuous.silent_minutes" :min="1" /></el-form-item><el-form-item label="探测间隔（秒）"><el-input-number v-model="policy.continuous.probe_interval_seconds" :min="1" /></el-form-item><el-form-item label="探测次数"><el-input-number v-model="policy.continuous.probe_count" :min="1" /></el-form-item><el-form-item label="软启动倍率"><el-input-number v-model="policy.continuous.soft_start_multiplier" :min=".01" :max="1" :step=".05" /></el-form-item></div></el-collapse-item></el-collapse>
      <el-button class="save" type="primary" :loading="saving" :disabled="!instanceID" @click="save">保存连续调度配置</el-button>
    </el-card>
    <el-card shadow="never"><template #header><strong>3. 调权事件</strong></template><el-timeline v-if="recentEvents.length"><el-timeline-item v-for="item in recentEvents" :key="item.id" :timestamp="formatTime(item.created_at)" placement="top"><el-tag :type="item.rule==='weight_write'?'success':'warning'">{{ eventName(item.rule) }}</el-tag><strong class="channel">{{ item.channel_name }}</strong><span>权重 {{ item.current_weight }} → {{ item.proposed_weight }}</span></el-timeline-item></el-timeline><el-empty v-else description="暂无连续调度事件" :image-size="52" /></el-card>
    <el-card shadow="never"><template #header><strong>4. 运行统计</strong></template><div class="stats"><div v-for="days in [7,30]" :key="days"><h3>最近 {{ days }} 天</h3><p><span>自动写权</span><b>{{ eventCount(days,'weight_write') }}</b></p><p><span>人工接管</span><b>{{ eventCount(days,'manual_takeover') }}</b></p><p><span>自动暂停</span><b>{{ eventCount(days,'auto_paused') }}</b></p><p><span>熔断 / 恢复探测</span><b>0 / 0</b></p></div></div></el-card>
    <el-card shadow="never"><template #header><strong>5. 运维说明</strong></template><el-descriptions :column="1" border><el-descriptions-item label="上线顺序">先按模型开启观察，确认建议合理和 Agent 指令链路正常后，再切换自动。</el-descriptions-item><el-descriptions-item label="人工接管">new-api 中人工修改权重后，系统暂停该渠道自动写权并记录事件。</el-descriptions-item><el-descriptions-item label="安全哨兵">过期或长期未完成命令会将自动模式降为观察。</el-descriptions-item><el-descriptions-item label="当前边界">B2 只连续配权；熔断、探测和软启动在 B3 启用。</el-descriptions-item></el-descriptions></el-card>
  </div></AppShell>
</template>
<style scoped>
.page{display:grid;gap:16px}.title,.model-head,.toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px}.title span{font-size:12px;color:#8491a5}.toolbar{justify-content:flex-start;margin-bottom:12px}.toolbar .el-select{width:300px}.model{margin-top:12px;border:1px solid #e2e8f2;border-radius:8px;overflow:hidden}.model-head{padding:10px 12px;background:#f7f9fc}.formula{margin-bottom:16px}.params{display:grid;grid-template-columns:repeat(5,minmax(150px,1fr));gap:0 16px}.flow{display:grid;grid-template-columns:repeat(4,1fr);gap:10px;margin:0 0 16px}.flow div{display:flex;flex-direction:column;gap:6px;padding:12px;border:1px solid #dfe7f3;border-radius:8px;background:#f8faff}.flow span{font-size:12px;color:#657086}.save{margin-top:16px}.channel{margin:0 12px}.stats{display:grid;grid-template-columns:1fr 1fr;gap:16px}.stats>div{padding:14px 18px;background:#f7f9fc;border-radius:8px}.stats h3{margin:0 0 10px}.stats p{display:flex;justify-content:space-between}.stats b{font-size:18px}@media(max-width:1200px){.params{grid-template-columns:repeat(3,1fr)}.flow{grid-template-columns:1fr 1fr}}
</style>
