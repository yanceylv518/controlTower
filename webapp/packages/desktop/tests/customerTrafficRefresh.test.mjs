import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, reactive, ref, shallowRef } from 'vue'

const sfc = readFileSync(new URL('../src/components/CustomerTrafficCard.vue', import.meta.url), 'utf8')
const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const trafficApi = {}
new Function('exports', ts.transpileModule(readFileSync(new URL('../src/utils/customerTraffic.ts', import.meta.url), 'utf8'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText)(trafficApi)
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
function card() {
  const props = reactive({ customer: { instance_id: 'a', dimension_key: 'a:user:7' }, totals: [], hours: 1, asOf: Date.now(), refreshKey: 1, minute: null })
  const requests = []
  const dashboard = { metricHistory: params => { const pending = deferred(); requests.push({ ...pending, params }); return pending.promise } }
  const create = new Function('computed', 'ref', 'shallowRef', 'watch', 'onBeforeUnmount', 'onMounted', 'defineProps', 'defineEmits', 'dashboard', 'buildCustomerTraffic', `${compiled}\nreturn {load, dimension, points, loadedScope, scope, error, loading, nearViewport, expanded, traffic, minuteStatus, lastPlotTime};`)
  return { ...create(computed, ref, shallowRef, () => {}, () => {}, () => {}, () => props, () => () => {}, dashboard, trafficApi.buildCustomerTraffic), props, requests }
}

test('offscreen cards defer loading and catch up to current revision when visible', async () => {
  const c = card();
  c.nearViewport.value = false;
  await c.load(); c.props.refreshKey++; await c.load();
  assert.equal(c.requests.length, 0);
  c.nearViewport.value = true;
  const first = c.load(); c.requests[0].resolve({ items: ['visible'] }); await first;
  c.nearViewport.value = false; c.props.refreshKey++; await c.load();
  assert.equal(c.requests.length, 1);
  assert.deepEqual(c.points.value, ['visible']);
  c.nearViewport.value = true;
  const second = c.load(); c.requests[1].resolve({ items: ['current'] }); await second;
  assert.deepEqual(c.points.value, ['current']);
});

test('dialog reuses cached data and keeps refreshing even if its background card leaves the viewport', async () => {
  const c = card(), first = c.load();
  c.requests[0].resolve({ items: ['initial'] }); await first;
  c.expanded.value = true;
  await c.load();
  assert.equal(c.requests.length, 1);
  c.nearViewport.value = false;
  c.props.refreshKey++;
  const refresh = c.load();
  c.requests[1].resolve({ items: ['dialog-current'] }); await refresh;
  assert.deepEqual(c.points.value, ['dialog-current']);
  c.expanded.value = false;
  c.props.refreshKey++;
  await c.load();
  assert.equal(c.requests.length, 2);
});
test('rapid model/channel switch rejects late response and caches successful current revision', async () => {
  const c = card(), a = c.load()
  c.dimension.value = 'channel'
  const b = c.load()
  c.requests[1].resolve({ items: ['channel'] }); await b
  c.requests[0].resolve({ items: ['model-old'] }); await a
  assert.deepEqual(c.points.value, ['channel'])
  assert.equal(c.loadedScope.value, c.scope.value)
  await c.load()
  assert.equal(c.requests.length, 2)
})
test('slow same-scope refresh is single flight and cannot starve', async () => {
  const c = card(), first = c.load()
  c.props.refreshKey++
  await c.load()
  assert.equal(c.requests.length, 1)
  c.requests[0].resolve({ items: ['finished'] }); await first
  assert.deepEqual(c.points.value, ['finished'])
  const next = c.load()
  assert.equal(c.requests.length, 2)
  c.requests[1].resolve({ items: ['fresh'] }); await next
  assert.deepEqual(c.points.value, ['fresh'])
})
test('scope changes prevent another customer or time range from reusing late data', async () => {
  const c = card(), old = c.load()
  c.props.customer = { instance_id: 'b', dimension_key: 'b:user:7' }
  c.props.hours = 24
  const current = c.load()
  c.requests[1].resolve({ items: ['b5m'] }); await current
  c.requests[0].reject(new Error('late failure')); await old
  assert.deepEqual(c.points.value, ['b5m'])
  assert.equal(c.error.value, '')
  assert.equal(c.requests[1].params.window, '5m')
  assert.equal(c.requests[1].params.dimension_key_prefix, 'b:user:7:model:')
})
test('refresh failure preserves same-scope data, but exposes no old data for a new dimension', async () => {
  const c = card(), initial = c.load()
  c.requests[0].resolve({ items: ['good'] }); await initial
  c.props.refreshKey++
  const refresh = c.load(); c.requests[1].reject(new Error('down')); await refresh
  assert.deepEqual(c.points.value, ['good'])
  assert.match(c.error.value, /失败/)
  c.dimension.value = 'channel'
  const changed = c.load(); c.requests[2].reject(new Error('down')); await changed
  assert.notEqual(c.loadedScope.value, c.scope.value)
})

const bucket = (minute, tpm, model = false) => ({ instance_id: 'a', dimension_key: `a:user:7${model ? ':model:kimi-k3' : ''}`, dimension_type: model ? 'instance_user_model' : 'instance_user', bucket_time: `2026-09-14T10:${minute}:00Z`, tpm })
const stamp = minute => Date.parse(`2026-09-14T10:${minute}:00Z`)
function latestCard() {
  const c = card()
  c.props.asOf = stamp('27') + 10_000
  c.props.minute = { tpm: 34_200_000, time: stamp('26'), stale: false }
  c.props.totals = [bucket('25', 75_000_000), bucket('26', 34_200_000)]
  return c
}

test('headline mismatch is marked updating while last plotted interval stays explicit; correction clears it', async () => {
  const c = latestCard(), first = c.load()
  assert.equal(c.minuteStatus.value, '数据更新中')
  c.requests[0].resolve({ items: [bucket('25', 75_000_000, true), bucket('26', 75_000_000, true)] }); await first
  assert.equal(c.minuteStatus.value, '数据更新中')
  assert.equal(c.traffic.value.lastCompleteTime, stamp('25'))
  const previousLabel = c.lastPlotTime.value
  assert.ok(previousLabel)
  assert.equal(c.props.minute.tpm, 34_200_000)
  c.props.totals[1].tpm = 75_000_000
  c.props.minute.tpm = 75_000_000
  assert.equal(c.minuteStatus.value, '')
  assert.equal(c.traffic.value.lastCompleteTime, stamp('26'))
  assert.notEqual(c.lastPlotTime.value, previousLabel)
})

test('failed breakdown and stale headline are not presented as actively updating', async () => {
  const c = latestCard(), first = c.load()
  c.requests[0].reject(new Error('network')); await first
  assert.equal(c.minuteStatus.value, '拆分更新失败')
  assert.equal(c.lastPlotTime.value, '')
  c.props.minute.stale = true
  assert.equal(c.minuteStatus.value, '数据滞后')
  c.props.minute = null
  assert.equal(c.minuteStatus.value, '')
})

test('five-minute plot never compares its mean to the independent minute headline', async () => {
  const c = latestCard()
  c.props.hours = 24
  c.props.totals = [bucket('20', 375_000_000)]
  const first = c.load()
  c.requests[0].resolve({ items: [bucket('20', 375_000_000, true)] }); await first
  assert.equal(c.minuteStatus.value, '')
  assert.equal(c.traffic.value.lastCompleteTime, stamp('20'))
  assert.equal(c.traffic.value.series[0].data.find(([time]) => time === stamp('20'))[1], 75_000_000)
  assert.ok(c.lastPlotTime.value)
})

test('verified zero is aligned; old gaps do not mark the latest aligned minute as updating', async () => {
  const c = latestCard()
  c.props.minute.tpm = 0
  c.props.totals = [bucket('25', 75_000_000)]
  c.props.verifiedBuckets = [stamp('26')]
  const first = c.load()
  c.requests[0].resolve({ items: [bucket('25', 60_000_000, true)] }); await first
  assert.equal(c.traffic.value.incompleteBuckets, 1)
  assert.equal(c.traffic.value.lastCompleteTime, stamp('26'))
  assert.equal(c.minuteStatus.value, '')
})
