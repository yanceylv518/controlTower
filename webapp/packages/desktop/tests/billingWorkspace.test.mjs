import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const code = ts.transpileModule(readFileSync(new URL('../src/utils/billingWorkspace.ts', import.meta.url), 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText
const { billingWorkspaceRows } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
const stats = { requests: 1, input: 17, output: 8, cache_read: 6, cache_write: 0, amount: '0.012280', before_amount: '0.026696', discount: '0.46', empty_count: 0, empty_amount: '0' }
const bill = (id, models) => ({ ...stats, job: { id }, currency: { type: 'CNY' }, models })

test('model rows retain exact saved fields and bill context without changing the daily total', () => {
  const input = [bill('one', [{ ...stats, model: 'model-a' }, { ...stats, model: 'model-b', before_amount: '', amount: '0.000001' }])]
  const before = JSON.stringify(input)
  const rows = billingWorkspaceRows(input, true)
  assert.equal(rows.length, 1)
  assert.equal(rows[0].requests, 1)
  assert.equal(rows[0].children.length, 2)
  assert.deepEqual(rows[0].children.map(v => v.model_name), ['model-a', 'model-b'])
  assert.equal(rows[0].children[0].amount, '0.012280')
  assert.equal(rows[0].children[1].before_amount, '')
  assert.equal(rows[0].children[1].amount, '0.000001')
  assert.equal(rows[0].children[1].job.id, 'one')
  assert.equal(rows[0].children[1].currency.type, 'CNY')
  assert.equal(JSON.stringify(input), before)
})

test('row keys isolate days, exact model names and refreshes', () => {
  const models = ['a', 'A', 'a ', '', 'x|y'].map(model => ({ ...stats, model }))
  const input = [bill('one', models), bill('two', models)]
  const keys = billingWorkspaceRows(input, true).flatMap(v => [v.key, ...v.children.map(m => m.key)])
  assert.equal(new Set(keys).size, 12)
  assert.deepEqual(keys, billingWorkspaceRows(structuredClone(input), true).flatMap(v => [v.key, ...v.children.map(m => m.key)]))
})

test('other billing tabs and old server responses keep the bill rows usable', () => {
  for (const input of [[bill('old')], [bill('empty', [])]]) {
    const rows = billingWorkspaceRows(input, true)
    assert.equal(rows.length, 1)
    assert.equal(rows[0].children, undefined)
  }
  assert.equal(billingWorkspaceRows([bill('month', [{ ...stats, model: 'a' }])], false)[0].children, undefined)
})
