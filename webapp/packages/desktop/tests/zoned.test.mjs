import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

// Run on the project's Node 20 baseline without an extra test dependency.
const source = readFileSync(new URL('../src/utils/zoned.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { parseWallTime, wallToDate, dateToWall, rezoneRange } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('Shanghai input maps to the same instant regardless of browser zone', () => {
  const date = wallToDate('2026-09-08 14:35:00', 'Asia/Shanghai')
  assert.equal(date.toISOString(), '2026-09-08T06:35:00.000Z')
  assert.equal(dateToWall(date, 'Asia/Shanghai'), '2026-09-08 14:35:00')
})
test('rejects invalid calendar dates and zones', () => {
  for (const wall of ['2026-02-30 12:00:00', '2026-09-08 24:00:00', '', '2026-13-01 00:00:00']) assert.equal(parseWallTime(wall, 'UTC').error, 'invalid')
  assert.equal(parseWallTime('2026-09-08 14:35:00', 'invalid-zone').error, 'invalid')
})
test('rejects spring gaps and autumn repeated times', () => {
  assert.equal(parseWallTime('2026-03-08 02:30:00', 'America/New_York').error, 'nonexistent')
  assert.equal(parseWallTime('2026-11-01 01:30:00', 'America/New_York').error, 'ambiguous')
  assert.equal(wallToDate('2026-03-08 03:30:00', 'America/New_York').toISOString(), '2026-03-08T07:30:00.000Z')
})
test('handles half-hour DST and skipped calendar days', () => {
  assert.equal(parseWallTime('2026-10-04 02:15:00', 'Australia/Lord_Howe').error, 'nonexistent')
  assert.equal(parseWallTime('2026-04-05 01:45:00', 'Australia/Lord_Howe').error, 'ambiguous')
  assert.equal(parseWallTime('2011-12-30 12:00:00', 'Pacific/Apia').error, 'nonexistent')
})
test('source disappearance or site switch preserves the selected historical instants', () => {
  const original = ['2026-09-07 10:00:00', '2026-09-07 10:15:00']
  const converted = rezoneRange(original, 'Asia/Shanghai', 'UTC')
  assert.deepEqual(converted, ['2026-09-07 02:00:00', '2026-09-07 02:15:00'])
  assert.deepEqual(rezoneRange(converted, 'UTC', 'Asia/Shanghai'), original)
  assert.deepEqual(original, ['2026-09-07 10:00:00', '2026-09-07 10:15:00'])
})
test('keeps the old zone when either endpoint cannot safely convert', () => {
  assert.equal(rezoneRange(['2026-11-01 05:15:00', '2026-11-01 05:45:00'], 'UTC', 'America/New_York'), null)
  assert.equal(rezoneRange(['', '2026-09-08 14:35:00'], 'Asia/Shanghai', 'UTC'), null)
})
