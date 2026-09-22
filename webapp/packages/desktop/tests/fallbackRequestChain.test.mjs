import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/fallbackRequestChain.ts', import.meta.url), 'utf8')
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { chainQuery, attemptChannels, buildRequestChain, loadRequestChain, retryLookupUnknown } = await import('data:text/javascript;base64,' + Buffer.from(code).toString('base64'))
const row = (id, channel, path, overrides = {}) => ({ id, channel_id: channel, type: 5, user_id: 7, request_id: 'request-a', created_at: `2026-09-18T00:00:${String(id).padStart(2, '0')}Z`, use_time: 3, other: JSON.stringify({ admin_info: { use_channel: path } }), ...overrides })
const final = row(3, 148, ['141', '191', '148'], { type: 2 })

test('request query has exact site/user/request scope and crosses list time boundaries', () => {
  const query = chainQuery('site-a', final)
  assert.equal(query.site, 'site-a')
  assert.equal(query.request_id, 'request-a')
  assert.equal(query.user_ids, '7')
  assert.ok(Date.parse(query.start_time) < Date.parse('2026-09-18T00:00:00Z'))
  assert.ok(Date.parse(query.end_time) > Date.parse(final.created_at))
  for (const filter of ['channel_id', 'log_type', 'model_name', 'group', 'username', 'upstream_request_id']) assert.equal(query[filter], undefined)
  const long = chainQuery('a', { ...final, use_time: 100000000 })
  assert.equal(Date.parse(long.end_time) - Date.parse(long.start_time), 31 * 86400000)
})
test('missing identity cannot issue a broad correlation query', () => {
  assert.equal(chainQuery('', final), undefined)
  assert.equal(chainQuery('a', { ...final, request_id: '' }), undefined)
  assert.equal(chainQuery('a', { ...final, user_id: 0 }), undefined)
  assert.equal(chainQuery('a', { ...final, created_at: 'bad' }), undefined)
})
test('original channel sequence preserves repeated attempts', () => {
  assert.deepEqual(attemptChannels(row(1, 148, ['141', '141', '148'], { fallback_channels: ['141', '148'] })), ['141', '141', '148'])
  assert.deepEqual(attemptChannels({ ...final, other: '{bad', fallback_channels: ['141', '148'] }), ['141', '148'])
  assert.deepEqual(attemptChannels({ ...final, other: 'null', fallback_channels: [] }), [])
})
test('recorded attempt sequence takes precedence over delayed error logging', () => {
  const first = row(8, 141, ['141'])
  const second = row(9, 191, ['141', '191'])
  const chain = buildRequestChain([final, second, first], final)
  assert.equal(chain.ordered, true)
  assert.deepEqual(chain.steps.map(step => step.row.id), [8, 9, 3])
  assert.equal(chain.missing, 0)
})
test('retries on the same channel have distinct positions', () => {
  const last = row(3, 148, ['141', '141', '148'], { type: 2 })
  const chain = buildRequestChain([last, row(2, 141, ['141', '141']), row(1, 141, ['141'])], last)
  assert.deepEqual(chain.steps.map(step => step.row.id), [1, 2, 3])
})
test('missing attempt remains explicit instead of inventing its result', () => {
  const chain = buildRequestChain([row(1, 141, ['141']), final], final)
  assert.equal(chain.missing, 1)
  assert.deepEqual(chain.steps[1], { channel: '191', position: 2 })
})
test('unrelated requests/users are excluded and refunds are not counted as retries', () => {
  const chain = buildRequestChain([final, row(4, 141, ['141'], { request_id: 'other' }), row(5, 141, ['141'], { user_id: 8 }), row(6, 148, [], { type: 6 })], final)
  assert.deepEqual(chain.steps.filter(step => step.row).map(step => step.row.id), [3])
  assert.deepEqual(chain.related.map(row => row.id), [6])
})
test('ambiguous duplicate rows remain visible without inventing attempt positions', () => {
  const chain = buildRequestChain([final, row(1, 141, []), row(2, 141, [])], final)
  assert.equal(chain.unplaced.length, 1)
  assert.equal(chain.unplaced[0].row.id, 2)
})
test('absent or conflicting sequences use explicit chronological fallback', () => {
  const chain = buildRequestChain([row(2, 148, []), row(1, 141, [])], final)
  assert.equal(chain.ordered, false)
  assert.deepEqual(chain.steps.map(step => step.row.id), [1, 2])
  assert.equal(buildRequestChain([final, row(1, 191, ['191', '141'])], final).ordered, false)
})
test('lookup paginates beyond the current page and deduplicates overlapping responses', async () => {
  const calls = []
  const result = await loadRequestChain(chainQuery('site-a', final), async params => {
    calls.push(params)
    return { configured: true, items: params.offset ? [final, row(1, 141, ['141'])] : [final], has_more: params.offset === 0 }
  }, new AbortController().signal)
  assert.deepEqual(calls.map(call => call.offset), [0, 100])
  assert.deepEqual(result.rows.map(row => row.id), [3, 1])
  assert.equal(result.truncated, false)
})
test('cancelled fetch is discarded even when transport ignores abort', async () => {
  const controller = new AbortController()
  await assert.rejects(loadRequestChain(chainQuery('a', final), async () => {
    controller.abort()
    return { configured: true, items: [final], has_more: false }
  }, controller.signal), { name: 'AbortError' })
})
test('query failure, unconfigured service and pagination cap are never marked complete', async () => {
  const query = chainQuery('a', final), signal = new AbortController().signal
  await assert.rejects(loadRequestChain(query, async () => { throw new Error('offline') }, signal), /offline/)
  await assert.rejects(loadRequestChain(query, async () => ({ configured: false, items: [] }), signal), /未配置/)
  let calls = 0
  const result = await loadRequestChain(query, async () => { calls++; return { configured: true, items: [final], has_more: true } }, signal)
  assert.equal(calls, 10)
  assert.equal(result.truncated, true)
})

test('only-this-request clears conflicting filters and refuses a stale site', async () => {
  const view = readFileSync(new URL('../src/views/ReadonlyLogsView.vue', import.meta.url), 'utf8')
  const functionSource = view.match(/async function filterRequestChain\([^]*?\n\}/)[0]
  const compiled = ts.transpileModule(functionSource, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  const context = { filters: { site_id: 'site-a' }, backgroundRefreshing: { value: false } }
  for (const key of ['username', 'tokenName', 'modelName', 'group', 'channelID', 'statusCode', 'upstreamRequestID', 'logType', 'requestID', 'requestScope', 'timeRange', 'selectedUserID']) context[key] = { value: 'old-filter' }
  context.emptyOutput = { value: true }
  let refreshed = 0
  context.refreshSearch = async () => { refreshed++ }
  const filter = new Function(...Object.keys(context), `${compiled}; return filterRequestChain`)(...Object.values(context))
  await filter(chainQuery('site-b', final))
  assert.equal(refreshed, 0)
  const query = chainQuery('site-a', final)
  await filter(query)
  assert.equal(refreshed, 1)
  for (const key of ['username', 'tokenName', 'modelName', 'group', 'channelID', 'statusCode', 'upstreamRequestID']) assert.equal(context[key].value, '')
  assert.equal(context.emptyOutput.value, false)
  assert.equal(context.logType.value, 0)
  assert.equal(context.requestID.value, 'request-a')
  assert.deepEqual(context.requestScope.value, { request: 'request-a', user: '7' })
  assert.deepEqual(context.timeRange.value.map(date => date.toISOString()), [query.start_time, query.end_time])
})


test('unknown retry lookup stays inspectable without claiming a retry happened',async()=>{
  const unknown=row(1,0,[],{fallback:false,fallback_checked:false})
  assert.equal(retryLookupUnknown(unknown),true)
  const query=chainQuery('a',unknown)
  assert.equal(query.request_id,'request-a');assert.equal(query.user_ids,'7')
  const result=await loadRequestChain(query,async()=>({configured:true,items:[unknown],has_more:false}),new AbortController().signal)
  assert.equal(result.rows.length,1)
  assert.equal(retryLookupUnknown({...unknown,fallback_checked:true}),false)
  assert.equal(retryLookupUnknown({...unknown,fallback_checked:undefined}),false)
  assert.equal(retryLookupUnknown({...unknown,fallback:true}),false)
  assert.equal(retryLookupUnknown({...unknown,request_id:''}),false)
  assert.equal(retryLookupUnknown({...unknown,user_id:0}),false)
  const page=readFileSync(new URL('../src/views/ReadonlyLogsView.vue',import.meta.url),'utf8')
  assert.equal((page.match(/v-if="view.retryUnknown">待确认/g)||[]).length,2)
  assert.match(page,/retryUnknown: admin && retryLookupUnknown\(row\)/)
})
