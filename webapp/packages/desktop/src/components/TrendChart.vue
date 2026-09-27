<script setup lang="ts">
import { withChartTheme } from "../utils/chartTheme";
import { useTheme } from "../composables/useTheme";
const { themeSignature } = useTheme();
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import * as echarts from "echarts/core";
import { BarChart, LineChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";
import {
  cancelChartRender,
  scheduleChartRender,
} from "../utils/chartRenderQueue";

export interface TrendSeries {
  name: string;
  color: string;
  data: Array<[string, number | null | undefined]>;
  unit?: string;
  type?: "line" | "bar";
  sparse?: boolean;
  smooth?: boolean;
}
const props = withDefaults(
  defineProps<{ title: string; series: TrendSeries[]; percent?: boolean; contextSeries?: TrendSeries[] }>(),
  { percent: false },
);
const chartEl = ref<HTMLDivElement>();
const hasData = computed(() =>
  props.series.some((item) => item.data.some(([, value]) => value != null)),
);
let chart: echarts.ECharts | undefined;
let observer: ResizeObserver | undefined;
const renderToken = {};

echarts.use([
  LineChart,
  BarChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  CanvasRenderer,
]);

async function render() {
  if (!hasData.value) {
    cancelChartRender(renderToken);
    chart?.dispose();
    chart = undefined;
    return;
  }
  await nextTick();
  if (!chartEl.value) return;
  scheduleChartRender(renderToken, renderNow);
}

function renderNow() {
  if (!chartEl.value || !hasData.value) return;
  const initial = !chart;
  chart ??= echarts.init(chartEl.value);
  chart.setOption(
    withChartTheme({
      animationDuration: initial ? 150 : 0,
      color: props.series.map((item) => item.color),
      tooltip: { trigger: "axis", ...(props.contextSeries?.length ? { formatter: (params: any) => {
        const values = Array.isArray(params) ? params : [params];
        const time = Date.parse(String(values[0]?.data?.[0] ?? ""));
        const escape = (v: unknown) => String(v).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]!));
        const context = props.contextSeries!.map(s => {const point=s.data.find(([t])=>Date.parse(t)===time);return `${escape(s.name)}：${point?.[1] == null ? '—' : escape(point[1])}`;});
        return [escape(values[0]?.axisValueLabel || ''), ...context, ...values.map((v:any)=>`${v.marker}${escape(v.seriesName)}：${v.data?.[1] == null ? '—' : escape(v.data[1])}`)].join('<br/>');
      }} : {}) },
      legend: { top: 0, right: 0, data: props.series.map((item) => item.name) },
      grid: { left: 44, right: 18, top: 38, bottom: 28 },
      xAxis: { type: "time", axisLabel: { hideOverlap: true } },
      yAxis: {
        type: "value",
        min: props.percent ? 0 : undefined,
        max: props.percent ? 100 : undefined,
        axisLabel: { formatter: props.percent ? "{value}%" : "{value}" },
      },
      series: props.series.map((item) => ({
        name: item.name,
        type: item.type || "line",
        showSymbol: item.sparse === true,
        symbolSize: item.sparse ? 5 : undefined,
        connectNulls: item.sparse === true,
        smooth: item.smooth ?? item.type !== "bar",
        data: item.data,
        tooltip: {
          valueFormatter: (value: unknown) =>
            value == null ? "—" : `${value}${item.unit || ""}`,
        },
      })),
    }),
    true,
  );
}

watch(
  () => [props.series, props.contextSeries],
  () => void render(),
  { deep: true, immediate: true },
);
watch(
  () => props.percent,
  () => void render(),
);
watch(chartEl, (element) => {
  observer?.disconnect();
  if (element) {
    observer = new ResizeObserver(() => chart?.resize());
    observer.observe(element);
  }
});
onBeforeUnmount(() => {
  cancelChartRender(renderToken);
  observer?.disconnect();
  chart?.dispose();
});
watch(themeSignature, () => { void render(); }, { flush: "post" });
</script>

<template>
  <section class="trend-chart">
    <header class="trend-header"><h3>{{ title }}</h3><slot name="actions" /></header>
    <div v-if="hasData" ref="chartEl" class="trend-chart-canvas"></div>
    <el-empty v-else :image-size="52" description="暂无趋势数据" />
  </section>
</template>

<style scoped>.trend-header{display:flex;align-items:center;justify-content:space-between;gap:8px;flex-wrap:wrap}.trend-header h3{margin-right:auto}</style>
