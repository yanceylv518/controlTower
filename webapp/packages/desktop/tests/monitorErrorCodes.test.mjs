import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {ref,computed} from 'vue';
const util={};new Function('exports',ts.transpileModule(readFileSync(new URL('../src/utils/monitorErrorTrend.ts',import.meta.url),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText)(util);
const source=readFileSync(new URL('../src/components/MonitorErrorCodes.vue',import.meta.url),'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm,'');
const compiled=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText;
function setup(){const props={site:'a',dimensionType:'instance_user',value:'4',hours:1,active:true,bucket:'1m',series:[{name:'请求量',data:[['2026-09-27T06:00:00Z',20]]}]};const calls=[];const client={request:(url,opts)=>new Promise((resolve,reject)=>calls.push({url,opts,resolve,reject}))};const make=new Function('ref','computed','watch','onBeforeUnmount','defineProps','client','errorTrend',compiled+';return {load,data,error,loading,mode,expanded}');return {...make(ref,computed,()=>{},()=>{},()=>props,client,util.errorTrend),props,calls};}
test('inactive panel does not query and parameter changes reject stale results',async()=>{
 const c=setup();c.props.active=false;await c.load();assert.equal(c.calls.length,0);c.props.active=true;c.mode.value='codes';
 const first=c.load();c.props.value='8';const second=c.load();assert.equal(c.calls[0].opts.signal.aborted,true);
 assert.equal(new URL(c.calls[1].url,'http://local').searchParams.get('value'),'8');
 c.calls[1].resolve({configured:true,total:1,items:[{code:'429',count:1}]});await second;
 c.calls[0].resolve({configured:true,total:999,items:[]});await first;assert.equal(c.data.value.total,1);
});
test('failed query clears old totals and reports limits instead of zero',async()=>{
 const c=setup();c.mode.value='codes';const a=c.load();c.calls[0].resolve({configured:true,total:3,items:[]});await a;
 const b=c.load(true);c.calls[1].reject({code:'error_statistics_limit'});await b;
 assert.equal(c.data.value,undefined);assert.match(c.error.value,/缩短/);assert.equal(c.loading.value,false);
});

test('overview stays lazy, expand requests the same monitoring bucket window',async()=>{
 const c=setup();await c.load();assert.equal(c.calls.length,0);c.expanded.value=true;const task=c.load();
 const q=new URL(c.calls[0].url,'http://local').searchParams;
 assert.equal(q.get('start_time'),'2026-09-27T06:00:00.000Z');assert.equal(q.get('end_time'),'2026-09-27T06:01:00.000Z');assert.equal(q.get('bucket'),'1m');
 c.calls[0].resolve({configured:true,total:0,items:[],buckets:[],bucket_seconds:60,start_time:q.get('start_time'),end_time:q.get('end_time')});await task;
 await c.load();assert.equal(c.calls.length,1);
});
test('Top5 plus other conserves counts, fills complete empty buckets, preserves unknown code',()=>{
 const result={configured:true,total:28,items:['429','502','timeout','500','503','', 'x'].map((code,i)=>({code,count:7-i})),bucket_seconds:60,start_time:'2026-09-27T06:00:00Z',end_time:'2026-09-27T06:03:00Z',buckets:[{time:'2026-09-27T06:01:00Z',counts:{'429':7,'502':6,timeout:5,'500':4,'503':3,'':2,x:1}}]};
 const curves=util.errorTrend(result);assert.equal(curves.length,6);assert.equal(curves.reduce((s,c)=>s+c.data[1][1],0),28);assert.equal(curves[5].data[1][1],3);assert.ok(curves.every(c=>c.data[0][1]===0&&c.data[2][1]===0));assert.equal(util.errorTrend(result,'')[0].name,'未知');assert.equal(util.errorTrend(result,'')[0].data[1][1],2);
});
