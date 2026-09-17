import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('../src/views/ReadonlyLogsView.vue', import.meta.url), 'utf8')
const detailsSource = readFileSync(new URL('../src/utils/billingDetails.ts', import.meta.url), 'utf8')
const detailsCode = ts.transpileModule(detailsSource, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { dynamicBillingSummary, decodeBillingExpression } = await import('data:text/javascript;base64,' + Buffer.from(detailsCode).toString('base64'))
const functions = ['first', 'numberValue', 'textValue', 'effectiveGroupRatio', 'groupRatioLabel', 'isTieredBilling', 'dynamicExpression', 'billingSummary']
  .map((name) => {
    const match = source.match(new RegExp(`function ${name}\\([^]*?\\n\\}`))
    assert.ok(match, `missing ${name}`)
    return match[0]
  }).join('\n')
const code = ts.transpileModule(functions, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
const summary = new Function('dynamicBillingSummary', 'decodeBillingExpression', `
  const extra = row => JSON.parse(row.other || '{}');
  const isConsume = row => row.type === 2;
  const isPerCallBilling = value => value !== undefined && value > 0;
  const billingPrice = value => '¥' + Number(value.toFixed(6));
  const ratioText = value => value + 'x';
  const cacheReadTokens = row => numberValue(row, 'cache_tokens') || 0;
  const cacheWriteTokens = row => (numberValue(row, 'cache_creation_tokens_5m') || 0) + (numberValue(row, 'cache_creation_tokens_1h') || 0) || numberValue(row, 'cache_creation_tokens') || 0;
  const cacheWrite1hTokens = row => numberValue(row, 'cache_creation_tokens_1h') || 0;
  ${code}
  return billingSummary;
`)(dynamicBillingSummary, decodeBillingExpression)
const row = (other, type = 2) => ({ type, quota: 206, content_summary: '原始摘要', other: JSON.stringify(other) })

test('dynamic billing does not expose zero placeholder rates as standard prices', () => {
  assert.equal(summary(row({ billing_mode: 'tiered_expr', model_ratio: 0, completion_ratio: 0 })), '动态计费 · 无匹配结果')
  assert.equal(summary(row({ billing_mode: 'tiered_expr', model_price: 2, model_ratio: 1 })), '动态计费 · 无匹配结果')
  assert.equal(summary(row({ billing_mode: 'tiered_expr' })), '动态计费 · 无匹配结果')
})

test('standard, genuinely free, per-call and error summaries retain their meaning', () => {
  assert.equal(summary(row({ model_ratio: 4, completion_ratio: 2 })), '标准 · ¥8 / ¥16/1M')
  assert.equal(summary(row({ model_ratio: 0, completion_ratio: 1 })), '标准 · ¥0 / ¥0/1M')
  assert.equal(summary(row({ model_price: 2 })), '按次 · ¥2')
  assert.equal(summary(row({ billing_mode: 'tiered_expr' }, 5)), '原始摘要')
})

const expression = 'v1:p <= 32000 ? tier("≤32K", p * 1 + c * 4 + cr * 0.1 + cc * 1.25 + cc1h * 2) : tier(">32K", p * 2 + c * 8 + cr * 0.2)'
const snapshot = (overrides = {}) => ({
  billing_mode: 'tiered_expr', model_ratio: 0, completion_ratio: 0,
  expr_b64: Buffer.from(expression).toString('base64'), matched_tier: '≤32K', ...overrides,
})

test('summary shows the recorded matched tier prices, including UTF-8 labels', () => {
  assert.equal(summary(row(snapshot())), '≤32K · ¥1 / ¥4/1M')
  assert.equal(summary(row(snapshot({ matched_tier: '>32K' }))), '>32K · ¥2 / ¥8/1M')
  assert.equal(summary(row(snapshot({ matched_tier: ' <= 32k ' }))), '≤32K · ¥1 / ¥4/1M')
})

test('cache rates appear only when this request has cache usage, including split writes', () => {
  for (const field of ['cache_tokens', 'cache_creation_tokens', 'cache_creation_tokens_5m', 'cache_creation_tokens_1h']) {
    assert.equal(summary(row(snapshot({ [field]: 100 }))), '≤32K · ¥1 / ¥4/1M；缓存 ¥0.1 / ¥1.25 / ¥2')
  }
})

test('missing, unknown or invalid snapshot never falls back to a guessed tier', () => {
  for (const overrides of [{ matched_tier: '' }, { matched_tier: 'missing' }, { expr_b64: 'invalid!' }, { expr_b64: Buffer.from('v1:u("duration") * 0.2').toString('base64') }]) {
    assert.equal(summary(row(snapshot(overrides))), '动态计费 · 无匹配结果')
  }
})

test('legacy raw expression and multimedia prices use the site price formatter', () => {
  assert.equal(summary(row(snapshot({ expr_b64: '', expr_string: expression }))), '≤32K · ¥1 / ¥4/1M')
  const mediaExpression = 'tier("多媒体", p * 1 + c * 2 + img * 3 + img_o * 4 + ai * 5 + ao * 6)'
  assert.equal(dynamicBillingSummary(mediaExpression, '多媒体', false, (price) => '¥' + price * 7),
    '多媒体 · ¥7 / ¥14/1M；图像输入 ¥21/1M · 图像输出 ¥28/1M · 音频输入 ¥35/1M · 音频输出 ¥42/1M')
})
