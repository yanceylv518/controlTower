import type { StatRow } from './archiveAnalysis'
import { historicalPrice, type ArchiveCurrency } from './archiveMoney'

export const anomalyKeys = ['log_rows', 'consumption', 'empty_output', 'missing_output', 'error', 'charged_empty_output'] as const
type Counts = Record<typeof anomalyKeys[number], bigint>
export type AnomalyHour = Counts & { hour: number }
const emptyCounts = (): Counts => Object.fromEntries(anomalyKeys.map(key => [key, 0n])) as Counts
function count(value: unknown) {
  if (typeof value !== 'string' || !/^\d+$/.test(value)) throw new Error('归档计数格式无效，请重新查询')
  return BigInt(value)
}
export function anomalySummary(items: Record<string, unknown>[]) {
  const hours: AnomalyHour[] = Array.from({ length: 24 }, (_, hour) => ({ hour, ...emptyCounts() }))
  const seen = new Set<number>(), total = emptyCounts()
  for (const item of items) {
    const hour = Number(item.hour)
    if (!/^\d{1,2}$/.test(String(item.hour)) || hour < 0 || hour > 23 || seen.has(hour)) throw new Error('归档小时数据无效')
    seen.add(hour)
    const row = hours[hour]
    for (const key of anomalyKeys) row[key] = count(item[key])
    if (row.empty_output + row.missing_output > row.consumption || row.charged_empty_output > row.empty_output || row.consumption + row.error > row.log_rows) throw new Error('归档计数不一致')
    for (const key of anomalyKeys) total[key] += row[key]
  }
  return { total, hours }
}
export function percent(numerator: bigint, denominator: bigint) {
  if (!denominator) return '—'
  const hundredths = (numerator * 10000n + denominator / 2n) / denominator
  return `${hundredths / 100n}.${String(hundredths % 100n).padStart(2, '0')}%`
}
function canonical(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'null'
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`
  return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([k,v]) => `${JSON.stringify(k)}:${canonical(v)}`).join(',')}}`
}
// Prices are references from historical evidence, never applied again to quota.
export function historicalPrices(rows: StatRow[], currency?: ArchiveCurrency) {
  const groups = new Map<string, { model: string; first: string; last: string; requests: bigint; evidence: Record<string, unknown> }>()
  for (const row of rows) {
    if (String(row.dimensions.type) !== '2') continue
    const model = String(row.dimensions.model_name ?? '未知模型'), raw = row.dimensions.pricing
    const evidence = raw && typeof raw === 'object' && !Array.isArray(raw) ? raw as Record<string, unknown> : {}
    const key = canonical([model, evidence]), requests = count(String(row.amounts.requests ?? row.amounts.log_rows ?? '0'))
    const old = groups.get(key)
    if (old) { old.requests += requests; if (row.date < old.first) old.first = row.date; if (row.date > old.last) old.last = row.date }
    else groups.set(key, { model, evidence, requests, first: row.date, last: row.date })
  }
  return [...groups].map(([key, row]) => {
    const p = row.evidence
    const expression = ['billing_expr','expr','expr_string','expr_b64'].some(k => p[k] != null && p[k] !== '') || (p.billing_mode != null && !['tokens','token','ratio','request','per_request'].includes(String(p.billing_mode)))
    return { ...row, key, evidenceText: JSON.stringify(p, null, 2),
      mode: !Object.keys(p).length ? '缺少历史价格' : expression ? '表达式 / 自定义计费' : '历史价格参数',
      input: expression ? '—' : historicalPrice(currency,'token',p.model_ratio), output: expression ? '—' : historicalPrice(currency,'token',p.model_ratio,p.completion_ratio),
      cache: expression ? '—' : historicalPrice(currency,'token',p.model_ratio,p.cache_ratio), request: expression ? '—' : historicalPrice(currency,'request',p.model_price),
    }
  }).sort((a,b) => a.model.localeCompare(b.model) || a.first.localeCompare(b.first) || a.key.localeCompare(b.key))
}
