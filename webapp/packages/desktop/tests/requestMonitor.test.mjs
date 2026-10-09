import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
import { computed,ref } from 'vue'
const utilCode=ts.transpileModule(fs.readFileSync(new URL('../src/utils/requestMonitor.ts',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const exports={};new Function('exports',utilCode)(exports)
const {monitorWindow,largePercent,bins,monitorError}=exports
const t=1791504000
const minute=(time,host,values={})=>({time,host,count:8,small:4,medium:1,large:1,huge:1,unknown:1,request_bytes:56789012,response_bytes:12345,response_unknown:0,latest:time+50,...values})
const data=()=>({status:'success',from:t-3600,to:t+30,queried_at:t+30,latest:t-30,settled_before:t-120,rows:[minute(t-180,'a.test'),minute(t-180,'47.1.2.3'),minute(t-120,'a.test',{count:999}),minute(t-240,'a.test')]})
test('four bins, totals, IP Hosts, actual bytes, unknowns and missing-minute gaps',()=>{
 const window=monitorWindow(data(),30,null),point=window.points.find(p=>p.time===t-180).value
 assert.equal(bins.length,4);assert.equal(point.count,16);assert.equal(point.small,8);assert.equal(point.unknown,2)
 assert.equal(point.request_bytes,113578024);assert.equal(largePercent(point),6/14*100)
 assert.equal(window.points.find(p=>p.time===t-120).value,null)
 assert.equal(window.points.find(p=>p.time===t-300).value,null)
 assert.equal(window.hosts.length,2);assert.equal(window.hosts[0].count,16)
 const filtered=monitorWindow(data(),30,'47.1.2.3')
 assert.equal(filtered.points.find(p=>p.time===t-180).value.count,8)
 assert.deepEqual(filtered.hosts,window.hosts)
})
test('window filtering, missing host and no-data never manufacture zero counts',()=>{
 const d=data();d.rows.push(minute(t-180,''))
 assert.equal(monitorWindow(d,15,'').points.find(p=>p.time===t-180).value.count,8)
 assert.ok(monitorWindow(d,15,null).points.every(p=>p.time>=t-900))
 assert.ok(monitorWindow({...d,rows:[]},30,null).points.every(p=>p.value===null))
 assert.equal(largePercent(minute(t,'a',{count:1,unknown:1})),null)
 assert.deepEqual(monitorWindow(undefined,30,null),{points:[],hosts:[]})
 assert.match(monitorError('alb_query_incomplete'),/部分统计/)
})
const source=fs.readFileSync(new URL('../src/views/RequestMonitorView.vue',import.meta.url),'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm,'')
const script=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText
class ApiError extends Error{constructor(status,code){super(code);this.status=status;this.code=code}}
function panel(){
 const requests=[],cleanup=[]
 const client={request:(path,options)=>new Promise((resolve,reject)=>requests.push({path,options,resolve,reject}))}
 const build=new Function('computed','ref','watch','onMounted','onUnmounted','useAuthStore','client','ApiError','bins','monitorWindow','largePercent','monitorError','document',script+';return {snapshot,channels,loading,error,channelError,loadALB,loadChannels,sort,selectedTime,index,row,current,availablePoints,latestAvailable,selectMinute};')
 const state=build(computed,ref,()=>{},()=>{},fn=>cleanup.push(fn),()=>({user:null}),client,ApiError,bins,monitorWindow,largePercent,monitorError,{removeEventListener:()=>{}})
 return {...state,requests,stop:()=>cleanup.forEach(fn=>fn())}
}
test('refresh coalesces; failures retain explicit old result; page aggregates all Hosts',async()=>{
 const p=panel(),run=p.loadALB();await p.loadALB();assert.equal(p.requests.length,1)
 p.requests[0].resolve(data());await run
 assert.equal(p.requests.length,1);assert.equal(p.row.value.count,16)
 const retry=p.loadALB();p.requests[1].reject(new Error('offline'));await retry
 assert.match(p.error.value,/上次/);assert.equal(p.snapshot.value.status,'success')
 p.stop()
})
test('channel sort race and unmount cannot overwrite current response',async()=>{
 const p=panel(),a=p.loadChannels();p.sort.value='latency';const b=p.loadChannels()
 assert.equal(p.requests[0].options.signal.aborted,true)
 p.requests[1].resolve({items:[{key:'new'}]});await b
 p.requests[0].resolve({items:[{key:'old'}]});await a
 assert.equal(p.channels.value.items[0].key,'new')
 const c=p.loadALB();p.stop();assert.equal(p.requests[2].options.signal.aborted,true)
 p.requests[2].resolve(data());await c;assert.equal(p.snapshot.value,undefined)
})

test('picker ends at latest displayable minute and follows newly available data',()=>{
 const p=panel();p.snapshot.value=data()
 assert.equal(p.current.value.time,t-180)
 assert.equal(p.index.value,p.availablePoints.value.length-1)
 p.index.value=999
 assert.equal(p.current.value.time,t-180)
 assert.notEqual(p.row.value,null)
 p.selectMinute(t*1000)
 assert.equal(p.current.value.time,t-180)
 const next=data();next.settled_before=t-60
 p.snapshot.value=next
 assert.equal(p.current.value.time,t-120)
 assert.equal(p.index.value,p.availablePoints.value.length-1)
 p.index.value=p.availablePoints.value.findIndex(point=>point.time===t-240)
 p.snapshot.value={...next,settled_before:t,rows:[...next.rows,minute(t-60,'a.test')]}
 assert.equal(p.current.value.time,t-240)
 p.selectedTime.value=null
 assert.equal(p.current.value.time,t-60)
 p.stop()
})

test('picker retains internal gaps and handles an empty window',()=>{
 const p=panel();p.snapshot.value=data()
 assert.equal(p.latestAvailable.value,t-180)
 p.snapshot.value={...data(),rows:[...data().rows,minute(t-360,'47.1.2.3')]}
 p.index.value=p.availablePoints.value.findIndex(point=>point.time===t-300)
 assert.equal(p.current.value.time,t-300)
 assert.equal(p.row.value,null)
 p.snapshot.value={...data(),rows:[]}
 assert.equal(p.availablePoints.value.length,0)
 assert.equal(p.current.value,undefined)
 assert.equal(p.latestAvailable.value,undefined)
 p.index.value=10
 assert.equal(p.selectedTime.value,null)
 p.stop()
})