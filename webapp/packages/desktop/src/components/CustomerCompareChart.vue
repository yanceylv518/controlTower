<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import * as echarts from "echarts/core";
import { LineChart } from "echarts/charts";
import { DataZoomComponent, GridComponent, LegendComponent, MarkLineComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

export interface CustomerCompareSeries { name: string; data: Array<[string, number | null]> }
export interface CustomerCompareThreshold { name: string; value: number; color: string }
const props = withDefaults(defineProps<{ series: CustomerCompareSeries[]; unit?: string; thresholds?: CustomerCompareThreshold[]; compact?: boolean }>(), { unit: "", compact: false, thresholds: () => [] });
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
  const observedMax = Math.max(...props.series.flatMap(item => item.data.map(([, value]) => value ?? 0)), 0);
  const configuredThresholdValues = props.thresholds.map(item => item.value).sort((a, b) => a - b);
  const activeThresholds = props.thresholds.filter(item => item.value <= observedMax);
  const thresholdValues = activeThresholds.map(item => item.value).sort((a, b) => a - b);
  const highestConfiguredThreshold = configuredThresholdValues.length ? Math.max(...configuredThresholdValues) : undefined;
  const highestVisibleThreshold = thresholdValues.length ? Math.max(...thresholdValues) : undefined;
  const hardCap = highestConfiguredThreshold == null ? undefined : highestConfiguredThreshold + 10;
  const actualCap = hardCap == null || highestVisibleThreshold == null
    ? undefined
    : Math.min(hardCap, Math.max(highestVisibleThreshold + 1, Math.ceil(observedMax * 1.1)));
  const yMax = actualCap == null ? undefined : thresholdValues.length + 1;
  const scaleValue = (value: number) => {
    if (actualCap == null) return value;
    const stops = [0, ...thresholdValues, actualCap];
    const capped = Math.min(value, actualCap);
    for (let index = 1; index < stops.length; index++) {
      if (capped <= stops[index]) {
        const width = stops[index] - stops[index - 1];
        return index - 1 + (width > 0 ? (capped - stops[index - 1]) / width : 0);
      }
    }
    return stops.length - 1;
  };
  const displaySeries = props.series.map(item => {
    const sparse = item.data.filter(([, value]) => value != null).length < 3;
    return {
      ...item,
      sparse,
      data: yMax == null ? item.data : item.data.map(([time, rawValue]) => {
        const clipped = rawValue != null && actualCap != null && rawValue > actualCap;
        return {
          value: [time, rawValue == null ? null : scaleValue(rawValue)],
          rawValue,
          clipped,
          symbol: sparse ? "circle" : "none",
          symbolSize: 5,
        };
      }),
    };
  });
  chart.setOption({
    animationDuration: 250,
    color: colors,
    tooltip: yMax == null ? {
      trigger: "axis",
      axisPointer: { type: "cross" },
      valueFormatter: (value: unknown) => value == null ? "—" : props.compact ? compactNumber(Number(value)) : `${Number(value).toFixed(2)}${props.unit}`,
    } : {
      trigger: "axis",
      axisPointer: { type: "cross" },
      formatter: (params: any) => {
        const items = Array.isArray(params) ? params : [params];
        const title = items[0]?.axisValueLabel || "";
        const rows = items.map((item: any) => {
          const rawValue = item.data?.rawValue;
          const value = rawValue == null ? "—" : `${Number(rawValue).toFixed(2)}${props.unit}`;
          return `${item.marker}${item.seriesName}: ${value}`;
        });
        return [title, ...rows].join("<br/>");
      },
    },
    legend: { top: 0, left: 0, right: 0, type: "scroll", data: props.series.map(item => item.name) },
    grid: { left: 56, right: 18, top: 40, bottom: 28 },
    dataZoom: [{ type: "inside", xAxisIndex: 0, filterMode: "none" }],
    xAxis: { type: "time", axisLabel: { hideOverlap: true }, axisLine: { lineStyle: { color: "#dfe4ec" } } },
    yAxis: {
      type: "value",
      min: 0,
      max: yMax,
      interval: yMax == null ? undefined : 1,
      axisLabel: { formatter: (value: number) => {
        if (props.compact) return compactNumber(value);
        if (actualCap == null) return `${value}${props.unit}`;
        const labels = [0, ...thresholdValues, actualCap];
        const actualValue = labels[Math.round(value)];
        const capped = hardCap != null && actualCap === hardCap;
        return actualValue == null ? "" : `${actualValue}${props.unit}${capped && value >= yMax! ? "+" : ""}`;
      } },
      splitLine: { lineStyle: { color: "#edf0f5" } },
    },
    series: displaySeries.map((item, index) => ({
      name: item.name,
      type: "line",
      data: item.data,
      smooth: false,
      showSymbol: yMax != null || item.sparse,
      symbolSize: 5,
      connectNulls: true,
      lineStyle: { width: 1.6 },
      markLine: index === 0 && activeThresholds.length ? {
        silent: true,
        symbol: "none",
        lineStyle: { type: "dashed", width: 1, opacity: .5 },
        label: { show: false },
        data: activeThresholds.map(item => ({
          name: item.name,
          yAxis: thresholdValues.indexOf(item.value) + 1,
          lineStyle: { color: item.color, type: "dashed", width: 1, opacity: .5 },
        })),
      } : undefined,
    })),
  }, true);
}

watch(() => [props.series, props.unit, props.thresholds, props.compact], () => void render(), { deep: true, immediate: true });
watch(chartEl, element => { observer?.disconnect(); if (element) { observer = new ResizeObserver(() => chart?.resize()); observer.observe(element); void render(); } });
onBeforeUnmount(() => { observer?.disconnect(); chart?.dispose(); });
</script>

<template>
  <div v-if="series.length" ref="chartEl" class="customer-chart-canvas"></div>
  <el-empty v-else :image-size="52" description="请选择客户进行对比" />
</template>
