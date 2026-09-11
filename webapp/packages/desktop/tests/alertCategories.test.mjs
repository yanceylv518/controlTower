import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/alertCategories.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { alertCategories, categoriesForRules, rulesForCategories, categorySummary } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('three categories partition all existing alert types without overlap', () => {
  assert.deepEqual(alertCategories.map(c => c.label), ['余额告警', '系统告警', '请求告警'])
  const rules = rulesForCategories(['balance', 'system', 'request'])
  assert.equal(rules.length, 11)
  assert.equal(new Set(rules).size, 11)
  assert.deepEqual(rulesForCategories(['balance']), ['user_low_balance'])
  assert.ok(!rulesForCategories(['system']).includes('high_error_rate'))
})
test('legacy partial selections are clearly labelled and category edits expand explicitly', () => {
  assert.equal(categorySummary(['high_cpu']), '系统告警（部分规则）')
  assert.deepEqual(categoriesForRules(['high_cpu', 'user_low_balance']), ['balance', 'system'])
  assert.equal(categorySummary(rulesForCategories(['request'])), '请求告警')
  assert.equal(categorySummary([]), '全部类别')
})
