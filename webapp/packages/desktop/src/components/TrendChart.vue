<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from "vue";
import * as echarts from "echarts/core";
import { BarChart, LineChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

export interface TrendSeries {
  name: string;
  color: string;
  data: Array<[string, number | null | undefined]>;
  unit?: string;
  type?: "line" | "bar";
  sparse?: boolean;
}
const props = withDefaults(
  defineProps<{ title: string; series: TrendSeries[]; percent?: boolean }>(),
  { percent: false },
);
const chartEl = ref<HTMLDivElement>();
const hasData = computed(() =>
  props.series.some((item) => item.data.some(([, value]) => value != null)),
);
let chart: echarts.ECharts | undefined;
let observer: ResizeObserver | undefined;

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
    chart?.dispose();
    chart = undefined;
    return;
  }
  await nextTick();
  if (!chartEl.value) return;
  chart ??= echarts.init(chartEl.value);
  chart.setOption(
    {
      animationDuration: 250,
      color: props.series.map((item) => item.color),
      tooltip: { trigger: "axis" },
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
        smooth: item.type !== "bar",
        data: item.data,
        tooltip: {
          valueFormatter: (value: unknown) =>
            value == null ? "—" : `${value}${item.unit || ""}`,
        },
      })),
    },
    true,
  );
}

watch(
  () => props.series,
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
  observer?.disconnect();
  chart?.dispose();
});
</script>

<template>
  <section class="trend-chart">
    <h3>{{ title }}</h3>
    <div v-if="hasData" ref="chartEl" class="trend-chart-canvas"></div>
    <el-empty v-else :image-size="52" description="暂无趋势数据" />
  </section>
</template>
