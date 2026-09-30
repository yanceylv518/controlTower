import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/monitorSearch.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const api = {}
new Function('exports', compiled)(api)
const { monitorSearchOptions, filterMonitorOptions } = api
const row = (key, name, instance = 'node-a', extra = {}) => ({ dimension_key: key, display_name: name, instance_id: instance, instance_name: instance, ...extra })

test('customer and channel candidates include names, IDs and instances', () => {
  for (const kind of ['customers', 'channels']) {
    const options = monitorSearchOptions([row('node-a:user:42', '嘉兴电信')], kind)
    assert.equal(options[0].value, '嘉兴电信')
    assert.equal(options[0].detail, 'ID 42 · node-a')
    assert.equal(filterMonitorOptions(options, '电信')[0].key, 'node-a:user:42')
    assert.equal(filterMonitorOptions(options, '42')[0].key, 'node-a:user:42')
  }
})

test('fuzzy matching ignores case and whitespace and combines search words', () => {
  const options = monitorSearchOptions([
    row('node-a:model:claude-sonnet', 'Claude-Sonnet'),
    row('node-b:model:claude-opus', 'Claude-Opus', 'node-b'),
  ], 'models')
  assert.deepEqual(filterMonitorOptions(options, ' CLAUDE  node-b ').map(option => option.value), ['Claude-Opus'])
  assert.deepEqual(filterMonitorOptions(options, 'SONNET').map(option => option.value), ['Claude-Sonnet'])
  assert.equal(filterMonitorOptions(options, 'not-found').length, 0)
  assert.deepEqual(filterMonitorOptions(options, '   '), options)
})

test('selection filters by identity even for duplicate names or shared numeric IDs', () => {
  const options = monitorSearchOptions([
    row('node-a:channel:42', '同名渠道'),
    row('node-b:channel:42', '同名渠道', 'node-b'),
    row('node-a:channel:142', '同名渠道'),
  ], 'channels')
  assert.equal(filterMonitorOptions(options, '同名').length, 3)
  assert.deepEqual(filterMonitorOptions(options, '同名渠道', 'node-b:channel:42').map(option => option.key), ['node-b:channel:42'])
  assert.equal(filterMonitorOptions(options, '', 'missing-key').length, 0)
  assert.deepEqual(filterMonitorOptions(options, ''), options)
})

test('candidate identities stay unique and missing display names have real-data fallbacks', () => {
  const options = monitorSearchOptions([row('a:user:42', ''), row('a:user:42', 'Alice'), row('a:user:43', '')], 'customers')
  assert.equal(options.length, 2)
  assert.equal(options[0].value, 'Alice')
  assert.equal(options[1].value, '客户 43')
  const model = monitorSearchOptions([row('a:model:vendor:model-v1', '', 'a', { display_key: 'vendor:model-v1' })], 'models')[0]
  assert.equal(model.value, 'vendor:model-v1')
  assert.equal(model.detail, 'a')
  assert.deepEqual(monitorSearchOptions([], 'channels'), [])
})

const component = readFileSync(new URL('../src/components/MonitorSearch.vue', import.meta.url), 'utf8')
function picker(overrides = {}) {
  const props = { modelValue: 'Alice', selectedKey: 'a:user:42', loading: false, options: monitorSearchOptions([row('a:user:42', 'Alice'), row('a:user:43', 'Bob')], 'customers'), ...overrides }
  const events = []
  const watchers = []
  const script = component.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '')
  const code = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
  const create = new Function('nextTick', 'ref', 'watch', 'defineProps', 'defineEmits', 'filterMonitorOptions', code + '; return { input, select, suggestions, autocomplete };')
  const controller = create(() => Promise.resolve(), value => ({ value }), (_sources, fn) => watchers.push(fn), () => props, () => (...args) => events.push(args), filterMonitorOptions)
  return { ...controller, props, events, refresh: () => watchers.forEach(fn => fn()) }
}

test('reopening a selected search lists all candidates; unselected text narrows suggestions', async () => {
  const control = picker()
  let suggestions
  await control.suggestions('Alice', result => { suggestions = result })
  assert.equal(suggestions.length, 2)
  control.props.selectedKey = ''
  await control.suggestions('bOB', result => { suggestions = result })
  assert.equal(suggestions[0].value, 'Bob')
})

test('typing and clearing release precise selection while choosing restores identity', () => {
  const control = picker()
  control.input('Al')
  assert.deepEqual(control.events.splice(0), [['update:selectedKey', ''], ['update:modelValue', 'Al']])
  control.input('')
  assert.deepEqual(control.events.splice(0), [['update:selectedKey', ''], ['update:modelValue', '']])
  control.select(control.props.options[1])
  assert.deepEqual(control.events, [['update:modelValue', 'Bob'], ['update:selectedKey', 'a:user:43'], ['select', 'a:user:43']])
})

test('empty and loading hints cannot change the filter', async () => {
  const control = picker({ selectedKey: '', options: [] })
  let suggestions
  await control.suggestions('missing', result => { suggestions = result })
  assert.equal(suggestions[0].detail, '没有匹配的监控项')
  assert.equal(suggestions[0].value, 'missing')
  control.select(suggestions[0])
  control.props.loading = true
  await control.suggestions('', result => { suggestions = result })
  assert.match(suggestions[0].detail, /加载中/)
  control.select(suggestions[0])
  assert.deepEqual(control.events, [])
})

test('suggestions wait for the parent to clear a previous precise selection', async () => {
  const control = picker()
  let suggestions
  const pending = control.suggestions('no-match', result => { suggestions = result })
  control.props.selectedKey = ''
  await pending
  assert.equal(suggestions.length, 1)
  assert.equal(suggestions[0].key, '')
  assert.equal(suggestions[0].value, 'no-match')
})

test('background refresh updates an open dropdown without reopening a closed one', () => {
  const control = picker()
  const queries = []
  control.autocomplete.value = { activated: false, getData: query => { queries.push(query) } }
  control.refresh()
  assert.deepEqual(queries, [])
  control.autocomplete.value.activated = true
  control.refresh()
  assert.deepEqual(queries, ['Alice'])
})

const customerView = readFileSync(new URL('../src/views/CustomerMonitorView.vue', import.meta.url), 'utf8')
const dimensionView = readFileSync(new URL('../src/views/DimensionView.vue', import.meta.url), 'utf8')
const computed = fn => ({ get value() { return fn() } })

test('customer non-TPM charts include a searched customer outside the original top eight', () => {
  const expression = customerView.match(/const visibleTrendKeys = computed\(\(\) => \{([\s\S]*?)\n\}\);/)[1]
  const search = { value: 'Bob' }, searchKey = { value: '' }, selectedKeys = { value: ['a:user:42'] }, filteredRows = { value: [row('a:user:43', 'Bob')] }
  const keys = new Function('search', 'searchKey', 'selectedKeys', 'filteredRows', 'computed', `return computed(() => {${expression}});`)(search, searchKey, selectedKeys, filteredRows, computed)
  assert.deepEqual(keys.value, ['a:user:43'])
  filteredRows.value = []
  assert.deepEqual(keys.value, [])
  search.value = ''
  assert.deepEqual(keys.value, ['a:user:42'])
})

test('selecting a disabled or idle channel shows it without changing default status filtering', () => {
  const expression = dimensionView.match(/const visibleRows = computed\(\(\) =>([\s\S]*?)\n\);/)[1]
  const searched = { value: [row('a:channel:1', 'Active', 'a', { status: 'ok', tokens: 1 }), row('a:channel:2', 'Disabled', 'a', { status: 'disabled', tokens: 0 }), row('a:channel:3', 'Idle', 'a', { status: 'idle', tokens: 0 })] }
  const activeKinds = { value: [] }, searchKey = { value: '' }
  const visible = new Function('searched', 'activeKinds', 'searchKey', 'rowKind', 'totalTokens', 'computed', `return computed(() => ${expression});`)(searched, activeKinds, searchKey, item => item.status, item => item.tokens, computed)
  assert.deepEqual(visible.value.map(item => item.dimension_key), ['a:channel:1'])
  searchKey.value = 'a:channel:2'
  assert.deepEqual(visible.value.map(item => item.dimension_key), ['a:channel:1', 'a:channel:2'])
  searchKey.value = 'a:channel:3'
  assert.deepEqual(visible.value.map(item => item.dimension_key), ['a:channel:1', 'a:channel:3'])
  activeKinds.value = ['disabled']
  assert.deepEqual(visible.value.map(item => item.dimension_key), ['a:channel:2'])
})
