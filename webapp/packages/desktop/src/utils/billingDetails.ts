// 日志详情只读这些字段，避免把动态计费表达式交给不受信任的执行器。
export type DynamicPriceKey = 'p' | 'c' | 'cr' | 'cc' | 'cc1h' | 'img' | 'img_o' | 'ai' | 'ao'

export type DynamicTierCondition = {
  variable: 'p' | 'c' | 'len'
  operator: '<' | '<=' | '>' | '>='
  value: number
}

export type DynamicTier = {
  label: string
  conditions: DynamicTierCondition[]
  prices: Partial<Record<DynamicPriceKey, number>>
}

export type DynamicRequestRule = {
  condition: string
  multiplier: number
  matched: boolean
}

export type DynamicUsageFact = {
  key: string
  value: string | number | boolean
}

export const dynamicPriceFields: readonly { key: DynamicPriceKey; label: string }[] = [
  { key: 'p', label: '输入' },
  { key: 'c', label: '输出' },
  { key: 'cr', label: '缓存读取' },
  { key: 'cc', label: '缓存写入' },
  { key: 'cc1h', label: '缓存写入 (1h)' },
  { key: 'img', label: '图像输入' },
  { key: 'img_o', label: '图像输出' },
  { key: 'ai', label: '音频输入' },
  { key: 'ao', label: '音频输出' },
]

const numericLiteral = '-?(?:\\d+\\.?\\d*|\\.\\d+)(?:[eE][+-]?\\d+)?'
const priceVariables = new Set<DynamicPriceKey>(dynamicPriceFields.map((field) => field.key))

// 兼容浏览器与 Node 测试环境，解码失败时返回空值并由界面展示原始快照。
export function decodeBillingExpression(value: unknown): string {
  if (typeof value !== 'string' || !value.trim()) return ''
  try {
    if (typeof globalThis.atob !== 'function') return ''
    const binary = globalThis.atob(value)
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    return typeof TextDecoder === 'function' ? new TextDecoder().decode(bytes) : String.fromCharCode(...bytes)
  } catch {
    return ''
  }
}

function stripExpressionVersion(expression: string): string {
  const match = /^v\d+:(.*)$/s.exec(expression.trim())
  return match ? match[1].trim() : expression.trim()
}

function parseConditions(raw: string): DynamicTierCondition[] {
  if (!raw.trim()) return []
  const conditions: DynamicTierCondition[] = []
  for (const part of raw.split(/\s*&&\s*/)) {
    const match = new RegExp(`^(p|c|len)\\s*(<=|>=|<|>)\\s*(${numericLiteral})$`).exec(part.trim())
    if (!match) return []
    const value = Number(match[3])
    if (!Number.isFinite(value)) return []
    conditions.push({ variable: match[1] as DynamicTierCondition['variable'], operator: match[2] as DynamicTierCondition['operator'], value })
  }
  return conditions
}

// 解析 New API 生成的普通 tier("label", p * ... + c * ...) 表达式。
// 任务矩阵等带 u("field") 的表达式不在这里臆测，调用方仍可展示原始表达式。
export function parseDynamicTiers(value: unknown): DynamicTier[] {
  if (typeof value !== 'string' || !value.trim()) return []
  const expression = stripExpressionVersion(value)
  const conditionPart = `(?:p|c|len)\\s*(?:<=|>=|<|>)\\s*${numericLiteral}`
  const pattern = new RegExp(`(?:((?:${conditionPart})(?:\\s*&&\\s*${conditionPart})*)\\s*\\?\\s*)?tier\\(\\s*"([^"]*)"\\s*,\\s*([^)]*)\\)`, 'g')
  const tiers: DynamicTier[] = []
  let match: RegExpExecArray | null
  while ((match = pattern.exec(expression)) !== null) {
    const prices: Partial<Record<DynamicPriceKey, number>> = {}
    const terms = match[3].split('+')
    for (const term of terms) {
      const priceMatch = new RegExp(`\\b(${Array.from(priceVariables).join('|')})\\s*\\*\\s*(${numericLiteral})`, 'i').exec(term.trim())
      if (!priceMatch) continue
      const price = Number(priceMatch[2])
      if (Number.isFinite(price)) prices[priceMatch[1].toLowerCase() as DynamicPriceKey] = price
    }
    tiers.push({ label: match[2], conditions: parseConditions(match[1] || ''), prices })
  }
  return tiers
}

export function normalizeDynamicLabel(value: string | undefined): string {
  return (value || '').replace(/\s+/g, '').toLocaleLowerCase()
}

export function dynamicTierMatched(tier: DynamicTier, matchedLabel: string | undefined): boolean {
  const expected = normalizeDynamicLabel(matchedLabel)
  return expected !== '' && normalizeDynamicLabel(tier.label) === expected
}

// request_rules 已经由 New API 写入条件文本，保留原条件可避免把未知字段翻译错。
export function normalizeDynamicRequestRules(value: unknown): DynamicRequestRule[] {
  let source: unknown = value
  if (typeof source === 'string') {
    try { source = JSON.parse(source) } catch { return [] }
  }
  if (!Array.isArray(source)) return []
  return source.flatMap((item) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return []
    const record = item as Record<string, unknown>
    const condition = typeof record.cond === 'string' ? record.cond.trim() : typeof record.condition === 'string' ? record.condition.trim() : ''
    const multiplier = Number(record.multiplier)
    if (!condition || !Number.isFinite(multiplier)) return []
    return [{ condition, multiplier, matched: record.matched === true }]
  }).slice(0, 100)
}

export function normalizeDynamicUsageFacts(value: unknown): DynamicUsageFact[] {
  let source: unknown = value
  if (typeof source === 'string') {
    try { source = JSON.parse(source) } catch { return [] }
  }
  if (!source || typeof source !== 'object' || Array.isArray(source)) return []
  return Object.entries(source as Record<string, unknown>).flatMap(([key, raw]) => {
    if (!key || (typeof raw !== 'string' && typeof raw !== 'number' && typeof raw !== 'boolean')) return []
    return [{ key, value: raw }]
  }).slice(0, 100)
}

// 阈值按 rc35 的 K/M 简写显示，长上下文条件在弹窗中仍保持可扫描。
export function formatDynamicCondition(condition: DynamicTierCondition): string {
  const labels: Record<DynamicTierCondition['variable'], string> = { p: '输入', c: '输出', len: '长度' }
  const operators: Record<DynamicTierCondition['operator'], string> = { '<': '<', '<=': '≤', '>': '>', '>=': '≥' }
  const value = condition.value >= 1_000_000
    ? `${condition.value / 1_000_000}M`
    : condition.value >= 1_000
      ? `${condition.value / 1_000}K`
      : String(condition.value)
  return `${labels[condition.variable]} ${operators[condition.operator]} ${value}`
}
