<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";
import { FullScreen } from "@element-plus/icons-vue";
import type { MetricItem } from "@ct/shared";
import { dashboard } from "../api";
import { buildCustomerTraffic, formatTrafficTPM as formatTokens, type TrafficDimension } from "../utils/customerTraffic";
import CustomerTrafficPanel from "./CustomerTrafficPanel.vue";
import MonitorNameButton from "./MonitorNameButton.vue";
import MonitorCopyButton from "./MonitorCopyButton.vue";

const props = defineProps<{
  customer: MetricItem;
  totals: MetricItem[];
  verifiedBuckets?: number[];
  minute: { tpm: number; time: number; stale: boolean } | null;
  hours: number;
  asOf: number;
  refreshKey: number;
}>();
const emit = defineEmits<{ detail: [] }>();
const cardElement = ref<HTMLElement>();
const nearViewport = ref(typeof IntersectionObserver === "undefined");
let visibilityObserver: IntersectionObserver | undefined;
onMounted(() => {
  if (!cardElement.value || typeof IntersectionObserver === "undefined") return;
  visibilityObserver = new IntersectionObserver(entries => {
    nearViewport.value = entries.some(entry => entry.isIntersecting);
  }, { rootMargin: "400px 0px" });
  visibilityObserver.observe(cardElement.value);
});
const dimension = ref<TrafficDimension>("model");
const expanded = ref(false);
const points = shallowRef<MetricItem[]>([]);
const loadedScope = ref("");
const loading = ref(false);
const error = ref("");
const failures = ref(0);
const sessionExpired = ref(false);
const checkedAt = ref(0);
const waitingSince = ref(0);
let statusScope = "";
const notice = computed(() => {
  if (sessionExpired.value || failures.value < 3 || checkedAt.value - waitingSince.value < 120_000) return "";
  return checkedAt.value - waitingSince.value >= 300_000
    ? "暂时无法更新，正在重试" : "数据更新稍有延迟";
});
const scope = computed(() => `${props.customer.instance_id}|${props.customer.dimension_key}|${props.hours}|${dimension.value}`);
const bucketMinutes = computed(() => props.hours === 24 ? 5 : 1);
const name = computed(() => props.customer.display_name || props.customer.display_key || props.customer.dimension_key);
const id = computed(() => props.customer.dimension_key.slice(props.customer.dimension_key.lastIndexOf(":user:") + 6));
const scopeCache = new Map<string, { revision: number; items: MetricItem[]; updatedAt: number }>();
let requestToken = 0;
let pendingScope: string | undefined;

async function load() {
  if (!nearViewport.value && !expanded.value) return;
  const context = scope.value;
  if (pendingScope === context) return;
  const token = ++requestToken;
  const cached = scopeCache.get(context);
  checkedAt.value = Date.now();
  if (statusScope !== context) {
    statusScope = context;
    failures.value = 0;
    sessionExpired.value = false;
    error.value = "";
    waitingSince.value = cached?.updatedAt ?? checkedAt.value;
    if (cached) { points.value = cached.items; loadedScope.value = context; }
  }
  if (cached?.revision === props.refreshKey) {
    points.value = cached.items; loadedScope.value = context; loading.value = false; pendingScope = undefined; return;
  }
  pendingScope = context;
  loading.value = true;
  const revision = props.refreshKey;
  try {
    const response = await dashboard.metricHistory({
      instance_id: props.customer.instance_id,
      window: bucketMinutes.value === 5 ? "5m" : "1m",
      dimension_type: `instance_user_${dimension.value}`,
      dimension_key_prefix: `${props.customer.dimension_key}:${dimension.value}:`,
      hours: props.hours,
    });
    if (token !== requestToken || context !== scope.value) return;
    points.value = response.items;
    loadedScope.value = context;
    checkedAt.value = Date.now();
    waitingSince.value = checkedAt.value;
    failures.value = 0;
    error.value = "";
    sessionExpired.value = false;
    scopeCache.set(context, { revision, items: response.items, updatedAt: checkedAt.value });
  } catch (cause) {
    if (token === requestToken && context === scope.value) {
      checkedAt.value = Date.now();
      failures.value++;
      const status = (cause as { status?: number } | null)?.status;
      sessionExpired.value = status === 401;
      // Session expiry is handled globally by the API client.
      error.value = status === 403 ? "暂无查看权限，请联系管理员"
        : status === 400 ? "查询条件不可用，请调整后重试" : "";
    }
  } finally {
    if (token === requestToken) { loading.value = false; pendingScope = undefined; }
  }
}
watch([scope, () => props.refreshKey, nearViewport, expanded], () => { void load(); }, { immediate: true });
onBeforeUnmount(() => { ++requestToken; visibilityObserver?.disconnect(); });
const traffic = computed(() => buildCustomerTraffic({
  customerKey: props.customer.dimension_key, instanceID: props.customer.instance_id,
  dimension: dimension.value, points: loadedScope.value === scope.value ? points.value : [],
  totals: props.totals, verifiedBuckets: props.verifiedBuckets, bucketMinutes: bucketMinutes.value, hours: props.hours, now: props.asOf,
}));
const hasPlot = computed(() => traffic.value.series.length > 0 && traffic.value.coveredMinutes > 0);
function timeRange(start: number, minutes: number) {
  const time = (value: number) => new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return `${time(start)}–${time(start + minutes * 60_000)}`;
}
const minuteTime = computed(() => {
  if (!props.minute) return "等待采集";
  return timeRange(props.minute.time, 1);
});
const minuteStatus = computed(() => {
  if (!props.minute) return "";
  if (props.minute.stale) return "数据滞后";
  if (failures.value) return "";
  // A five-minute chart cannot certify the independent one-minute headline.
  // Only compare the headline's exact bucket when both use one-minute data.
  if (loading.value || (bucketMinutes.value === 1 && !traffic.value.completeTimes.has(props.minute.time))) return "数据更新中";
  return "";
});
const lastPlotTime = computed(() => hasPlot.value && traffic.value.lastCompleteTime !== null
  ? timeRange(traffic.value.lastCompleteTime, bucketMinutes.value) : "");
const emptyText = computed(() => loadedScope.value !== scope.value && failures.value
  ? "数据暂未就绪"
  : dimension.value === "channel"
  ? "暂无完整渠道拆分；升级 Agent 后开始积累"
  : "暂无完整模型拆分数据");
const panelProps = computed(() => ({
  traffic: traffic.value, dimension: dimension.value, name: name.value,
  loading: loading.value, error: error.value, emptyText: emptyText.value,
  notice: error.value ? "" : notice.value,
  hours: props.hours, bucketMinutes: bucketMinutes.value, lastPlotTime: lastPlotTime.value,
}));
</script>

<template>
  <article ref="cardElement" class="traffic-card">
    <header>
      <div class="customer-heading">
        <div class="customer-name-line">
          <MonitorNameButton :name="name" :title="`${name} · ${customer.instance_name} · ID ${id}`" @detail="emit('detail')" />
          <MonitorCopyButton :value="name" label="复制客户名称" />
        </div>
        <span class="customer-id">ID {{ id }}</span>
      </div>
      <div class="compact-rate" :class="{ stale: minuteStatus }" title="最近结束的 1 分钟已收到的 Token；每 30 秒刷新，后续上报可能补齐。数据更新中表示拆分正在加载或与该分钟总量尚未对齐；对齐也不代表采集已全部结束。">
        <div><strong>{{ minute ? formatTokens(minute.tpm) : '—' }}</strong><span>TPM</span></div>
        <small>{{ minuteTime }}<span v-if="minuteStatus" class="rate-status"> · {{ minuteStatus }}</span></small>
      </div>
      <el-button class="expand-button" text :icon="FullScreen" aria-label="弹窗查看大图" title="弹窗查看大图" @click="expanded = true" />
    </header>
    <CustomerTrafficPanel v-bind="panelProps" :active="nearViewport && !expanded" @dimension="dimension = $event" @retry="load" />
    <el-dialog v-model="expanded" :title="`${name} · #${id} · TPM 流量构成`" width="min(1100px, calc(100vw - 40px))" top="8vh" append-to-body destroy-on-close class="customer-traffic-dialog">
      <div class="dialog-summary">
        <span>最近 1 分钟 <strong>{{ minute ? formatTokens(minute.tpm) : '—' }}</strong> TPM</span>
        <span>{{ minuteTime }}{{ minuteStatus ? ` · ${minuteStatus}` : '' }}</span>
      </div>
      <CustomerTrafficPanel v-if="expanded" v-bind="panelProps" active expanded @dimension="dimension = $event" @retry="load" />
    </el-dialog>
  </article>
</template>

<style scoped>
.traffic-card { min-width: 0; padding: 14px 14px 9px; border: 1px solid var(--ct-line); border-radius: 12px; background: var(--ct-surface); box-shadow: 0 2px 4px #25436d04, 0 5px 17px #25436d08; }
.traffic-card > header { display: flex; align-items: center; gap: 7px; min-height: 36px; margin-bottom: 10px; }
.customer-heading { flex: 1; min-width: 0; }
.customer-name-line { display: flex; align-items: center; min-width: 0; gap: 3px; }
.customer-id { color: var(--ct-ink-3); font-size: 11px; }
.compact-rate { flex-shrink: 0; text-align: right; font-variant-numeric: tabular-nums; }
.compact-rate strong { font-size: 19px; font-weight: 500; letter-spacing: -.5px; line-height: 1.2; }
.compact-rate span { color: var(--ct-ink-3); font-size: 11px; margin-left: 4px; }
.compact-rate small { display: block; color: var(--ct-ink-3); font-size: 11px; line-height: 1.4; }
.compact-rate.stale small { color: var(--ct-warn); }
.compact-rate .rate-status { color: inherit; margin-left: 0; }
.expand-button { flex-shrink: 0; width: 22px; padding: 0; color: var(--ct-ink-3); }
.dialog-summary { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: baseline; gap: 8px; margin-bottom: 14px; color: var(--ct-ink-3); font-size: 12px; }
.dialog-summary strong { color: var(--ct-ink); font-size: 22px; font-weight: 500; margin: 0 4px; }
</style>
<style>
.customer-traffic-dialog { max-height: 84vh; overflow-y: auto; border-radius: 12px; }
.customer-traffic-dialog .traffic-chart.expanded { height: clamp(260px, 48vh, 520px); }
@media(max-width:900px) {
  .customer-traffic-dialog { width:100%!important;height:100dvh;max-height:100dvh;margin:0!important;border-radius:0;padding:16px;padding-bottom:calc(16px + env(safe-area-inset-bottom)); }
  .customer-traffic-dialog .el-dialog__header { padding-right:40px;overflow-wrap:anywhere; }
  .customer-traffic-dialog .el-dialog__headerbtn { width:44px;height:44px; }
}
</style>
<style scoped>
@media(max-width:900px) {
  .traffic-card>header { flex-wrap:wrap; }
  .expand-button { width:36px;min-height:36px; }
  .compact-rate { max-width:100%; }
}
</style>
