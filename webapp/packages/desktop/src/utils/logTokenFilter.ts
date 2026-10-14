import type { ReadonlyLogTokenQuery } from '@ct/shared'

export interface LogTokenFilterValue {
  mode: 'input' | 'output' | 'both'
  input: string
  output: string
}

export const emptyLogTokenFilter = (): LogTokenFilterValue => ({ mode: 'input', input: '', output: '' })

const fields = [
  { key: 'input', label: '输入', minParam: 'prompt_tokens_min', maxParam: 'prompt_tokens_max' },
  { key: 'output', label: '输出', minParam: 'completion_tokens_min', maxParam: 'completion_tokens_max' },
] as const

export const logTokenFilterFields = (value: LogTokenFilterValue) => fields.filter(field => value.mode === 'both' || value.mode === field.key)
export const hasLogTokenFilter = (value: LogTokenFilterValue) => logTokenFilterFields(value).some(field => value[field.key].trim() !== '')

function tokenCount(raw: string) {
  const text = raw.trim(), value = Number(text)
  return /^\d+$/.test(text) && Number.isSafeInteger(value) && value >= 0 ? value : undefined
}

export function validateLogTokenFilter(value: LogTokenFilterValue): string {
  for (const field of logTokenFilterFields(value)) {
    if (value[field.key].trim() && tokenCount(value[field.key]) === undefined) {
      return `${field.label} Tokens 必须是 0～${Number.MAX_SAFE_INTEGER} 的整数`
    }
  }
  return ''
}

export function logTokenFilterParams(value: LogTokenFilterValue): ReadonlyLogTokenQuery {
  const params: ReadonlyLogTokenQuery = {}
  // 切换到单项筛选后不发送另一项保留的草稿，避免隐藏条件继续影响结果。
  for (const field of logTokenFilterFields(value)) {
    const count = tokenCount(value[field.key])
    params[field.minParam] = count
    params[field.maxParam] = count
  }
  return params
}

export function logTokenFilterSummary(value: LogTokenFilterValue): string {
  if (!hasLogTokenFilter(value)) return 'Tokens 不限'
  return logTokenFilterFields(value).map(field => {
    const count = tokenCount(value[field.key])
    return `${field.label} ${count !== undefined ? `= ${count.toLocaleString('zh-CN')}` : '不限'}`
  }).join(' · ')
}
