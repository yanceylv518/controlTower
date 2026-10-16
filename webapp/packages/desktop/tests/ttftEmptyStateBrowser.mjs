import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createServer as createViteServer } from 'vite'
import { createTrafficMock, mockMetricHistory, trafficProfiles } from './monitorTrafficMock.mjs'

const args = Object.fromEntries(process.argv.slice(2).map(value => { const index = value.indexOf('='); return [value.slice(0, index), value.slice(index + 1)] }))
if (!args['--playwright'] || !args['--chromium']) throw new Error('Required: --playwright=PATH --chromium=PATH')
const desktop = fileURLToPath(new URL('../', import.meta.url))
const artifacts = resolve(args['--artifacts'] || fileURLToPath(new URL('../../../../output/playwright/ttft-empty/', import.meta.url)))
const { chromium } = createRequire(import.meta.url)(resolve(args['--playwright']))
trafficProfiles.standard = { users: 6, channels: 4, models: 4 }
const mock = await createTrafficMock(desktop)
mock.state.asOf = Date.parse('2026-10-16T10:10:00+08:00')
let vite, browser, context
const errors = [], consoleErrors = [], external = [], results = [], calls = []
const fixture = { first: 'normal', second: 'non-stream' }
let caseIndex = 0

function metricRows(params) {
  return mockMetricHistory(params, mock.state).map(row => {
    const id = Number((row.dimension_key.match(/^mock-[ab]:channel:(\d+)/) || row.dimension_key.match(/:model:.*inner-(\d+)/))?.[1])
    const mode = id === 1 ? fixture.first : id === 2 ? fixture.second : id === 3 ? 'missing-ttft' : id === 4 ? 'no-requests' : 'normal'
    const item = { ...row, consume_count: row.request_count, stream_rate: .5 }
    if (mode === 'normal') return item
    if (mode === 'zero') return { ...item, ttft_p50_ms: 0, ttft_p90_ms: 0, ttft_p95_ms: 0 }
    Object.assign(item, { ttft_count: 0, ttft_p50_ms: null, ttft_p90_ms: null, ttft_p95_ms: null })
    if (mode === 'non-stream') item.stream_rate = 0
    if (mode === 'unknown-stream') item.stream_rate = null
    if (mode === 'no-requests') Object.assign(item, { request_count: 0, consume_count: 0, success_count: 0, tpm: 0, prompt_tokens: 0, completion_tokens: 0, stream_rate: null, otps: null, cache_hit_rate: null })
    return item
  })
}

try {
  await mkdir(artifacts, { recursive: true })
  vite = await createViteServer({ root: desktop, configFile: resolve(desktop, 'vite.config.ts'), server: { host: '127.0.0.1', port: 0, proxy: { '/api': { target: mock.url, changeOrigin: true } } } })
  await vite.listen()
  const origin = `http://127.0.0.1:${vite.httpServer.address().port}`
  browser = await chromium.launch({ headless: true, executablePath: resolve(args['--chromium']) })
  context = await browser.newContext({ viewport: { width: 1600, height: 1000 }, timezoneId: 'Asia/Shanghai', colorScheme: 'light' })
  await context.route('**/*', async route => {
    const url = new URL(route.request().url())
    if (['data:', 'blob:'].includes(url.protocol)) return route.continue()
    if (url.origin !== origin) { external.push(url.origin); return route.abort() }
    if (url.pathname === '/api/dashboard/metric-history') {
      const params = Object.fromEntries(url.searchParams)
      calls.push(params)
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ items: metricRows(params) }) })
    }
    return route.continue()
  })
  const page = await context.newPage()
  page.setDefaultTimeout(20000)
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
  await page.clock.install({ time: mock.state.asOf })
  const frames = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  const card = (kind, id) => page.locator('.dimension-chart-card').filter({ hasText: `合成${kind === 'channels' ? '渠道' : '模型'} ${String(id).padStart(4, '0')}` })
  async function metric(name) {
    await page.locator('[aria-label="监控指标"]').getByText(name, { exact: true }).click()
    await frames()
  }
  async function refresh() {
    mock.state.revision++
    await page.locator('button[title="刷新"]').click()
    await page.waitForLoadState('networkidle')
    await frames()
  }
  async function ready(kind) {
    await page.goto(`${origin}/${kind}`)
    await page.waitForLoadState('networkidle')
    try {
      await page.locator('.dimension-toolbar').waitFor()
      await page.locator('.status-chip.ok').click()
      await page.locator('.status-chip.idle').click()
      await page.waitForFunction(() => document.querySelectorAll('.dimension-chart-card').length === 4)
      await page.locator('[aria-label="监控视图"]').getByText('排名与明细', { exact: true }).click()
      for (const checkbox of await page.locator('.dimension-ranking .el-checkbox:not(.is-checked)').all()) await checkbox.click()
      await page.locator('[aria-label="监控视图"]').getByText('指标图表', { exact: true }).click()
    } catch (error) {
      await page.screenshot({ path: resolve(artifacts, 'initialization-failure.png'), fullPage: true })
      throw new Error(JSON.stringify({ url: page.url(), body: (await page.locator('body').innerText()).slice(0, 2400), errors, consoleErrors, calls }), { cause: error })
    }
  }
  async function empty(kind, id, text, hint) {
    const target = card(kind, id)
    await target.getByText(text, { exact: true }).waitFor()
    assert.ok((await target.innerText()).includes(hint))
    assert.equal(await target.locator('canvas').count(), 0)
    assert.equal(await target.locator('[role="status"]').count(), 1)
  }
  async function normal(kind, id = 1) {
    const canvas = card(kind, id).locator('.dimension-chart canvas').first()
    await canvas.waitFor()
    await page.waitForFunction(element => {
      const ratio = element.width / element.clientWidth
      const data = element.getContext('2d').getImageData(0, 0, element.width, element.height).data
      let colored = 0
      for (let y = 0; y < (element.clientHeight - 25) * ratio; y++) {
        for (let x = Math.floor(element.width * .85); x < element.width - 18 * ratio; x++) {
          const index = (y * element.width + x) * 4
          const channels = [data[index], data[index + 1], data[index + 2]]
          if (data[index + 3] > 64 && Math.max(...channels) - Math.min(...channels) > 32 && ++colored > 10) return true
        }
      }
      return false
    }, await canvas.elementHandle())
    assert.equal(await card(kind, id).locator('.customer-chart-empty').count(), 0)
  }
  async function checkpoint(name, run) {
    if (++caseIndex < Number(args['--from'] || 1)) return
    const started = performance.now()
    const details = await run()
    results.push({ name, elapsedMs: Math.round(performance.now() - started), ...details })
    process.stdout.write(`PASS ${JSON.stringify(results.at(-1))}\n`)
  }
  process.stdout.write(`BROWSER ${await browser.version()} ${origin}\n`)
  await ready('channels')
  await checkpoint('channel-empty-reasons-and-normal-plot', async () => {
    await card('channels', 1).locator('.traffic-chart svg').waitFor()
    await metric('TTFT')
    await normal('channels')
    await empty('channels', 2, '所选时段无流式请求', 'TPM 或 OTPS')
    await empty('channels', 3, '暂无 TTFT 样本', '已记录流式请求')
    await empty('channels', 4, '所选时段暂无请求', '有流式请求')
    const normalHeight = (await card('channels', 1).locator('.dimension-chart').boundingBox()).height
    const emptyHeight = (await card('channels', 2).locator('.dimension-chart').boundingBox()).height
    assert.ok(Math.abs(normalHeight - emptyHeight) <= 1, `height mismatch ${normalHeight}/${emptyHeight}`)
    await page.screenshot({ path: resolve(artifacts, 'ttft-light.png'), fullPage: true })
    return { normalHeight, emptyHeight }
  })
  await checkpoint('empty-to-data-recovery-and-zero-value', async () => {
    for (let iteration = 0; iteration < 2; iteration++) {
      const before = await card('channels', 1).locator('.dimension-chart .customer-chart-canvas').getAttribute('_echarts_instance_')
      fixture.first = 'non-stream'; await refresh()
      await empty('channels', 1, '所选时段无流式请求', 'TPM 或 OTPS')
      fixture.first = iteration ? 'normal' : 'zero'; await refresh()
      await normal('channels')
      const after = await card('channels', 1).locator('.dimension-chart .customer-chart-canvas').getAttribute('_echarts_instance_')
      assert.ok(before && after && before !== after)
      if (!iteration) assert.ok((await card('channels', 1).locator('.dimension-headline').innerText()).includes('0.00s'))
    }
    return { recoveries: 2, zeroRendered: true }
  })
  await checkpoint('unknown-stream-information-is-not-labeled-non-streaming', async () => {
    fixture.second = 'unknown-stream'; await refresh()
    await empty('channels', 2, '暂无 TTFT 样本', 'TTFT 需要流式请求')
    assert.ok(!(await card('channels', 2).innerText()).includes('所选时段无流式请求'))
    fixture.second = 'non-stream'; await refresh()
    await empty('channels', 2, '所选时段无流式请求', 'TPM 或 OTPS')
    return { unknownPreserved: true }
  })
  await checkpoint('dark-theme-and-normal-chart-resizing', async () => {
    await page.emulateMedia({ colorScheme: 'dark' })
    await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark')
    await normal('channels')
    await empty('channels', 2, '所选时段无流式请求', 'TPM 或 OTPS')
    await page.screenshot({ path: resolve(artifacts, 'ttft-dark.png'), fullPage: true })
    return { theme: 'dark' }
  })
  await checkpoint('other-shared-metrics-keep-rendering', async () => {
    await page.emulateMedia({ colorScheme: 'dark' })
    await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark')
    for (const name of ['OTPS', '缓存命中率']) {
      await metric(name)
      await normal('channels')
    }
    await metric('TPM')
    await card('channels', 1).locator('.traffic-chart svg').waitFor()
    await metric('TTFT'); await normal('channels')
    return { metrics: ['OTPS', '缓存命中率', 'TPM', 'TTFT'] }
  })
  await checkpoint('model-view-and-one-five-minute-windows', async () => {
    await ready('models')
    await metric('TTFT'); await normal('models')
    await empty('models', 2, '所选时段无流式请求', 'TPM 或 OTPS')
    await empty('models', 3, '暂无 TTFT 样本', '已记录流式请求')
    for (const hours of [24, 6, 1]) {
      await page.getByText(`${hours}小时`, { exact: true }).click()
      await page.waitForLoadState('networkidle')
      await normal('models')
      await empty('models', 2, '所选时段无流式请求', 'TPM 或 OTPS')
      assert.ok(calls.some(call => call.dimension_type === 'instance_model' && call.hours === String(hours) && call.window === (hours === 24 ? '5m' : '1m')))
    }
    return { hours: [1, 6, 24] }
  })
  await checkpoint('mobile-layout-and-hint-wrapping', async () => {
    await page.setViewportSize({ width: 390, height: 980 })
    await page.emulateMedia({ colorScheme: 'light' })
    await ready('channels'); await metric('TTFT')
    await normal('channels')
    await empty('channels', 2, '所选时段无流式请求', 'TPM 或 OTPS')
    const emptyBox = await card('channels', 2).locator('.customer-chart-empty').boundingBox()
    assert.ok(emptyBox.x >= 0 && emptyBox.x + emptyBox.width <= 391)
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
    assert.ok(overflow <= 1, `horizontal overflow ${overflow}`)
    await card('channels', 2).scrollIntoViewIfNeeded()
    await page.screenshot({ path: resolve(artifacts, 'ttft-mobile.png'), fullPage: true })
    return { width: 390, overflow }
  })
  assert.deepEqual(errors, [])
  assert.deepEqual(consoleErrors, [])
  assert.deepEqual(external, [])
  process.stdout.write(`RESULT ${JSON.stringify({ cases: results.length, skipped: caseIndex - results.length, results, pageErrors: errors, consoleErrors, externalRequests: external, artifacts })}\n`)
} finally {
  await context?.close()
  await browser?.close()
  await vite?.close()
  await mock.close()
}
