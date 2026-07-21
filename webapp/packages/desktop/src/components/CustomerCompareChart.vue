<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import * as echarts from "echarts/core";
import { LineChart } from "echarts/charts";
import { DataZoomComponent, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

export interface CustomerCompareSeries { name: string; data: Array<[string, number | null]> }
const props = withDefaults(defineProps<{ series: CustomerCompareSeries[]; unit?: string; threshold?: number; compact?: boolean }>(), { unit: "", compact: false });
const chartEl = ref<HTMLDivElement>();
let chart: echarts.ECharts | undefined;
let observer: ResizeObserver | undefined;
echarts.use([LineChart, DataZoomComponent, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent, CanvasRenderer]);
const colors = ["#2f6fed", "#16a6b6", "#7357d8", "#e47b22", "#36a269"];
const compactNumber = (value: number) => value >= 1_000_000 ? `${(value / 1_000_000).toFixed(1)}M` : value >= 1_000 ? `${(value / 1_000).toFixed(0)}K` : String(Math.round(value));

async function render() {
  await nextTick();
  if (!chartEl.value || !props.series.length) return;
  chart ??= echarts.init(chartEl.value);
  chart.setOption({
    animationDuration: 250,
    color: colors,
    tooltip: {
      trigger: "axis",
      axisPointer: { type: "cross" },
      valueFormatter: (value: unknown) => value == null ? "—" : props.compact ? compactNumber(Number(value)) : `${Number(value).toFixed(2)}${props.unit}`,
    },
    legend: { top: 0, left: 0, right: 0, type: "scroll", data: props.series.map(item => item.name) },
    grid: { left: 56, right: 18, top: 40, bottom: 28 },
    dataZoom: [{ type: "inside", xAxisIndex: 0, filterMode: "none" }],
    xAxis: { type: "time", axisLabel: { hideOverlap: true }, axisLine: { lineStyle: { color: "#dfe4ec" } } },
    yAxis: {
      type: "value",
      min: 0,
      axisLabel: { formatter: (value: number) => props.compact ? compactNumber(value) : `${value}${props.unit}` },
      splitLine: { lineStyle: { color: "#edf0f5" } },
    },
    series: props.series.map((item, index) => ({
      name: item.name,
      type: "line",
      data: item.data,
      smooth: false,
      showSymbol: item.data.filter(([, value]) => value != null).length < 3,
      symbolSize: 5,
      connectNulls: true,
      lineStyle: { width: 1.6 },
      markLine: index === 0 && props.threshold != null ? {
        silent: true,
        symbol: "none",
        lineStyle: { color: "#e47b22", type: "dashed" },
        label: { formatter: `阈值 ${props.threshold}${props.unit}`, color: "#a85d17" },
        data: [{ yAxis: props.threshold }],
      } : undefined,
    })),
  }, true);
}

watch(() => [props.series, props.unit, props.threshold, props.compact], () => void render(), { deep: true, immediate: true });
watch(chartEl, element => { observer?.disconnect(); if (element) { observer = new ResizeObserver(() => chart?.resize()); observer.observe(element); void render(); } });
onBeforeUnmount(() => { observer?.disconnect(); chart?.dispose(); });
</script>

<template>
  <div v-if="series.length" ref="chartEl" class="customer-chart-canvas"></div>
  <el-empty v-else :image-size="52" description="请选择客户进行对比" />
</template>
