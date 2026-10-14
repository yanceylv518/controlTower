import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const load = async path => {
  const source = readFileSync(new URL(path, import.meta.url), 'utf8')
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
  return import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
}
const { dashboardApi } = await load('../../shared/src/api/dashboard.ts')
const { ApiClient } = await load('../../shared/src/client.ts')
const reads = [
  ['policy', '/policy', (api, signal) => api.tuningPolicy('mock-a', signal)],
  ['states', '/continuous-states', (api, signal) => api.tuningContinuousStates('mock-a', signal)],
  ['bases', '/base-values', (api, signal) => api.tuningBaseValues('mock-a', 'mock-model', signal)],
  ['directory', '/channels', (api, signal) => api.tuningChannels('mock-a', signal)],
  ['records', '/recommendations', (api, signal) => api.tuningRecommendations('mock-a', 300, undefined, signal)],
  ['schedules', '/channel-schedules', (api, signal) => api.channelSchedules('mock-a', signal)],
]

test('every tuning read cancels through the real API client without changing GET scope', async t => {
  let observed
  t.mock.method(globalThis, 'fetch', (url, init) => {
    observed = { url, init }
    return new Promise((_resolve, reject) => init.signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true }))
  })
  const api = dashboardApi(new ApiClient())
  for (const [name, suffix, read] of reads) await t.test(name, async () => {
    const controller = new AbortController(), result = read(api, controller.signal)
    const url = new URL(observed.url, 'http://mock.local')
    assert.equal(url.pathname, '/api/dashboard/tuning' + suffix)
    assert.equal(url.searchParams.get('site_id'), 'mock-a')
    assert.equal(observed.init.method, 'GET')
    assert.equal(observed.init.signal, controller.signal)
    assert.equal(observed.init.credentials, 'same-origin')
    assert.equal(observed.init.body, undefined)
    controller.abort()
    await assert.rejects(result, { name: 'AbortError' })
  })
})

test('existing tuning callers work without a cancellation argument', async t => {
  const requests = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    requests.push({ url, init })
    return new Response(JSON.stringify({ items: [] }), { status: 200 })
  })
  const api = dashboardApi(new ApiClient())
  for (const [, , read] of reads) assert.deepEqual(await read(api), { items: [] })
  assert.equal(requests.length, reads.length)
  assert.ok(requests.every(request => request.init.signal === undefined))
  assert.equal(new URL(requests[2].url, 'http://mock.local').searchParams.get('model'), 'mock-model')
  assert.equal(new URL(requests[4].url, 'http://mock.local').searchParams.get('limit'), '300')
})

test('tuning write contracts retain confirmation, values and mutation headers', async t => {
  const requests = []
  t.mock.method(globalThis, 'fetch', async (url, init) => {
    requests.push({ url, init })
    return new Response(JSON.stringify({ items: [] }), { status: 200 })
  })
  const api = dashboardApi(new ApiClient())
  const items = [{ channel_id: 1, model_name: 'mock-model', base_weight: 0, base_priority: 0 }]
  await api.saveTuningBaseValues('mock-a', items)
  await api.saveTuningChannelStatus('mock-a', 1, 2)
  assert.deepEqual(JSON.parse(requests[0].init.body), { items })
  assert.deepEqual(JSON.parse(requests[1].init.body), { status: 2, confirm: true })
  assert.ok(requests.every(request => request.init.method === 'PUT' && request.init.headers.get('X-Requested-With') === 'XMLHttpRequest'))
  assert.ok(requests.every(request => request.init.headers.get('Content-Type') === 'application/json'))
  assert.ok(requests.every(request => request.init.signal === undefined))
})
