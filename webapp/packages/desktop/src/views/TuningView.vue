<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { siteOf, type ChannelBaseValue, type TuningPolicy, type TuningRecommendation, type TuningReport, type TuningLadders } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";

const filters = useFiltersStore();
const loading = ref(false);
const mode = ref<"observe" | "confirm" | "auto">("observe");
const policy = reactive<TuningPolicy>({
  scheduling: {
    window_minutes: 15, min_samples: 20,
    sparse_min_samples: 10, sparse_lookback_minutes: 360,
    trial_initial_minutes: 60,
    trial_backoff_factor: 2, trial_max_minutes: 1440, trial_windows: 2,
    cooldown_minutes: 10, daily_action_limit: 6,
  },
  dynamic_weighting: {
    mode: "observe", ttft_influence: .5, error_influence: .3,
    cache_influence: .1, otps_influence: .1,
    min_multiplier: .5, max_multiplier: 1.5, smoothing_alpha: .3,
    max_increase_per_round: .2, max_decrease_per_round: .3,
  },
  criteria: [{
    name: "default", error_rate_threshold: .15, severe_threshold: .5,
    latency_multiplier: 2, latency_floor_seconds: 10, sustained_windows: 2,
  }],
  assignments: {},
  dispatch_modes: {},
});
const baseValues = ref<ChannelBaseValue[]>([]);
const selectedModels = ref<string[]>([]);
const baseValuesDirty = ref(false);
const availableModels = computed(() => [...new Set(ladders.value.channels.flatMap(x => x.Models))].sort());
const groupedBaseValues = computed(() => {
  const groups = new Map<string, ChannelBaseValue[]>();
  baseValues.value.forEach(row => { const list=groups.get(row.model_name)||[]; list.push(row); groups.set(row.model_name,list); });
  return [...groups.entries()].map(([model, rows]) => ({ model, rows }));
});
const recommendations = ref<TuningRecommendation[]>([]);
const weightRecommendations = ref<TuningRecommendation[]>([]);
const reports = reactive<Record<7 | 30, TuningReport | null>>({ 7: null, 30: null });
const reportDays = ref<7 | 30>(7);
const ladders = ref<TuningLadders>({ channels: [], dispatch_states: [] });
const instanceID = computed(() => filters.instances
  .filter(x => x.enabled && siteOf(x) === filters.site_id)
  .map(x => x.instance_id).sort()[0] || "");
const stateByChannel = computed(() => new Map(ladders.value.dispatch_states.map(x => [x.ChannelID, x])));
const latestWeightByChannel = computed(() => {
  const result = new Map<number, TuningRecommendation>();
  const maxAgeMs = policy.scheduling.window_minutes * 2 * 60_000;
  const now = Date.now();
  for (const item of weightRecommendations.value) {
    if (result.has(item.channel_id) || now - new Date(item.created_at).getTime() > maxAgeMs) continue;
    result.set(item.channel_id, item);
  }
  return result;
});
function weightTip(channelID: number) {
  const item = latestWeightByChannel.value.get(channelID);
  if (!item) return "";
  return `建议时间 ${formatTime(item.created_at)}；原始倍率 ${Number(item.evidence.raw_multiplier || 1).toFixed(2)}，保护后倍率 ${Number(item.evidence.protected_multiplier || 1).toFixed(2)}`;
}
const groupedLadders = computed(() => {
  const groups = new Map<string, typeof ladders.value.channels>();
  ladders.value.channels.forEach(channel => channel.Models.forEach(model => {
    const list = groups.get(model) || []; list.push(channel); groups.set(model, list);
  }));
  return [...groups.entries()].map(([model, channels]) => ({ model, channels: channels.sort((a, b) => b.Priority - a.Priority) }));
});
const ruleText: Record<string, string> = { demote: "降级", trial: "恢复验证", rebalance: "动态配权", no_backup: "无备岗", ladder_exhausted: "梯队用尽", mixed_channel: "混布", auto_paused: "自动暂停" };

async function load() {
  await filters.loadInstances();
  if (!instanceID.value) return;
  loading.value = true;
  try {
    const [p, recs, weightRecs, ladder, r7, r30, bases] = await Promise.all([
      dashboard.tuningPolicy(instanceID.value), dashboard.tuningRecommendations(instanceID.value),
      dashboard.tuningRecommendations(instanceID.value, 100, "rebalance"),
      dashboard.tuningLadders(instanceID.value), dashboard.tuningReport(instanceID.value, 7),
      dashboard.tuningReport(instanceID.value, 30),
      dashboard.tuningBaseValues(instanceID.value),
    ]);
    mode.value = p.mode; Object.assign(policy, p.policy); policy.dispatch_modes ||= {};
    recommendations.value = recs.items; weightRecommendations.value = weightRecs.items;
    ladders.value = ladder; reports[7] = r7; reports[30] = r30;
    baseValues.value = bases.items;
    baseValues.value.forEach(row => { policy.dispatch_modes[row.model_name] ||= "off"; });
    baseValuesDirty.value = false;
  } finally { loading.value = false; }
}
async function syncBaseValues(kind: "weight" | "priority") {
  const models = selectedModels.value.length ? selectedModels.value : availableModels.value;
  const synced = (await dashboard.syncTuningBaseValues(instanceID.value, models)).items;
  const existing = new Map(baseValues.value.map(x => [`${x.channel_id}:${x.model_name}`, x]));
  synced.forEach(row => {
    policy.dispatch_modes[row.model_name] ||= "off";
    const old = existing.get(`${row.channel_id}:${row.model_name}`);
    if (old) {
      if (kind === "weight") old.base_weight = row.current_weight;
      else old.base_priority = row.current_priority;
    } else baseValues.value.push(row);
  });
  baseValuesDirty.value = true;
  ElMessage.warning("已从 new-api 回填到表单，尚未保存");
}
async function saveBaseValues() {
  baseValues.value = (await dashboard.saveTuningBaseValues(instanceID.value, baseValues.value)).items;
  baseValuesDirty.value = false;
  await dashboard.saveTuningPolicy(instanceID.value, policy, mode.value);
  ElMessage.success("基础值与各模型调度模式已保存");
}
async function save() {
  await dashboard.saveTuningPolicy(instanceID.value, policy, mode.value);
  ElMessage.success("策略已保存，将在下一轮评估生效");
}
async function act(item: TuningRecommendation, action: "adopt" | "dismiss") {
  if (action === "adopt") {
    const change = item.rule === "rebalance"
      ? `权重从 ${item.current_weight} 调整为 ${item.proposed_weight}`
      : `优先级从 ${item.current_priority} 调整为 ${item.proposed_priority}`;
    await ElMessageBox.confirm(
      `将渠道 ${item.channel_name} 的${change}，并创建渠道指令。`,
      "确认采纳建议", { type: "warning" },
    );
  }
  await dashboard.tuningRecommendationAction(item.id, action);
  ElMessage.success(action === "adopt" ? "已采纳并创建渠道指令" : "已忽略");
  await load();
}
function evidence(item: TuningRecommendation) {
  const e = item.evidence;
  if (item.rule === "rebalance") return `样本 ${e.samples ?? "-"}，TTFT P95 ${Number(e.ttft_p95 || 0).toFixed(2)}s，错误率 ${(Number(e.error_rate || 0) * 100).toFixed(1)}%，缓存命中 ${(Number(e.cache_hit_rate || 0) * 100).toFixed(1)}%，OTPS ${Number(e.otps || 0).toFixed(1)}；原始倍率 ${Number(e.raw_multiplier || 1).toFixed(2)}，保护后 ${Number(e.protected_multiplier || 1).toFixed(2)}`;
  if (item.rule === "demote") return `样本 ${e.samples ?? "-"}，错误率 ${(Number(e.error_rate || 0) * 100).toFixed(1)}%，P95 ${e.p95 ?? "-"}s`;
  if (item.rule === "trial") return `第 ${Number(e.trial_attempts || 0) + 1} 次恢复验证`;
  return String(e.model || (Array.isArray(e.models) ? e.models.join("、") : "信息建议"));
}
watch(() => filters.site_id, () => void load());
onMounted(() => void load());
</script>

<template>
  <AppShell title="调权中心">
    <div v-loading="loading" class="tuning-page">
      <el-card shadow="never">
        <template #header><strong>模式与策略</strong><span class="hint">　当前采集实例：{{ instanceID || "无" }}</span></template>
        <section class="base-values-section" :class="{ 'is-dirty': baseValuesDirty }">
          <div class="base-values-title">
            <div><h3>v3.0 基础值配置</h3><p class="hint">基础权重和基础优先级是连续调度的锚点。当前批次只保存配置，v3.0-B2 上线前不会改变现有调度引擎。</p></div>
            <el-tag v-if="baseValuesDirty" type="warning">有未保存修改</el-tag>
          </div>
          <el-alert type="info" :closable="false" show-icon title="各模型调度模式">
            关闭：不参与 v3.0；观察：计算拟写值但不修改 new-api；自动：自动写入 new-api。开关将在 B2 引擎上线后生效。
          </el-alert>
          <div class="base-toolbar">
            <el-select v-model="selectedModels" multiple collapse-tags placeholder="选择模型；留空表示全部">
              <el-option v-for="model in availableModels" :key="model" :label="model" :value="model" />
            </el-select>
            <el-button @click="syncBaseValues('weight')">一键同步权重</el-button>
            <el-button @click="syncBaseValues('priority')">一键同步优先级</el-button>
            <el-button type="primary" :disabled="!baseValuesDirty" @click="saveBaseValues">保存基础值</el-button>
          </div>
          <el-empty v-if="!groupedBaseValues.length" description="尚未建立基础值，请先选择模型并执行一键同步" :image-size="48" />
          <div v-for="group in groupedBaseValues" :key="group.model" class="base-model">
            <div class="base-model-header"><strong>{{ group.model }}</strong>
              <el-radio-group v-model="policy.dispatch_modes[group.model]" size="small" @change="baseValuesDirty = true">
                <el-radio-button value="off">关闭</el-radio-button><el-radio-button value="observe">观察</el-radio-button><el-radio-button value="auto">自动</el-radio-button>
              </el-radio-group>
            </div>
            <el-table :data="group.rows" size="small">
              <el-table-column prop="channel_name" label="渠道" min-width="180" />
              <el-table-column label="当前权重" width="100"><template #default="{ row }">{{ row.current_weight }}</template></el-table-column>
              <el-table-column label="基础权重" width="170"><template #default="{ row }"><el-input-number v-model="row.base_weight" :min="0" size="small" @change="baseValuesDirty = true" /></template></el-table-column>
              <el-table-column label="当前优先级" width="110"><template #default="{ row }">{{ row.current_priority }}</template></el-table-column>
              <el-table-column label="基础优先级" width="170"><template #default="{ row }"><el-input-number v-model="row.base_priority" :min="0" size="small" @change="baseValuesDirty = true" /></template></el-table-column>
            </el-table>
          </div>
        </section>
        <el-form label-position="top">
          <el-form-item label="运行模式"><el-radio-group v-model="mode"><el-radio-button value="observe">观察</el-radio-button><el-radio-button value="confirm">人工确认</el-radio-button><el-radio-button value="auto">自动</el-radio-button></el-radio-group></el-form-item>
          <section class="policy-section">
            <h3>健康渠道动态配权</h3>
            <p class="hint">只比较同一模型、同一优先级且样本充足的健康渠道；配权模式独立决定关闭评估、只记录建议或自动写入 new-api。</p>
            <el-alert class="criteria-summary" type="info" :closable="false" show-icon title="计算与保护顺序">
              先按请求量计算同模型基线，再综合 TTFT P95、服务端错误率、缓存命中率和 OTPS；随后执行倍率上下限、平滑和单轮涨跌幅保护。客户端参数错误不会计入服务端错误率。
            </el-alert>
            <div class="policy-grid">
              <el-form-item class="weighting-mode" label="配权模式">
                <el-radio-group v-model="policy.dynamic_weighting.mode">
                  <el-radio-button value="off">关闭</el-radio-button>
                  <el-radio-button value="observe">观察</el-radio-button>
                  <el-tooltip content="建议生成后立即自动执行，并保留命令与审计记录以便回滚" placement="top">
                    <el-radio-button value="auto">自动</el-radio-button>
                  </el-tooltip>
                </el-radio-group>
                <span class="hint">独立于上方运行模式，仅控制动态配权。</span>
              </el-form-item>
              <el-form-item label="TTFT 影响"><el-input-number v-model="policy.dynamic_weighting.ttft_influence" :min="0" :max="1" :step=".05" /></el-form-item>
              <el-form-item label="错误率影响"><el-input-number v-model="policy.dynamic_weighting.error_influence" :min="0" :max="1" :step=".05" /></el-form-item>
              <el-form-item label="缓存影响"><el-input-number v-model="policy.dynamic_weighting.cache_influence" :min="0" :max="1" :step=".05" /></el-form-item>
              <el-form-item label="OTPS 影响"><el-input-number v-model="policy.dynamic_weighting.otps_influence" :min="0" :max="1" :step=".05" /></el-form-item>
              <el-form-item label="最低倍率"><el-input-number v-model="policy.dynamic_weighting.min_multiplier" :min=".1" :max="1" :step=".1" /></el-form-item>
              <el-form-item label="最高倍率"><el-input-number v-model="policy.dynamic_weighting.max_multiplier" :min="1" :max="5" :step=".1" /></el-form-item>
              <el-form-item label="平滑系数"><el-input-number v-model="policy.dynamic_weighting.smoothing_alpha" :min=".05" :max="1" :step=".05" /></el-form-item>
              <el-form-item label="单轮最多上调"><el-input-number v-model="policy.dynamic_weighting.max_increase_per_round" :min=".01" :max="1" :step=".05" /></el-form-item>
              <el-form-item label="单轮最多下调"><el-input-number v-model="policy.dynamic_weighting.max_decrease_per_round" :min=".01" :max="1" :step=".05" /></el-form-item>
            </div>
          </section>
          <section class="policy-section">
            <h3>调度参数</h3>
            <p class="hint">控制评估频率、恢复验证和动作节奏，不决定何时触发降级。</p>
            <el-alert
              class="criteria-summary"
              type="info"
              :closable="false"
              show-icon
              title="低流量渠道回退规则"
            >
              低流量渠道回退用最近 {{ policy.scheduling.sparse_min_samples }} 条请求统计，
              最多回看 {{ policy.scheduling.sparse_lookback_minutes }} 分钟，仅作用于错误率与恢复验证；
              不参与延迟判定和动态配权。当前评估窗口必须有新请求，避免旧数据重复触发。
            </el-alert>
            <div class="dispatch-flow">
              <div class="flow-step">
                <span class="step-number">1</span>
                <div><b>触发降级</b><p>兼容调度引擎判定渠道异常后，将其优先级降到同模型梯队末尾，由下一可用渠道接管流量。</p></div>
              </div>
              <div class="flow-step">
                <span class="step-number">2</span>
                <div><b>等待恢复</b><p>渠道保持降级，首次等待 {{ policy.scheduling.trial_initial_minutes }} 分钟；冷却期和每日动作上限仍然有效。</p></div>
              </div>
              <div class="flow-step">
                <span class="step-number">3</span>
                <div><b>恢复验证</b><p>临时恢复原优先级并接收真实流量，连续观察 {{ policy.scheduling.trial_windows }} 个评估窗口，确认是否已经恢复。</p></div>
              </div>
              <div class="flow-step">
                <span class="step-number">4</span>
                <div><b>确认或再次降级</b><p>连续健康则保留原优先级；仍异常则再次移到梯队末尾，下次等待按 {{ policy.scheduling.trial_backoff_factor }} 倍延长，最长 {{ policy.scheduling.trial_max_minutes }} 分钟。</p></div>
              </div>
            </div>
            <el-alert class="mode-effect" type="warning" :closable="false" show-icon>
              <template #title>当前运行模式如何执行</template>
              <span v-if="mode === 'observe'">观察模式只生成建议和模拟状态，不会实际修改渠道优先级或权重。</span>
              <span v-else-if="mode === 'confirm'">人工确认模式会生成待处理建议；只有点击“采纳”后，才会创建渠道调权指令。</span>
              <span v-else>自动模式会在达到降级或恢复验证条件时自动创建渠道指令；动态配权是否自动执行由上方“配权模式”单独决定。</span>
            </el-alert>
            <div class="policy-grid">
              <el-form-item label="评估窗口（分钟）"><el-input-number v-model="policy.scheduling.window_minutes" :min="1" /></el-form-item>
              <el-form-item label="最少样本"><el-input-number v-model="policy.scheduling.min_samples" :min="1" /></el-form-item>
              <el-form-item label="稀疏统计最少样本"><el-input-number v-model="policy.scheduling.sparse_min_samples" :min="1" :max="policy.scheduling.min_samples" /></el-form-item>
              <el-form-item label="稀疏统计回看（分钟）"><el-input-number v-model="policy.scheduling.sparse_lookback_minutes" :min="policy.scheduling.window_minutes" :max="2880" /></el-form-item>
              <el-form-item label="首次恢复验证等待（分钟）"><el-input-number v-model="policy.scheduling.trial_initial_minutes" :min="1" /></el-form-item>
              <el-form-item label="失败退避倍数"><el-input-number v-model="policy.scheduling.trial_backoff_factor" :min="1" :step=".1" /></el-form-item>
              <el-form-item label="最大验证等待（分钟）"><el-input-number v-model="policy.scheduling.trial_max_minutes" :min="1" /></el-form-item>
              <el-form-item label="恢复验证观察窗口"><el-input-number v-model="policy.scheduling.trial_windows" :min="1" /></el-form-item>
              <el-form-item label="冷却（分钟）"><el-input-number v-model="policy.scheduling.cooldown_minutes" :min="1" /></el-form-item>
              <el-form-item label="每日动作上限"><el-input-number v-model="policy.scheduling.daily_action_limit" :min="1" /></el-form-item>
            </div>
          </section>
          <el-button type="primary" :disabled="!instanceID" @click="save">保存策略</el-button>
        </el-form>
      </el-card>

      <el-card shadow="never"><template #header><strong>梯队总览</strong></template>
        <el-empty v-if="!groupedLadders.length" description="暂无单模型渠道快照" :image-size="50" />
        <div v-else class="ladder-grid"><div v-for="group in groupedLadders" :key="group.model" class="ladder"><strong>{{ group.model }}</strong>
          <div v-for="channel in group.channels" :key="channel.ID" class="ladder-row"><span>{{ channel.Name }}</span><code>P{{ channel.Priority }}</code>
            <el-tooltip :disabled="!latestWeightByChannel.has(channel.ID)" :content="weightTip(channel.ID)" placement="top">
              <span class="weight-value">权重 {{ channel.Weight }}<template v-if="latestWeightByChannel.has(channel.ID)"> → {{ latestWeightByChannel.get(channel.ID)!.proposed_weight }}</template></span>
            </el-tooltip>
            <el-tag v-if="stateByChannel.get(channel.ID)?.NextTrialAt" type="warning">降级中 · {{ formatTime(stateByChannel.get(channel.ID)!.NextTrialAt!) }}</el-tag>
            <el-tag v-else-if="stateByChannel.has(channel.ID)" type="success">恢复验证中</el-tag><el-tag v-else>在岗</el-tag>
          </div></div></div>
      </el-card>

      <el-card shadow="never"><template #header><strong>建议流水</strong></template>
        <el-timeline v-if="recommendations.length"><el-timeline-item v-for="item in recommendations" :key="item.id" :timestamp="formatTime(item.created_at)" placement="top">
          <div class="recommendation"><div><el-tag>{{ ruleText[item.rule] || item.rule }}</el-tag> <strong>{{ item.channel_name }}</strong>
            <el-tag v-if="item.evidence.sparse" class="status" type="warning">稀疏统计</el-tag>
            <el-tag class="status" :type="item.status === 'pending' ? 'warning' : ['adopted', 'auto_executed'].includes(item.status) ? 'success' : 'info'">{{ item.status }}</el-tag></div>
            <p>{{ evidence(item) }}<template v-if="item.rule === 'rebalance'">；权重 {{ item.current_weight }} → {{ item.proposed_weight }}</template><template v-else-if="item.proposed_priority !== item.current_priority">；优先级 {{ item.current_priority }} → {{ item.proposed_priority }}</template></p>
            <div v-if="item.outcome_at">事后：<b>{{ item.hit === true ? "命中 ✓" : item.hit === false ? "未命中 ✕" : "样本不足" }}</b></div>
            <div v-if="item.status === 'pending'"><el-button type="primary" size="small" @click="act(item, 'adopt')">采纳</el-button><el-button size="small" @click="act(item, 'dismiss')">忽略</el-button></div>
          </div></el-timeline-item></el-timeline><el-empty v-else description="暂无建议" :image-size="50" />
      </el-card>

      <el-card shadow="never"><template #header><strong>命中率报告</strong><el-radio-group v-model="reportDays" size="small"><el-radio-button :value="7">7 天</el-radio-button><el-radio-button :value="30">30 天</el-radio-button></el-radio-group></template>
        <div class="report-grid"><div><span>动作建议</span><b>{{ reports[reportDays]?.total || 0 }}</b></div><div><span>采纳率</span><b>{{ ((reports[reportDays]?.adoption_rate || 0) * 100).toFixed(1) }}%</b></div><div><span>已判断</span><b>{{ reports[reportDays]?.judged || 0 }}</b></div><div><span>命中率</span><b>{{ ((reports[reportDays]?.hit_rate || 0) * 100).toFixed(1) }}%</b></div></div>
        <el-alert title="自动模式已可用；建议先观察命中率，并确认 Agent 指令回执正常后再开启。" type="info" :closable="false" />
      </el-card>

      <el-card shadow="never"><template #header><strong>说明</strong></template>
        <p><b>值班模型：</b>同一模型按渠道优先级组成梯队；在岗异常时建议让位给下一岗，等待后再进行恢复验证。</p>
        <p><b>降级调度：</b>观察只记录建议；人工确认等待采纳；自动会直接创建渠道指令。<b>动态配权：</b>拥有独立的关闭、观察、自动三态，不受降级调度模式影响；建议先落库，执行结果和失败原因仍可追踪。</p>
      </el-card>
    </div>
  </AppShell>
</template>

<style scoped>
.tuning-page{display:grid;gap:16px}.hint{font-size:12px;color:#8491a5}.base-values-section{padding-bottom:18px;border-bottom:1px solid #ebeef5}.base-values-title,.base-model-header,.base-toolbar{display:flex;align-items:center;justify-content:space-between;gap:12px}.base-values-title h3{margin:0 0 5px}.base-values-title p{margin:0}.base-toolbar{justify-content:flex-start;margin:12px 0}.base-toolbar .el-select{width:320px}.base-model{margin-top:12px;border:1px solid #e4eaf3;border-radius:8px;overflow:hidden}.base-model-header{padding:10px 12px;background:#f7f9fc}.policy-section{margin:18px 0;padding-top:16px;border-top:1px solid #ebeef5}.policy-section h3{margin:0 0 6px;font-size:15px}.policy-section .hint{margin:0 0 12px}.criteria-summary{margin-bottom:12px}.criteria-formula p{margin:5px 0;line-height:1.65}.criteria-notes{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin:0 0 14px}.criteria-notes div{display:flex;flex-direction:column;gap:4px;padding:10px 12px;border:1px solid #e4eaf3;border-radius:6px;background:#fafbfd}.criteria-notes b{font-size:13px;color:#303b4d}.criteria-notes span{font-size:12px;line-height:1.55;color:#657086}.criteria-notes .wide{grid-column:1/-1;background:#fffaf2;border-color:#f3dfb7}.dispatch-flow{display:grid;grid-template-columns:repeat(4,1fr);gap:10px;margin:0 0 12px}.flow-step{display:flex;gap:10px;padding:12px;border:1px solid #dfe7f3;border-radius:8px;background:#f8faff}.step-number{display:flex;align-items:center;justify-content:center;flex:0 0 24px;width:24px;height:24px;border-radius:50%;background:#3568e8;color:#fff;font-weight:700}.flow-step b{font-size:13px}.flow-step p{margin:5px 0 0;color:#657086;font-size:12px;line-height:1.55}.mode-effect{margin:0 0 14px}.policy-grid{display:grid;grid-template-columns:repeat(5,minmax(150px,1fr));gap:0 16px}.weighting-mode{grid-column:span 2}.weighting-mode .hint{display:block;margin:6px 0 0}.ladder-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}.ladder{border:1px solid #e5eaf2;border-radius:8px;padding:12px}.ladder-row{display:grid;grid-template-columns:minmax(0,1fr) auto auto auto;gap:8px;align-items:center;margin-top:10px}.weight-value{white-space:nowrap;color:#526078;font-size:12px}.recommendation{border:1px solid #e8edf5;border-radius:8px;padding:12px}.recommendation p{color:#657086}.status{margin-left:8px}.report-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin-bottom:14px}.report-grid div{background:#f6f8fb;padding:16px;border-radius:8px}.report-grid span,.report-grid b{display:block}.report-grid b{font-size:24px;margin-top:6px}@media(max-width:1200px){.criteria-notes{grid-template-columns:1fr 1fr}.dispatch-flow{grid-template-columns:1fr 1fr}.policy-grid{grid-template-columns:repeat(3,minmax(150px,1fr))}}
</style>
<style scoped>
.base-values-section.is-dirty {
  background: #fffaf0;
  border-color: #f3d19e;
}
</style>
