import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
const mod={};new Function('exports',ts.transpileModule(readFileSync(new URL('../src/utils/aggregatedMonitorErrors.ts',import.meta.url),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText)(mod);
const trend={};new Function('exports',ts.transpileModule(readFileSync(new URL('../src/utils/monitorErrorTrend.ts',import.meta.url),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText)(trend);
const query=()=>new URLSearchParams({site:'site',instance_id:'a',dimension_type:'instance_model',value:'GPT:variant',hours:'1',bucket:'1m',start_time:'2026-10-09T01:00:00Z',end_time:'2026-10-09T02:00:00Z'});
const result=()=>({since:'2026-10-09T01:00:00Z',until:'2026-10-09T02:00:00Z',started_at:'2026-10-09T00:00:00Z',observed_at:'2026-10-09T02:00:30Z',covered_until:'2026-10-09T02:00:00Z',dropped:0,total_errors:25001,codes:[{key:'http:429',count:25001},{key:'zero_output',count:2}],buckets:[{time:'2026-10-09T01:00:00Z',counts:{'http:429':25001}}],truncated:false});
test('one CT statistics query handles large counts and colon-containing model keys',async()=>{
 let calls=0;const data=await mod.loadAggregatedMonitorErrors(query(),new AbortController().signal,async url=>{calls++;assert.ok(url.startsWith('/api/dashboard/error-statistics?'));const p=new URL(url,'http://local').searchParams;assert.equal(p.get('dimension_key'),'a:model:GPT:variant');assert.equal(p.get('timeline'),'true');return result();});
 assert.equal(calls,1);assert.equal(data.total,25001);assert.equal(data.configured,true);assert.equal(data.items.length,1);assert.equal(trend.errorTrend(data)[0].data[0][1],25001);
});
test('missing agent, backlog and unknown loss never render zero curves',async()=>{
 for(const state of [{started_at:null},{covered_until:null},{dropped:3}]) {const data=await mod.loadAggregatedMonitorErrors(query(),new AbortController().signal,async()=>({...result(),...state}));assert.equal(data.configured,false);assert.equal(data.buckets.length,0);assert.ok(data.notice);}
});
test('partial history and trailing backlog clamp to covered minutes',async()=>{
 const data=await mod.loadAggregatedMonitorErrors(query(),new AbortController().signal,async()=>({...result(),since:'2026-10-09T01:10:00Z',until:'2026-10-09T01:30:00Z',covered_until:'2026-10-09T01:30:20Z',buckets:[]}));
 assert.equal(data.configured,true);assert.match(data.notice,/历史未覆盖/);assert.match(data.notice,/后续分钟/);const points=trend.errorTrend(data)[0].data;assert.equal(points.length,20);assert.equal(points[0][0],'2026-10-09T01:10:00.000Z');assert.equal(points.at(-1)[0],'2026-10-09T01:29:00.000Z');
});
test('known historical loss can recover in a later covered window',async()=>{
 const data=await mod.loadAggregatedMonitorErrors(query(),new AbortController().signal,async()=>({...result(),dropped:2,last_loss_at:'2026-10-09T00:30:00Z'}));assert.equal(data.configured,true);assert.match(data.notice,/已排除/);
});
test('focused codes pass through and errors never fall back to source logs',async()=>{
 const q=query();q.set('code','business:tail');let calls=0;
 await assert.rejects(mod.loadAggregatedMonitorErrors(q,new AbortController().signal,async url=>{calls++;assert.equal(new URL(url,'http://local').searchParams.get('code'),'business:tail');throw new Error('offline');}),/offline/);assert.equal(calls,1);
});
