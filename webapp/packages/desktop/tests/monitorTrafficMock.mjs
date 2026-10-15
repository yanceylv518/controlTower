import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import { extname, resolve, sep } from 'node:path'
import { gzipSync } from 'node:zlib'

const minuteMs = 60000
export const trafficProfiles = {
  standard: { users: 120, channels: 48, models: 24 },
  large: { users: 500, channels: 120, models: 80 },
}
const pad = value => String(value).padStart(4, '0')
const modelName = value => `mock:model:channel:inner-${pad(value)}`
const displayName = (kind, id, instance) => `${instance === 'mock-b' ? 'B ' : ''}合成${kind === 'user' ? '客户' : kind === 'channel' ? '渠道' : '模型'} ${kind === 'model' ? id.split('-').at(-1) : pad(id)}`

export function mockMetricHistory(params, state) {
  const profile = trafficProfiles[state.profile]
  if (!profile) throw new Error('unknown mock profile')
  const instance = params.instance_id || 'mock-a'
  if (!['mock-a', 'mock-b'].includes(instance)) return []
  const kinds = params.dimension_type === 'instance' ? [] : params.dimension_type?.replace(/^instance_/, '').split('_')
  if (!kinds || kinds.some(kind => !['user', 'channel', 'model'].includes(kind)) || kinds.length > 2) throw new Error('unsupported mock dimension')
  const minutes = params.window === '5m' ? 5 : 1
  const hours = Number(params.hours)
  if (![1, 6, 24].includes(hours)) throw new Error('unsupported mock range')
  const width = minutes * minuteMs
  const end = Math.floor(state.asOf / width) * width
  const buckets = hours * 60 / minutes
  const aggregate = params.aggregate === true || params.aggregate === 'true'
  const groups = new Map()
  const dates = Array.from({ length: buckets + 1 }, (_, slot) => new Date(end - (buckets - slot) * width).toISOString())
  const userMatch = params.dimension_key_prefix?.match(/^mock-[ab]:user:([1-9]\d*):/)
  const users = userMatch ? [Number(userMatch[1])] : Array.from({ length: profile.users }, (_, i) => i + 1)
  for (const user of users) {
    for (let slot = 0; slot <= buckets; slot++) {
      for (let flow = 0; flow < 3; flow++) {
        const channel = (user - 1 + flow * 17) % profile.channels + 1
        const model = modelName((user * 7 + flow * 13 + Math.floor(slot / 12)) % profile.models + 1)
        const ids = { user, channel, model }
        const key = kinds.reduce((key, kind) => `${key}:${kind}:${ids[kind]}`, instance)
        if (params.dimension_key && key !== params.dimension_key) continue
        if (params.dimension_key_prefix && !key.startsWith(params.dimension_key_prefix)) continue
        if (state.partial && kinds.length === 2 && slot === buckets - 1 && user === 1 && flow === 0) continue
        const bucket = aggregate ? 0 : slot
        let item = groups.get(key)
        if (!item) { item = new Map(); groups.set(key, item) }
        let counts = item.get(bucket)
        if (!counts) { counts = { requests: 0, tokens: 0 }; item.set(bucket, counts) }
        const requests = ((user % 11) + 1) * 7 + flow * 2 + slot % 13 + state.revision
        const tokens = requests * (100 + user % 7 * 401)
        counts.requests += requests
        counts.tokens += tokens
      }
    }
  }
  const points = []
  for (const [key, values] of groups) {
    const lastKind = kinds.at(-1)
    const id = key.slice(key.lastIndexOf(`:${lastKind}:`) + lastKind?.length + 2)
    const name = lastKind ? displayName(lastKind, id, instance) : '合成实例'
    for (const [slot, counts] of values) points.push({
      instance_id: instance, instance_name: instance === 'mock-b' ? '合成站点 B' : '合成站点 A',
      dimension_type: params.dimension_type, dimension_key: key,
      display_key: id, display_name: name, bucket_time: aggregate ? dates.at(-2) : dates[slot],
      request_count: counts.requests, success_count: counts.requests, error_count: 0,
      success_rate: 1, error_rate: 0, tpm: counts.tokens,
      prompt_tokens: Math.floor(counts.tokens * .6), completion_tokens: counts.tokens - Math.floor(counts.tokens * .6),
      ttft_count: counts.requests, ttft_p50_ms: 450, ttft_p90_ms: 650, ttft_p95_ms: 900,
      otps: 50, otps_sample_tokens: counts.tokens, otps_duration_seconds: counts.tokens / 50,
      cache_hit_rate: .4, quota: 0,
    })
  }
  return points
}

export async function createTrafficMock(directory, profile = 'standard') {
  const root = resolve(directory)
  const state = { profile, revision: 0, asOf: Date.now(), delay: 15, partial: false, failure: null, calls: [], responses: [], active: 0, peak: 0 }
  const cache = new Map()
  function json(response, data, status = 200) {
    const bytes = Buffer.from(JSON.stringify(data))
    const zipped = gzipSync(bytes)
    response.writeHead(status, { 'Content-Type': 'application/json', 'Content-Encoding': 'gzip', 'Cache-Control': 'no-store' })
    response.end(zipped)
  }
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1')
    try {
      if (request.method !== 'GET') return json(response, { error: 'mock_read_only' }, 405)
      if (url.pathname === '/api/auth/me') return json(response, { id: 1, username: 'mock-admin', display_name: '合成压测', role: 'admin', permissions: ['*'], scope_site: '', scope_user_ids: [], enabled: true })
      if (url.pathname === '/api/dashboard/instances') return json(response, { items: ['a', 'b'].map(site => ({ instance_id: `mock-${site}`, site_id: `mock-${site}`, name: `合成站点 ${site.toUpperCase()}`, enabled: true, online: true, last_seen_at: new Date(state.asOf).toISOString() })) })
      if (url.pathname === '/api/dashboard/settings' || url.pathname === '/api/dashboard/menu-visibility') return json(response, { items: {} })
      if (url.pathname === '/api/dashboard/passthrough/currency') return json(response, { quota_per_unit: 500000, price_multiplier: 1, symbol: '$', type: 'USD' })
      if (url.pathname === '/api/dashboard/channel-snapshots') return json(response, { items: Array.from({ length: trafficProfiles[state.profile].channels }, (_, i) => ({ instance_id: url.searchParams.get('instance_id'), channel_id: i + 1, channel_name: displayName('channel', i + 1, url.searchParams.get('instance_id')), status: 'enabled', captured_at: new Date(state.asOf).toISOString() })) })
      if (url.pathname === '/api/dashboard/metric-history') {
        const params = Object.fromEntries(url.searchParams)
        state.calls.push({ ...params, revision: state.revision })
        state.active++; state.peak = Math.max(state.peak, state.active)
        response.once('close', () => state.active--)
        if (state.delay) await new Promise(resolve => setTimeout(resolve, state.delay))
        const failure = state.failure
        if (failure && (!failure.dimension || failure.dimension === params.dimension_type)) {
          if (--failure.remaining <= 0) state.failure = null
          return json(response, { error: 'mock_injected_failure' }, failure.status)
        }
        const cacheKey = JSON.stringify([state.profile, state.revision, state.asOf, state.partial, url.search])
        let result = cache.get(cacheKey)
        if (!result) {
          const started = performance.now(), rows = mockMetricHistory(params, state)
          const bytes = Buffer.from(JSON.stringify({ items: rows })), zipped = gzipSync(bytes)
          result = { zipped, rows: rows.length, bytes: bytes.length, generationMs: performance.now() - started }
          if (cache.size >= 16) cache.delete(cache.keys().next().value)
          cache.set(cacheKey, result)
        }
        state.responses.push({ ...params, rows: result.rows, bytes: result.bytes, zipped: result.zipped.length, generationMs: result.generationMs })
        response.writeHead(200, { 'Content-Type': 'application/json', 'Content-Encoding': 'gzip', 'Cache-Control': 'no-store' })
        return response.end(result.zipped)
      }
      if (url.pathname.startsWith('/api/')) return json(response, { error: `unsupported_mock_route:${url.pathname}` }, 404)
      const path = resolve(root, `.${decodeURIComponent(url.pathname)}`)
      if (path !== root && !path.startsWith(root + sep)) return json(response, { error: 'invalid_path' }, 400)
      const extension = extname(path)
      const data = await readFile(extension ? path : resolve(root, 'index.html'))
      response.writeHead(200, { 'Content-Type': ({ '.js': 'text/javascript', '.css': 'text/css', '.woff2': 'font/woff2', '.svg': 'image/svg+xml', '.png': 'image/png' })[extension] || 'text/html', 'Cache-Control': 'no-store' })
      response.end(data)
    } catch (error) { json(response, { error: error.message }, 500) }
  })
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  return { state, url: `http://127.0.0.1:${server.address().port}`, async close() { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)) } }
}
