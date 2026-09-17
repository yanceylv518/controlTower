import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, ref } from 'vue'

const sfc = readFileSync(new URL('../src/views/DimensionView.vue', import.meta.url), 'utf8')
const expression = sfc.match(/const weightedOTPS = computed\([\s\S]*?\n\}\);/)[0]
const compiled = ts.transpileModule(expression, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
const get = rows => new Function('computed', 'visibleRows', compiled + '\nreturn weightedOTPS.value;')(computed, ref(rows))

test('monitor summary divides total eligible output by total request time', () => {
  assert.equal(get([
    { otps: 100, otps_sample_tokens: 100, otps_duration_seconds: 1 },
    { otps: 20, otps_sample_tokens: 300, otps_duration_seconds: 15 },
  ]), 25)
})

test('old or unavailable metrics never become zero-speed samples or a mixed formula', () => {
  assert.equal(get([{ otps: 100, otps_sample_tokens: 100 }]), null)
  assert.equal(get([
    { otps: 100, otps_sample_tokens: 100 },
    { otps: 20, otps_sample_tokens: 300, otps_duration_seconds: 15 },
    { otps: null, otps_sample_tokens: 1000, otps_duration_seconds: 100 },
  ]), 20)
})
