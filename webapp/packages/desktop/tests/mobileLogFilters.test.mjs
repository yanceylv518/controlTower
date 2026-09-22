import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, reactive, ref } from 'vue'

const source = readFileSync(new URL('../src/components/MobileLogFilters.vue', import.meta.url), 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target:ts.ScriptTarget.ES2022, module:ts.ModuleKind.ES2022 } }).outputText
const factory = new Function('defineProps', 'defineEmits', 'computed', 'reactive', 'ref', compiled + '\nreturn { draft, sheetOpen, openFilters, applyFilters, filterCount, emptyFilters };')
const initial = () => ({logType:2,modelName:'gpt-4.1',group:'default',tokenName:'',requestID:'',upstreamRequestID:'',channelID:'',statusCode:'',emptyOutput:false})
function setup(extra = {}) {
  const props = reactive({filters:initial(),admin:false,busy:false,...extra}), events=[]
  const panel = factory(()=>props,()=>((...args)=>events.push(args)),computed,reactive,ref)
  return {props,events,...panel}
}
test('closing filter sheet discards edits; reopening starts from current query',()=>{
  const p=setup();p.openFilters();p.draft.modelName='draft-only';p.sheetOpen.value=false
  assert.equal(p.props.filters.modelName,'gpt-4.1');assert.deepEqual(p.events,[])
  p.props.filters.modelName='latest-query';p.openFilters();assert.equal(p.draft.modelName,'latest-query')
})
test('apply emits one complete filter snapshot and closes the sheet',()=>{
  const p=setup();p.openFilters();p.draft.requestID='req-demo';p.applyFilters()
  assert.deepEqual(p.events,[['apply',{...initial(),requestID:'req-demo'}]])
  assert.equal(p.sheetOpen.value,false);p.draft.requestID='changed-later';assert.equal(p.events[0][1].requestID,'req-demo')
})
test('reset stays in draft until apply, and busy state prevents submission',()=>{
  const p=setup();p.openFilters();Object.assign(p.draft,p.emptyFilters());assert.deepEqual(p.props.filters,initial());assert.deepEqual(p.events,[])
  p.props.busy=true;p.applyFilters();assert.deepEqual(p.events,[]);assert.equal(p.sheetOpen.value,true)
  p.props.busy=false;p.applyFilters();assert.deepEqual(p.events,[['apply',p.emptyFilters()]])
})
test('viewer cannot submit hidden channel filter; admin preserves channel input',()=>{
  for(const admin of [false,true]){
    const p=setup({admin,filters:{...initial(),channelID:'42'}});p.openFilters();p.applyFilters()
    assert.equal(p.events[0][1].channelID,admin?'42':'');assert.equal(p.filterCount.value,admin?4:3)
  }
})
test('all mobile filter values reach the shared query before search',()=>{
  const page=readFileSync(new URL('../src/views/ReadonlyLogsView.vue',import.meta.url),'utf8')
  const fn=page.match(/function applyMobileFilters\(value: MobileLogFilterValues\) \{([\s\S]*?)\n\}/)[1]
  const values={...initial(),tokenName:'key',requestID:'request',upstreamRequestID:'upstream',channelID:'9',statusCode:'429',emptyOutput:true}
  const keys=Object.keys(values),refs=keys.map(()=>ref('old'));let calls=0
  const run=new Function(...keys,'search','value',fn)
  run(...refs,()=>{calls++;assert.deepEqual(Object.fromEntries(keys.map((k,i)=>[k,refs[i].value])),values)},values)
  assert.equal(calls,1)
})
