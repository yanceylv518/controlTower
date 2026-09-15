import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, ref } from 'vue'

const sfc = readFileSync(new URL('../src/components/VoiceAlertsSettings.vue', import.meta.url), 'utf8')
const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
function settings() {
 const calls = [], errors = []
 const response = { config: { enabled: true, tts_code: 'TTS_test', called_show_number: '', percent: 50, delta: 10000000, recipients: [{ phone: '13800000000', targets: [] }] }, customers: [{ site: 'a', user_id: 7, label: 'alice' }, { site: 'b', user_id: 7, label: 'bob' }], unavailable_sites: [], calls: [], targets: [] }
 const client = { request: async (url, options) => { calls.push({url, ...options}); return structuredClone(response) } }
 const create = new Function('computed', 'ref', 'onMounted', 'ElMessage', 'client', `${compiled}\nreturn {request, config, customers, customerOptions, directoryWarning, deltaWan};`)
 return { ...create(computed, ref, () => {}, {error: e => errors.push(e), success: () => {}}, client), calls, errors, response }
}

test('all recipients save dynamic all scope without a manual customer list', async () => {
 const s = settings(); await s.request()
 assert.equal(s.config.value.recipients[0].scope, 'all')
 s.config.value.recipients[0].targets = ['a/7'] // previous selection while all is active
 await s.request(true)
 const body = JSON.parse(s.calls.at(-1).body)
 assert.deepEqual(body.recipients, [{ phone: '13800000000', targets: [] }])
 assert.equal('targets' in body, false)
 assert.equal('scope' in body.recipients[0], false)
})

test('multi-select preserves site identity and refuses accidentally empty selected scope', async () => {
 const s = settings(); await s.request()
 s.config.value.recipients[0].scope = 'selected'
 await s.request(true)
 assert.equal(s.calls.length, 1)
 assert.equal(s.errors.length, 1)
 s.config.value.recipients[0].targets = ['a/7', 'b/7']
 await s.request(true)
 assert.deepEqual(JSON.parse(s.calls.at(-1).body).recipients[0].targets, ['a/7', 'b/7'])
})

test('directory failure and record refresh retain unsaved phone and specific subscriptions', async () => {
 const s = settings(); await s.request()
 const recipient = s.config.value.recipients[0]
 recipient.phone = '13900000000'; recipient.scope = 'selected'; recipient.targets = ['a/7']
 s.response.customers = []; s.response.directory_error = '目录暂不可用'
 await s.request(false, true)
 assert.equal(s.config.value.recipients[0].phone, '13900000000')
 assert.deepEqual(s.config.value.recipients[0].targets, ['a/7'])
 assert.equal(s.config.value.recipients[0].scope, 'selected')
 assert.deepEqual(s.customerOptions.value, [{key:'a/7', label:'暂不可用（a/7）', unavailable:true}])
 assert.equal(s.directoryWarning.value, '目录暂不可用')
})

test('legacy specific subscriptions load from directory without broadening scope', async () => {
 const s = settings(); s.response.config.recipients[0].targets = ['b/7']
 await s.request()
 assert.equal(s.config.value.recipients[0].scope, 'selected')
 assert.equal(s.customerOptions.value.find(o => o.key === 'b/7').label, 'bob（b · #7）')
})

test('old server without directory is explained and cannot receive incompatible saves', async () => {
 const s = settings(); delete s.response.customers
 await s.request()
 assert.match(s.directoryWarning.value, /远程 Server 尚未支持客户自动获取/)
 await s.request(true)
 assert.equal(s.calls.length, 1)
 assert.match(s.errors[0], /更新远程 Server/)
})

test('TPM threshold displays ten-thousands and saves integer tokens without changing existing values', async () => {
 const s = settings(); await s.request()
 assert.equal(s.deltaWan.value, 1000)
 await s.request(true)
 assert.equal(JSON.parse(s.calls.at(-1).body).delta, 10000000)
 s.deltaWan.value = 1234.5678
 await s.request(true)
 assert.equal(JSON.parse(s.calls.at(-1).body).delta, 12345678)
 for (const tokens of [1, 10000001, 1000000000000]) {
  s.config.value.delta = tokens
  s.deltaWan.value = s.deltaWan.value
  assert.equal(s.config.value.delta, tokens)
 }
 s.deltaWan.value = undefined
 const count = s.calls.length
 await s.request(true)
 assert.equal(s.calls.length, count)
 assert.match(s.errors.at(-1), /TPM 差值/)
})

test('percent defaults to 20 with optional condition and preserves saved false', async () => {
 const s = settings()
 assert.equal(s.config.value.percent, 20)
 assert.equal(s.config.value.use_percent, true)
 await s.request()
 assert.equal(s.config.value.percent, 50)
 assert.equal(s.config.value.use_percent, true)
 s.config.value.use_percent = false
 s.config.value.percent = 20
 await s.request(true)
 const body = JSON.parse(s.calls.at(-1).body)
 assert.equal(body.use_percent, false)
 assert.equal(body.percent, 20)
 assert.equal(body.delta, 10000000)
 s.response.config.use_percent = false
 await s.request()
 assert.equal(s.config.value.use_percent, false)
})
