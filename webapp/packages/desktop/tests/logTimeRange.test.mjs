import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../src/components/CompactDateTimeRangePicker.vue', import.meta.url), 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '')
const code = ts.transpileModule(script, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
function setup() {
  const events=[]
  const create=new Function('computed','ref','watch','onMounted','onUnmounted','nextTick','defineProps','withDefaults','defineEmits',code+';return {parseInput,toInputValue,syncDraft,updateDraftPart,parseDraft,label,draftStart,draftEnd,applyDraft};')
  const p=create(fn=>({get value(){return fn()}}),value=>({value}),()=>{},()=>{},()=>{},async()=>{},()=>({compact:true,modelValue:[new Date(2026,8,20,8,15,12),new Date(2026,8,20,9,45,56)]}),p=>p,()=>((...args)=>events.push(args)))
  p.syncDraft();return {...p,events}
}
test('mobile range exposes times and preserves seconds when only changing a date',()=>{
  const p=setup();assert.match(p.label.value,/08:15:12.*09:45:56/)
  p.updateDraftPart('start','date','2026-09-19')
  assert.equal(p.draftStart.value,'2026-09-19T08:15:12')
  p.updateDraftPart('end','time','10:20:30');p.applyDraft()
  assert.equal(p.events[0][1][1].getHours(),10)
  assert.equal(p.events[0][1][1].getSeconds(),30)
})
test('invalid, incomplete and reversed ranges cannot be submitted',()=>{
  const p=setup()
  for(const value of ['2026-02-30T10:00:00','2026-09-20T24:00:00','2026-09-20T10:00:60','2026-09-20T'])assert.equal(p.parseInput(value),undefined)
  p.updateDraftPart('end','time','07:00:00');p.applyDraft();assert.equal(p.events.length,0)
  assert.equal(p.parseInput('2026-09-20T10:20').getSeconds(),0)
})
