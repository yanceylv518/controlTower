import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'

// Exercise the actual page script with controlled API promises. Lifecycle
// timers and unrelated components are excluded so request ordering is explicit.
const sfc = readFileSync(new URL('../src/views/ContinuousTuningView.vue', import.meta.url), 'utf8')
const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const row = { channel_id: 1, model_name: 'm', base_weight: 100, current_weight: 80, current_priority: 1, models: ['m'] }
const state = (requests, weight = 80) => ({ channel_id: 1, model_name: 'm', last_observed_requests: requests, proposed_weight: weight, phase: 'normal', metric_ready: true, baseline_ready: true, updated_at: '2026-09-14T00:00:00Z' })
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

function page() {
  const filters = reactive({ site_id: 'a', loadInstances: async () => {} })
  const dashboard = {
    tuningPolicy: async () => ({ mode: 'observe', policy: { dispatch_modes: { m: 'auto' } } }),
    tuningBaseValues: async () => ({ items: [{ ...row }] }),
    tuningRecommendations: async () => ({ items: [] }),
    tuningContinuousStates: async () => ({ items: [state(42)] }),
  }
  const names = ['computed', 'reactive', 'ref', 'watch', 'onMounted', 'onBeforeUnmount', 'useFiltersStore', 'dashboard', 'formatTime', 'ApiError', 'ElMessage', 'ElMessageBox']
  const create = new Function(...names, `${compiled}\nreturn { load, refreshRuntime, acceptStates, stateFor, sampleText, evaluationText, states, refreshError, bases, policy };`)
  const view = create(computed, reactive, ref, () => {}, () => {}, () => {}, () => filters, dashboard, String, class extends Error {}, {}, {})
  return { ...view, filters, dashboard }
}

test('initial unavailable state is unknown, and retry loads real samples', async () => {
  const p = page()
  p.dashboard.tuningContinuousStates = async () => { throw new Error('network down') }
  await p.load()
  assert.equal(p.sampleText(row), '—')
  assert.equal(p.evaluationText(row), '等待评估数据')
  assert.match(p.refreshError.value, /network down/)
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(42)] })
  await p.refreshRuntime()
  assert.equal(p.sampleText(row), '42/20')
  assert.equal(p.refreshError.value, '')
})

test('same-site reload failure and empty refresh preserve samples, while actual zero is accepted', async () => {
  const p = page()
  await p.load()
  p.dashboard.tuningContinuousStates = async () => { throw new Error('temporary') }
  await p.load()
  assert.equal(p.stateFor(row).last_observed_requests, 42)
  p.dashboard.tuningContinuousStates = async () => ({ items: [] })
  await p.refreshRuntime()
  assert.equal(p.stateFor(row).last_observed_requests, 42)
  assert.match(p.refreshError.value, /上次成功结果/)
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(0, 0)] })
  await p.refreshRuntime()
  assert.equal(p.stateFor(row).proposed_weight, 0)
  assert.equal(p.sampleText(row), '0/20')
  assert.equal(p.refreshError.value, '')
})

test('a failed second site cannot reuse matching channel IDs from the first', async () => {
  const p = page()
  await p.load()
  p.filters.site_id = 'b'
  assert.equal(p.stateFor(row), undefined)
  p.dashboard.tuningContinuousStates = async () => { throw new Error('site b failed') }
  await p.load()
  assert.equal(p.stateFor(row), undefined)
  assert.equal(p.sampleText(row), '—')
})

test('late refresh results cannot overwrite a newer completed refresh', async () => {
  const p = page()
  await p.load()
  const old = deferred(), latest = deferred()
  let call = 0
  p.dashboard.tuningContinuousStates = () => (++call === 1 ? old.promise : latest.promise)
  const first = p.refreshRuntime(), second = p.refreshRuntime()
  latest.resolve({ items: [state(90)] }); await second
  old.resolve({ items: [state(10)] }); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
})

test('a refresh started before a full reload cannot overwrite the reload', async () => {
  const p = page()
  await p.load()
  const old = deferred()
  p.dashboard.tuningContinuousStates = () => old.promise
  const first = p.refreshRuntime()
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(90)] })
  await p.load()
  old.resolve({ items: [state(10)] }); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
})

test('slow polling still displays completed results while a newer request is pending', async () => {
  const p = page()
  await p.load()
  const firstResponse = deferred(), secondResponse = deferred()
  let call = 0
  p.dashboard.tuningContinuousStates = () => (++call === 1 ? firstResponse.promise : secondResponse.promise)
  const first = p.refreshRuntime(), second = p.refreshRuntime()
  firstResponse.resolve({ items: [state(60)] }); await first
  assert.equal(p.stateFor(row).last_observed_requests, 60, 'polls taking over 30 seconds must not starve updates')
  secondResponse.resolve({ items: [state(90)] }); await second
  assert.equal(p.stateFor(row).last_observed_requests, 90)
})

test('late failures cannot replace a newer successful result with an error', async () => {
  const p = page()
  await p.load()
  const old = deferred(), latest = deferred()
  let call = 0
  p.dashboard.tuningContinuousStates = () => (++call === 1 ? old.promise : latest.promise)
  const first = p.refreshRuntime(), second = p.refreshRuntime()
  latest.resolve({ items: [state(90)] }); await second
  old.reject(new Error('old timeout')); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
  assert.equal(p.refreshError.value, '')
})

test('late errors from the previous site cannot affect the current site', async () => {
  const p = page()
  await p.load()
  const old = deferred()
  p.dashboard.tuningContinuousStates = () => old.promise
  const first = p.refreshRuntime()
  p.filters.site_id = 'b'
  p.dashboard.tuningContinuousStates = async () => ({ items: [state(90)] })
  await p.load()
  old.reject(new Error('site a timeout')); await first
  assert.equal(p.stateFor(row).last_observed_requests, 90)
  assert.equal(p.refreshError.value, '')
})
