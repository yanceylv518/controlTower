<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from "vue";
import * as echarts from "echarts/core";
import { LineChart } from "echarts/charts";
import { GridComponent, TooltipComponent } from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import { cancelChartRender, scheduleChartRender } from "../utils/chartRenderQueue";
import { customerTooltipPosition, highlightCustomerTooltip } from "../utils/customerTooltip";
import { escapeChartText, trafficRGBA, formatTrafficTPM as formatTokens, type TrafficSeries } from "../utils/customerTraffic";

const props = defineProps<{ series: TrafficSeries[]; expanded?: boolean }>();
echarts.use([LineChart, GridComponent, TooltipComponent, SVGRenderer]);
const element = ref<HTMLDivElement>();
const token = {};
let chart: echarts.ECharts | undefined;
let observer: ResizeObserver | undefined;
let activeKey: string | undefined;
let tooltipElement: HTMLElement | undefined;

function focusTooltip(key?: string) {
  if (activeKey === key) return;
  activeKey = key;
  highlightCustomerTooltip(tooltipElement, activeKey);
}

function renderNow() {
  if (!element.value) return;
  if (!chart) {
    chart = echarts.init(element.value, undefined, { renderer: "svg" });
    const onSeries = (event: any) => focusTooltip(event.componentType === "series" ? props.series[event.seriesIndex]?.key : undefined);
    chart.on("mouseover", onSeries);
    chart.on("mousemove", onSeries);
    chart.on("mouseout", () => focusTooltip());
    chart.on("globalout", () => focusTooltip());
  }
  activeKey = undefined;
  const css = getComputedStyle(element.value);
  const muted = css.getPropertyValue("--ct-ink-3").trim() || "#8390a5";
  const line = css.getPropertyValue("--ct-line").trim() || "#e9edf5";
  chart.setOption({
    animation: false,
    grid: { left: 48, right: 12, top: 12, bottom: 27 },
    tooltip: {
      trigger: "axis", triggerOn: "mousemove|click", renderMode: "html",
      confine: false, appendTo: "body", enterable: true,
      showDelay: 0, hideDelay: 80, transitionDuration: 0, displayTransition: false,
      axisPointer: { type: "line", axis: "x", animation: false, snap: true },
      position: (point: number[], _params: unknown, dom: any, _rect: unknown, size: { contentSize: number[] }) => {
        const node = element.value;
        if (!node) return [0, 0];
        tooltipElement = dom;
        highlightCustomerTooltip(tooltipElement, activeKey);
        const placement = customerTooltipPosition(node.getBoundingClientRect(), point, size.contentSize, [document.documentElement.clientWidth, window.innerHeight]);
        if (dom?.style) dom.style.pointerEvents = placement.outside ? "auto" : "none";
        return placement.position;
      },
      backgroundColor: css.getPropertyValue("--ct-surface").trim() || "#fff", borderColor: line,
      textStyle: { fontSize: 11, color: css.getPropertyValue("--ct-ink").trim() || "#263449" },
      extraCssText: "border-radius:8px;max-width:min(340px,calc(100vw - 32px));max-height:min(320px,calc(100vh - 32px));box-sizing:border-box;white-space:normal;overflow-wrap:anywhere;overflow:auto;overscroll-behavior:contain;box-shadow:0 6px 24px rgba(25,43,68,.12)",
      formatter: (params: any) => {
        const items = (Array.isArray(params) ? params : [params]).filter((item: any) => item.value?.[1] != null);
        const total = items.reduce((sum: number, item: any) => sum + Number(item.value[1]), 0);
        const time = items[0]?.value?.[0];
        if (time == null) return "该时段暂无完整拆分数据";
        const exact = (value: number) => value.toLocaleString("zh-CN", { maximumFractionDigits: 2 });
        return [`<div style="margin-bottom:5px">${escapeChartText(new Date(time).toLocaleString())}<br/>总 TPM：${exact(total)}</div>`,
          ...items.sort((a: any, b: any) => b.value[1] - a.value[1]).map((item: any) => {
            const series = props.series[item.seriesIndex];
            return `<div data-traffic-key="${escapeChartText(series?.key || "")}" data-traffic-color="${escapeChartText(series?.color || "#4170cd")}" style="padding:3px 6px;border-left:2px solid transparent;border-radius:3px;line-height:1.5">${item.marker}${escapeChartText(item.seriesName)}：${exact(item.value[1])} · ${total ? (item.value[1] / total * 100).toFixed(1) : "0.0"}%</div>`;
          }),
        ].join("");
      },
    },
    xAxis: { type: "time", min: props.series[0]?.data[0]?.[0], max: props.series[0]?.data.at(-1)?.[0], splitNumber: props.expanded ? 6 : 3, axisTick: { show: false }, axisLine: { lineStyle: { color: line } }, axisLabel: { color: muted, fontSize: 11, hideOverlap: true } },
    yAxis: { type: "value", min: 0, splitNumber: 3, axisLabel: { color: muted, fontSize: 11, formatter: formatTokens }, splitLine: { lineStyle: { color: line, opacity: .65 } } },
    series: props.series.map(item => ({
      id: item.key, name: item.name, type: "line", stack: "customer-tpm", data: item.data,
      smooth: .18, smoothMonotone: "x", connectNulls: false, triggerLineEvent: true,
      showSymbol: item.data.filter(([, value]) => value != null).length < 3, symbolSize: 4,
      itemStyle: { color: item.color }, lineStyle: { width: .8, color: trafficRGBA(item.color, .62) },
      areaStyle: { opacity: 1, color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [{ offset: 0, color: trafficRGBA(item.color, .16) }, { offset: 1, color: trafficRGBA(item.color, .40) }]) },
      emphasis: { focus: "series", lineStyle: { width: 1.4 } },
    })),
  }, true);
}
function highlight(key?: string) {
  focusTooltip(key);
  chart?.dispatchAction({ type: "downplay" });
  if (key) chart?.dispatchAction({ type: "highlight", seriesId: key });
}
defineExpose({ highlight });
watch(() => [props.series, props.expanded], async () => { await nextTick(); scheduleChartRender(token, renderNow); }, { deep: true, immediate: true });
watch(element, node => {
  observer?.disconnect();
  if (node) { observer = new ResizeObserver(() => { chart?.resize(); }); observer.observe(node); scheduleChartRender(token, renderNow); }
});
onBeforeUnmount(() => { cancelChartRender(token); observer?.disconnect(); chart?.dispose(); tooltipElement = undefined; });
</script>

<template><div ref="element" class="traffic-chart" :class="{ expanded }" role="img" aria-label="该客户按固定顺序叠加的 TPM 流量趋势" /></template>
<style scoped>
.traffic-chart { width: 100%; height: 190px; }
.traffic-chart.expanded { height: 320px; }
</style>
