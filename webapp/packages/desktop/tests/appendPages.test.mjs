import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, effectScope, onScopeDispose, ref, shallowRef, watch } from 'vue'
const source = readFileSync(new URL('../src/composables/useAppendPages.ts', import.meta.url), 'utf8').replace(/^import .*$/gm, '').replace('export function', 'function')
const js = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
const useAppendPages = new Function('computed','onScopeDispose','ref','shallowRef','watch',js+';return useAppendPages')(computed,onScopeDispose,ref,shallowRef,watch)
const row = id => ({id})
function setup(loader) {
  const base=shallowRef({items:[row(1),row(2)],total:5}),busy=ref(false),scope=effectScope()
  const feed=scope.run(()=>useAppendPages(()=>base.value,()=>busy.value,loader,x=>x.id))
  return {base,busy,scope,...feed}
}
test('appends without replacing previous rows; deduplicates while advancing raw offset',async()=>{
  const offsets=[]
  const p=setup(async(_,offset)=>{offsets.push(offset);return {items:offset===2?[row(2),row(3)]:[row(4)],total:5}})
  await p.loadMore();assert.deepEqual(p.items.value.map(x=>x.id),[1,2,3]);assert.equal(p.hasMore.value,true)
  await p.loadMore();assert.deepEqual(offsets,[2,4]);assert.equal(p.hasMore.value,false);p.scope.stop()
})
test('failure retains existing rows and retries the same offset',async()=>{
  let calls=0;const offsets=[]
  const p=setup(async(_,offset)=>{offsets.push(offset);if(!calls++)throw Error('offline');return {items:[row(3)],has_more:false}})
  await p.loadMore();assert.ok(p.error.value);assert.equal(p.items.value.length,2)
  await p.loadMore();assert.deepEqual(offsets,[2,2]);assert.equal(p.error.value,'');assert.equal(p.items.value.length,3);p.scope.stop()
})
test('ignores stale append when a new filtered response arrives',async()=>{
  let resolve;const p=setup(()=>new Promise(r=>resolve=r))
  const pending=p.loadMore();p.base.value={items:[row(9)],total:1};resolve({items:[row(3)],total:5});await pending
  assert.deepEqual(p.items.value.map(x=>x.id),[9]);assert.equal(p.hasMore.value,false);p.scope.stop()
})
test('prevents concurrent requests and cancels append while base query refreshes',async()=>{
  let resolve,calls=0,signal;const p=setup((_,offset,s)=>{calls++;signal=s;return new Promise(r=>resolve=r)})
  const pending=p.loadMore();await p.loadMore();assert.equal(calls,1)
  p.busy.value=true;assert.equal(signal.aborted,true);resolve({items:[row(3)],total:5});await pending
  assert.equal(p.items.value.length,2);assert.equal(p.loading.value,false);p.scope.stop()
})
test('an empty page terminates loading even when has_more incorrectly remains true',async()=>{
  const p=setup(async()=>({items:[],has_more:true}));await p.loadMore();assert.equal(p.hasMore.value,false);p.scope.stop()
})
