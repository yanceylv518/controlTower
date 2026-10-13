import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
import { computed,ref } from 'vue'
const utilCode=ts.transpileModule(fs.readFileSync(new URL('../src/utils/requestMonitor.ts',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const exports={};new Function('exports',utilCode)(exports)
const {monitorWindow,largePercent,bins,legacyBins,hasDetailedSizes,monitorError,beijingInput,historyRange}=exports
const t=1791504000
const minute=(time,host,values={})=>({time,host,count:8,small:4,medium:1,large:1,huge:1,unknown:1,request_bytes:56789012,response_bytes:12345,response_unknown:0,latest:time+50,...values})
const data=()=>({status:'success',from:t-3600,to:t+30,queried_at:t+30,latest:t-30,settled_before:t-120,rows:[minute(t-180,'a.test'),minute(t-180,'47.1.2.3'),minute(t-120,'a.test',{count:999}),minute(t-240,'a.test')]})
test('four bins, totals, IP Hosts, actual bytes, unknowns and missing-minute gaps',()=>{
 const window=monitorWindow(data(),30,null),point=window.points.find(p=>p.time===t-180).value
 assert.equal(legacyBins.length,4);assert.equal(point.count,16);assert.equal(point.small,8);assert.equal(point.unknown,2)
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
 const build=new Function('computed','ref','watch','onMounted','onUnmounted','useAuthStore','client','ApiError','bins','legacyBins','hasDetailedSizes','monitorWindow','largePercent','monitorError','beijingInput','historyRange','document',script+';return {snapshot,channels,loading,error,channelError,loadALB,loadChannels,sort,selectedTime,index,row,current,availablePoints,latestAvailable,selectMinute,detailedSizes,activeBins,sizeSeries,minutes,historyFrom,historyTo,appliedHistory,historyMode,queryHistory,changeRange,rangeError,outdated,visible};')
 const state=build(computed,ref,()=>{},()=>{},fn=>cleanup.push(fn),()=>({user:null}),client,ApiError,bins,legacyBins,hasDetailedSizes,monitorWindow,largePercent,monitorError,beijingInput,historyRange,{removeEventListener:()=>{},visibilityState:'visible'})
 return {...state,requests,stop:()=>cleanup.forEach(fn=>fn())}
}

const detailedMinute=(time,host)=>minute(time,host,{from_20_to_30:0,from_30_to_40:0,at_least_40:1})

test('history inputs use Beijing time and validate dates, duration and future bounds',()=>{
 const end=Date.parse('2026-10-11T01:00:00+08:00')/1000
 assert.deepEqual(historyRange('2026-10-10T01:00','2026-10-11T01:00',end),{from:end-86400,to:end})
 assert.equal(beijingInput(end),'2026-10-11T01:00')
 for(const [from,to] of [['2026-10-10T00:59','2026-10-11T01:00'],['2026-10-11T01:00','2026-10-11T01:00'],['2026-10-11T00:00','2026-10-11T01:01'],['2026-02-30T00:00','2026-03-01T00:00'],['','2026-10-11T01:00']])assert.equal(historyRange(from,to,end),null)
})

test('switching ranges aborts old queries and rejects an older server instead of showing the wrong window',async()=>{
 const p=panel(),first=p.loadALB()
 p.minutes.value=180;p.changeRange()
 assert.equal(p.requests[0].options.signal.aborted,true)
 assert.equal(p.requests[1].path,'/api/dashboard/request-monitor?minutes=180')
 p.requests[0].resolve(data());await first
 assert.equal(p.snapshot.value,undefined);assert.equal(p.loading.value,true)
 p.requests[1].resolve(data());await new Promise(resolve=>setImmediate(resolve))
 assert.equal(p.snapshot.value,undefined);assert.match(p.error.value,/更新 Server/)
 const retry=p.loadALB();p.requests.at(-1).resolve({...data(),range_supported:true,from:t-10800});await retry
 assert.equal(p.availablePoints.value[0].time,t-10800)
 p.stop()
})

test('history queries keep the final minute, span midnight, pause auto refresh and keep result range separate from draft inputs',async()=>{
 const p=panel();p.snapshot.value=data();p.minutes.value=0;p.changeRange()
 assert.equal(p.snapshot.value,undefined);assert.equal(p.requests.length,0)
 p.historyFrom.value='2026-10-08T23:00';p.historyTo.value='2026-10-09T01:00';p.queryHistory()
 const {from,to}=p.appliedHistory.value
 assert.equal(p.requests[0].path,`/api/dashboard/request-monitor?from=${from}&to=${to}`)
 p.requests[0].resolve({range_supported:true,status:'success',from,to,queried_at:t,latest:to-10,settled_before:to,rows:[minute(to-60,'',{unknown:0,request_bytes:60000000})]})
 await new Promise(resolve=>setImmediate(resolve))
 assert.equal(p.current.value.time,to-60);assert.equal(p.outdated.value,false)
 assert.equal(p.availablePoints.value[0].time,from)
 p.visible();assert.equal(p.requests.length,1)
 p.historyFrom.value='2026-10-01T00:00';p.queryHistory()
 assert.match(p.rangeError.value,/24 小时/);assert.equal(p.requests.length,1)
 assert.equal(p.appliedHistory.value.from,from);assert.equal(p.current.value.time,to-60)
 p.stop()
})

test('history rejects a mismatched response interval and never falls back to recent data',async()=>{
 const p=panel();p.minutes.value=0;p.changeRange()
 p.historyFrom.value='2026-10-08T23:00';p.historyTo.value='2026-10-09T01:00';p.queryHistory()
 p.requests[0].resolve({...data(),range_supported:true})
 await new Promise(resolve=>setImmediate(resolve))
 assert.equal(p.snapshot.value,undefined);assert.match(p.error.value,/不一致/)
 p.stop()
})
test('six disjoint bins aggregate across Hosts without changing summary totals or filling gaps',()=>{
 const d={...data(),rows:[detailedMinute(t-180,'a'),detailedMinute(t-180,'b')]}
 const p=panel();p.snapshot.value=d
 assert.equal(p.detailedSizes.value,true)
 assert.deepEqual(p.activeBins.value.map(b=>b.name),['<5 MiB','5–10 MiB','10–20 MiB','20–30 MiB','30–40 MiB','≥40 MiB'])
 assert.equal(hasDetailedSizes(p.row.value),true)
 assert.equal(p.row.value.small,8);assert.equal(p.row.value.at_least_40,2)
 assert.equal(bins.reduce((sum,b)=>sum+p.row.value[b.key],0)+p.row.value.unknown,p.row.value.count)
 assert.equal(largePercent(p.row.value),6/14*100)
 assert.equal(p.sizeSeries.value[5].data.find(([time])=>Date.parse(time)===(t-180)*1000)[1],2)
 assert.equal(p.sizeSeries.value[5].data.find(([time])=>Date.parse(time)===(t-240)*1000)[1],null)
 p.stop()
})
test('older or mixed responses retain four bins instead of inventing detailed zeros',()=>{
 for(const rows of [[minute(t-180,'old')],[detailedMinute(t-180,'new'),minute(t-180,'old')],[minute(t-180,'old'),detailedMinute(t-180,'new')]]){
  const p=panel();p.snapshot.value={...data(),rows}
  assert.equal(p.detailedSizes.value,false);assert.equal(p.activeBins.value.length,4)
  assert.equal(p.row.value.at_least_40,undefined)
  assert.equal(hasDetailedSizes(p.row.value),false)
  p.stop()
 }
 assert.equal(hasDetailedSizes({...detailedMinute(t,'a'),at_least_40:2}),false)
 assert.equal(hasDetailedSizes({...detailedMinute(t,'a'),from_20_to_30:-1,from_30_to_40:1}),false)
})
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
 assert.ok(!p.requests[0].path.includes('site='));assert.equal(p.requests[0].options.signal.aborted,true)
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
test('channel values retain unknown and lower bounds, and chart values use readable precision',()=>{
 const m={value:90,samples:100,minutes:5,lower_bound:true,trend:[24.749999999999996,null,90]}
 assert.equal(exports.measureText(m),'≥90.0秒')
 assert.equal(exports.measureText({...m,value:null}),'—')
 assert.equal(exports.measureText({...m,value:5,lower_bound:false},'%'),'5.0%')
 const series=exports.channelSeries({ttft:m,duration:m,errors:m},t)
 assert.equal(series[0].data[0][1],24.7)
 assert.equal(series[0].data[1][1],null)
 assert.equal(exports.channelSeries({errors:m},t,true)[0].unit,'%')
})
