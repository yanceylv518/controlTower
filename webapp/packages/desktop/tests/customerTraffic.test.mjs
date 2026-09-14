import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/customerTraffic.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const api = {}
new Function('exports', compiled)(api)
const { buildCustomerTraffic: build, latestCustomerMinute, trafficColor, escapeChartText, verifiedCustomerBuckets } = api
const now = Date.parse('2026-09-14T10:10:00Z')
const row = (key, tpm, time = '2026-09-14T10:05:00Z', type = 'instance_user_model', instance = 'inst') => ({ instance_id: instance, dimension_key: key, dimension_type: type, tpm, bucket_time: time, display_name: '' })
const total = (tpm, time) => row('inst:user:7', tpm, time, 'instance_user')
const options = { customerKey: 'inst:user:7', instanceID: 'inst', dimension: 'model', bucketMinutes: 1, hours: 1, now }

test('stack identity/color stay fixed while legend order follows volume', () => {
  const first = build({ ...options, totals: [total(30)], points: [row('inst:user:7:model:b', 20), row('inst:user:7:model:a', 10)] })
  const second = build({ ...options, totals: [total(30)], points: [row('inst:user:7:model:a', 25), row('inst:user:7:model:b', 5)] })
  assert.deepEqual(first.series.map(s => s.key), ['a', 'b'])
  assert.deepEqual(first.ranked.map(s => s.key), ['b', 'a'])
  assert.deepEqual(second.ranked.map(s => s.key), ['a', 'b'])
  assert.deepEqual(first.series.map(s => s.color), second.series.map(s => s.color))
  assert.equal(first.totalTokens, 30)
  assert.equal(trafficColor('model:a'), first.series[0].color)
})

test('5m buckets divide tokens by five; missing series are zero only in complete buckets', () => {
  const time = '2026-09-14T10:00:00Z'
  const result = build({ ...options, bucketMinutes: 5, totals: [total(500, time), total(200)], points: [row('inst:user:7:model:a', 500, time), row('inst:user:7:model:b', 200)] })
  const values = key => result.series.find(s => s.key === key).data.filter(([, value]) => value !== null).map(([, value]) => value)
  // 10:05–10:10 is eligible as soon as it closes, with no extra settling delay.
  assert.deepEqual(values('a'), [100, 0])
  assert.deepEqual(values('b'), [0, 40])
  assert.equal(result.coveredMinutes, 10)
  assert.equal(result.totalTokens, 700)
})

test('missing/broken cross history is a gap, never silently normalized or merged into Other', () => {
  const result = build({ ...options, totals: [total(100), total(100, '2026-09-14T10:06:00Z')], points: [row('inst:user:7:model:a', 25)] })
  assert.equal(result.incompleteBuckets, 2)
  assert.equal(result.totalTokens, 0)
  assert.ok(result.series[0].data.every(([, value]) => value === null))
})

test('time alignment keeps exact totals, honors zero traffic, and leaves missing total buckets blank', () => {
  const result = build({ ...options, totals: [total(10), total(20, '2026-09-14T10:06:00Z'), total(0, '2026-09-14T10:07:00Z')], points: [row('inst:user:7:model:a', 10), row('inst:user:7:model:b', 20, '2026-09-14T10:06:00Z')] })
  assert.equal(result.coveredMinutes, 3)
  for (const [time, amount] of [[Date.parse('2026-09-14T10:05:00Z'), 10], [Date.parse('2026-09-14T10:06:00Z'), 20], [Date.parse('2026-09-14T10:07:00Z'), 0]]) {
    assert.equal(result.series.reduce((sum, series) => sum + series.data.find(([t]) => t === time)[1], 0), amount)
  }
  assert.equal(result.series[0].data[0][1], null)
})

test('cross user/instance/dimension values cannot leak, colon model names remain intact', () => {
  const result = build({ ...options, totals: [total(10)], points: [
    row('inst:user:7:model:provider:model-a', 10), row('inst:user:70:model:b', 100),
    row('inst:user:7:model:c', 100, undefined, undefined, 'other'),
    row('inst:user:7:model:d', 100, undefined, 'instance_channel_model'),
  ] })
  assert.deepEqual(result.series.map(s => s.name), ['provider:model-a'])
  assert.equal(result.totalTokens, 10)
})

test('all model series retained; numeric channel order and identity survive channel rename', () => {
  const many = build({ ...options, totals: [total(12)], points: Array.from({ length: 12 }, (_, i) => row(`inst:user:7:model:m${i}`, 1)) })
  assert.equal(many.series.length, 12)
  assert.equal(many.ranked.length, 12)
  const points = [row('inst:user:7:channel:12', 20, undefined, 'instance_user_channel'), row('inst:user:7:channel:3', 10, undefined, 'instance_user_channel')]
  const one = build({ ...options, dimension: 'channel', totals: [total(30)], points })
  const two = build({ ...options, dimension: 'channel', totals: [total(30)], points: points.map(p => ({ ...p, display_name: 'renamed' })) })
  assert.deepEqual(one.series.map(s => s.key), ['3', '12'])
  assert.deepEqual(one.series.map(s => s.color), two.series.map(s => s.color))
})

test('header shows latest closed minute without delay, excludes current minute, flags stale samples', () => {
  const result = latestCustomerMinute([total(30, '2026-09-14T10:08:00Z'), total(99, '2026-09-14T10:09:00Z'), total(12, '2026-09-14T10:10:00Z')], now)
  assert.equal(result.tpm, 99)
  assert.equal(result.stale, false)
  assert.equal(latestCustomerMinute([total(5, '2026-09-14T09:50:00Z')], now).stale, true)
  assert.equal(latestCustomerMinute([], now), null)
})

test('chart and header advance at minute close and accept later sample corrections', () => {
  const time = '2026-09-14T10:09:00Z', stamp = Date.parse(time);
  const snapshot = (at, tokens) => build({ ...options, now: at, totals: [total(tokens, time)], points: [row('inst:user:7:model:a', tokens, time)] });
  assert.equal(snapshot(now - 1, 20).series.length, 0);
  assert.equal(latestCustomerMinute([total(20, time)], now - 1), null);
  for (const [at, tokens] of [[now, 20], [now + 30_000, 40]]) {
    assert.equal(snapshot(at, tokens).series[0].data.find(([t]) => t === stamp)[1], tokens);
    assert.equal(latestCustomerMinute([total(tokens, time)], at).tpm, tokens);
  }
})

test('model names are escaped before entering chart tooltip HTML', () => {
  assert.equal(escapeChartText('<img src=x onerror="oops">'), '&lt;img src=x onerror=&quot;oops&quot;&gt;')
})

test('reconciled instance coverage closes idle gaps without altering observed peaks', () => {
  const times = ['2026-09-14T10:04:00Z', '2026-09-14T10:05:00Z', '2026-09-14T10:06:00Z', '2026-09-14T10:07:00Z'];
  const customers = times.map(time => ({ ...row('inst:user:8', 100, time, 'instance_user'), request_count: 1 }));
  customers.push({ ...total(30, times[1]), request_count: 2 });
  const instance = times.map((time, i) => ({ ...row('inst', i === 1 ? 130 : 100, time, 'instance'), request_count: i === 1 ? 3 : 1 }));
  const verifiedBuckets = verifiedCustomerBuckets('inst', instance, customers);
  const result = build({ ...options, verifiedBuckets, totals: [total(30, times[1])], points: [row('inst:user:7:model:a', 20, times[1]), row('inst:user:7:model:b', 10, times[1])] });
  const values = result.series.map(s => times.map(time => s.data.find(([t]) => t === Date.parse(time))[1]));
  assert.deepEqual(values, [[0, 20, 0, 0], [0, 10, 0, 0]]);
  assert.equal(result.totalTokens, 30);
  assert.equal(result.coveredMinutes, 4);
  assert.equal(result.series[0].data[0][1], null);
  assert.deepEqual(latestCustomerMinute([total(30, times[1])], now, verifiedBuckets), { tpm: 0, time: Date.parse(times[3]), stale: false });
});

test('unknown/restricted/mismatched instance history cannot turn gaps into zeros', () => {
  const user = { ...total(100), request_count: 2 };
  const instance = { ...row('inst', 100, user.bucket_time, 'instance'), request_count: 2 };
  assert.deepEqual(verifiedCustomerBuckets('inst', [], [user]), []);
  assert.deepEqual(verifiedCustomerBuckets('inst', [instance], []), []);
  assert.deepEqual(verifiedCustomerBuckets('inst', [{ ...instance, tpm: 101 }], [user]), []);
  assert.deepEqual(verifiedCustomerBuckets('inst', [{ ...instance, request_count: 3 }], [user]), []);
  assert.deepEqual(verifiedCustomerBuckets('other', [instance], [user]), []);
  const result = build({ ...options, verifiedBuckets: [Date.parse(user.bucket_time)], totals: [total(100)], points: [row('inst:user:7:model:a', 50)] });
  assert.equal(result.incompleteBuckets, 1);
  assert.ok(result.series[0].data.every(([, value]) => value === null));
  const orphan = build({ ...options, verifiedBuckets: [Date.parse(user.bucket_time)], totals: [], points: [row('inst:user:7:model:a', 50)] });
  assert.equal(orphan.incompleteBuckets, 1);
  assert.ok(orphan.series[0].data.every(([, value]) => value === null));
});

test('5m idle coverage stays zero while known traffic retains correct TPM scale', () => {
  const time = '2026-09-14T10:00:00Z', idle = Date.parse('2026-09-14T09:55:00Z');
  const result = build({ ...options, bucketMinutes: 5, verifiedBuckets: [idle], totals: [total(500, time)], points: [row('inst:user:7:model:a', 500, time)] });
  assert.equal(result.series[0].data.find(([t]) => t === idle)[1], 0);
  assert.equal(result.series[0].data.find(([t]) => t === Date.parse(time))[1], 100);
  assert.equal(result.totalTokens, 500);
});
