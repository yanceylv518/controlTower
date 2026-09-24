import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../src/utils/channelGroup.ts', import.meta.url), 'utf8')
const viewSource = readFileSync(new URL('../src/views/ContinuousTuningView.vue', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { hiddenChannelGroupCount, matchesChannelGroup, normalizeChannelGroups, splitChannelGroups, visibleChannelGroups } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('group strings split and normalize duplicate labels', () => {
  assert.deepEqual(splitChannelGroups(' default, vip,default '), ['default', 'vip'])
  assert.equal(normalizeChannelGroups([' default', 'vip,default']), 'default,vip')
})

test('group display keeps three labels and folds the rest into a count', () => {
  assert.deepEqual(visibleChannelGroups('default,vip,fast,slow'), ['default', 'vip', 'fast'])
  assert.equal(hiddenChannelGroupCount('default,vip,fast,slow'), 1)
  assert.equal(hiddenChannelGroupCount('default,vip,fast'), 0)
})

test('group normalization allows clearing all groups and rejects control-character values', () => {
  assert.equal(normalizeChannelGroups([]), '')
  assert.throws(() => normalizeChannelGroups(['default\nadmin']), /控制字符/)
})

test('group normalization enforces the storage length boundary', () => {
  assert.throws(() => normalizeChannelGroups(['x'.repeat(129)]), /128/)
})

test('group editor uses a dedicated site-scoped combination manager', () => {
  assert.match(viewSource, /<ChannelGroupEditor/)
  assert.match(viewSource, /:site="siteID"/)
  assert.doesNotMatch(viewSource, /当前站点已有组合/)
})

test('group filtering matches a selected group exactly across comma-separated assignments', () => {
  assert.equal(matchesChannelGroup('vip,default', 'vip'), true)
  assert.equal(matchesChannelGroup('vip-plus,default', 'vip'), false)
  assert.equal(matchesChannelGroup('vip,default', 'VIP'), false)
  assert.equal(matchesChannelGroup('vip,default', null), true)
})

test('stale channel writes refresh the live channel directory', () => {
  assert.match(viewSource, /error\.code === "channel_not_found"/)
  assert.match(viewSource, /await loadChannelDirectory\(groupSite\)/)
})

test('group targets are selected from the complete site option list', () => {
  assert.match(viewSource, /channels\.value\.flatMap\(row => splitChannelGroups\(row\.group_name\)\)/)
  assert.match(viewSource, /bases\.value\.flatMap\(row => splitChannelGroups\(row\.group_name\)\)/)
  assert.doesNotMatch(readFileSync(new URL('../src/components/ChannelGroupEditor.vue', import.meta.url), 'utf8'), /allow-create/)
})

test('tuning group filter exposes searchable single-select group options', () => {
  assert.match(viewSource, /<el-popover v-model:visible="groupFilterOpen"/)
  assert.match(viewSource, /v-model="groupFilterSearch"/)
  assert.match(viewSource, /所有分组/)
  assert.match(viewSource, /toggleGroupFilter\(group\)/)
  assert.match(viewSource, /<div class="channel-toolbar model-filter">/)
  assert.doesNotMatch(viewSource, /按分组筛选，如 vip/)
})

test('tuning group options scroll locally at the NewAPI list height limit', () => {
  assert.match(viewSource, /group-filter-options\)\{max-height:min\(288px,calc\(100vh - 160px\)\);overflow-x:hidden;overflow-y:auto;overscroll-behavior:contain/)
})
