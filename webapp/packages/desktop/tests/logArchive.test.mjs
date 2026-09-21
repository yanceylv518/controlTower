import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/logArchive.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText
const { beijingDate, buildArchiveDays, formatArchiveCount, canCheckArchiveDay } = await import(
  `data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`
)

const report = (date, archivedRows = '12') => ({
  date, archived_rows: archivedRows, request_rows: '10', error_rows: '2',
  last_id: '9007199254740993', verified_at: '2026-09-20T00:00:00Z',
})
const comparison = (date, state = 'matched') => ({
  id: 'reconcile-1', date, state, source_rows: 0, target_rows: 0,
})
const byDate = (rows, date) => rows.find(row => row.date === date)

test('archive business date changes at Beijing midnight, including the year boundary', () => {
  assert.equal(beijingDate(Date.parse('2026-09-19T15:59:59.999Z')), '2026-09-19')
  assert.equal(beijingDate(Date.parse('2026-09-19T16:00:00.000Z')), '2026-09-20')
  assert.equal(beijingDate(Date.parse('2026-12-31T16:00:00.000Z')), '2027-01-01')
})

test('calendar includes every date and follows leap-year century rules', () => {
  for (const [month, count, last] of [
    ['2024-02', 29, '2024-02-29'], ['2026-02', 28, '2026-02-28'],
    ['2000-02', 29, '2000-02-29'], ['1900-02', 28, '1900-02-28'],
    ['2026-04', 30, '2026-04-30'], ['2026-12', 31, '2026-12-31'],
  ]) {
    const rows = buildArchiveDays(month, [], '2027-01-01')
    assert.equal(rows.length, count)
    assert.equal(rows[0].date, `${month}-01`)
    assert.equal(rows.at(-1).date, last)
    assert.equal(new Set(rows.map(row => row.date)).size, count)
  }
})

test('invalid year/month and invalid current date cannot produce a misleading calendar', () => {
  for (const month of ['', '2026-00', '2026-13', '2026-9', '26-09', '0000-01', '2026-09-01', '2026-09 ']) {
    assert.deepEqual(buildArchiveDays(month, [], '2026-09-20'), [])
  }
  for (const today of ['', '2026-02-29', '2026-09-00', '2026-13-01']) {
    assert.deepEqual(buildArchiveDays('2026-09', [], today), [])
  }
})

test('today and future dates take precedence over reports and stale comparison results', () => {
  for (const date of ['2026-09-20', '2026-09-21']) {
    const row = byDate(buildArchiveDays('2026-09', [report(date)], '2026-09-20', comparison(date)), date)
    assert.equal(row.kind, date.endsWith('20') ? 'today' : 'future')
    assert.equal(row.reconciliation, undefined)
    assert.doesNotMatch(row.label, /已封存|对账一致/)
  }
})

test('no report remains unknown; an explicit zero report is still only a cumulative report', () => {
  const rows = buildArchiveDays('2026-09', [report('2026-09-02', '0')], '2026-09-20')
  const unknown = byDate(rows, '2026-09-01')
  assert.equal(unknown.kind, 'unknown')
  assert.equal(unknown.reported, undefined)
  assert.equal(formatArchiveCount(unknown.reported?.archived_rows), '—')
  const zero = byDate(rows, '2026-09-02')
  assert.equal(zero.kind, 'reported')
  assert.equal(zero.label, '已上报')
  assert.equal(zero.reported.archived_rows, '0')
  assert.match(zero.reason, /不代表.*完整.*封存/)
})

test('matched applies only to its date and never certifies sealing, completeness, or zero business', () => {
  const result = comparison('2026-09-02')
  const rows = buildArchiveDays('2026-09', [report('2026-09-01')], '2026-09-20', result)
  const matched = byDate(rows, '2026-09-02')
  assert.equal(matched.kind, 'matched')
  assert.equal(matched.label, '最近对账一致')
  assert.equal(matched.reconciliation, result)
  assert.equal(matched.reported, undefined)
  assert.match(matched.reason, /不代表.*封存.*完整.*零业务/)
  assert.equal(byDate(rows, '2026-09-01').kind, 'reported')
  assert.equal(rows.filter(row => row.reconciliation).length, 1)
  assert.equal(buildArchiveDays('2026-08', [], '2026-09-20', result).some(row => row.reconciliation), false)
})

test('running, mismatched and failed comparisons remain distinct from a successful report', () => {
  for (const [state, kind] of [['running', 'checking'], ['mismatched', 'mismatched'], ['failed', 'failed']]) {
    const row = byDate(buildArchiveDays('2026-09', [report('2026-09-02')], '2026-09-20', comparison('2026-09-02', state)), '2026-09-02')
    assert.equal(row.kind, kind)
    assert.equal(row.reconciliation.state, state)
    assert.notEqual(row.tagType, 'success')
  }
  const row = byDate(buildArchiveDays('2026-09', [], '2026-09-20', comparison('2026-09-02', 'unsupported')), '2026-09-02')
  assert.equal(row.kind, 'unknown')
})

test('undated, out-of-month and impossible reports are not included or silently relabeled', () => {
  const valid = Object.freeze(report('2026-09-01'))
  const reports = Object.freeze([valid, report('undated'), report('2026-08-31'), report('2026-09-31')])
  const rows = buildArchiveDays('2026-09', reports, '2026-09-20')
  assert.equal(rows.length, 30)
  assert.deepEqual(rows.filter(row => row.reported).map(row => row.date), ['2026-09-01'])
  assert.equal(rows[0].reported, valid)
})

test('count formatting keeps every large integer digit and distinguishes unknown from zero', () => {
  assert.equal(formatArchiveCount('90071992547409931234567890123456789012'), '90,071,992,547,409,931,234,567,890,123,456,789,012')
  assert.equal(formatArchiveCount('0'), '0')
  assert.equal(formatArchiveCount('1000'), '1,000')
  for (const value of [undefined, '', 'NaN', '1.2', '-1', '1e9']) assert.equal(formatArchiveCount(value), '—')
})

test('day check requires strict passage beyond Beijing day end plus the configured delay', () => {
  const boundary = Date.parse('2026-09-20T00:05:00+08:00')
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, boundary - 1), false)
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, boundary), false)
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, boundary + 1), true)
  assert.equal(canCheckArchiveDay('2026-09-20', '2026-09-20', 300, boundary + 1), false)
  assert.equal(canCheckArchiveDay('2026-09-21', '2026-09-20', 300, boundary + 1), false)
})

test('day check handles leap/year boundaries and a full day of delay without local timezone arithmetic', () => {
  assert.equal(canCheckArchiveDay('2024-02-29', '2024-03-01', 300, Date.parse('2024-03-01T00:05:00.001+08:00')), true)
  assert.equal(canCheckArchiveDay('2026-12-31', '2027-01-01', 300, Date.parse('2027-01-01T00:05:00.001+08:00')), true)
  const boundary = Date.parse('2026-09-20T00:00:00+08:00')
  assert.equal(canCheckArchiveDay('2026-09-18', '2026-09-20', 86400, boundary), false)
  assert.equal(canCheckArchiveDay('2026-09-18', '2026-09-20', 86400, boundary + 1), true)
})

test('invalid dates, delay values and clocks cannot enable a check', () => {
  const now = Date.parse('2026-09-20T12:00:00+08:00')
  for (const date of ['2026-02-29', '2026-09-31', '2026-09-00', '2026-9-01', 'undated', '']) {
    assert.equal(canCheckArchiveDay(date, '2026-09-20', 300, now), false)
  }
  for (const delay of [-1, 0.5, Infinity, NaN]) assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', delay, now), false)
  assert.equal(canCheckArchiveDay('2026-09-19', 'invalid', 300, now), false)
  assert.equal(canCheckArchiveDay('2026-09-19', '2026-09-20', 300, NaN), false)
})
