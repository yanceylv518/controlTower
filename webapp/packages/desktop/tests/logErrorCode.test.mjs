import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../src/utils/logErrorCode.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
const exports = {}
new Function('exports', compiled)(exports)
const code = (extra = {}) => exports.logErrorCode({type:5,other:'',content_summary:'',...extra})
test('reads explicit structured status and provider error codes', () => {
  assert.equal(code({other:'{"status_code":429,"error_code":"rate_limit"}'}), '429')
  assert.equal(code({content_summary:'{"error":{"code":"insufficient_quota"}}'}), 'insufficient_quota')
  assert.equal(code({content_summary:'request failed: status code: 503, retry later'}), '503')
})
test('does not invent codes from unrelated numbers or successful logs', () => {
  assert.equal(code({content_summary:'timeout after 500 ms on channel 429'}), '')
  assert.equal(code({type:2,other:'{"status_code":200}'}), '')
  assert.equal(code({other:'invalid json',content_summary:'unknown error'}), '')
  assert.equal(code({other:'{"code":null}'}), '')
})
