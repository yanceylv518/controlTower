import { createServer } from 'node:http'
import { readFile, stat } from 'node:fs/promises'
import { readFileSync } from 'node:fs'
import { resolve, join, extname, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
import { gzipSync } from 'node:zlib'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

const root = fileURLToPath(new URL('../../../../', import.meta.url))
const require = createRequire(new URL('../package.json', import.meta.url))
const ts = require('typescript')
const source = readFileSync(join(root, 'webapp/packages/desktop/src/views/ContinuousTuningView.vue'), 'utf8')
const defaults = source.match(/^const defaults = \(\) => \((.+)\);$/m)?.[1]
if (!defaults) throw new Error('没有找到当前调权默认参数')
const code = ts.transpileModule(`const params = ${defaults};`, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
const params = new Function(`${code}; return params;`)()
const variants = [['baseline', 18982], ['optimized', 18983]]
const mime = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.woff2': 'font/woff2' }
const runFile = promisify(execFile)
const previewProgram = join(root, 'output/tuning-performance', process.platform === 'win32' ? 'schedule-preview.exe' : 'schedule-preview')
let previewBuild
function preparePreview() {
  return previewBuild ||= runFile(process.platform === 'win32' ? 'go.exe' : 'go', ['build', '-o', previewProgram, './server/internal/channelschedule/testdata/mock-preview'], { cwd: root, timeout: 60000, windowsHide: true })
}
async function schedulePreviews(configs, signal) {
  if (!configs.length) return []
  const task = runFile(previewProgram, [], { timeout: 5000, maxBuffer: 2 * 1024 * 1024, windowsHide: true, signal })
  const writing = new Promise((resolve, reject) => {
    task.child.stdin.once('error', reject)
    task.child.stdin.end(JSON.stringify({ configs, now: new Date().toISOString() }), resolve)
  })
  let output
  try { [output] = await Promise.all([task, writing]) }
  catch (error) { task.child.kill(); throw error }
  const value = JSON.parse(output.stdout)
  if (value.error) return configs.map(() => value)
  if (!Array.isArray(value.items) || value.items.length !== configs.length) throw new Error('invalid schedule preview response')
  return value.items
}
const planConfig = plan => ({ enabled: plan.enabled, days: plan.days, windows: plan.windows, revision: plan.revision, align_now: plan.align_now })

function fixture(total, revision) {
  const now = new Date().toISOString()
  const channels = [], bases = [], states = []
  for (let index = 0; index < total; index++) {
    const id = index + 1, model = index < Math.min(total, 116) ? 'mock-model-00' : `mock-model-${String(1 + index % 24).padStart(2, '0')}`
    const enabled = index >= 116 || index < 18
    const circuit = index === 18
    const current = 80 + revision % 10
    channels.push({ channel_id: id, channel_name: `合成渠道 ${String(id).padStart(4, '0')}`, status: enabled ? 'enabled' : 'disabled', weight: current, priority: 1, models: [model], group_name: index % 3 ? 'default,vip' : 'default' })
    const base = { instance_id: 'mock-a', channel_id: id, channel_name: channels.at(-1).channel_name, model_name: model, base_weight: 100, base_priority: 1, max_rpm: 1000, max_tpm: 1000000, current_weight: current, current_priority: 1, group_name: channels.at(-1).group_name, models: [model], updated_at: now, snapshot_at: now, updated_by: 'mock' }
    if (enabled || circuit) bases.push(base)
    states.push({
      instance_id: 'mock-a', channel_id: id, model_name: model, phase: circuit ? 'circuit' : 'normal', paused_reason: enabled || circuit ? '' : 'manual_disabled', circuit_disabled: circuit, circuit_status_target: 0,
      speed_sample_count: 100, speed_retry_count: 2, speed_unknown_count: 0, speed_legacy_count: 0, speed_stats_version: 1,
      k_error: 1, k_speed: 1.05, k_cache: 0.98, k_otps: 1.1, multiplier: 1.1, proposed_weight: current, last_observed_requests: 150, last_observed_errors: 1,
      metric_ready: true, baseline_ready: true, metric_ttft_p50: 0.5, metric_ttft_p90: 0.8, metric_ttft_p95: 1, baseline_ttft_p50: 0.6, baseline_ttft_p90: 1, baseline_ttft_p95: 1.2,
      metric_cache: 0.4, baseline_cache: 0.5, cache_ready: true, metric_otps: 80, baseline_otps: 70, otps_ready: true, otps_sample_count: 100, otps_retry_count: 2, otps_unknown_count: 0, otps_stats_version: 1,
      metric_rpm: 30, metric_tpm: 10000, smoothed_error_rate: 0.01, updated_at: now, next_probe_at: circuit ? now : null,
      probe_speed: { ready: true, samples: 100, mean_seconds: 0.5, baseline_seconds: 0.6, factor: 1.05, evaluated_at: now },
      capacity: { initialized: true, active: false, phase: 'normal', max_rpm: 1000, max_tpm: 1000000, sample_at: now, confirmed_weight: current, raw_target: current, bound_weight: current, utilization: 0.1 },
      evaluation: { base_weight: 100, evaluated_at: now, performance_evaluated: true, params: { ...params } },
    })
  }
  const items = Array.from({ length: 300 }, (_, i) => ({ id: `mock-record-${revision}-${i}`, instance_id: 'mock-a', channel_id: 1 + i % total, channel_name: channels[i % total].channel_name, rule: 'weight_write', created_at: now, current_weight: 80, proposed_weight: 90, current_priority: 1, proposed_priority: 1, mode_at_creation: 'auto', status: 'succeeded', evidence: { model: channels[i % total].models[0], evaluation: states[i % total].evaluation, state: states[i % total] }, outcome: { command_status: 'succeeded', confirmed_weight: 90 }, outcome_at: now }))
  const coverage = Object.fromEntries(states.map(s => [s.channel_id, { scheduled: { due: 90, sent: 89, global_limited: 1, channel_limited: 0, evidence_limited: 0, last_due_at: now, last_sent_at: now, last_skipped_at: now }, accelerated: { due: 1, sent: 1, global_limited: 0, channel_limited: 0, evidence_limited: 0, last_due_at: now, last_sent_at: now } }]))
  return { channels, bases, states, records: items, coverage, now }
}

async function body(request) {
  let text = ''
  for await (const chunk of request) {
    text += chunk
    if (text.length > 1048576) throw new Error('mock请求体超限')
  }
  return text ? JSON.parse(text) : {}
}

export async function createMockServer(name, directory, { total = 210, delay = 35 } = {}) {
  await preparePreview()
  const state = { total, revision: 0, interval: 0, timer: null, fixture: fixture(total, 0), calls: {}, peak: {}, active: {}, waiters: new Set(), baseEdits: new Map(), modeEdits: {}, plans: [], delay, planClock: 0 }
  const modes = () => Object.fromEntries(Array.from({ length: 25 }, (_, i) => [`mock-model-${String(i).padStart(2, '0')}`, i < 7 ? 'auto' : i < 11 ? 'observe' : 'off']))
  const notify = () => {
    state.revision++
    state.fixture = fixture(state.total, state.revision)
    for (const done of [...state.waiters]) done()
  }
  function json(response, value, status = 200) {
    if (response.destroyed || response.writableEnded) return
    const bytes = Buffer.from(JSON.stringify(value)), zipped = gzipSync(bytes)
    response.writeHead(status, { 'Content-Type': 'application/json', 'Content-Encoding': 'gzip', 'Cache-Control': 'no-store' })
    response.end(zipped)
  }
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1'), path = url.pathname
    const key = `${request.method} ${path}${url.searchParams.has('rates_only') ? '?rates_only' : ''}`
    state.calls[key] = (state.calls[key] || 0) + 1
    state.active[key] = (state.active[key] || 0) + 1
    state.peak[key] = Math.max(state.peak[key] || 0, state.active[key])
    response.once('close', () => state.active[key]--)
    try {
      if (path === '/__mock/stats') return json(response, { name, total: state.total, revision: state.revision, interval: state.interval, calls: state.calls, peak: state.peak, sizes: { states: JSON.stringify(state.fixture.states).length, records: JSON.stringify(state.fixture.records).length } })
      if (path === '/__mock/control' && request.method === 'POST') {
        const input = await body(request)
        if (state.timer) clearInterval(state.timer)
        state.timer = null
        if (input.total != null) {
          if (!Number.isInteger(input.total) || input.total < 116 || input.total > 2000) return json(response, { error: 'invalid_total' }, 400)
          state.total = input.total
        }
        if (input.delay != null) state.delay = Math.max(0, Math.min(5000, Number(input.delay) || 0))
        state.interval = Math.max(0, Math.min(30000, Number(input.interval) || 0))
        if (input.reset) { state.calls = {}; state.peak = {}; state.baseEdits.clear(); state.modeEdits = {}; state.plans = [] }
        notify()
        if (state.interval) state.timer = setInterval(notify, state.interval)
        return json(response, { ok: true, total: state.total, interval: state.interval })
      }
      if (path === '/api/auth/me') return json(response, { id: 1, username: 'mock-admin', display_name: '本地合成压测', role: 'admin', permissions: ['tuning.manage'], scope_site: '', scope_user_ids: [], enabled: true })
      if (path === '/api/dashboard/instances') return json(response, { items: ['a', 'b'].map(site => ({ instance_id: `mock-${site}`, site_id: `mock-${site}`, name: `合成站点 ${site.toUpperCase()}`, enabled: true, online: true, last_seen_at: state.fixture.now })) })
      if (path === '/api/dashboard/menu-visibility') return json(response, { items: {} })
      if (path === '/api/dashboard/tuning/channels/changes') {
        const revision = () => `mock:${state.revision}`
        if (url.searchParams.get('after') !== revision()) return json(response, { revision: revision() })
        const finish = () => { clearTimeout(timer); state.waiters.delete(finish); json(response, { revision: revision() }) }
        const timer = setTimeout(finish, 25000)
        state.waiters.add(finish)
        response.once('close', () => { clearTimeout(timer); state.waiters.delete(finish) })
        return
      }
      if (path.startsWith('/api/dashboard/tuning/')) {
        const data = state.fixture
        const input = request.method === 'GET' ? {} : await body(request)
        await new Promise(resolve => setTimeout(resolve, state.delay))
        if (path.endsWith('/policy')) {
          if (request.method === 'PUT') state.modeEdits = { ...state.modeEdits, ...input.policy?.dispatch_modes }
          return json(response, { mode: 'observe', policy: { scheduling: { window_minutes: 15, min_samples: 20, sparse_min_samples: 10, sparse_lookback_minutes: 360 }, continuous: params, dispatch_modes: { ...modes(), ...state.modeEdits } } })
        }
        if (path.endsWith('/base-values')) {
          if (request.method === 'PUT') for (const row of input.items || []) state.baseEdits.set(row.channel_id, row)
          return json(response, { items: data.bases.map(row => ({ ...row, ...state.baseEdits.get(row.channel_id) })) })
        }
        if (path.endsWith('/recommendations')) return json(response, { items: data.records })
        if (path.endsWith('/continuous-states')) return json(response, url.searchParams.has('rates_only') ? { items: data.channels.map(row => ({ channel_id: row.channel_id, rpm: 30 + state.revision % 5, tpm: 10000 + state.revision % 5 })), as_of: data.now, window_start: new Date(Date.parse(data.now) - 60000).toISOString(), delay_seconds: 0 } : { items: data.states, probe_coverage: { from: new Date(Date.parse(data.now) - 900000).toISOString(), to: data.now, as_of: data.now, items: data.coverage } })
        if (path.endsWith('/channels')) return json(response, { items: data.channels })
        if (path.endsWith('/groups')) return json(response, { items: ['default', 'vip'] })
        if (path.endsWith('/channels/refresh')) return json(response, { synced: true })
        if (path.endsWith('/channel-schedules/preview') && request.method === 'GET') {
          const raw = url.searchParams.get('config') || ''
          let config
          try { if (raw.length > 8192) throw new Error('config too large'); config = JSON.parse(raw) }
          catch { return json(response, { error: 'invalid_schedule' }, 400) }
          const abort = new AbortController()
          response.once('close', () => abort.abort())
          const [result] = await schedulePreviews([config], abort.signal)
          return result.error ? json(response, result, 400) : json(response, result.preview)
        }
        if (path.endsWith('/channel-schedules') && request.method === 'GET') {
          const plans = state.plans.filter(plan => plan.site_id === url.searchParams.get('site_id'))
          const abort = new AbortController()
          response.once('close', () => abort.abort())
          const previews = await schedulePreviews(plans.map(planConfig), abort.signal)
          const items = plans.map((plan, index) => ({ ...plan, ...previews[index].preview }))
          return json(response, { items, worker_enabled: true, direct_configured: true })
        }
        if (/\/channel-schedules\/\d+$/.test(path) && ['PUT', 'DELETE'].includes(request.method)) {
          const id = Number(path.split('/').at(-1)), site = url.searchParams.get('site_id')
          if (!data.channels.some(row => row.channel_id === id)) return json(response, { error: 'schedule_channel_not_found' }, 404)
          if (!['mock-a', 'mock-b'].includes(site) || input.confirm !== true) return json(response, { error: 'invalid_schedule' }, 400)
          const index = state.plans.findIndex(plan => plan.site_id === site && plan.channel_id === id), existing = state.plans[index]
          if (input.revision !== (existing?.revision || 0) || existing && input.expected_updated_at !== existing.updated_at) return json(response, { error: 'schedule_conflict' }, 409)
          if (request.method === 'DELETE') {
            if (existing) state.plans.splice(index, 1)
            notify()
            return json(response, { saved: true })
          }
          const { confirm: _confirm, ...config } = input
          const abort = new AbortController()
          response.once('close', () => abort.abort())
          const [result] = await schedulePreviews([config], abort.signal)
          if (result.error) return json(response, result, 400)
          if (state.plans.find(plan => plan.site_id === site && plan.channel_id === id) !== existing) return json(response, { error: 'schedule_conflict' }, 409)
          if (config.enabled && !config.align_now && !result.preview.next_at) return json(response, { error: 'invalid_schedule', message: '请选择同步当前状态' }, 400)
          if (!existing && state.plans.filter(plan => plan.site_id === site).length >= 200) return json(response, { error: 'schedule_limit' }, 409)
          const changed = !existing || existing.enabled !== result.config.enabled || JSON.stringify(existing.days) !== JSON.stringify(result.config.days) || JSON.stringify(existing.windows) !== JSON.stringify(result.config.windows) || config.align_now
          if (!changed) return json(response, { saved: true, revision: existing.revision, changed: false })
          state.planClock = Math.max(Date.now(), state.planClock + 1)
          const at = new Date(state.planClock).toISOString()
          const plan = { ...result.config, ...result.preview, site_id: site, channel_id: id, revision: (existing?.revision || 0) + 1, updated_at: at, effective_at: at, updated_by: 'mock-admin', closed_by_schedule: existing?.closed_by_schedule || false }
          if (existing) state.plans[index] = plan
          else state.plans.push(plan)
          notify()
          return json(response, { saved: true, revision: plan.revision, changed: true })
        }
        if (/\/channel-schedules\/\d+\/executions$/.test(path)) return json(response, { items: [] })
        if (/\/channels\/\d+\/group$/.test(path)) {
          const id = Number(path.split('/').at(-2)), row = data.channels.find(row => row.channel_id === id)
          if (row) row.group_name = input.group
          return json(response, { channel_id: id, group: input.group, command_id: `mock-group-${id}`, status: 'succeeded' })
        }
        if (/\/channels\/\d+\/status$/.test(path)) {
          const id = Number(path.split('/').at(-2)), row = data.channels.find(row => row.channel_id === id)
          if (row) row.status = input.status === 1 ? 'enabled' : 'disabled'
          return json(response, { channel_id: id, channel_status: input.status, command_id: `mock-status-${id}`, status: 'succeeded' })
        }
        return json(response, { error: 'unsupported_mock_route', path }, 404)
      }
      if (path.startsWith('/api/')) return json(response, { error: 'unsupported_mock_route', path }, 404)
      const requested = resolve(directory, `.${decodeURIComponent(path)}`)
      if (!requested.startsWith(`${directory}${sep}`) && requested !== directory) return json(response, { error: 'invalid_path' }, 400)
      let file = requested
      try { if (!(await stat(file)).isFile()) file = join(directory, 'index.html') } catch { file = join(directory, 'index.html') }
      const bytes = await readFile(file)
      response.writeHead(200, { 'Content-Type': mime[extname(file)] || 'application/octet-stream', 'Cache-Control': 'no-store' })
      response.end(bytes)
    } catch (error) { json(response, { error: String(error.message) }, 500) }
  })
  server.on('close', () => { if (state.timer) clearInterval(state.timer) })
  server.stopMock = () => { if (state.timer) clearInterval(state.timer); for (const done of [...state.waiters]) done(); server.close() }
  return server
}

if (resolve(process.argv[1] || '') === fileURLToPath(import.meta.url)) {
  for (const [name, port] of variants) {
    const server = await createMockServer(name, resolve(root, 'output/tuning-performance', name))
    server.listen(port, '127.0.0.1', () => console.log(`${name}: http://127.0.0.1:${port}/tuning`))
    server.on('error', error => { console.error(`${name}: ${error.message}`); process.exitCode = 1 })
    process.once('SIGINT', () => server.stopMock())
  }
}
