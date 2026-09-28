<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { buildCustomerTraffic, formatTrafficTPM as formatTokens, type TrafficDimension } from "../utils/customerTraffic";
import CustomerTrafficChart from "./CustomerTrafficChart.vue";

const props = defineProps<{
  traffic: ReturnType<typeof buildCustomerTraffic>;
  dimension: TrafficDimension;
  name: string;
  loading: boolean;
  error: string;
  notice?: string;
  emptyText: string;
  hours: number;
  bucketMinutes: number;
  lastPlotTime: string;
  active: boolean;
  expanded?: boolean;
  hideMode?: boolean;
}>();
const emit = defineEmits<{ dimension: [value: TrafficDimension]; retry: [] }>();
const chart = ref<InstanceType<typeof CustomerTrafficChart>>();
const moreVisible = ref(false);
const search = ref("");
// null follows all layers; an explicit selection also keeps new arrivals hidden.
const selectedKeys = ref<Set<string> | null>(null);
const isSelected = (key: string) => selectedKeys.value === null || selectedKeys.value.has(key);
const selectedSeries = computed(() => props.expanded ? props.traffic.series.filter(item => isSelected(item.key)) : props.traffic.series);
const listedItems = computed(() => props.traffic.ranked.filter(item => `${item.name} ${item.key}`.toLocaleLowerCase().includes(search.value.trim().toLocaleLowerCase())));
const filtered = computed(() => selectedSeries.value.length !== props.traffic.series.length);
function toggleSeries(key: string, checked: boolean) {
  const next = new Set(selectedKeys.value ?? props.traffic.series.map(item => item.key));
  if (checked) next.add(key); else next.delete(key);
  selectedKeys.value = next;
}
function selectAll(checked: boolean) {
  selectedKeys.value = checked ? null : new Set();
}
watch(() => props.dimension, () => { selectedKeys.value = null; search.value = ""; });
const hasPlot = computed(() => props.traffic.series.length > 0 && props.traffic.coveredMinutes > 0);
const dimensionLabel = computed(() => props.dimension === "model" ? "模型" : props.dimension === "user" ? "客户" : "渠道");
watch(() => [props.dimension, props.active], () => { moreVisible.value = false; });
</script>

<template>
  <div class="traffic-panel">
    <div v-if="!hideMode" class="traffic-mode"><span>流量构成</span><span v-if="dimension === 'user'">按客户 · TPM</span><el-segmented v-else :model-value="dimension" @update:model-value="emit('dimension', $event as TrafficDimension)" :options="[{ label: '按模型', value: 'model' }, { label: '按渠道', value: 'channel' }]" size="small" /></div>
    <div v-if="hasPlot && !expanded" class="traffic-legend" :aria-label="`按流量排序的${dimensionLabel}图例`">
      <button v-for="item in traffic.ranked.slice(0, 2)" :key="item.key" type="button" :title="item.name" @mouseenter="chart?.highlight(item.key)" @mouseleave="chart?.highlight()" @focus="chart?.highlight(item.key)" @blur="chart?.highlight()"><i :style="{ background: item.color }" /><span>{{ item.name }}</span></button>
      <el-popover v-if="traffic.ranked.length > 2" v-model:visible="moreVisible" trigger="click" placement="bottom-end" :width="360" popper-class="customer-traffic-popover">
        <template #reference><button class="more" type="button">⋯ 更多 {{ traffic.ranked.length - 2 }}</button></template>
        <div class="legend-title">{{ name }} · 全部{{ dimensionLabel }}</div>
        <div class="legend-note">按完整拆分的 {{ traffic.coveredMinutes }} 分钟统计</div>
        <div class="legend-table-head"><span>{{ dimensionLabel }}</span><span>平均 TPM</span><span>占比</span></div>
        <el-scrollbar max-height="300px">
          <div v-for="item in traffic.ranked" :key="item.key" class="legend-table-row">
            <span class="legend-name"><i :style="{ background: item.color }" />{{ item.name }}</span>
            <span>{{ formatTokens(item.tokens / traffic.coveredMinutes) }}</span><span>{{ traffic.totalTokens ? (item.tokens / traffic.totalTokens * 100).toFixed(1) : '0.0' }}%</span>
          </div>
        </el-scrollbar>
      </el-popover>
    </div>
    <div v-if="error" class="traffic-notice" role="alert">{{ error }}<el-button link type="primary" @click="emit('retry')">重试</el-button></div>
    <div v-else-if="notice" class="traffic-delay" role="status">{{ notice }}<el-button link type="primary" @click="emit('retry')">重试</el-button></div>
    <div class="traffic-layout" :class="{ expanded }">
    <div v-loading="loading && !hasPlot" class="traffic-content">
      <div v-if="hasPlot && expanded && !selectedSeries.length" class="traffic-empty selection-empty" role="status">请勾选列表中的{{ dimensionLabel }}<el-button link type="primary" @click="selectAll(true)">显示全部</el-button></div>
      <CustomerTrafficChart v-else-if="hasPlot && active" ref="chart" :series="selectedSeries" :expanded="expanded" :filtered="filtered" />
      <div v-else-if="hasPlot" :style="{ height: expanded ? '320px' : '190px' }" />
      <div v-else class="traffic-empty">{{ loading ? '正在读取流量构成…' : emptyText }}</div>
    </div>
    <aside v-if="expanded && hasPlot" class="traffic-selection" :aria-label="`${dimensionLabel}分层选择`">
      <div class="selection-heading"><strong>{{ dimensionLabel }}列表</strong><span>已选 {{ selectedSeries.length }} / {{ traffic.series.length }}</span></div>
      <div class="selection-tools"><el-input v-model="search" size="small" clearable :placeholder="`搜索${dimensionLabel}`" :aria-label="`搜索${dimensionLabel}`" /><el-button link type="primary" @click="selectAll(true)">全选</el-button><el-button link @click="selectAll(false)">清空</el-button></div>
      <div class="selection-note">完整拆分 {{ traffic.coveredMinutes }} 分钟 · 占比按全部流量</div>
      <div class="selection-columns"><span>{{ dimensionLabel }}</span><span>平均 TPM / 占比</span></div>
      <div class="selection-list">
        <label v-for="item in listedItems" :key="item.key" class="selection-row" :class="{ muted: !isSelected(item.key) }" @mouseenter="isSelected(item.key) && chart?.highlight(item.key)" @mouseleave="chart?.highlight()">
          <input type="checkbox" :checked="isSelected(item.key)" :aria-label="`显示${dimensionLabel} ${item.name}`" @change="toggleSeries(item.key, ($event.target as HTMLInputElement).checked)" />
          <i :style="{ background: item.color }" /><span class="selection-name">{{ item.name }}</span>
          <span class="selection-values"><b>{{ formatTokens(item.tokens / traffic.coveredMinutes) }}</b><small>{{ traffic.totalTokens ? (item.tokens / traffic.totalTokens * 100).toFixed(1) : '0.0' }}%</small></span>
        </label>
        <div v-if="!listedItems.length" class="selection-no-results">未找到匹配的{{ dimensionLabel }}</div>
      </div>
    </aside>
    </div>
    <div class="traffic-caption">
      <span title="最近结束的时段会随采集上报继续补齐">近 {{ hours }} 小时 · {{ bucketMinutes }}分钟粒度</span>
      <span v-if="lastPlotTime" title="最后一段总量与拆分对齐、实际绘制的时间区间；可能早于顶部最近一分钟。24 小时视图的曲线为 5 分钟均值。">曲线最后有效：{{ lastPlotTime }}</span>
      <span v-if="hasPlot && traffic.incompleteBuckets" title="总量与拆分未对齐的时段留空；不将缺失流量填成零">部分时段拆分未齐</span>
    </div>
  </div>
</template>

<style scoped>
.traffic-layout.expanded { display: grid; grid-template-columns: minmax(0, 1fr) 300px; gap: 18px; margin: 12px 0; }
.traffic-content { min-width: 0; }
.traffic-selection { display: flex; flex-direction: column; min-width: 0; height: clamp(260px, 48vh, 520px); border-left: 1px solid var(--ct-line); padding-left: 16px; }
.selection-heading, .selection-tools, .selection-columns { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.selection-heading { font-size: 12px; margin-bottom: 10px; }.selection-heading strong { font-weight: 500; }.selection-heading span, .selection-note, .selection-columns { color: var(--ct-ink-3); font-size: 11px; }
.selection-tools .el-input { flex: 1; min-width: 0; }.selection-tools .el-button + .el-button { margin-left: 0; }
.selection-note { margin: 10px 0; }.selection-columns { padding-bottom: 8px; border-bottom: 1px solid var(--ct-line); }
.selection-list { flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; }
.selection-row { display: grid; grid-template-columns: 14px 8px minmax(0, 1fr) 76px; align-items: center; gap: 7px; padding: 10px 2px; border-bottom: 1px solid var(--ct-line); cursor: pointer; font-size: 12px; }
.selection-row:hover { background: var(--ct-surface-2); }.selection-row.muted { color: var(--ct-ink-3); }
.selection-row input { margin: 0; width: 14px; height: 14px; accent-color: var(--ct-primary); }.selection-row i { width: 8px; height: 8px; border-radius: 2px; }
.selection-name { overflow-wrap: anywhere; min-width: 0; }.selection-values { text-align: right; font-variant-numeric: tabular-nums; }.selection-values b { display: block; font-weight: 500; }.selection-values small { color: var(--ct-ink-3); font-size: 11px; }
.selection-no-results { padding: 24px 8px; text-align: center; color: var(--ct-ink-3); font-size: 12px; }.selection-empty { height: clamp(260px, 48vh, 520px); align-content: center; gap: 12px; }
@media(max-width:760px) { .traffic-layout.expanded { grid-template-columns: minmax(0, 1fr); gap: 12px; }.traffic-selection { height: 260px; border-left: 0; border-top: 1px solid var(--ct-line); padding: 12px 0 0; } }
.traffic-mode { display: flex; align-items: center; justify-content: space-between; gap: 6px; padding-top: 11px; border-top: 1px solid var(--ct-line); color: var(--ct-ink-3); font-size: 11px; }
.traffic-mode :deep(.el-segmented) { --el-segmented-bg-color: var(--ct-surface-2); --el-segmented-item-selected-bg-color: var(--ct-surface); --el-segmented-item-selected-color: var(--ct-primary); font-size: 11px; }
.traffic-legend { display: flex; align-items: center; gap: 3px 7px; flex-wrap: wrap; min-height: 32px; padding-top: 7px; }
.traffic-legend button { display: inline-flex; align-items: center; gap: 4px; padding: 3px 1px; border: 0; border-radius: 3px; background: none; color: var(--ct-ink-3); cursor: pointer; font: inherit; font-size: 11px; }
.traffic-legend button:hover { color: var(--ct-ink); background: var(--ct-surface-2); }
.traffic-legend button { max-width: 100%; }
.traffic-legend button span { min-width: 0; overflow-wrap: anywhere; text-align: left; }
.traffic-legend i, .legend-name i { width: 8px; height: 8px; border-radius: 2px; flex-shrink: 0; }
.traffic-caption { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 3px; font-size: 11px; color: var(--ct-ink-3); }
.traffic-empty { display: grid; place-items: center; min-height: 210px; padding: 20px; text-align: center; color: var(--ct-ink-3); font-size: 12px; }
.traffic-notice { color: var(--ct-warn); font-size: 11px; margin-top: 8px; }
.traffic-delay { color: var(--ct-ink-3); font-size: 11px; margin-top: 8px; }
.legend-title { font-size: 13px; color: var(--ct-ink); font-weight: 500; }
.legend-note { font-size: 11px; color: var(--ct-ink-3); margin: 4px 0 10px; }
.legend-table-head, .legend-table-row { display: grid; grid-template-columns: minmax(0, 1fr) 78px 50px; gap: 7px; padding: 7px 0; font-size: 11px; color: var(--ct-ink-2); }
.legend-table-head { color: var(--ct-ink-3); border-bottom: 1px solid var(--ct-line); }
.legend-table-row > span:not(:first-child), .legend-table-head > span:not(:first-child) { text-align: right; font-variant-numeric: tabular-nums; }
.legend-name { display: flex; align-items: flex-start; gap: 5px; overflow-wrap: anywhere; min-width: 0; }
.legend-name i { margin-top: 4px; }
</style>
<style>
.customer-traffic-popover { max-width: calc(100vw - 24px); box-sizing: border-box; }
</style>
<style scoped>
@media(max-width:900px) {
  .traffic-mode :deep(.el-segmented__item),.traffic-legend button { min-height:32px; }
  .traffic-mode { flex-wrap:wrap; }
}
</style>
