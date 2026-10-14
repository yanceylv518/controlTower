import assert from 'node:assert/strict'
import { after, before, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createMockServer } from './tuningPerformanceMock.mjs'

let server, base
const config = { enabled: true, days: [1, 2, 3, 4, 5, 6, 7], windows: [{ start: '00:00', end: '08:00' }], revision: 0, align_now: false }
before(async () => {
  server = await createMockServer('test', fileURLToPath(new URL('../', import.meta.url)), { total: 400, delay: 0 })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  base = `http://127.0.0.1:${server.address().port}/api/dashboard/tuning/channel-schedules`
})
after(() => new Promise(resolve => { server.once('close', resolve); server.stopMock() }))
const preview = async value => {
  const response = await fetch(base + '/preview?site_id=mock-a&config=' + encodeURIComponent(JSON.stringify(value)))
  return { status: response.status, value: await response.json() }
}
const list = async (site = 'mock-a') => (await fetch(base + '?site_id=' + site)).json()
const write = async (id, value, method = 'PUT') => {
  const response = await fetch(base + `/${id}?site_id=mock-a`, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ...value, confirm: true }) })
  return { status: response.status, value: await response.json() }
}

test('mock exposes the real server preview contract for daily and cross-week windows', async () => {
  const daily = await preview(config)
  assert.equal(daily.status, 200)
  assert.deepEqual(daily.value.effective_windows, Array.from({ length: 7 }, (_, day) => ({ start_minute: day * 1440, end_minute: day * 1440 + 480 })))
  assert.equal(typeof daily.value.allowed, 'boolean')
  assert.ok(Number.isFinite(Date.parse(daily.value.next_at)))
  const cross = await preview({ ...config, days: [7], windows: [{ start: '22:00', end: '08:00' }] })
  assert.equal(cross.status, 200)
  assert.deepEqual(cross.value.effective_windows, [{ start_minute: 9960, end_minute: 10560 }])
})

test('mock rejects invalid schedule drafts instead of returning successful placeholder previews', async () => {
  for (const invalid of [{ ...config, days: [0] }, { ...config, windows: [{ start: '00:00', end: '00:00' }] }, { ...config, unsupported: true }]) {
    const result = await preview(invalid)
    assert.equal(result.status, 400)
    assert.equal(result.value.error, 'invalid_schedule')
  }
})

test('mock saves, reads, edits and deletes schedules with revision and site isolation', async () => {
  const saved = await write(313, config)
  assert.equal(saved.status, 200)
  assert.deepEqual(saved.value, { saved: true, revision: 1, changed: true })
  const plan = (await list()).items.find(item => item.channel_id === 313)
  assert.equal(plan.site_id, 'mock-a')
  assert.deepEqual(plan.windows, config.windows)
  assert.equal(typeof plan.allowed, 'boolean')
  assert.ok(plan.next_at && plan.updated_at)
  assert.deepEqual((await list('mock-b')).items, [])
  assert.equal((await write(313, config)).status, 409)
  const version = { ...config, revision: 1, expected_updated_at: plan.updated_at }
  assert.deepEqual((await write(313, version)).value, { saved: true, revision: 1, changed: false })
  const update = await write(313, { ...version, windows: [{ start: '22:00', end: '08:00' }] })
  assert.equal(update.value.revision, 2)
  assert.equal((await write(313, version, 'DELETE')).status, 409)
  const latest = (await list()).items.find(item => item.channel_id === 313)
  assert.equal((await write(313, { revision: 2, expected_updated_at: latest.updated_at }, 'DELETE')).status, 200)
  assert.deepEqual((await list()).items, [])
})

test('concurrent mock schedule writes cannot overwrite the same revision', async () => {
  const results = await Promise.all([write(314, config), write(314, { ...config, windows: [{ start: '22:00', end: '08:00' }] })])
  assert.deepEqual(results.map(result => result.status).sort(), [200, 409])
  const plan = (await list()).items.find(item => item.channel_id === 314)
  assert.equal(plan.revision, 1)
  await write(314, { revision: plan.revision, expected_updated_at: plan.updated_at }, 'DELETE')
})
