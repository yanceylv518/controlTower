import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const compiled=ts.transpileModule(readFileSync(new URL('../src/utils/logArchive.ts',import.meta.url),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
const {toggleArchiveGroup,archiveDataLabel}=await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
const base=()=>({version:9,running:true,history_immutable:true,pipeline:{migration:true,organization:true,verification:true,collection:true,collection_from:'2026-09-01',collection_through:'2026-09-20'}})
test('pausing historical processing preserves collection, range, migration and policy',()=>{
 const input=base(),next=toggleArchiveGroup(input,'history')
 assert.equal(next.pipeline.organization,false);assert.equal(next.pipeline.verification,false)
 assert.equal(next.pipeline.collection,true);assert.equal(next.pipeline.migration,true)
 assert.equal(next.pipeline.collection_from,'2026-09-01');assert.equal(next.history_immutable,true)
 assert.equal(next.running,true);assert.equal(input.pipeline.organization,true)
})
test('resuming one task after global pause does not revive the other task',()=>{
 for(const group of ['history','collection']){
  const input={...base(),running:false},next=toggleArchiveGroup(input,group)
  assert.equal(next.running,true)
  assert.equal(next.pipeline.collection,group==='collection')
  assert.equal(next.pipeline.organization,group==='history')
  assert.equal(next.pipeline.verification,group==='history')
 }
})
test('partially enabled historical task pauses both stages before resuming together',()=>{
 const input=base();input.pipeline.organization=false
 const paused=toggleArchiveGroup(input,'history'),resumed=toggleArchiveGroup(paused,'history')
 assert.equal(paused.pipeline.verification,false)
 assert.equal(resumed.pipeline.organization,true);assert.equal(resumed.pipeline.verification,true)
})
test('simplified data labels preserve uncertainty and failures',()=>{
 assert.equal(archiveDataLabel('sealed'),'已封存')
 assert.equal(archiveDataLabel('changed'),'待处理')
 assert.equal(archiveDataLabel('verification'),'处理中')
 assert.equal(archiveDataLabel('blocked'),'处理失败')
 assert.equal(archiveDataLabel('unknown'),'状态待确认')
 assert.equal(archiveDataLabel('reported'),'状态待确认')
 assert.equal(archiveDataLabel('matched'),'状态待确认')
})
