<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import * as echarts from "echarts/core";
import { BarChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

export interface TokenRankItem { name: string; prompt: number; completion: number }
const props = defineProps<{ items: TokenRankItem[] }>();
const chartEl = ref<HTMLDivElement>();
let chart: echarts.ECharts | undefined;
let observer: ResizeObserver | undefined;
echarts.use([BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

const compact = (value: number) => {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(0)}K`;
  return String(value);
};

async function render() {
  await nextTick();
  if (!chartEl.value || !props.items.length) return;
  chart ??= echarts.init(chartEl.value);
  const items = [...props.items].reverse();
  chart.setOption({
    animationDuration: 300,
    color: ["#2f6fed", "#f08a24"],
    tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, valueFormatter: compact },
    legend: { top: 0, right: 4, data: ["Token In", "Token Out"] },
    grid: { left: 94, right: 142, top: 34, bottom: 22 },
    xAxis: { type: "value", axisLabel: { formatter: compact }, splitLine: { lineStyle: { color: "#edf0f5" } } },
    yAxis: { type: "category", data: items.map(item => item.name), axisTick: { show: false }, axisLine: { show: false }, axisLabel: { width: 82, overflow: "truncate" } },
    series: [
      {
        name: "Token In",
        type: "bar",
        stack: "token",
        barWidth: 15,
        data: items.map(item => item.prompt),
        itemStyle: { borderRadius: [3, 0, 0, 3] },
      },
      {
        name: "Token Out",
        type: "bar",
        stack: "token",
        barWidth: 15,
        data: items.map(item => item.completion),
        itemStyle: { borderRadius: [0, 3, 3, 0] },
        label: {
          show: true,
          position: "right",
          distance: 8,
          formatter: (p: { dataIndex: number }) => {
            const item = items[p.dataIndex];
            const total = item.prompt + item.completion;
            const share = total ? item.completion / total * 100 : 0;
            return `{out|Out ${compact(item.completion)}} {pct|${share.toFixed(1)}%}`;
          },
          rich: {
            out: { color: "#b85c00", fontSize: 10, fontWeight: 700 },
            pct: { color: "#8b95a7", fontSize: 10 },
          },
        },
      },
    ],
  }, true);
}

watch(() => props.items, () => void render(), { deep: true, immediate: true });
watch(chartEl, element => {
  observer?.disconnect();
  if (element) { observer = new ResizeObserver(() => chart?.resize()); observer.observe(element); void render(); }
});
onBeforeUnmount(() => { observer?.disconnect(); chart?.dispose(); });
</script>

<template>
  <div v-if="items.length" ref="chartEl" class="customer-chart-canvas"></div>
  <el-empty v-else :image-size="52" description="暂无 Token 数据" />
</template>
