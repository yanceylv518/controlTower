import type { EChartsCoreOption } from 'echarts/core'

function colors() {
  const css = getComputedStyle(document.documentElement)
  const read = (name: string) => css.getPropertyValue(`--ct-${name}`).trim()
  return { font: css.fontFamily || 'sans-serif', ink: read('ink'), muted: read('ink-3'), line: read('line'), surface: read('surface'), bg: read('bg'), accent: read('chart-blue') }
}

function luminance(rgb: number[]) {
  const [r, g, b] = rgb.map(v => { v /= 255; return v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4 })
  return .2126 * r + .7152 * g + .0722 * b
}

/** Preserve a series' hue, lifting/darkening only when it is too faint on the surface. */
export function chartColor(color: string, background = colors().surface): string {
  const parse = (value: string) => {
    const hex = value.replace('#', '')
    if (!/^(?:[\da-f]{3}|[\da-f]{6})$/i.test(hex)) return undefined
    const full = hex.length === 3 ? [...hex].map(c => c + c).join('') : hex
    return [0, 2, 4].map(i => parseInt(full.slice(i, i + 2), 16))
  }
  const rgb = parse(color), base = parse(background)
  if (!rgb || !base) return color
  const bg = luminance(base)
  const target = bg < .3 ? 255 : 0
  for (let i = 0; i <= 20; i++) {
    const mixed = rgb.map(v => Math.round(v + (target - v) * i / 20))
    const fg = luminance(mixed)
    if ((Math.max(bg, fg) + .05) / (Math.min(bg, fg) + .05) >= 3) return '#' + mixed.map(v => v.toString(16).padStart(2, '0')).join('')
  }
  return color
}

/** Shared defaults are applied on every render so existing charts switch immediately. */
export function withChartTheme(option: EChartsCoreOption): EChartsCoreOption {
  const c = colors()
  const axis = (value: any) => {
    const apply = (a: any) => ({ ...a, axisLabel: { ...a.axisLabel, color: c.muted, fontFamily: c.font }, axisLine: { ...a.axisLine, lineStyle: { ...a.axisLine?.lineStyle, color: c.line } }, splitLine: { ...a.splitLine, lineStyle: { ...a.splitLine?.lineStyle, color: c.line } }, axisPointer: { ...a.axisPointer, label: { ...a.axisPointer?.label, color: c.ink, fontFamily: c.font, backgroundColor: c.surface } } })
    return Array.isArray(value) ? value.map(apply) : value ? apply(value) : value
  }
  const legend = (v: any) => ({ ...v, textStyle: { ...v?.textStyle, color: c.muted, fontFamily: c.font }, pageTextStyle: { color: c.muted, fontFamily: c.font }, pageIconColor: c.muted, pageIconInactiveColor: c.line, inactiveColor: c.muted })
  const tooltip = option.tooltip as any
  return {
    ...option,
    backgroundColor: 'transparent',
    textStyle: { ...option.textStyle, color: c.ink, fontFamily: c.font },
    color: Array.isArray(option.color) ? option.color.map(v => typeof v === 'string' ? chartColor(v, c.surface) : v) : [c.accent],
    xAxis: axis(option.xAxis), yAxis: axis(option.yAxis),
    legend: Array.isArray(option.legend) ? option.legend.map(legend) : option.legend ? legend(option.legend) : undefined,
    tooltip: tooltip ? { ...tooltip, backgroundColor: c.surface, borderColor: c.line, textStyle: { ...tooltip.textStyle, color: c.ink, fontFamily: c.font }, axisPointer: { ...tooltip.axisPointer, lineStyle: { color: c.muted, fontFamily: c.font }, crossStyle: { color: c.muted, fontFamily: c.font }, label: { color: c.ink, fontFamily: c.font, backgroundColor: c.surface } } } : undefined,
    series: Array.isArray(option.series) ? option.series.map((series: any) => ({
      ...series,
      ...(series.itemStyle?.color && typeof series.itemStyle.color === 'string' ? { itemStyle: { ...series.itemStyle, color: chartColor(series.itemStyle.color, c.surface) } } : {}),
      ...(series.lineStyle?.color && typeof series.lineStyle.color === 'string' ? { lineStyle: { ...series.lineStyle, color: chartColor(series.lineStyle.color, c.surface) } } : {}),
    })) : option.series,
  }
}
