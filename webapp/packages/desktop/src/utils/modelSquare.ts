export interface SquareModel {
  model_name: string; description?: string; tags?: string; vendor_id?: number; owner_by?: string;
  quota_type: number; model_ratio: number | null; model_price: number | null; completion_ratio: number | null;
  cache_ratio?: number; create_cache_ratio?: number; image_ratio?: number; audio_ratio?: number; audio_completion_ratio?: number;
  enable_groups: string[]; supported_endpoint_types: string[]; billing_mode?: string; billing_expr?: string;
}
export interface ModelSquare {
  items: SquareModel[]; vendors: Array<{id:number; name:string; description?:string}> | null;
  group_ratios: Record<string,number>; usable_groups: Record<string,string> | null;
  updated_at: string; expires_at: string; stale: boolean; warning?: string; source: string;
}
const valid = (n: unknown): n is number => typeof n === 'number' && Number.isFinite(n) && n >= 0
export interface SquareCurrency { type: string; symbol: string; price_multiplier: number }
export function displayPrice(value: number | null, currency: SquareCurrency | null): string {
  if (value === null || !valid(value) || !currency || !valid(currency.price_multiplier) || currency.price_multiplier === 0) return '—'
  const converted = value * currency.price_multiplier
  return Number.isFinite(converted) ? priceText(converted) : '—'
}
export function currencyUnit(currency: SquareCurrency | null): string {
  return currency ? (currency.type === 'TOKENS' ? '额度' : currency.type === 'CUSTOM' ? currency.symbol : currency.type) : '币种未加载'
}

export interface ExpressionTier { label: string; prices: Array<{label: string; value: number}>; unit: string; raw: string }
// Read literal prices only. Never execute upstream expressions or infer a fixed price
// from arithmetic, request multipliers, task usage, or unsupported functions.
export function expressionTiers(expression: string | undefined): ExpressionTier[] {
  if (!expression) return []
  const fields: Record<string,string> = {p:'输入',c:'输出',cr:'缓存读取',cc:'缓存写入',cc1h:'缓存写入 (1h)',img:'图像输入',img_o:'图像输出',ai:'音频输入',ao:'音频输出'}
  const number = '(?:\\d+\\.?\\d*|\\.\\d+)(?:[eE][+-]?\\d+)?'
  const tiers: ExpressionTier[] = []
  const pattern = /\btier\(\s*("(?:[^"\\]|\\.)*")\s*,/g
  let match: RegExpExecArray | null
  while ((match = pattern.exec(expression))) {
    let depth = 1, end = pattern.lastIndex, quoted = false, escaped = false
    for (; end < expression.length; end++) {
      const char = expression[end]
      if (quoted) { if (escaped) escaped = false; else if (char === '\\') escaped = true; else if (char === '"') quoted = false; continue }
      if (char === '"') quoted = true
      else if (char === '(') depth++
      else if (char === ')' && --depth === 0) break
    }
    if (depth !== 0) break
    const raw = expression.slice(pattern.lastIndex,end).trim()
    pattern.lastIndex = end + 1
    let label: string
    try { label = JSON.parse(match[1]) } catch { continue }
    const tier: ExpressionTier = {label,prices:[],unit:'1M Tokens',raw}
    const fixed = new RegExp(`^fixed\\(\\s*(${number})\\s*\\)$`).exec(raw)
    if (fixed) {
      if (valid(Number(fixed[1]))) tier.prices.push({label:'每次调用',value:Number(fixed[1])})
      tier.unit = '次'
    } else {
      const term = new RegExp(`\\s*(p|c|cr|cc|cc1h|img|img_o|ai|ao)\\s*\\*\\s*(${number})\\s*(?:\\+|$)`, 'gy')
      const seen = new Set<string>()
      let part: RegExpExecArray | null, consumed = 0
      while ((part = term.exec(raw))) {
        if (!valid(Number(part[2])) || seen.has(part[1])) break
        seen.add(part[1]); tier.prices.push({label:fields[part[1]],value:Number(part[2])}); consumed = term.lastIndex
      }
      if (consumed !== raw.length || raw.endsWith('+')) tier.prices = []
    }
    tiers.push(tier)
  }
  return tiers
}
export function pricingMode(model: SquareModel): 'token' | 'request' | 'expression' | 'unknown' {
  if (model.billing_expr || (model.billing_mode && !['tokens','token','ratio'].includes(model.billing_mode))) return 'expression'
  return model.quota_type === 0 ? 'token' : model.quota_type === 1 ? 'request' : 'unknown'
}
// NewAPI pricing frontend uses model_ratio * 2 USD per million tokens.
export function modelPrice(model: SquareModel, field: 'input'|'output'|'cache'|'write', groupRatio = 1): number | null {
  if (!valid(groupRatio)) return null
  const mode = pricingMode(model)
  if (mode === 'request') {
    const result = field === 'input' && valid(model.model_price) ? model.model_price * groupRatio : null
    return result !== null && Number.isFinite(result) ? result : null
  }
  if (mode !== 'token' || !valid(model.model_ratio)) return null
  const multiplier = field === 'input' ? 1 : field === 'output' ? model.completion_ratio : field === 'cache' ? model.cache_ratio : model.create_cache_ratio
  if (!valid(multiplier)) return null
  const result = model.model_ratio * 2 * multiplier * groupRatio
  return Number.isFinite(result) ? result : null
}
export function priceText(value: number | null): string {
  return value === null ? '—' : new Intl.NumberFormat('en-US',{maximumSignificantDigits:8}).format(value)
}
