import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/customerTraffic.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const api = {}
new Function('exports', compiled)(api)
const { buildModelChannelTraffic: build, trafficColor } = api
const now = Date.parse('2026-09-26T10:10:00Z')
const modelKey = 'inst:model:provider:model:channel:7'
const time = '2026-09-26T10:05:00Z'
const row = (key, tpm, bucket = time, type = 'instance_model_channel', instance = 'inst') => ({
  instance_id: instance, dimension_key: key, dimension_type: type, tpm, bucket_time: bucket, display_name: '',
})
const total = (tpm, bucket = time) => row(modelKey, tpm, bucket, 'instance_model')
const channel = (id, tpm, bucket = time) => row(`${modelKey}:channel:${id}`, tpm, bucket)
const options = { modelKey, instanceID: 'inst', bucketMinutes: 1, hours: 1, now }
const valueAt = (series, bucket = time) => series.data.find(([t]) => t === Date.parse(bucket))[1]

test('model customer layers preserve names, isolate exact IDs and use five-minute TPM', () => {
  const user = (id, tpm, name = '') => ({ ...row(`${modelKey}:user:${id}`, tpm, time, 'instance_model_user'), display_name: name })
  const result = api.buildModelTraffic({ ...options, dimension: 'user', bucketMinutes: 5,
    totals: [total(500)], points: [user('7', 300, 'Alice'), user('12', 200),
      user('7:user:9', 999), user('07', 999), channel('3', 999),
      { ...user('8', 999), instance_id: 'other' }],
  })
  assert.deepEqual(result.series.map(s => s.name), ['Alice · #7', '客户 12 · #12'])
  assert.deepEqual(result.series.map(s => valueAt(s)), [60, 40])
  assert.equal(result.totalTokens, 500)
  assert.equal(result.series[0].color, trafficColor('inst:user:7'))
  const incomplete = api.buildModelTraffic({ ...options, dimension: 'user', totals: [total(500)], points: [user('7', 300)] })
  assert.equal(incomplete.coveredMinutes, 0)
  assert.equal(incomplete.incompleteBuckets, 1)
  assert.ok(incomplete.series[0].data.every(([, value]) => value === null))
})

test('model channel stacks reconcile exact totals and keep channel identity across volume and name changes', () => {
  const first = build({ ...options, totals: [total(30)], points: [channel('12', 20), channel('3', 10)] })
  const second = build({ ...options, totals: [total(30)], points: [
    { ...channel('3', 25), display_name: '新名称' }, channel('12', 5),
  ] })
  assert.deepEqual(first.series.map(s => s.key), ['3', '12'])
  assert.deepEqual(first.ranked.map(s => s.key), ['12', '3'])
  assert.deepEqual(second.ranked.map(s => s.key), ['3', '12'])
  assert.equal(second.series[0].name, '新名称 · #3')
  assert.deepEqual(first.series.map(s => s.color), second.series.map(s => s.color))
  assert.equal(first.series[0].color, trafficColor('inst:channel:3'))
  assert.equal(first.series.reduce((sum, s) => sum + valueAt(s), 0), 30)
  assert.equal(first.coveredMinutes, 1)
})

test('model channel 5m stacks show average TPM and zero only for confirmed complete buckets', () => {
  const earlier = '2026-09-26T10:00:00Z'
  const zero = '2026-09-26T09:55:00Z'
  const result = build({ ...options, bucketMinutes: 5,
    totals: [total(500, earlier), total(200), total(0, zero)],
    points: [channel('3', 500, earlier), channel('12', 200)],
  })
  assert.deepEqual(result.series.map(s => [valueAt(s, zero), valueAt(s, earlier), valueAt(s)]), [[0, 100, 0], [0, 0, 40]])
  assert.equal(result.series[0].data[0][1], null)
  assert.equal(result.totalTokens, 700)
  assert.equal(result.coveredMinutes, 15)
})

test('missing totals, incomplete splits, and absent channels leave model traffic gaps', () => {
  const missingSplit = '2026-09-26T10:06:00Z'
  const orphan = '2026-09-26T10:07:00Z'
  const result = build({ ...options, totals: [total(100), total(100, missingSplit)],
    points: [channel('3', 25), channel('3', 20, orphan)],
  })
  assert.equal(result.incompleteBuckets, 3)
  assert.equal(result.coveredMinutes, 0)
  assert.equal(result.totalTokens, 0)
  assert.ok(result.series[0].data.every(([, value]) => value === null))
})

test('exact model, instance, dimension and canonical channel IDs isolate colon-bearing model names', () => {
  const result = build({ ...options, totals: [total(10),
    row(modelKey, 100, time, 'instance_user'), row(`${modelKey}:next`, 100, time, 'instance_model'),
    row(modelKey, 100, time, 'instance_model', 'other'),
  ], points: [channel('3', 10),
    row(`${modelKey}:next:channel:4`, 100), channel('5:channel:6', 100), channel('6:extra', 100),
    channel('0', 100), channel('-1', 100), channel('01', 100), channel('1.5', 100),
    channel('', 100), channel('7 ', 100),
    row(`${modelKey}:channel:8`, 100, time, 'instance_user_channel'),
    row(`${modelKey}:channel:9`, 100, time, 'instance_model_channel', 'other'),
  ] })
  assert.deepEqual(result.series.map(s => s.key), ['3'])
  assert.equal(result.totalTokens, 10)
  assert.equal(valueAt(result.series[0]), 10)
})

test('model stacks exclude open buckets and out-of-range traffic and accept later completed corrections', () => {
  const closed = '2026-09-26T10:09:00Z'
  const current = '2026-09-26T10:10:00Z'
  const old = '2026-09-26T09:09:00Z'
  const beforeClose = build({ ...options, now: now - 1, totals: [total(20, closed)], points: [channel('3', 20, closed)] })
  assert.equal(beforeClose.series.length, 0)
  for (const amount of [20, 40]) {
    const result = build({ ...options,
      totals: [total(amount, closed), total(900, current), total(800, old)],
      points: [channel('3', amount, closed), channel('4', 900, current), channel('5', 800, old)],
    })
    assert.deepEqual(result.series.map(s => s.key), ['3'])
    assert.equal(result.totalTokens, amount)
    assert.equal(valueAt(result.series[0], closed), amount)
  }
})

test('model stacks retain all channels and sort large integer IDs without rounding', () => {
  const ids = ['10000000000000000', '9999999999999999', ...Array.from({ length: 12 }, (_, i) => String(i + 1))]
  const result = build({ ...options, totals: [total(ids.length)], points: ids.map(id => channel(id, 1)) })
  assert.equal(result.series.length, ids.length)
  assert.deepEqual(result.series.slice(-2).map(s => s.key), ['9999999999999999', '10000000000000000'])
  assert.equal(result.totalTokens, ids.length)
})
