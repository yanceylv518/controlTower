<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";
import type { MetricItem } from "@ct/shared";
import { dashboard } from "../api";
import { buildModelTraffic } from "../utils/customerTraffic";
import CustomerTrafficPanel from "./CustomerTrafficPanel.vue";

const props = defineProps<{
  model: MetricItem;
  totals: MetricItem[];
  hours: number;
  asOf: number;
  refreshKey: number;
  active: boolean;
  expanded?: boolean;
  dimension?: "user" | "channel";
}>();
const emit = defineEmits<{ "update:dimension": [value: "user" | "channel"] }>();
const element = ref<HTMLElement>();
const localMode = ref<"user" | "channel">("user");
const mode = computed({
  get: () => props.dimension ?? localMode.value,
  set: (value: "user" | "channel") => { localMode.value = value; emit("update:dimension", value); },
});
const dimensionLabel = computed(() => mode.value === "user" ? "客户" : "渠道");
const nearViewport = ref(typeof IntersectionObserver === "undefined");
let observer: IntersectionObserver | undefined;
onMounted(() => {
  if (!element.value || typeof IntersectionObserver === "undefined") return;
  observer = new IntersectionObserver(entries => {
    nearViewport.value = entries.some(entry => entry.isIntersecting);
  }, { rootMargin: "400px 0px" });
  observer.observe(element.value);
});

const points = shallowRef<MetricItem[]>([]);
const loadedScope = ref("");
const loadedRevision = ref(-1);
const loading = ref(false);
const error = ref("");
const scope = computed(() => JSON.stringify([props.model.instance_id, props.model.dimension_key, props.hours, mode.value]));
const cache = new Map<string, { revision: number; items: MetricItem[] }>();
const bucketMinutes = computed(() => props.hours === 24 ? 5 : 1);
let requestToken = 0;
let pendingScope: string | undefined;
let disposed = false;

async function load() {
  if (disposed || !props.active || (!nearViewport.value && !props.expanded)) return;
  const context = scope.value;
  if (pendingScope === context) return;
  const token = ++requestToken;
  const revision = props.refreshKey;
  const cached = cache.get(context);
  if (cached?.revision === revision) {
    points.value = cached.items;
    loadedScope.value = context;
    loadedRevision.value = revision;
    pendingScope = undefined;
    loading.value = false;
    error.value = "";
    return;
  }
  pendingScope = context;
  loading.value = true;
  error.value = "";
  try {
    const response = await dashboard.metricHistory({
      instance_id: props.model.instance_id,
      window: bucketMinutes.value === 5 ? "5m" : "1m",
      dimension_type: `instance_model_${mode.value}`,
      dimension_key_prefix: `${props.model.dimension_key}:${mode.value}:`,
      hours: props.hours,
    });
    if (token !== requestToken || context !== scope.value) return;
    points.value = response.items;
    loadedScope.value = context;
    loadedRevision.value = revision;
    // Only the current model/time range needs at most two cached dimensions.
    if (cache.size >= 2 && !cache.has(context)) cache.clear();
    cache.set(context, { revision, items: response.items });
  } catch {
    if (token === requestToken && context === scope.value) error.value = `${dimensionLabel.value}拆分加载失败，请重试`;
  } finally {
    if (token === requestToken) {
      loading.value = false;
      pendingScope = undefined;
      // A slow request may span a parent refresh. Finish it, then catch up once.
      if (context === scope.value && revision !== props.refreshKey) void load();
    }
  }
}
watch([scope, mode, () => props.refreshKey, () => props.active, nearViewport], () => { void load(); }, { immediate: true });
onBeforeUnmount(() => { disposed = true; ++requestToken; observer?.disconnect(); });

const traffic = computed(() => buildModelTraffic({
  modelKey: props.model.dimension_key, instanceID: props.model.instance_id,
  dimension: mode.value,
  points: loadedScope.value === scope.value ? points.value : [], totals: props.totals,
  bucketMinutes: bucketMinutes.value, hours: props.hours, now: props.asOf,
}));
const lastPlotTime = computed(() => {
  const start = traffic.value.lastCompleteTime;
  if (!traffic.value.series.length || start === null) return "";
  const time = (value: number) => new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return `${time(start)}–${time(start + bucketMinutes.value * 60_000)}`;
});
</script>

<template>
  <div ref="element" class="model-traffic">
    <div class="model-traffic-mode">
      <span>TPM · 流量构成</span>
      <el-segmented v-model="mode" :options="[{ label: '按客户', value: 'user' }, { label: '按渠道', value: 'channel' }]" size="small" :aria-label="`${model.display_name || model.display_key} TPM 图层`" />
    </div>
    <CustomerTrafficPanel
      :traffic="traffic" :dimension="mode" hide-mode
      :name="model.display_name || model.display_key || model.dimension_key"
      :loading="loading" :error="error" :empty-text="`暂无完整${dimensionLabel}拆分数据`"
      :hours="hours" :bucket-minutes="bucketMinutes" :last-plot-time="lastPlotTime"
      :active="active && (nearViewport || !!expanded)" :expanded="expanded" @retry="load"
    />
  </div>
</template>

<style scoped>
.model-traffic-mode { display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:6px;padding-top:11px;border-top:1px solid var(--ct-line);color:var(--ct-ink-3);font-size:11px; }
.model-traffic-mode :deep(.el-segmented) { --el-segmented-bg-color:var(--ct-surface-2);--el-segmented-item-selected-bg-color:var(--ct-surface);--el-segmented-item-selected-color:var(--ct-primary);font-size:11px; }
.model-traffic :deep(.customer-chart-canvas) { height:190px; }
@media(max-width:900px) { .model-traffic-mode :deep(.el-segmented__item) { min-height:32px; } }
</style>
