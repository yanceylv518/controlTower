import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { computed, ref } from 'vue'
import ts from 'typescript'
const source = readFileSync(new URL('../src/views/DimensionDetailView.vue', import.meta.url), 'utf8')
const block = source.slice(source.indexOf('const crossPrefix ='), source.indexOf('async function loadHistory'))
const compiled = ts.transpileModule(block, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
const run = (kind, key, items) => new Function('computed', 'props', 'instancePart', 'idPart', 'crossMetrics', `${compiled}; return crossRows.value`)(computed, { kind, dimensionKey: key }, ref('inst'), ref(key.split(':').at(-1)), ref(items))
const row = (key, name, count = 1) => ({ dimension_key: key, display_name: name, request_count: count })
test('model customer table uses names from its own rows, keeps IDs and request order', () => {
 const key = 'inst:model:provider:kimi'
 const rows = run('models', key, [row(`${key}:user:104`, 'Alice'), row(`${key}:user:105`, 'Bob', 2), row('other:model:kimi:user:104', 'Wrong')])
 assert.deepEqual(rows.map(r => [r.crossName, r.crossUserID]), [['Bob', '105'], ['Alice', '104']])
})
test('missing or old-server raw names fall back to explicit user IDs', () => {
 const key = 'inst:model:kimi'
 const rows = run('models', key, [row(`${key}:user:104`, ''), row(`${key}:user:105`, `${key}:user:105`)])
 assert.deepEqual(rows.map(r => r.crossName), ['用户 104', '用户 105'])
})
test('customer and channel detail model names retain namespaced model names', () => {
 for (const kind of ['customers', 'channels']) {
  const key = kind === 'customers' ? 'inst:user:4' : 'inst:channel:4'
  const rows = run(kind, key, [row(`${key}:model:provider:kimi`, 'irrelevant')])
  assert.equal(rows[0].crossName, 'provider:kimi')
  assert.equal(rows[0].crossUserID, '')
 }
})
