import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/billingDiscount.ts', import.meta.url), 'utf8')
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText
const { formatBillingDiscount } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))

test('recorded discounts render without floating point tails', () => {
  for (const [rate, label] of [['0.46', '4.6 折'], ['0.460000', '4.6 折'], [0.46, '4.6 折'],
    ['0.29', '2.9 折'], ['0.58', '5.8 折'], ['0.07', '0.7 折']]) {
    assert.equal(formatBillingDiscount(rate), label)
  }
})

test('all supported multiplier precision is retained', () => {
  for (const [rate, label] of [['0.123456', '1.23456 折'], ['0.000001', '0.00001 折'],
    ['0.999999', '9.99999 折'], ['0', '0 折'], ['1', '原价'], ['1.000000', '原价']]) {
    assert.equal(formatBillingDiscount(rate), label)
  }
})

test('empty, mixed and invalid discounts have explicit labels', () => {
  for (const rate of [undefined, null, '', '  ']) assert.equal(formatBillingDiscount(rate), '原价')
  assert.equal(formatBillingDiscount('mixed'), '多种折扣')
  for (const rate of ['unknown', 'NaN', 'Infinity', NaN, Infinity]) assert.equal(formatBillingDiscount(rate), '—')
})
