import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {computed,ref,reactive,watch,effectScope} from 'vue'
const source=readFileSync(new URL('../src/components/ReportGenerationTasks.vue',import.meta.url),'utf8')
const script=source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import.*\r?\n/gm,'')
const code=ts.transpile(script,{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None})
const flush=()=>new Promise(resolve=>setImmediate(resolve))
function create(t){
 const props=reactive({site:'one',from:'2025-01-01',to:'2025-01-02',label:'day'}),calls=[],messages=[],dispose=[]
 const dashboard={reportTasks:async()=>({items:[]}),retryReportTask:async(site,id)=>{calls.push({site,id})}}
 const scope=effectScope();t.after(()=>{dispose.forEach(f=>f());scope.stop()})
 const init=new Function('computed','ref','watch','onBeforeUnmount','defineProps','defineEmits','setTimeout','clearTimeout','dashboard','ElMessage','ElMessageBox',code+'\nreturn {tasks,percent,status,taskStatus,retry,refresh,opened,selected,submitting,canRetry};')
 const state=scope.run(()=>init(computed,ref,watch,f=>dispose.push(f),()=>props,()=>()=>{},()=>1,()=>{},dashboard,{error:v=>messages.push(v)},{}))
 return {...state,props,dashboard,calls,messages}
}
test('failed and cancelled days are not counted as successfully generated',t=>{
 const v=create(t),task={status:'failed',days:[{status:'failed',processed:5060000}]}
 assert.equal(v.percent(task),0);assert.equal(v.taskStatus(task),'生成失败');assert.equal(v.canRetry(task),true)
 task.days.push({status:'complete'},{status:'reused'},{status:'cancelled'})
 assert.equal(v.percent(task),50);assert.equal(v.taskStatus(task),'部分生成失败')
 assert.match(source,/:status="current.status==='complete'/)
})

test('pending report explains billing queue wait without changing success progress',t=>{
 const v=create(t),task={status:'pending',waiting_for:'billing',days:[{status:'pending',processed:0}]}
 assert.equal(v.taskStatus(task),'等待账单完成');assert.equal(v.percent(task),0)
 task.status='running';assert.equal(v.taskStatus(task),'正在生成')
})
test('continue uses the existing task id and does not submit a new generation',async t=>{
 const v=create(t);await flush()
 await v.retry({id:'original',status:'failed',days:[]})
 assert.deepEqual(v.calls,[{site:'one',id:'original'}]);assert.equal(v.opened.value,true);assert.equal(v.selected.value,'original')
})
test('old-site retry completion cannot reopen its task dialog on the new site',async t=>{
 const v=create(t);await flush();let finish
 v.dashboard.retryReportTask=()=>new Promise(resolve=>finish=resolve)
 const pending=v.retry({id:'old',status:'failed',days:[]})
 v.props.site='two';await flush();finish();await pending
 assert.equal(v.opened.value,false);assert.equal(v.submitting.value,false);assert.equal(v.messages.length,0)
})
test('switching sites during the refresh after retry cannot reopen old dialog',async t=>{
 const v=create(t);await flush();let finish
 v.dashboard.reportTasks=()=>new Promise(resolve=>finish=resolve)
 const pending=v.retry({id:'old',status:'failed',days:[]});await flush()
 v.dashboard.reportTasks=async()=>({items:[]});v.props.site='two';await flush()
 finish({items:[{id:'old',status:'running',days:[]}]});await pending
 assert.equal(v.opened.value,false);assert.deepEqual(v.tasks.value,[])
})
test('stale polling response cannot replace a newly resumed task with failed status',async t=>{
 const v=create(t);await flush();let finish
 v.dashboard.reportTasks=()=>new Promise(resolve=>finish=resolve)
 const old=v.refresh()
 v.dashboard.reportTasks=async()=>({items:[{id:'original',status:'running',days:[]}]})
 await v.retry({id:'original',status:'failed',days:[]})
 finish({items:[{id:'original',status:'failed',days:[]}]});await old
 assert.equal(v.tasks.value[0].status,'running')
})
