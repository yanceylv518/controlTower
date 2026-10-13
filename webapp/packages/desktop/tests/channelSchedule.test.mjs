import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/channelSchedule.ts', import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '')
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const utils = new Function('exports', `${code}\nreturn exports;`)({})

test('week strip splits effective windows into one 24 hour lane per weekday', () => {
  const rows = utils.weekStrip([{ start_minute: 0, end_minute: 480 }, { start_minute: 1440 + 720, end_minute: 1440 + 1080 }])
  assert.deepEqual(rows.map(row => row.label), ['周一', '周二', '周三', '周四', '周五', '周六', '周日'])
  assert.deepEqual(rows[0].segments, [{ left: 0, width: 480 / 14.4 }])
  assert.deepEqual(rows[1].segments, [{ left: 50, width: 25 }])
  assert.deepEqual(rows.slice(2).map(row => row.segments.length), [0, 0, 0, 0, 0])
})

test('week strip carries overnight and cross-week windows onto the following day', () => {
  // 周日 22:00 至下周一 06:00
  const rows = utils.weekStrip([{ start_minute: 6 * 1440 + 1320, end_minute: 7 * 1440 + 360 }])
  assert.deepEqual(rows[6].segments, [{ left: 1320 / 14.4, width: 120 / 14.4 }])
  assert.deepEqual(rows[0].segments, [{ left: 0, width: 25 }])
  const whole = utils.weekStrip([{ start_minute: 0, end_minute: 10080 }])
  assert.ok(whole.every(row => row.segments.length === 1 && row.segments[0].left === 0 && row.segments[0].width === 100))
})

test('plan summary and row follow-up keep their compact wording', () => {
  assert.equal(utils.scheduleSummary({ enabled: true, days: [1, 2, 3, 4, 5, 6, 7], windows: [{ start: '00:00', end: '08:00' }] }), '每天 00:00–08:00')
  assert.equal(utils.scheduleSummary({ enabled: true, days: [1, 2, 3, 4, 5], windows: [{ start: '22:00', end: '06:00' }, { start: '12:00', end: '13:00' }] }), '周一至周五 22:00–06:00次日 +1')
  assert.equal(utils.scheduleSummary({ enabled: false, days: [1], windows: [{ start: '00:00', end: '08:00' }] }), '计划已停用')
  assert.equal(utils.scheduleTone({ status: 'skipped' }), 'warning')
  assert.equal(utils.scheduleTone({ status: 'failed' }), 'danger')
  assert.equal(utils.scheduleFollowup({ status: 'failed' }), '本时点不再重试')
})
