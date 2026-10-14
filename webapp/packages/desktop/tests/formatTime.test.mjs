import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/format.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const { formatTime } = new Function('exports', `${compiled}\nreturn exports;`)({})

test('cached time formatter preserves the existing locale and local time output', () => {
  for (const value of ['2026-01-02T00:00:00Z', '2026-10-14T01:02:03+08:00', '2026-10-14T16:00:00Z', '2000-02-29T23:59:59.999Z']) {
    assert.equal(formatTime(value), new Date(value).toLocaleString('zh-CN', { hour12: false }))
  }
})

test('cached time formatter preserves empty and invalid values', () => {
  assert.equal(formatTime(null), '—')
  assert.equal(formatTime(undefined), '—')
  assert.equal(formatTime(''), '—')
  assert.equal(formatTime('invalid-date'), 'invalid-date')
})
