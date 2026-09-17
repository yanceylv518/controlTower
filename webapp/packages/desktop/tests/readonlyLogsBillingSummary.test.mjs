import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('../src/views/ReadonlyLogsView.vue', import.meta.url), 'utf8')
const functions = ['first', 'numberValue', 'textValue', 'effectiveGroupRatio', 'groupRatioLabel', 'isTieredBilling', 'billingSummary']
  .map((name) => {
    const match = source.match(new RegExp(`function ${name}\\([^]*?\\n\\}`))
    assert.ok(match, `missing ${name}`)
    return match[0]
  }).join('\n')
const code = ts.transpileModule(functions, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
const summary = new Function(`
  const extra = row => JSON.parse(row.other || '{}');
  const isConsume = row => row.type === 2;
  const isPerCallBilling = value => value !== undefined && value > 0;
  const billingPrice = value => '¥' + Number(value.toFixed(6));
  const ratioText = value => value + 'x';
  const cacheReadTokens = row => numberValue(row, 'cache_tokens') || 0;
  const cacheWriteTokens = row => numberValue(row, 'cache_creation_tokens') || 0;
  const cacheWrite1hTokens = row => numberValue(row, 'cache_creation_tokens_1h') || 0;
  ${code}
  return billingSummary;
`)()
const row = (other, type = 2) => ({ type, quota: 206, content_summary: '原始摘要', other: JSON.stringify(other) })

test('dynamic billing does not expose zero placeholder rates as standard prices', () => {
  assert.equal(summary(row({ billing_mode: 'tiered_expr', model_ratio: 0, completion_ratio: 0 })), '动态计费 · 查看计费详情')
  assert.equal(summary(row({ billing_mode: 'tiered_expr', model_price: 2, model_ratio: 1 })), '动态计费 · 查看计费详情')
  assert.equal(summary(row({ billing_mode: 'tiered_expr' })), '动态计费 · 查看计费详情')
})

test('standard, genuinely free, per-call and error summaries retain their meaning', () => {
  assert.equal(summary(row({ model_ratio: 4, completion_ratio: 2 })), '标准 · ¥8 / ¥16/1M')
  assert.equal(summary(row({ model_ratio: 0, completion_ratio: 1 })), '标准 · ¥0 / ¥0/1M')
  assert.equal(summary(row({ model_price: 2 })), '按次 · ¥2')
  assert.equal(summary(row({ billing_mode: 'tiered_expr' }, 5)), '原始摘要')
})
