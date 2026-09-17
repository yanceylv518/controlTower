import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
import { computed, ref } from 'vue'

const source = fs.readFileSync(new URL('../src/views/BillingTasksView.vue', import.meta.url), 'utf8')
const script = source.split('<script setup lang="ts">')[1].split('</script>')[0]
  .replace(/^import .*;\r?\n/gm, '')
  .replace(/void state\.reload\(\)\.then\(.*\);?/, '')
const compiled = ts.transpile(script, { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None })
function page() {
  const data = ref({ items: [], pricing_source_selection: true })
  const calls = [], errors = []
  const dashboard = { billingUpstreams: async () => ({ items: [] }), createBillingStatement: async value => { calls.push(value) } }
  const passthrough = { users: async () => ({ items: [] }) }
  const state = { data, reload: async () => {}, refresh: async () => {} }
  const create = new Function('computed','ref','onUnmounted','watch','useRoute','useRouter','ElMessage','ElMessageBox','dashboard','passthrough','useAsyncData','useFiltersStore','billingReadErrorMessage','billingTaskErrorMessage','formatNumber','siteOf', `${compiled}\nreturn { createForm, openCreate, createJob };`)
  const view = create(computed,ref,()=>{},()=>{},()=>({query:{}}),()=>({}),{error: m=>errors.push(m),warning:m=>errors.push(m),success:()=>{}},{},dashboard,passthrough,()=>state,()=>({site_id:'site',instances:[]}),String,String,String,item=>item.site_id||item.instance_id)
  return { ...view, calls, errors, data }
}
function fill(p) {
  p.createForm.value = { instance_id:'site',bill_type:'user',user_id:7,exclude_zero_output:false,recalculate:false,range:[new Date(2026,8,1),new Date(2026,8,1)] }
}
test('new statement defaults to source amount and sends false', async () => {
  const p=page(); await p.openCreate(); assert.equal(p.createForm.value.recalculate,false)
  p.createForm.value.user_id=7; await p.createJob()
  assert.equal(p.calls.length,1); assert.equal(p.calls[0].recalculate,false)
})
test('recalculate is explicitly sent, and opening another new task resets it', async () => {
  const p=page(); fill(p);p.createForm.value.recalculate=true;await p.createJob()
  assert.equal(p.calls[0].recalculate,true)
  await p.openCreate();assert.equal(p.createForm.value.recalculate,false)
})
test('dedup distinguishes pricing modes and legacy usage format', async () => {
  for(const [source,version,blocked] of [['newapi',2,true],['recalculate',2,false],['newapi',1,false],['newapi',0,false]]) {
    const p=page();fill(p)
    p.data.value.items=[{instance_id:'site',status:'complete',job_type:'user_statement',user_id:7,range_from:'2026-09-01T00:00:00+08:00',range_to:'2026-09-02T00:00:00+08:00',pricing_source:source,usage_version:version}]
    await p.createJob(); assert.equal(p.calls.length,blocked?0:1)
  }
})
test('old server cannot silently ignore source mode selection', async () => {
  const p=page();fill(p);delete p.data.value.pricing_source_selection;await p.createJob()
  assert.equal(p.calls.length,0);assert.match(p.errors[0],/升级 Server/)
})
