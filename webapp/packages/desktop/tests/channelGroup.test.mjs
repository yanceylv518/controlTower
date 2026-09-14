import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../src/utils/channelGroup.ts', import.meta.url), 'utf8')
const viewSource = readFileSync(new URL('../src/views/ContinuousTuningView.vue', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { hiddenChannelGroupCount, normalizeChannelGroups, splitChannelGroups, visibleChannelGroups } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

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

test('group editor does not allow creating unknown groups', () => {
  assert.doesNotMatch(viewSource, /allow-create/)
  assert.match(viewSource, /placeholder="选择已有分组"/)
})
