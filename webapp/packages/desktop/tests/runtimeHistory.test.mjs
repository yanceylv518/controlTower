import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/runtimeHistory.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { loadRuntimeHistory } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('loads the full six-hour history for each selected instance beyond the API cap', async () => {
  const end = '2026-09-10T08:22:00.000Z'
  const start = '2026-09-10T02:22:00.000Z'
  const samples = Object.fromEntries(['one', 'two'].map(id => [id, Array.from({ length: 721 }, (_, i) => ({
    instance_id: id,
    collected_at: new Date(Date.parse(end) - i * 30_000).toISOString(),
  }))]))
  const calls = []
  const result = await loadRuntimeHistory(async query => {
    calls.push(query)
    assert.equal(query.start_time, start)
    assert.equal(query.end_time, end)
    assert.equal(query.limit, 200)
    return { items: samples[query.instance_id].slice(query.offset, query.offset + query.limit) }
  }, ['one', 'two'], start, end)
  assert.equal(result.length, 1442)
  for (const id of ['one', 'two']) {
    assert.deepEqual(calls.filter(q => q.instance_id === id).map(q => q.offset), [0, 200, 400, 600])
    assert.equal(result.filter(item => item.instance_id === id).at(-1).collected_at, start)
  }
})

test('stops on an empty page after an exact page and skips sites without instances', async () => {
  const offsets = []
  const fetchPage = async query => {
    offsets.push(query.offset)
    return { items: query.offset === 0 ? Array(200).fill({ instance_id: 'one' }) : [] }
  }
  assert.equal((await loadRuntimeHistory(fetchPage, ['one'], 'start', 'end')).length, 200)
  assert.deepEqual(offsets, [0, 200])
  assert.deepEqual(await loadRuntimeHistory(() => assert.fail('unexpected request'), [], 'start', 'end'), [])
})

test('reports a failed history page instead of returning a truncated chart', async () => {
  await assert.rejects(loadRuntimeHistory(async query => {
    if (query.offset) throw new Error('history unavailable')
    return { items: Array(200).fill({ instance_id: 'one' }) }
  }, ['one'], 'start', 'end'), /history unavailable/)
})
