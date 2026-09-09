import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../src/utils/logHistory.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { groupLogHistory } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const task = (id, agent, seconds, batch) => ({ id, actor: 'admin', instance_id: agent, agent_id: agent, created_at: `2026-09-08T18:16:${seconds}Z`, query: { batch_id: batch, source_id: 's', container: 'new-api', from: 'start', to: 'end' }, result: { status: 'succeeded' } })
test('legacy records merge across seconds but repeated source stays separate', () => {
  const groups = groupLogHistory([task('a', 'one', '02'), task('b', 'two', '03'), task('c', 'one', '04')])
  assert.equal(groups.length, 2)
  assert.deepEqual(groups.map(g => g.tasks.length), [2, 1])
})
test('batch identity separates repeat searches and joins slow submissions', () => {
  const groups = groupLogHistory([task('a', 'one', '02', 'first'), task('b', 'two', '40', 'first'), task('c', 'one', '03', 'second')])
  assert.equal(groups.length, 2)
  assert.equal(groups[0].tasks.length, 2)
})
test('different filters never merge; status includes failed sources', () => {
  const a = task('a', 'one', '02'), b = task('b', 'two', '03')
  b.result.status = 'failed'
  assert.equal(groupLogHistory([a, b])[0].status, '部分失败')
  b.query.keyword = 'different'
  assert.equal(groupLogHistory([a, b]).length, 2)
})
