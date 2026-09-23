import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { ref, shallowRef, computed } from 'vue'
import ts from 'typescript'

// 执行页面实际查询逻辑与实际异步状态管理器，通过受控 Promise 验证竞态而非依赖时间等待。
const source = readFileSync(new URL('../src/views/ReadonlyLogsView.vue', import.meta.url), 'utf8')
const asyncSource = readFileSync(new URL('../src/composables/useAsyncData.ts', import.meta.url), 'utf8').replace(/^import .*$/gm, '').replace('export function', 'function')
const compile = text => ts.transpileModule(text, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const useAsyncData = new Function('ref','shallowRef','billingReadErrorMessage',compile(asyncSource)+';return useAsyncData')(ref,shallowRef,String)
const range = (start,end) => source.slice(source.indexOf(start),source.indexOf(end,source.indexOf(start)))
const code = range('const parsedChannelID =','// 首屏加载实例') + range('function commitQuery()', 'const pageSizeOptions =') + range('const changePage =','function closePageSizeMenu()')
const deferred = () => {let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
function setup() {
  const calls={logs:[],logStat:[],logCount:[]}, messages=[]
  const passthrough=Object.fromEntries(Object.keys(calls).map(name=>[name,(params,signal)=>{const d=deferred();calls[name].push({...d,params,signal});return d.promise}]))
  const deps={ref,shallowRef,computed,useAsyncData,useAppendPages:()=>({}),passthrough,filters:{site_id:'a'},auth:{user:{role:'admin'}},scopedUserIDs:ref(undefined),selectedUserID:ref(undefined),timeRange:ref([new Date('2026-09-01'),new Date('2026-09-02')]),logType:ref(0),limit:ref(100),offset:ref(0),closeRequestChain(){},ElMessage:{warning:m=>messages.push(m),error:m=>messages.push(m)},pageSizeOptions:[10,20,50,100]}
  for(const key of ['channelID','username','tokenName','modelName','group','requestID','upstreamRequestID','statusCode'])deps[key]=ref('')
  deps.emptyOutput=ref(false)
  deps.fallbackFinalOnly=ref(false)
  const state=new Function(...Object.keys(deps),compile(code)+';return {state,statState,countState,reloadAll,refreshSearch,reloadPage,changePage,changePageSize,submitted,listIsCurrent,countIsCurrent,backgroundRefreshing,tableScroll} ')(...Object.values(deps))
  return {...deps,...state,calls,messages}
}
const response={items:[{id:1}],configured:true,total:1000,has_more:true}

test('pagination uses submitted filters and slow statistics do not block the list',async()=>{
  const h=setup();h.modelName.value='first';const refresh=h.refreshSearch()
  h.calls.logs[0].resolve(response);await refresh
  assert.equal(h.backgroundRefreshing.value,false)
  assert.equal(h.statState.loading.value,true)
  h.modelName.value='draft';h.changePage(2)
  assert.equal(h.calls.logs[1].params.model_name,'first')
  assert.equal(h.calls.logs[1].params.offset,100)
  h.calls.logs[1].resolve(response)
})

test('failed and superseded counts cannot appear as the current query total',async()=>{
  const h=setup();let job=h.refreshSearch();h.calls.logs[0].resolve(response);await job;h.calls.logCount[0].resolve({total:1000});await new Promise(resolve=>setImmediate(resolve))
  assert.equal(h.countIsCurrent.value,true)
  h.modelName.value='new';job=h.refreshSearch();assert.equal(h.countIsCurrent.value,false)
  h.calls.logs[1].resolve(response);await job;h.calls.logCount[1].reject(new Error('count failed'));await new Promise(resolve=>setImmediate(resolve))
  assert.equal(h.countIsCurrent.value,false)
  assert.ok(h.countState.error.value)
  h.calls.logStat[0].resolve({summary:{quota:999}});await Promise.resolve();await Promise.resolve()
  assert.equal(h.statState.data.value,undefined)
})

test('invalid channel IDs never issue a query or silently remove the filter',async()=>{
  for(const value of ['abc','-1','1.2','1e3','9007199254740992']){
    const h=setup();h.channelID.value=value;await h.refreshSearch();assert.equal(h.calls.logs.length,0);assert.equal(h.messages.length,1)
  }
})

test('status code and empty output filters are submitted with every query batch',async()=>{
  const h=setup();h.statusCode.value='429';h.emptyOutput.value=true;const job=h.refreshSearch()
  assert.equal(h.calls.logs[0].params.status_code,429)
  assert.equal(h.calls.logs[0].params.empty_output,1)
  h.calls.logs[0].resolve(response);await job
  assert.equal(h.calls.logStat[0].params.status_code,429)
  assert.equal(h.calls.logStat[0].params.empty_output,1)
  assert.equal(h.calls.logCount[0].params.status_code,429)
  assert.equal(h.calls.logCount[0].params.empty_output,1)
})

test('admin fallback final-only filter is submitted with every query batch',async()=>{
  const h=setup();h.fallbackFinalOnly.value=true;const job=h.refreshSearch()
  assert.equal(h.calls.logs[0].params.fallback_final_only,1)
  h.calls.logs[0].resolve(response);await job
  assert.equal(h.calls.logStat[0].params.fallback_final_only,1)
  assert.equal(h.calls.logCount[0].params.fallback_final_only,1)
})

test('failed searches preserve labelled old rows and prevent pagination under new conditions',async()=>{
  const h=setup();let job=h.refreshSearch();h.calls.logs[0].resolve(response);await job
  h.modelName.value='new';job=h.refreshSearch();h.calls.logs[1].reject(new Error('failed'));await job
  assert.equal(h.listIsCurrent.value,false);assert.equal(h.state.data.value.items[0].id,1)
  h.changePage(2);assert.equal(h.calls.logs.length,2)
})

test('pagination failures restore the shown page and successful pages reset only vertical scroll',async()=>{
  const h=setup();const job=h.refreshSearch();h.calls.logs[0].resolve(response);await job
  h.tableScroll.value={scrollTop:500,scrollLeft:150}
  h.changePage(2);h.calls.logs[1].reject(new Error('failed'));await new Promise(resolve=>setImmediate(resolve))
  assert.equal(h.offset.value,0);assert.equal(h.tableScroll.value.scrollTop,500)
  h.changePage(2);h.calls.logs[2].resolve(response);await new Promise(resolve=>setImmediate(resolve))
  assert.equal(h.offset.value,100);assert.equal(h.tableScroll.value.scrollTop,0);assert.equal(h.tableScroll.value.scrollLeft,150)
})


test('list is issued before statistics; new searches cancel old statistics immediately',async()=>{
  const h=setup();let job=h.refreshSearch()
  assert.equal(h.calls.logs.length,1);assert.equal(h.calls.logStat.length,0);assert.equal(h.calls.logCount.length,0)
  h.calls.logs[0].resolve(response);await job
  assert.equal(h.calls.logStat.length,1);assert.equal(h.calls.logCount.length,1)
  job=h.refreshSearch()
  assert.equal(h.calls.logStat[0].signal.aborted,true);assert.equal(h.calls.logCount[0].signal.aborted,true)
  assert.equal(h.calls.logCount.length,1)
  h.calls.logs[1].resolve(response);await job
})

test('adjacent pages use server cursors, jumps and page-size changes use offsets',async()=>{
  const h=setup();const job=h.refreshSearch()
  h.calls.logs[0].resolve({...response,next_cursor:'next-1'});await job
  h.changePage(2);assert.equal(h.calls.logs[1].params.cursor,'next-1')
  h.calls.logs[1].resolve({...response,next_cursor:'next-2',previous_cursor:'prev-2'})
  await new Promise(resolve=>setImmediate(resolve))
  h.changePage(1);assert.equal(h.calls.logs[2].params.cursor,'prev-2')
  h.calls.logs[2].resolve({...response,next_cursor:'next-1'});await new Promise(resolve=>setImmediate(resolve))
  h.changePage(8);assert.equal(h.calls.logs[3].params.cursor,undefined);assert.equal(h.calls.logs[3].params.offset,700)
  h.calls.logs[3].resolve(response);await new Promise(resolve=>setImmediate(resolve))
  h.changePageSize(20);assert.equal(h.calls.logs[4].params.cursor,undefined);assert.equal(h.calls.logs[4].params.offset,0)
})


test('failed cursor navigation restores the complete request used by the displayed page',async()=>{
  const h=setup();const first=h.refreshSearch()
  h.calls.logs[0].resolve({...response,next_cursor:'next-1'});await first
  h.changePage(2);h.calls.logs[1].reject(new Error('temporary failure'));await new Promise(setImmediate)
  let retry=h.reloadPage()
  assert.equal(h.calls.logs[2].params.offset,0);assert.equal(h.calls.logs[2].params.cursor,undefined)
  h.calls.logs[2].resolve({...response,next_cursor:'next-1'});await retry
  h.changePage(2);h.calls.logs[3].resolve({...response,next_cursor:'next-2',previous_cursor:'prev-2'});await new Promise(setImmediate)
  h.changePage(3);h.calls.logs[4].reject(new Error('temporary failure'));await new Promise(setImmediate)
  retry=h.reloadPage()
  assert.equal(h.calls.logs[5].params.offset,100);assert.equal(h.calls.logs[5].params.cursor,'next-1')
  h.calls.logs[5].resolve({...response,next_cursor:'next-2',previous_cursor:'prev-2'});await retry
  h.changePageSize(20);h.calls.logs[6].reject(new Error('size failed'));await new Promise(setImmediate)
  retry=h.reloadPage()
  assert.equal(h.calls.logs[7].params.offset,100);assert.equal(h.calls.logs[7].params.limit,100);assert.equal(h.calls.logs[7].params.cursor,'next-1')
  h.calls.logs[7].resolve(response);await retry
  assert.doesNotMatch(source, /@click="reloadPage">重新加载/)
})

test('pagination cancels pending summaries and resumes only after the list completes',async()=>{
  const h=setup();const first=h.refreshSearch();h.calls.logs[0].resolve(response);await first
  h.changePage(2)
  assert.equal(h.calls.logStat[0].signal.aborted,true);assert.equal(h.calls.logCount[0].signal.aborted,true)
  assert.equal(h.calls.logStat.length,1);assert.equal(h.calls.logCount.length,1)
  h.calls.logStat[0].resolve({summary:{quota:999}});h.calls.logCount[0].resolve({total:999});await new Promise(setImmediate)
  assert.equal(h.statState.data.value,undefined);assert.equal(h.countState.data.value,undefined)
  h.calls.logs[1].resolve(response);await new Promise(setImmediate)
  assert.equal(h.calls.logStat.length,2);assert.equal(h.calls.logCount.length,2)
})

test('stale pagination cannot resume statistics or roll back a new search',async()=>{
  const h=setup();const first=h.refreshSearch();h.calls.logs[0].resolve(response);await first
  h.changePage(2);h.modelName.value='new';const next=h.refreshSearch()
  h.calls.logs[1].reject(new Error('old failure'));await new Promise(setImmediate)
  assert.equal(h.calls.logStat.length,1);assert.equal(h.calls.logCount.length,1)
  h.calls.logs[2].resolve(response);await next
  assert.equal(h.calls.logStat.length,2);assert.equal(h.calls.logStat[1].params.model_name,'new')
  assert.equal(h.offset.value,0)
})

test('query failures show one floating message per attempt, including initial load and retries', async () => {
  const h = setup()
  let job = h.reloadAll()
  h.calls.logs[0].reject(new Error('list failed')); await job
  h.calls.logStat[0].reject(new Error('stat failed'))
  h.calls.logCount[0].reject(new Error('count failed'))
  await new Promise(setImmediate)
  assert.deepEqual(h.messages, ['查询失败，请重试'])
  job = h.refreshSearch()
  h.calls.logs[1].resolve(response); await job
  h.calls.logStat[1].reject(new Error('stat failed again'))
  h.calls.logCount[1].reject(new Error('count failed again'))
  await new Promise(setImmediate)
  assert.deepEqual(h.messages, ['查询失败，请重试', '查询失败，请重试'])
  assert.equal(h.state.data.value.items[0].id, 1)
  h.changePage(2)
  h.calls.logs[2].reject(new Error('page failed'))
  await new Promise(setImmediate)
  assert.equal(h.messages.length, 3)
  assert.equal(h.offset.value, 0)
})

test('superseded summary failures do not produce error messages for a new query', async () => {
  const h = setup()
  let job = h.refreshSearch()
  h.calls.logs[0].resolve(response); await job
  job = h.refreshSearch()
  h.calls.logStat[0].reject(new Error('obsolete stat'))
  h.calls.logCount[0].reject(new Error('obsolete count'))
  h.calls.logs[1].resolve(response); await job
  h.calls.logStat[1].resolve({summary:{quota:0}})
  h.calls.logCount[1].resolve({total:1})
  await new Promise(setImmediate)
  assert.deepEqual(h.messages, [])
})
