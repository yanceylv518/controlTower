import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/logLine.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { splitLogLine } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('message starts after request ID, preserving source labels and multiline content', () => {
  for (const source of ['', '[服务器一 / new-api-prod] ']) {
    const prefix = `${source}oneapi-20260908132105.log [byte 305038584] [INFO] 2026/09/08 - 17:55:18 | request123 | `
    const body = 'record consume log: params={"model":"glm-5.2"} | extra\n  continuation'
    assert.deepEqual(splitLogLine(prefix + body), { prefix, body })
  }
})

test('unknown formats and GIN records remain intact', () => {
  for (const body of ['plain log', '[GIN] 2026/09/08 - 17:55:18 | route | request123 | 500', '', 'stack trace\n[INFO] 2026/09/08 - 17:55:18 | id | nested']) {
    assert.deepEqual(splitLogLine(body), { prefix: '', body })
  }
})
