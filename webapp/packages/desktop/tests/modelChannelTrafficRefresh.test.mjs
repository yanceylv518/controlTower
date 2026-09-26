import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, nextTick, reactive, ref, shallowRef, watch } from 'vue'

const sfc = readFileSync(new URL('../src/components/ModelChannelTraffic.vue', import.meta.url), 'utf8')
const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const trafficApi = {}
new Function('exports', ts.transpileModule(readFileSync(new URL('../src/utils/customerTraffic.ts', import.meta.url), 'utf8'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS },
}).outputText)(trafficApi)
const deferred = () => {
  let resolve, reject
  const promise = new Promise((a, b) => { resolve = a; reject = b })
  return { promise, resolve, reject }
}
const flush = async () => { await Promise.resolve(); await nextTick(); await Promise.resolve() }
const now = Date.parse('2026-09-26T10:10:00Z')
const model = (instance = 'a', name = 'provider:channel:inner') => ({ instance_id: instance, dimension_key: `${instance}:model:${name}` })
const metric = (m, tpm, channel) => ({ ...m, dimension_key: `${m.dimension_key}${channel ? `:channel:${channel}` : ''}`,
  dimension_type: channel ? 'instance_model_channel' : 'instance_model', bucket_time: '2026-09-26T10:05:00Z', tpm, display_name: '',
})

// Execute the component script with actual Vue reactivity and watch scheduling.
// Only network, mount hooks and IntersectionObserver are replaced at the edge.
function card(overrides = {}, initialMode = 'channel') {
  const props = reactive({ model: model(), totals: [], hours: 1, asOf: now, refreshKey: 1, active: true, ...overrides })
  const requests = [], mounted = [], unmounting = [], stops = [], observers = []
  const dashboard = { metricHistory: params => {
    const pending = deferred(); requests.push({ ...pending, params }); return pending.promise
  } }
  class Observer {
    constructor(callback, options) { this.callback = callback; this.options = options; this.disconnected = false; observers.push(this) }
    observe(element) { this.element = element }
    disconnect() { this.disconnected = true }
  }
  const create = new Function('computed', 'ref', 'shallowRef', 'watch', 'onBeforeUnmount', 'onMounted', 'defineProps', 'defineEmits',
    'dashboard', 'buildModelTraffic', 'IntersectionObserver', `${compiled}\nreturn {load, mode, points, loadedScope, loadedRevision, scope, error, loading, nearViewport, traffic, lastPlotTime, element};`)
  const state = create(computed, ref, shallowRef, (...args) => { const stop = watch(...args); stops.push(stop); return stop },
    callback => unmounting.push(callback), callback => mounted.push(callback), () => props, () => () => {},
    dashboard, trafficApi.buildModelTraffic, Observer)
  if (initialMode) state.mode.value = initialMode
  state.element.value = {}
  mounted.forEach(callback => callback())
  return { ...state, props, requests, observers,
    async visible(value = true) { observers[0].callback([{ isIntersecting: value }]); await nextTick() },
    unmount() { unmounting.forEach(callback => callback()); stops.forEach(stop => stop()) },
  }
}

test('inactive and offscreen model cards defer requests and catch up when active and visible', async t => {
  const c = card({ active: false }); t.after(c.unmount)
  await c.visible()
  c.props.refreshKey++; await nextTick()
  assert.equal(c.requests.length, 0)
  c.props.active = true; await nextTick()
  assert.equal(c.requests.length, 1)
  c.requests[0].resolve({ items: [] }); await flush()
  await c.visible(false)
  c.props.refreshKey += 2; await nextTick()
  assert.equal(c.requests.length, 1)
  await c.visible()
  assert.equal(c.requests.length, 2)
  c.requests[1].resolve({ items: [] }); await flush()
  assert.equal(c.loadedRevision.value, c.props.refreshKey)
  c.props.active = false; c.props.refreshKey++; await nextTick()
  assert.equal(c.requests.length, 2)
  assert.equal(c.observers[0].options.rootMargin, '400px 0px')
})

test('model channel requests use the full scoped model key and matching 1m or 5m windows', async t => {
  const c = card({ hours: 24 }); t.after(c.unmount)
  await c.visible()
  assert.deepEqual(c.requests[0].params, {
    instance_id: 'a', window: '5m', dimension_type: 'instance_model_channel',
    dimension_key_prefix: 'a:model:provider:channel:inner:channel:', hours: 24,
  })
  c.requests[0].resolve({ items: [] }); await flush()
  c.props.hours = 6; await nextTick()
  assert.equal(c.requests[1].params.window, '1m')
  assert.equal(c.requests[1].params.hours, 6)
  c.requests[1].resolve({ items: [] }); await flush()
})

test('model TPM defaults to customers and each card switches independently', async t => {
  const a = card({}, null), b = card({ model: model('b') }, null)
  t.after(a.unmount); t.after(b.unmount)
  assert.equal(a.mode.value, 'user')
  await a.visible(); await b.visible()
  assert.equal(a.requests[0].params.dimension_type, 'instance_model_user')
  assert.equal(a.requests[0].params.dimension_key_prefix, 'a:model:provider:channel:inner:user:')
  a.requests[0].resolve({ items: [] }); b.requests[0].resolve({ items: [] }); await flush()
  a.mode.value = 'channel'; await nextTick()
  assert.equal(a.requests[1].params.dimension_type, 'instance_model_channel')
  assert.equal(b.mode.value, 'user')
  assert.equal(b.requests.length, 1)
  a.requests[1].resolve({ items: [] }); await flush()
})

test('customer and channel cache are distinct and refreshed on the next revision', async t => {
  const c = card(); t.after(c.unmount)
  c.props.totals = [metric(c.props.model, 10)]
  await c.visible()
  c.requests[0].resolve({ items: [metric(c.props.model, 10, 3)] }); await flush()
  c.mode.value = 'user'; await nextTick()
  assert.equal(c.traffic.value.series.length, 0)
  const user = { ...metric(c.props.model, 10), dimension_type: 'instance_model_user', dimension_key: `${c.props.model.dimension_key}:user:7`, display_name: 'Alice' }
  c.requests[1].resolve({ items: [user] }); await flush()
  assert.equal(c.traffic.value.series[0].name, 'Alice · #7')
  c.mode.value = 'channel'; await nextTick()
  assert.equal(c.requests.length, 2)
  assert.equal(c.traffic.value.series[0].key, '3')
  c.mode.value = 'user'; await nextTick()
  assert.equal(c.requests.length, 2)
  c.props.refreshKey++; await nextTick()
  assert.equal(c.requests[2].params.dimension_type, 'instance_model_user')
  c.requests[2].resolve({ items: [user] }); await flush()
  c.mode.value = 'channel'; await nextTick()
  assert.equal(c.requests.length, 4)
  c.requests[3].resolve({ items: [metric(c.props.model, 10, 8)] }); await flush()
  assert.equal(c.traffic.value.series[0].key, '8')
})

test('late customer response cannot replace the selected channel dimension', async t => {
  const c = card({}, null); t.after(c.unmount)
  await c.visible()
  c.mode.value = 'channel'; await nextTick()
  c.requests[1].resolve({ items: [] }); await flush()
  c.requests[0].reject(new Error('late user failure')); await flush()
  assert.equal(c.loadedScope.value, c.scope.value)
  assert.equal(c.error.value, '')
  assert.equal(c.loading.value, false)
})

test('switching model and instance hides old data and ignores late success or failure', async t => {
  const c = card(); t.after(c.unmount)
  c.props.totals = [metric(c.props.model, 10)]
  await c.visible()
  c.requests[0].resolve({ items: [metric(c.props.model, 10, 3)] }); await flush()
  assert.equal(c.traffic.value.totalTokens, 10)
  c.props.refreshKey++; await nextTick()
  c.props.model = model('b', 'next:model'); c.props.totals = [metric(c.props.model, 20)]; await nextTick()
  assert.equal(c.traffic.value.series.length, 0)
  assert.equal(c.lastPlotTime.value, '')
  c.requests[1].resolve({ items: [metric(model(), 999, 3)] }); await flush()
  assert.equal(c.loading.value, true)
  assert.equal(c.traffic.value.series.length, 0)
  c.requests[2].resolve({ items: [metric(c.props.model, 20, 7)] }); await flush()
  assert.equal(c.traffic.value.totalTokens, 20)
  assert.deepEqual(c.traffic.value.series.map(s => s.key), ['7'])
  c.props.refreshKey++; await nextTick()
  c.props.model = model('c', 'last'); c.props.totals = [metric(c.props.model, 30)]; await nextTick()
  c.requests[4].resolve({ items: [metric(c.props.model, 30, 8)] }); await flush()
  c.requests[3].reject(new Error('late previous-site failure')); await flush()
  assert.equal(c.error.value, '')
  assert.equal(c.traffic.value.totalTokens, 30)
  assert.equal(c.loadedScope.value, c.scope.value)
})

test('failed refresh preserves same-model traffic and explicit retry replaces it', async t => {
  const c = card(); t.after(c.unmount)
  c.props.totals = [metric(c.props.model, 10)]
  await c.visible()
  c.requests[0].resolve({ items: [metric(c.props.model, 10, 3)] }); await flush()
  c.props.refreshKey++; await nextTick()
  c.requests[1].reject(new Error('network unavailable')); await flush()
  assert.match(c.error.value, /加载失败/)
  assert.equal(c.loading.value, false)
  assert.equal(c.traffic.value.totalTokens, 10)
  const retry = c.load()
  assert.equal(c.error.value, '')
  c.requests[2].resolve({ items: [metric(c.props.model, 10, 8)] }); await retry
  assert.deepEqual(c.traffic.value.series.map(s => s.key), ['8'])
  await c.load()
  assert.equal(c.requests.length, 3)
})

test('slow same-scope request remains single flight and immediately catches up to newest refresh', async t => {
  const c = card(); t.after(c.unmount)
  await c.visible()
  for (let revision = 2; revision <= 4; revision++) { c.props.refreshKey = revision; await nextTick() }
  await c.load()
  assert.equal(c.requests.length, 1)
  c.requests[0].resolve({ items: [] }); await flush()
  assert.equal(c.requests.length, 2)
  assert.equal(c.loadedRevision.value, 1)
  assert.equal(c.loading.value, true)
  c.requests[1].resolve({ items: [] }); await flush()
  assert.equal(c.loadedRevision.value, 4)
  assert.equal(c.loading.value, false)
  assert.equal(c.requests.length, 2)
})

test('unmount disconnects visibility observer and prevents late response or retry updates', async () => {
  const c = card()
  await c.visible()
  const before = { points: c.points.value, scope: c.loadedScope.value, revision: c.loadedRevision.value, loading: c.loading.value, error: c.error.value }
  c.unmount()
  c.props.refreshKey++; c.props.model = model('b'); await nextTick()
  c.requests[0].resolve({ items: [metric(model(), 99, 3)] }); await flush()
  await c.load()
  assert.equal(c.requests.length, 1)
  assert.equal(c.observers[0].disconnected, true)
  assert.deepEqual({ points: c.points.value, scope: c.loadedScope.value, revision: c.loadedRevision.value, loading: c.loading.value, error: c.error.value }, before)
})
