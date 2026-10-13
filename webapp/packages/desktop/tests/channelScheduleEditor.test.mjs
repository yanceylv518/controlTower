import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, nextTick, reactive, ref, watch } from 'vue'

const source = readFileSync(new URL('../src/components/ChannelScheduleEditor.vue', import.meta.url), 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*\r?\n/gm, '')
const code = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const utilitySource = readFileSync(new URL('../src/utils/channelSchedule.ts', import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '')
const utilityCode = ts.transpileModule(utilitySource, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const utilities = new Function('exports', `${utilityCode}\nreturn exports;`)({})

async function settle() {
  for (let i = 0; i < 5; i++) await nextTick()
}

function editor() {
  const props = reactive({ modelValue: true, site: 'a', channel: { channel_id: 2, status: 'disabled' } })
  const dashboard = {
    channelSchedules: async () => ({ items: [], direct_configured: true, worker_enabled: true }),
    channelScheduleExecutions: async () => ({ items: [] }),
    previewChannelSchedule: async () => ({ allowed: false, next_at: '2026-10-14T00:00:00Z', effective_windows: [] }),
  }
  const dependencies = {
    computed, nextTick, reactive, ref, watch, dashboard,
    defineProps: () => props, defineEmits: () => () => {}, defineExpose: () => {},
    onBeforeRouteLeave: () => {}, onBeforeUnmount: () => {},
    ElMessage: { success() {} }, ElMessageBox: { confirm: async () => {} }, ApiError: class extends Error {},
    window: { addEventListener() {}, removeEventListener() {} },
    formatBeijingTime: value => value, ...utilities,
  }
  const result = new Function(...Object.keys(dependencies), `${code}\nreturn { form, ready, plan, conflict, dirty, currentStatus, refreshContext, stopRequests };`)(...Object.values(dependencies))
  return { ...result, props, dashboard }
}

test('channel polling preserves unsaved schedule when the same channel object is refreshed', async () => {
  const e = editor()
  try {
    await settle()
    assert.equal(e.ready.value, true)
    e.form.days = [6, 7]
    e.form.windows = [{ start: '22:00', end: '08:00' }]
    await settle()
    e.props.channel = { channel_id: 2, status: 'enabled', current_weight: 80 }
    await settle()
    await e.refreshContext()
    assert.deepEqual(e.form.days, [6, 7])
    assert.deepEqual(e.form.windows, [{ start: '22:00', end: '08:00' }])
    assert.equal(e.dirty.value, true)
    assert.equal(e.conflict.value, false)
  } finally { e.stopRequests() }
})

test('a different channel resets the schedule context', async () => {
  const e = editor()
  try {
    await settle()
    e.form.windows = [{ start: '22:00', end: '08:00' }]
    e.props.channel = { channel_id: 4, status: 'enabled' }
    await settle()
    assert.deepEqual(e.form.windows, [{ start: '00:00', end: '08:00' }])
    assert.equal(e.dirty.value, false)
  } finally { e.stopRequests() }
})

test('a concurrent plan update preserves the draft and reports a revision conflict', async () => {
  const e = editor()
  try {
    await settle()
    e.form.windows = [{ start: '22:00', end: '08:00' }]
    e.dashboard.channelSchedules = async () => ({ items: [{ channel_id: 2, revision: 1, updated_at: '2026-10-13T00:00:00Z', enabled: true, days: [1], windows: [{ start: '12:00', end: '13:00' }] }], direct_configured: true, worker_enabled: true })
    await e.refreshContext()
    assert.equal(e.conflict.value, true)
    assert.deepEqual(e.form.windows, [{ start: '22:00', end: '08:00' }])
  } finally { e.stopRequests() }
})

test('a failed status refresh cannot attribute a closure from the cached plan', async () => {
  const e = editor()
  try {
    await settle()
    const response = { items: [{ channel_id: 2, revision: 1, updated_at: '2026-10-13T00:00:00Z', enabled: true, days: [1], windows: [{ start: '12:00', end: '13:00' }], closed_by_schedule: true }], direct_configured: true, worker_enabled: true }
    e.dashboard.channelSchedules = async () => response
    await e.refreshContext()
    assert.equal(e.currentStatus.value, '定时关闭')
    e.dashboard.channelSchedules = async () => { throw new Error('offline') }
    await e.refreshContext()
    assert.equal(e.currentStatus.value, '关闭来源待确认')
    e.dashboard.channelSchedules = async () => response
    await e.refreshContext()
    assert.equal(e.currentStatus.value, '定时关闭')
    e.props.state = { paused_reason: 'manual_disabled', circuit_disabled: false, circuit_status_target: 0 }
    assert.equal(e.currentStatus.value, '手动关闭', 'manual ownership wins over the cached plan origin')
  } finally { e.stopRequests() }
})
