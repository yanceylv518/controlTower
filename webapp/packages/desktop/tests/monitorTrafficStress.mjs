import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { resolve } from 'node:path'
import { createTrafficMock, mockMetricHistory, trafficProfiles } from './monitorTrafficMock.mjs'

const args = Object.fromEntries(process.argv.slice(2).map(value => { const index = value.indexOf('='); return [value.slice(0, index), value.slice(index + 1)] }))
if (!args['--playwright'] || !args['--chromium'] || !args['--directory']) throw new Error('Required: --playwright=PATH --chromium=PATH --directory=BUILD_DIRECTORY')
const { chromium } = createRequire(import.meta.url)(resolve(args['--playwright']))
const mock = await createTrafficMock(resolve(args['--directory']))
const browser = await chromium.launch({ headless: true, executablePath: resolve(args['--chromium']) })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, timezoneId: 'Asia/Shanghai' })
const errors = [], consoleErrors = [], external = [], results = []
await context.route('**/*', route => {
  const url = new URL(route.request().url())
  if (url.origin === mock.url || ['data:', 'blob:'].includes(url.protocol)) return route.continue()
  external.push(url.origin)
  return route.abort()
})
await context.addInitScript(() => {
  window.__trafficTasks = []
  new PerformanceObserver(list => { for (const item of list.getEntries()) window.__trafficTasks.push({ at: item.startTime, duration: item.duration }) }).observe({ type: 'longtask', buffered: true })
})
const page = await context.newPage()
page.setDefaultTimeout(10000)
page.on('pageerror', error => errors.push(error.message))
page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()) })
await page.clock.setFixedTime(mock.state.asOf)
const frames = () => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
const cards = kind => page.locator(kind === 'customers' ? '.traffic-card' : '.dimension-chart-card')
async function ready(kind, count) {
  await page.waitForFunction(({ kind, count }) => document.querySelectorAll(kind === 'customers' ? '.traffic-card' : '.dimension-chart-card').length === count, { kind, count }, { timeout: 30000 })
  await page.waitForSelector('.traffic-chart svg', { timeout: 30000 })
  await page.waitForLoadState('networkidle')
}
async function metric(value) {
  await page.locator('[aria-label="监控指标"]').getByText(value, { exact: true }).click()
  await frames()
  await page.waitForSelector(`.traffic-chart[aria-label*="${value}"] svg`, { timeout: 30000 })
}
async function search(kind, value) {
  const input = page.locator('.monitor-search input')
  await input.fill(value)
  await input.press('Escape')
  await frames()
  if (value) await ready(kind, 1)
}
async function closeDialog() {
  await page.getByRole('dialog').getByRole('button', { name: 'Close this dialog' }).click()
  await page.getByRole('dialog').waitFor({ state: 'hidden' })
}
async function refresh() {
  mock.state.revision++
  await page.locator('button[title="刷新"]').click()
  await page.waitForLoadState('networkidle')
  await frames()
  await page.waitForTimeout(100)
  await page.waitForLoadState('networkidle')
}
async function checkpoint(name, run) {
  const started = performance.now()
  process.stdout.write(`START ${name}\n`)
  const data = await run()
  const result = { name, elapsedMs: Math.round(performance.now() - started), ...data }
  results.push(result)
  process.stdout.write(`RESULT ${JSON.stringify(result)}\n`)
}
try {
  process.stdout.write(`BROWSER ${await browser.version()} ${mock.url}\n`)
  if (!args['--scenario']) {
    await checkpoint('customer-functional', async () => {
      await page.goto(mock.url + '/customers')
      await ready('customers', trafficProfiles.standard.users)
      await search('customers', '合成客户 0001')
      const count = mock.state.calls.length
      await metric('RPM')
      await page.waitForLoadState('networkidle')
      const source = mockMetricHistory({ instance_id: 'mock-a', window: '1m', dimension_type: 'instance_user', dimension_key: 'mock-a:user:1', hours: 1 }, mock.state)
      const latest = source.filter(row => Date.parse(row.bucket_time) + 60000 <= mock.state.asOf).at(-1)
      assert.equal(await cards('customers').locator('.compact-rate strong').innerText(), String(latest.request_count))
      assert.equal(mock.state.calls.length, count, 'metric switch must reuse this loaded customer data')
      await cards('customers').getByRole('button', { name: '弹窗查看大图', exact: true }).click()
      const dialog = page.getByRole('dialog')
      await dialog.waitFor()
      assert.match(await dialog.innerText(), /RPM 流量构成/)
      assert.match(await dialog.innerText(), /平均 RPM \/ 占比/)
      await dialog.getByRole('button', { name: '清空', exact: true }).click()
      await page.getByText('请勾选列表中的模型', { exact: false }).waitFor()
      await dialog.getByRole('button', { name: '全选', exact: true }).click()
      await dialog.locator('.traffic-chart svg').waitFor()
      await closeDialog()
      await cards('customers').getByText('按渠道', { exact: true }).click()
      await page.waitForLoadState('networkidle')
      await cards('customers').locator('.traffic-chart svg').waitFor()
      assert.match(await cards('customers').innerText(), /合成渠道/)
      await metric('TPM'); await metric('RPM')
      assert.equal(await cards('customers').locator('.compact-rate strong').innerText(), String(latest.request_count))
      return { customers: trafficProfiles.standard.users, rpm: latest.request_count, errors: errors.length }
    })
    await checkpoint('customer-failure-partial-retry', async () => {
      mock.state.partial = true
      await refresh()
      await page.getByText('部分时段拆分未齐', { exact: true }).waitFor()
      mock.state.partial = false
      await refresh()
      await page.getByText('部分时段拆分未齐', { exact: true }).waitFor({ state: 'hidden' })
      mock.state.failure = { dimension: 'instance_user_channel', remaining: 1000, status: 403 }
      await refresh()
      await page.getByText('暂无查看权限，请联系管理员', { exact: false }).waitFor()
      mock.state.failure = null
      await cards('customers').getByRole('button', { name: '重试', exact: true }).click()
      await page.getByText('暂无查看权限，请联系管理员', { exact: false }).waitFor({ state: 'hidden' })
      return { partialRecovered: true, permissionRetryRecovered: true }
    })
    await checkpoint('dimension-functional', async () => {
      for (const kind of ['channels', 'models']) {
        await page.goto(`${mock.url}/${kind}`)
        await ready(kind, trafficProfiles.standard[kind])
        await search(kind, kind === 'channels' ? '合成渠道 0001' : '合成模型 0001')
        const before = mock.state.calls.length
        await metric('RPM')
        await page.waitForLoadState('networkidle')
        assert.equal(mock.state.calls.length, before, 'same dimension metric switch must reuse loaded data')
        assert.match(await cards(kind).innerText(), /RPM/)
        await cards(kind).getByRole('button', { name: '弹窗查看大图', exact: true }).click()
        await page.getByRole('dialog').waitFor()
        await page.getByRole('dialog').locator('.traffic-chart svg').waitFor()
        assert.match(await page.getByRole('dialog').innerText(), /平均 RPM \/ 占比/)
        if (kind === 'models') {
          await page.getByRole('dialog').getByText('按渠道', { exact: true }).click()
          await page.waitForLoadState('networkidle')
          await page.getByRole('dialog').locator('.traffic-chart svg').waitFor()
          assert.match(await page.getByRole('dialog').innerText(), /合成渠道/)
        }
        await closeDialog()
        await metric('TPM'); await metric('RPM')
      }
      return { channels: trafficProfiles.standard.channels, models: trafficProfiles.standard.models }
    })
    await checkpoint('model-failure-retry-and-mobile', async () => {
      mock.state.failure = { dimension: 'instance_model_channel', remaining: 1000, status: 500 }
      await refresh()
      await page.getByText('渠道拆分加载失败，请重试', { exact: false }).waitFor()
      mock.state.failure = null
      await cards('models').getByRole('button', { name: '重试', exact: true }).click()
      await page.getByText('渠道拆分加载失败，请重试', { exact: false }).waitFor({ state: 'hidden' })
      await page.setViewportSize({ width: 390, height: 844 })
      await frames()
      const widths = await page.evaluate(() => ({ viewport: innerWidth, content: document.documentElement.scrollWidth }))
      assert.ok(widths.content <= widths.viewport, JSON.stringify(widths))
      await cards('models').getByRole('button', { name: '弹窗查看大图', exact: true }).click()
      await page.getByRole('dialog').waitFor()
      await page.getByRole('dialog').locator('.traffic-chart svg').waitFor()
      await page.waitForLoadState('networkidle')
      await page.screenshot({ path: resolve(args['--directory'], '../rpm-mobile.png') })
      await closeDialog()
      await page.setViewportSize({ width: 1440, height: 1000 })
      return { retryRecovered: true, widths }
    })
  }
  for (const [profile, hours] of [['large', 1], ['standard', 24]]) {
    mock.state.profile = profile
    mock.state.asOf = Date.now()
    await page.clock.setFixedTime(mock.state.asOf)
    for (const kind of ['customers', 'channels', 'models']) {
      if (args['--scenario'] && args['--scenario'] !== `${profile}-${hours}h-${kind}`) continue
      await checkpoint(`${profile}-${hours}h-${kind}`, async () => {
        const callStart = mock.state.responses.length
        await page.goto(`${mock.url}/${kind}`)
        await ready(kind, trafficProfiles[profile][kind === 'customers' ? 'users' : kind])
        if (hours === 24) {
          await page.getByText('24小时', { exact: true }).click()
          await page.waitForLoadState('networkidle')
          await ready(kind, trafficProfiles[profile][kind === 'customers' ? 'users' : kind])
        }
        await metric('RPM')
        await page.waitForLoadState('networkidle')
        const taskStart = await page.evaluate(() => window.__trafficTasks.length)
        const started = performance.now()
        for (let index = 0; index < 12; index++) {
          await metric(index % 2 ? 'RPM' : 'TPM')
          if (index % 4 === 3) await refresh()
        }
        assert.equal(await cards(kind).count(), trafficProfiles[profile][kind === 'customers' ? 'users' : kind])
        assert.doesNotMatch(await cards(kind).first().innerText(), /NaN|Infinity|undefined/)
        await page.waitForTimeout(100)
        const performanceData = await page.evaluate(taskStart => {
          const tasks = window.__trafficTasks.slice(taskStart)
          return { longTasks: tasks.length, totalLongTaskMs: Math.round(tasks.reduce((sum, task) => sum + task.duration, 0)), maxLongTaskMs: Math.round(Math.max(0, ...tasks.map(task => task.duration))) }
        }, taskStart)
        const cdp = await context.newCDPSession(page)
        const heap = await cdp.send('Runtime.getHeapUsage')
        await cdp.detach()
        const renderedCharts = await page.locator('.traffic-chart svg').count()
        if (kind === 'channels') {
          await cards(kind).last().scrollIntoViewIfNeeded()
          await cards(kind).last().locator('.traffic-chart svg').waitFor()
          assert.doesNotMatch(await cards(kind).last().innerText(), /NaN|Infinity|undefined/)
        }
        const payloads = mock.state.responses.slice(callStart)
        return { ...performanceData, sampleMs: Math.round(performance.now() - started), heapMB: Math.round(heap.usedSize / 1048576), cards: await cards(kind).count(), renderedCharts, lastChannelChart: kind === 'channels', metricSwitches: 12, refreshes: 3, maxResponseRows: Math.max(...payloads.map(row => row.rows)), maxResponseBytes: Math.max(...payloads.map(row => row.bytes)), errors: errors.length }
      })
    }
  }
  if (!args['--scenario'] || args['--scenario'] === 'race') {
    await checkpoint('range-site-race-and-empty-search', async () => {
      mock.state.delay = 200
      await page.goto(mock.url + '/customers')
      await ready('customers', trafficProfiles.standard.users)
      await metric('RPM')
      await page.locator('button[title="刷新"]').click()
      await page.locator('.user .el-select').click()
      await page.getByRole('option', { name: 'mock-b', exact: true }).click()
      await page.getByText('24小时', { exact: true }).click()
      await page.getByText('6小时', { exact: true }).click()
      await ready('customers', trafficProfiles.standard.users)
      await page.waitForLoadState('networkidle')
      assert.ok((await cards('customers').locator('.customer-name-line').allTextContents()).every(name => name.trim().startsWith('B 合成客户')))
      assert.ok((await cards('customers').locator('.traffic-caption').allTextContents()).every(text => text.includes('近 6 小时')))
      const input = page.locator('.monitor-search input')
      await input.fill('不存在的合成客户')
      await page.locator('.customer-metric-view').getByText('没有匹配的客户', { exact: true }).waitFor()
      assert.equal(await cards('customers').count(), 0)
      await input.fill('B 合成客户 0001')
      await input.press('Escape')
      await ready('customers', 1)
      await cards('customers').locator('.el-loading-mask').waitFor({ state: 'hidden' })
      await frames()
      await page.screenshot({ path: resolve(args['--directory'], '../rpm-desktop.png') })
      return { finalSite: 'mock-b', finalHours: 6, emptyRecovered: true, cards: await cards('customers').count() }
    })
  }
  if (args['--scenario'] === 'mobile') {
    await checkpoint('mobile-dialog-finished-render', async () => {
      await page.goto(mock.url + '/models')
      await ready('models', trafficProfiles.standard.models)
      await search('models', '合成模型 0001')
      await metric('RPM')
      await cards('models').getByText('按渠道', { exact: true }).click()
      await page.setViewportSize({ width: 390, height: 844 })
      await frames()
      await cards('models').getByRole('button', { name: '弹窗查看大图', exact: true }).click()
      const dialog = page.getByRole('dialog')
      await dialog.locator('.traffic-chart svg').waitFor()
      await dialog.evaluate(async node => {
        const overlay = node.closest('.el-overlay') || node
        await Promise.all(overlay.getAnimations({ subtree: true })
          .filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity)
          .map(animation => animation.finished.catch(() => {})))
      })
      await frames()
      const bounds = await dialog.boundingBox()
      assert.ok(bounds.x >= 0 && bounds.width <= 390, JSON.stringify(bounds))
      await page.screenshot({ path: resolve(args['--directory'], '../rpm-mobile.png') })
      return { viewport: 390, dialogWidth: bounds.width }
    })
  }
  assert.deepEqual(errors, [])
  assert.deepEqual(external, [])
  process.stdout.write(`SUMMARY ${JSON.stringify({ passed: results.length, errors, consoleErrors, blockedExternal: external, peakRequests: mock.state.peak, totalRequests: mock.state.calls.length })}\n`)
} catch (error) {
  process.stderr.write(`FAIL ${error.stack}\n`)
  process.stderr.write(`STATE ${JSON.stringify({ url: page.url(), errors, consoleErrors, text: (await page.locator('body').innerText().catch(() => '')).slice(0, 5000), recentCalls: mock.state.calls.slice(-6) })}\n`)
  await page.screenshot({ path: resolve(args['--directory'], '../failure.png') }).catch(() => {})
  process.exitCode = 1
} finally {
  await browser.close()
  await mock.close()
}
