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
