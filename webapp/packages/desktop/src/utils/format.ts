export const formatNumber = (value: number | null | undefined) => value == null ? '—' : new Intl.NumberFormat('zh-CN').format(value)
export const formatPercent = (value: number | null | undefined, digits = 1) => value == null ? '—' : `${(value * 100).toFixed(digits)}%`
export const formatSeconds = (value: number | null | undefined) => value == null ? '—' : `${value.toFixed(2)}s`
export function formatBytes(value: number | null | undefined) {
  if (value == null) return '—'; const units = ['B', 'KB', 'MB', 'GB', 'TB']; let size = value; let index = 0
  while (size >= 1024 && index < units.length - 1) { size /= 1024; index++ }
  return `${size.toFixed(index ? 1 : 0)} ${units[index]}`
}
export function formatTime(value: string | null | undefined) {
  if (!value) return '—'; const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}
export function formatTokens(value: number | null | undefined) {
  if (value == null) return '—'
  if (Math.abs(value) >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`
  return new Intl.NumberFormat('zh-CN').format(value)
}
export function formatQuota(value: number | null | undefined, perUnit: number, symbol: string) {
  if (value == null) return '—'
  const amount = value / (perUnit || 500000)
  return `${symbol}${amount >= 100 ? amount.toFixed(0) : amount.toFixed(2)}`
}
