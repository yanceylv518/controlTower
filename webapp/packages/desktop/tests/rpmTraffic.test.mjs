import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const api = {}
new Function('exports', ts.transpileModule(readFileSync(new URL('../src/utils/customerTraffic.ts', import.meta.url), 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText)(api)
const now = Date.parse('2026-10-15T10:10:00Z')
const bucket = '2026-10-15T10:05:00Z'
const root = (kind, key) => `mock:${kind}:${key}`
const row = (key, type, requests, tokens, time = bucket, instance = 'mock') => ({
  instance_id: instance, dimension_key: key, dimension_type: type, bucket_time: time,
  request_count: requests, consume_count: requests, tpm: tokens, display_name: '',
})
const cases = [
  ['customer models', 'user', '7', 'model', ['provider:model:a', 'b']],
  ['customer channels', 'user', '7', 'channel', ['3', '12']],
  ['channel customers', 'channel', '5', 'user', ['3', '12']],
  ['model customers', 'model', 'provider:channel:inner:user:7', 'user', ['3', '12']],
  ['model channels', 'model', 'provider:channel:inner:user:7', 'channel', ['3', '12']],
]
function build(kind, key, dimension, options) {
  const common = { instanceID: 'mock', bucketMinutes: 1, hours: 1, now, dimension, ...options }
  return kind === 'model'
    ? api.buildModelTraffic({ ...common, modelKey: root(kind, key) })
    : api.buildCustomerTraffic({ ...common, customerKey: root(kind, key), customerParent: kind === 'channel' ? 'channel' : undefined })
}

for (const [name, kind, key, dimension, ids] of cases) {
  const parent = root(kind, key), type = `instance_${kind}`
  const totals = [row(parent, type, 7, 10000)]
  const points = [row(`${parent}:${dimension}:${ids[0]}`, `${type}_${dimension}`, 5, 1000), row(`${parent}:${dimension}:${ids[1]}`, `${type}_${dimension}`, 2, 9000)]
  test(`${name}: RPM uses consumption counts for layers, shares, totals and ranking, while TPM stays unchanged`, () => {
    const rpm = build(kind, key, dimension, { metric: 'rpm', totals, points })
    const tpm = build(kind, key, dimension, { totals, points })
    assert.equal(rpm.metric, 'rpm')
    assert.equal(tpm.metric, 'tpm')
    assert.equal(rpm.totalRequests, 7)
    assert.equal(rpm.totalTokens, 10000)
    assert.equal(rpm.coveredMinutes, 1)
    assert.equal(rpm.ranked[0].key, ids[0])
    assert.equal(tpm.ranked[0].key, ids[1])
    assert.deepEqual(rpm.series.map(s => s.key), tpm.series.map(s => s.key))
    assert.deepEqual(rpm.series.map(s => s.color), tpm.series.map(s => s.color))
    assert.equal(rpm.series.reduce((sum, s) => sum + s.data.find(([at]) => at === Date.parse(bucket))[1], 0), 7)
    assert.equal(tpm.series.reduce((sum, s) => sum + s.data.find(([at]) => at === Date.parse(bucket))[1], 0), 10000)
    assert.equal(rpm.ranked.reduce((sum, s) => sum + s.requests / rpm.totalRequests, 0), 1)
  })
  test(`${name}: five-minute RPM retains fractional averages and excludes the open interval`, () => {
    const open = '2026-10-15T10:10:00Z'
    const rpm = build(kind, key, dimension, { metric: 'rpm', bucketMinutes: 5, totals: [...totals, row(parent, type, 999, 999, open)], points: [...points, row(`${parent}:${dimension}:${ids[0]}`, `${type}_${dimension}`, 999, 999, open)] })
    assert.equal(rpm.coveredMinutes, 5)
    assert.equal(rpm.totalRequests, 7)
    assert.equal(rpm.series.find(s => s.key === ids[1]).data.find(([at]) => at === Date.parse(bucket))[1], .4)
    assert.equal(rpm.ranked[0].requests / rpm.coveredMinutes, 1)
    assert.ok(rpm.series.every(s => s.data.every(([at]) => at < Date.parse(open))))
  })
  test(`${name}: consumption mismatch stays a gap even when Token totals match`, () => {
    const missing = points.map((p, i) => ({ ...p, consume_count: i ? 1 : p.consume_count }))
    const rpm = build(kind, key, dimension, { metric: 'rpm', totals, points: missing })
    assert.equal(rpm.incompleteBuckets, 1)
    assert.equal(rpm.coveredMinutes, 0)
    assert.ok(rpm.series.every(s => s.data.every(([, count]) => count === null)))
    assert.equal(build(kind, key, dimension, { totals, points: missing }).coveredMinutes, 1)
  })
  test(`${name}: zero-Token consumption counts in RPM while error requests are excluded`, () => {
    const rpm = build(kind, key, dimension, { metric: 'rpm', totals: [{ ...row(parent, type, 7, 0), consume_count: 3 }], points: points.map((p, i) => ({ ...p, tpm: 0, consume_count: i ? 1 : 2 })) })
    assert.equal(rpm.totalRequests, 3)
    assert.equal(rpm.totalTokens, 0)
    assert.equal(rpm.ranked[0].key, ids[0])
    assert.equal(rpm.series.reduce((sum, s) => sum + s.requests, 0), 3)
  })
}

test('RPM headline uses the latest closed minute, verified zero, stale data and unknown state', () => {
  const points = [row('mock:user:7', 'instance_user', 3, 9000, '2026-10-15T10:08:00Z'), row('mock:user:7', 'instance_user', 7, 1000, '2026-10-15T10:09:00Z'), row('mock:user:7', 'instance_user', 99, 999999, '2026-10-15T10:10:00Z')]
  const latest = api.latestCustomerMinute(points, now, [], 'rpm')
  assert.equal(latest.rpm, 7)
  assert.equal(latest.tpm, 1000)
  assert.equal(latest.stale, false)
  assert.equal(api.latestCustomerMinute(points, now + 240000, [], 'rpm').stale, true)
  assert.equal(api.latestCustomerMinute([], now, [], 'rpm'), null)
  const verified = api.latestCustomerMinute(points.slice(0, 1), now, [Date.parse('2026-10-15T10:09:00Z')], 'rpm')
  assert.equal(verified.rpm, 0)
  assert.equal(verified.time, Date.parse('2026-10-15T10:09:00Z'))
  assert.equal(api.latestCustomerMinute(points, now - 1, [], 'rpm').rpm, 3)
})

test('RPM only fills absent layers with zero in reconciled buckets and isolates unrelated data', () => {
  const parent = 'mock:user:7', time = Date.parse(bucket), zero = time - 60000
  const options = { metric: 'rpm', totals: [row(parent, 'instance_user', 2, 0)], points: [
    row(`${parent}:model:m`, 'instance_user_model', 2, 0),
    row('mock:user:70:model:x', 'instance_user_model', 999, 999),
    row(`${parent}:model:x`, 'instance_user_model', 999, 999, bucket, 'other'),
  ] }
  const result = build('user', '7', 'model', { ...options, verifiedBuckets: [zero] })
  assert.equal(result.series.length, 1)
  assert.equal(result.coveredMinutes, 2)
  assert.equal(result.series[0].data.find(([at]) => at === zero)[1], 0)
  assert.equal(result.series[0].data.find(([at]) => at === zero - 60000)[1], null)
  const orphan = build('user', '7', 'model', { ...options, totals: [], verifiedBuckets: [time] })
  assert.equal(orphan.coveredMinutes, 0)
  assert.ok(orphan.series[0].data.every(([, count]) => count === null))
})

test('shared traffic formatting retains fractional RPM and compact Token units', () => {
  for (const [value, formatted] of [[0, '0'], [.4, '0.4'], [1.49, '1.5'], [999.99, '1,000'], [1000, '1.0K'], [1000000, '1.0M'], [1000000000, '1.0B']]) {
    assert.equal(api.formatTrafficTPM(value), formatted)
  }
})

test('legacy customer/channel buckets keep RPM unknown and TPM usable until consumption counters arrive', () => {
  for (const [kind, key, dimension] of [['user', '7', 'channel'], ['channel', '5', 'user']]) {
    const parent = root(kind, key), type = `instance_${kind}`
    const totals = [row(parent, type, 7, 1000)]
    const legacy = [{ ...row(`${parent}:${dimension}:3`, `${type}_${dimension}`, 7, 1000), consume_count: null }]
    assert.equal(build(kind, key, dimension, { totals, points: legacy }).totalTokens, 1000)
    const unknown = build(kind, key, dimension, { metric: 'rpm', totals, points: legacy })
    assert.equal(unknown.coveredMinutes, 0)
    assert.equal(unknown.incompleteBuckets, 1)
    assert.ok(unknown.series[0].data.every(([, value]) => value === null))
    const current = build(kind, key, dimension, { metric: 'rpm', totals, points: [{ ...legacy[0], consume_count: 7 }] })
    assert.equal(current.coveredMinutes, 1)
    assert.equal(current.totalRequests, 7)
    assert.equal(current.series[0].data.find(([at]) => at === Date.parse(bucket))[1], 7)
  }
})
