import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { createPinia, setActivePinia } from 'pinia'

const pending = new Map()
globalThis.currencyTestAPI = { siteCurrency: site => new Promise((resolve, reject) => pending.set(site, { resolve, reject })) }
let source = readFileSync(new URL('../src/stores/prefs.ts', import.meta.url), 'utf8')
source = source.replace('from "pinia"', `from ${JSON.stringify(import.meta.resolve('pinia'))}`).replace('import { dashboard } from "../api";', 'const dashboard = globalThis.currencyTestAPI;')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { usePrefsStore } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

test('switching sites immediately clears money and ignores late responses from the previous site', async () => {
  setActivePinia(createPinia())
  const prefs = usePrefsStore()
  const a = prefs.loadCurrency('a')
  const b = prefs.loadCurrency('b')
  assert.ok(Number.isNaN(prefs.quotaPerUnit))
  pending.get('b').resolve({ quota_per_unit: 1000000, symbol: '$', price_multiplier: 1 })
  await b
  pending.get('a').resolve({ quota_per_unit: 500000 / 7.2, symbol: '¥', price_multiplier: 7.2 })
  await a
  assert.equal(prefs.currencySite, 'b')
  assert.equal(prefs.currencySymbol, '$')
  assert.equal(prefs.quotaPerUnit, 1000000)
})
test('an unavailable site never falls back to a global or previous currency', async () => {
  setActivePinia(createPinia())
  const prefs = usePrefsStore()
  const request = prefs.loadCurrency('missing')
  pending.get('missing').reject(new Error('unavailable'))
  await request
  assert.ok(Number.isNaN(prefs.quotaPerUnit))
  assert.equal(prefs.currencySymbol, '')
  assert.ok(prefs.currencyError)
})
