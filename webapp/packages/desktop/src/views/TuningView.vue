<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { ElMessage, ElMessageBox } from "element-plus";
import { siteOf, type TuningPolicy, type TuningRecommendation, type TuningReport, type TuningLadders } from "@ct/shared";
import { dashboard } from "../api";
import AppShell from "../components/AppShell.vue";
import { useFiltersStore } from "../stores/filters";
import { formatTime } from "../utils/format";

const filters = useFiltersStore();
const loading = ref(false);
const mode = ref<"observe" | "confirm">("observe");
const policy = reactive<TuningPolicy>({
  scheduling: {
    window_minutes: 15, min_samples: 20, trial_initial_minutes: 60,
    trial_backoff_factor: 2, trial_max_minutes: 1440, trial_windows: 2,
    cooldown_minutes: 10, daily_action_limit: 6,
  },
  criteria: [{
    name: "default", error_rate_threshold: .15, severe_threshold: .5,
    latency_multiplier: 2, latency_floor_seconds: 10, sustained_windows: 2,
  }],
  assignments: {},
});
const defaultCriteria = computed(() => policy.criteria.find(item => item.name === "default") || policy.criteria[0]);
const recommendations = ref<TuningRecommendation[]>([]);
const reports = reactive<Record<7 | 30, TuningReport | null>>({ 7: null, 30: null });
const reportDays = ref<7 | 30>(7);
const ladders = ref<TuningLadders>({ channels: [], dispatch_states: [] });
const instanceID = computed(() => filters.instances
  .filter(x => x.enabled && siteOf(x) === filters.site_id)
  .map(x => x.instance_id).sort()[0] || "");
const stateByChannel = computed(() => new Map(ladders.value.dispatch_states.map(x => [x.ChannelID, x])));
const groupedLadders = computed(() => {
  const groups = new Map<string, typeof ladders.value.channels>();
  ladders.value.channels.forEach(channel => channel.Models.forEach(model => {
    const list = groups.get(model) || []; list.push(channel); groups.set(model, list);
  }));
  return [...groups.entries()].map(([model, channels]) => ({ model, channels: channels.sort((a, b) => b.Priority - a.Priority) }));
});
const ruleText: Record<string, string> = { demote: "降级", trial: "试岗", no_backup: "无备岗", ladder_exhausted: "梯队用尽", mixed_channel: "混布" };

async function load() {
  await filters.loadInstances();
  if (!instanceID.value) return;
  loading.value = true;
  try {
    const [p, recs, ladder, r7, r30] = await Promise.all([
      dashboard.tuningPolicy(instanceID.value), dashboard.tuningRecommendations(instanceID.value),
      dashboard.tuningLadders(instanceID.value), dashboard.tuningReport(instanceID.value, 7),
      dashboard.tuningReport(instanceID.value, 30),
    ]);
    mode.value = p.mode; Object.assign(policy, p.policy);
    recommendations.value = recs.items; ladders.value = ladder; reports[7] = r7; reports[30] = r30;
  } finally { loading.value = false; }
}
async function save() {
  await dashboard.saveTuningPolicy(instanceID.value, policy, mode.value);
  ElMessage.success("策略已保存，将在下一轮评估生效");
}
async function act(item: TuningRecommendation, action: "adopt" | "dismiss") {
  if (action === "adopt") await ElMessageBox.confirm(
    `将渠道 ${item.channel_name} 的优先级从 ${item.current_priority} 调整为 ${item.proposed_priority}，并创建渠道指令。`,
    "确认采纳建议", { type: "warning" },
  );
  await dashboard.tuningRecommendationAction(item.id, action);
  ElMessage.success(action === "adopt" ? "已采纳并创建渠道指令" : "已忽略");
  await load();
}
function evidence(item: TuningRecommendation) {
  const e = item.evidence;
  if (item.rule === "demote") return `样本 ${e.samples ?? "-"}，错误率 ${(Number(e.error_rate || 0) * 100).toFixed(1)}%，P95 ${e.p95 ?? "-"}s`;
  if (item.rule === "trial") return `第 ${Number(e.trial_attempts || 0) + 1} 次试岗`;
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
        <el-form label-position="top">
          <el-form-item label="运行模式"><el-radio-group v-model="mode"><el-radio-button value="observe">观察</el-radio-button><el-radio-button value="confirm">人工确认</el-radio-button><el-radio-button value="auto" disabled>自动（B3）</el-radio-button></el-radio-group></el-form-item>
          <section v-if="defaultCriteria" class="policy-section">
            <h3>降级标准（默认标准）</h3>
            <p class="hint">当前所有模型使用默认标准；后续可增加多套标准并按模型指派。</p>
            <div class="policy-grid">
              <el-form-item label="错误率线"><el-input-number v-model="defaultCriteria.error_rate_threshold" :min=".01" :max="1" :step=".01" /></el-form-item>
              <el-form-item label="熔断线"><el-input-number v-model="defaultCriteria.severe_threshold" :min=".01" :max="1" :step=".01" /></el-form-item>
              <el-form-item label="延迟倍数"><el-input-number v-model="defaultCriteria.latency_multiplier" :min="1" :step=".1" /></el-form-item>
              <el-form-item label="延迟下限（秒）"><el-input-number v-model="defaultCriteria.latency_floor_seconds" :min=".1" /></el-form-item>
              <el-form-item label="持续窗口"><el-input-number v-model="defaultCriteria.sustained_windows" :min="1" /></el-form-item>
            </div>
          </section>
          <section class="policy-section">
            <h3>调度参数</h3>
            <p class="hint">控制评估频率、试岗退避和动作节奏，不决定何时触发降级。</p>
            <div class="policy-grid">
              <el-form-item label="评估窗口（分钟）"><el-input-number v-model="policy.scheduling.window_minutes" :min="1" /></el-form-item>
              <el-form-item label="最少样本"><el-input-number v-model="policy.scheduling.min_samples" :min="1" /></el-form-item>
              <el-form-item label="首次试岗（分钟）"><el-input-number v-model="policy.scheduling.trial_initial_minutes" :min="1" /></el-form-item>
              <el-form-item label="退避倍数"><el-input-number v-model="policy.scheduling.trial_backoff_factor" :min="1" :step=".1" /></el-form-item>
              <el-form-item label="试岗上限（分钟）"><el-input-number v-model="policy.scheduling.trial_max_minutes" :min="1" /></el-form-item>
              <el-form-item label="试岗观察窗口"><el-input-number v-model="policy.scheduling.trial_windows" :min="1" /></el-form-item>
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
            <el-tag v-if="stateByChannel.get(channel.ID)?.NextTrialAt" type="warning">降级中 · {{ formatTime(stateByChannel.get(channel.ID)!.NextTrialAt!) }}</el-tag>
            <el-tag v-else-if="stateByChannel.has(channel.ID)" type="success">试岗中</el-tag><el-tag v-else>在岗</el-tag>
          </div></div></div>
      </el-card>

      <el-card shadow="never"><template #header><strong>建议流水</strong></template>
        <el-timeline v-if="recommendations.length"><el-timeline-item v-for="item in recommendations" :key="item.id" :timestamp="formatTime(item.created_at)" placement="top">
          <div class="recommendation"><div><el-tag>{{ ruleText[item.rule] || item.rule }}</el-tag> <strong>{{ item.channel_name }}</strong>
            <el-tag class="status" :type="item.status === 'pending' ? 'warning' : item.status === 'adopted' ? 'success' : 'info'">{{ item.status }}</el-tag></div>
            <p>{{ evidence(item) }}<template v-if="item.proposed_priority !== item.current_priority">；优先级 {{ item.current_priority }} → {{ item.proposed_priority }}</template></p>
            <div v-if="item.outcome_at">事后：<b>{{ item.hit === true ? "命中 ✓" : item.hit === false ? "未命中 ✕" : "样本不足" }}</b></div>
            <div v-if="item.status === 'pending'"><el-button type="primary" size="small" @click="act(item, 'adopt')">采纳</el-button><el-button size="small" @click="act(item, 'dismiss')">忽略</el-button></div>
          </div></el-timeline-item></el-timeline><el-empty v-else description="暂无建议" :image-size="50" />
      </el-card>

      <el-card shadow="never"><template #header><strong>命中率报告</strong><el-radio-group v-model="reportDays" size="small"><el-radio-button :value="7">7 天</el-radio-button><el-radio-button :value="30">30 天</el-radio-button></el-radio-group></template>
        <div class="report-grid"><div><span>动作建议</span><b>{{ reports[reportDays]?.total || 0 }}</b></div><div><span>采纳率</span><b>{{ ((reports[reportDays]?.adoption_rate || 0) * 100).toFixed(1) }}%</b></div><div><span>已判断</span><b>{{ reports[reportDays]?.judged || 0 }}</b></div><div><span>命中率</span><b>{{ ((reports[reportDays]?.hit_rate || 0) * 100).toFixed(1) }}%</b></div></div>
        <el-alert title="命中率持续 ≥ 85% 且无最小可用集险情，才建议开启 auto。" type="info" :closable="false" />
      </el-card>

      <el-card shadow="never"><template #header><strong>说明</strong></template>
        <p><b>值班模型：</b>同一模型按渠道优先级组成梯队；在岗异常时建议让位给下一岗，冷却后再安排试岗。</p>
        <p><b>观察：</b>只记录建议。<b>人工确认：</b>动作建议等待人工处理，采纳后进入现有渠道指令队列。<b>自动：</b>B3 才会提供。</p>
      </el-card>
    </div>
  </AppShell>
</template>

<style scoped>
.tuning-page{display:grid;gap:16px}.hint{font-size:12px;color:#8491a5}.policy-section{margin:18px 0;padding-top:16px;border-top:1px solid #ebeef5}.policy-section h3{margin:0 0 6px;font-size:15px}.policy-section .hint{margin:0 0 12px}.policy-grid{display:grid;grid-template-columns:repeat(5,minmax(150px,1fr));gap:0 16px}.ladder-grid{display:grid;grid-template-columns:repeat(3,1fr);gap:12px}.ladder{border:1px solid #e5eaf2;border-radius:8px;padding:12px}.ladder-row{display:grid;grid-template-columns:1fr auto auto;gap:8px;align-items:center;margin-top:10px}.recommendation{border:1px solid #e8edf5;border-radius:8px;padding:12px}.recommendation p{color:#657086}.status{margin-left:8px}.report-grid{display:grid;grid-template-columns:repeat(4,1fr);gap:12px;margin-bottom:14px}.report-grid div{background:#f6f8fb;padding:16px;border-radius:8px}.report-grid span,.report-grid b{display:block}.report-grid b{font-size:24px;margin-top:6px}
</style>
