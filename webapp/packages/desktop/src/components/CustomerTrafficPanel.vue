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
}>();
const emit = defineEmits<{ dimension: [value: TrafficDimension]; retry: [] }>();
const chart = ref<InstanceType<typeof CustomerTrafficChart>>();
const moreVisible = ref(false);
const hasPlot = computed(() => props.traffic.series.length > 0 && props.traffic.coveredMinutes > 0);
const dimensionLabel = computed(() => props.dimension === "model" ? "模型" : props.dimension === "user" ? "客户" : "渠道");
watch(() => [props.dimension, props.active], () => { moreVisible.value = false; });
</script>

<template>
  <div class="traffic-panel">
    <div class="traffic-mode"><span>流量构成</span><span v-if="dimension === 'user'">按客户 · TPM</span><el-segmented v-else :model-value="dimension" @update:model-value="emit('dimension', $event as TrafficDimension)" :options="[{ label: '按模型', value: 'model' }, { label: '按渠道', value: 'channel' }]" size="small" /></div>
    <div v-if="hasPlot" class="traffic-legend" :aria-label="`按流量排序的${dimensionLabel}图例`">
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
    <div v-loading="loading && !hasPlot" class="traffic-content">
      <CustomerTrafficChart v-if="hasPlot && active" ref="chart" :series="traffic.series" :expanded="expanded" />
      <div v-else-if="hasPlot" :style="{ height: expanded ? '320px' : '190px' }" />
      <div v-else class="traffic-empty">{{ loading ? '正在读取流量构成…' : emptyText }}</div>
    </div>
    <div class="traffic-caption">
      <span title="最近结束的时段会随采集上报继续补齐">近 {{ hours }} 小时 · {{ bucketMinutes }}分钟粒度</span>
      <span v-if="lastPlotTime" title="最后一段总量与拆分对齐、实际绘制的时间区间；可能早于顶部最近一分钟。24 小时视图的曲线为 5 分钟均值。">曲线最后有效：{{ lastPlotTime }}</span>
      <span v-if="hasPlot && traffic.incompleteBuckets" title="总量与拆分未对齐的时段留空；不将缺失流量填成零">部分时段拆分未齐</span>
    </div>
  </div>
</template>

<style scoped>
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
