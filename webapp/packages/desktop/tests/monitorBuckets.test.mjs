import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/monitorBuckets.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const api = {}
new Function('exports', compiled)(api)
const { closedMonitorBuckets } = api
const at = value => Date.parse(`2026-09-22T${value}Z`)
const row = (time, tpm) => ({ bucket_time: `2026-09-22T${time}Z`, tpm })

test('one-minute curve excludes partial tail but retains a real closed low or zero', () => {
  const points = [row('14:00:00', 64e6), row('14:01:00', 0), row('14:02:00', 10e6)]
  assert.deepEqual(closedMonitorBuckets(points, 1, at('14:02:15')), points.slice(0, 2))
  assert.equal(points.length, 3)
})
test('five-minute chart waits for the whole interval', () => {
  const points = [row('14:00:00', 300e6), row('14:05:00', 10e6)]
  assert.deepEqual(closedMonitorBuckets(points, 5, at('14:09:59')), points.slice(0, 1))
  assert.deepEqual(closedMonitorBuckets(points, 5, at('14:10:00')), points)
})
test('request crossing minute boundary does not prematurely expose its partial sample', () => {
  const points = [row('14:01:00', 64e6), row('14:02:00', 10e6)]
  const requestedAt = at('14:02:59')
  assert.deepEqual(closedMonitorBuckets(points, 1, requestedAt), points.slice(0, 1))
  const refreshed = [points[0], row('14:02:00', 63e6)]
  assert.deepEqual(closedMonitorBuckets(refreshed, 1, at('14:03:10')), refreshed)
})
test('missing and invalid buckets do not become fabricated zeros', () => {
  assert.deepEqual(closedMonitorBuckets([], 1, at('14:03:00')), [])
  assert.deepEqual(closedMonitorBuckets([{ bucket_time: 'invalid' }, row('14:04:00', 1)], 1, at('14:03:00')), [])
})
