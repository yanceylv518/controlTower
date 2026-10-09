<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError } from '@ct/shared'
import { client } from '../api'
import { can } from '../permissions'
import { useAuthStore } from '../stores/auth'
import RequestMonitorRules from '../components/RequestMonitorRules.vue'
import AppShell from '../components/AppShell.vue'
import TrendChart from '../components/TrendChart.vue'
import { bins, monitorWindow, largePercent, monitorError, channelReason, channelSeries, measureText, type RequestSnapshot, type ChannelSnapshot } from '../utils/requestMonitor'

const auth = useAuthStore()
const rulesOpen=ref(false)
const snapshot = ref<RequestSnapshot>(), channels = ref<ChannelSnapshot>()
const minutes = ref(30), selectedTime = ref<number|null>(null), sort = ref('volume')
const loading = ref(false), channelsLoading = ref(false), error = ref(''), channelError = ref('')
const auto = ref(true), clock = ref(Date.now())
let timer: ReturnType<typeof setInterval> | undefined, alive = true, channelSequence = 0
let albAbort: AbortController | undefined, channelAbort: AbortController | undefined
const windowData = computed(()=>monitorWindow(snapshot.value,minutes.value,null))
const points = computed(()=>windowData.value.points)
// Keep internal missing minutes visible, but never let the picker enter the
// trailing minutes that have no displayable data.
const availablePoints = computed(()=> {
 const last=points.value.map(p=>p.value!==null).lastIndexOf(true)
 return points.value.slice(0,last+1)
})
const latestAvailable = computed(()=>availablePoints.value.at(-1)?.time)
const index = computed({
 get:()=> { const found=availablePoints.value.findIndex(p=>p.time===selectedTime.value);return found>=0?found:Math.max(0,availablePoints.value.length-1) },
 set:(v:number)=>{
  const last=availablePoints.value.length-1
  const next=Math.max(0,Math.min(v,last))
  // The right endpoint follows new data; selecting history pins that minute.
  selectedTime.value=next===last?null:availablePoints.value[next]?.time??null
 },
})
const current = computed(()=>availablePoints.value[index.value])
const row = computed(()=>current.value?.value)
const percent = computed(()=>largePercent(row.value))
const sizeSeries = computed(()=>bins.map(bin=>({
 name:bin.name,color:bin.color,unit:' 次/分钟',smooth:false,
 data:points.value.map(p=>[new Date(p.time*1000).toISOString(),p.value?.[bin.key]??null] as [string,number|null]),
})))
const flowSeries = computed(()=>[
 { key:'request_bytes' as const,name:'请求字节量',color:'#3b82f6' },
 { key:'response_bytes' as const,name:'响应体字节量',color:'#10b981' },
].map(s=>({name:s.name,color:s.color,unit:' GiB/分钟',smooth:false,data:points.value.map(p=>[new Date(p.time*1000).toISOString(),p.value ? Number((p.value[s.key]/1073741824).toFixed(4)) : null] as [string,number|null])})))
const outdated = computed(()=>snapshot.value?.queried_at ? clock.value/1000-snapshot.value.queried_at>90 : false)
const channelsOutdated = computed(()=>channels.value?.queried_at ? clock.value/1000-channels.value.queried_at>90 : false)
const status = computed(()=>error.value?'查询失败':outdated.value?'数据已过期':({success:'查询成功',delayed:'日志时间落后',no_data:'窗口内暂无日志',unconfigured:'尚未接入',failed:'查询失败'} as Record<string,string>)[snapshot.value?.status||'']||'等待查询')
const channelCeiling = computed(()=>Math.max(40,...(channels.value?.items.flatMap(c=>[...c.ttft.trend,...c.duration.trend].filter((n): n is number=>n!==null))||[])))
function selectMinute(value:number) {const found=points.value.findIndex(p=>p.time===Math.floor(value/60000)*60);if(found>=0)index.value=found}
function time(value?:number,full=false) {return value?new Date(value*1000).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',...(full?{}:{hour:'2-digit',minute:'2-digit'})}):'—'}
function num(value?:number|null) {return value==null?'—':value.toLocaleString('zh-CN',{maximumFractionDigits:1})}
function bytes(value?:number) {return value==null?'—':(value/1073741824).toFixed(3)+' GiB'}

async function loadALB() {
 if(loading.value || !alive)return
 loading.value=true;error.value='';albAbort=new AbortController()
 try {
  const data=await client.request<RequestSnapshot>('/api/dashboard/request-monitor',{signal:albAbort.signal})
  if(!alive)return
  snapshot.value=data
  if(data.status==='failed')error.value=monitorError(data.code)
 } catch(e) {if(alive)error.value=e instanceof ApiError&&e.status===403?'没有请求监控权限。':e instanceof ApiError?monitorError(e.code):'无法连接 CT，当前保留上次查询结果。'}
 finally {if(alive)loading.value=false}
}
async function loadChannels() {
 const sequence=++channelSequence
 channelAbort?.abort();channelAbort=new AbortController()
 channelsLoading.value=true;channelError.value=''
 try {
  const data=await client.request<ChannelSnapshot>('/api/dashboard/request-monitor/channels?sort='+sort.value,{signal:channelAbort.signal})
  if(alive&&sequence===channelSequence)channels.value=data
 } catch(e) {if(alive&&sequence===channelSequence)channelError.value=e instanceof ApiError&&e.code==='channel_site_unconfigured'?'请在“判断规则”中选择 ALB 对应站点。':e instanceof ApiError&&e.code==='channel_metrics_limit'?'渠道指标超出查询上限，本次不展示截断排名。':'渠道指标查询失败，请检查 Server 或稍后重试。'}
 finally {if(alive&&sequence===channelSequence)channelsLoading.value=false}
}
function refresh() {void loadALB();if(!channelsLoading.value)void loadChannels()}
function visible() {if(auto.value&&document.visibilityState==='visible')refresh()}
watch(sort,()=>{channels.value=undefined;channelSequence++;channelAbort?.abort();void loadChannels()})
watch(minutes,()=>{selectedTime.value=null})
onMounted(()=>{refresh();timer=setInterval(()=>{clock.value=Date.now();visible()},30000);document.addEventListener('visibilitychange',visible)})
onUnmounted(()=>{alive=false;channelSequence++;clearInterval(timer);albAbort?.abort();channelAbort?.abort();document.removeEventListener('visibilitychange',visible)})
</script>

<template>
 <AppShell title="请求监控">
  <div class="request-monitor">
   <header class="monitor-heading">
    <div><h2>请求监控</h2><p>ALB 请求趋势与对应站点重点渠道</p></div>
    <div class="controls"><el-switch v-model="auto" active-text="30 秒刷新" /><el-button :loading="loading || channelsLoading" @click="refresh">刷新</el-button><router-link v-if="can(auth.user,'settings.manage')" to="/settings?tab=external"><el-button>ALB 接入配置</el-button></router-link></div>
   </header>
   <div class="source-line"><el-tag :type="snapshot?.status==='success'&&!outdated?'success':'warning'">{{status}}</el-tag><span>最近查询 {{time(snapshot?.queried_at,true)}}</span></div>
   <el-alert v-if="error" :title="error" type="error" :closable="false"/>
   <el-alert v-if="outdated" title="数据已过期，请刷新。" type="warning" :closable="false"/>
   <el-alert v-if="snapshot?.status==='delayed'" title="ALB 日志延迟超过 3 分钟。" type="warning" :closable="false"/>
   <section class="monitor-panel">
    <div class="panel-heading"><div><h3>请求大小趋势</h3><p>每分钟请求数 · 大小包含请求行、请求头及请求体</p></div><div class="controls">
     <el-select v-model="minutes" style="width:135px" aria-label="时间范围"><el-option v-for="n in [15,30,60]" :key="n" :label="'最近 '+n+' 分钟'" :value="n"/></el-select>
    </div></div>
    <el-empty v-if="!snapshot || snapshot.status==='unconfigured'" :description="loading?'正在查询…':'请先在系统设置中配置 ALB 访问日志接入'"/>
    <el-empty v-else-if="snapshot.status==='failed'" description="本次查询失败，未展示部分统计"/>
    <el-empty v-else-if="snapshot.status==='no_data'" description="窗口内无匹配日志，请核对 ALB ID、投递状态和时间范围"/>
    <template v-else>
     <div class="minute-summary">
      <div><span>所选分钟（北京时间）</span><b>{{time(current?.time)}}</b></div>
      <div><span>已结束请求</span><b>{{num(row?.count)}}</b></div>
      <div><span>≥5 MiB</span><b class="orange">{{row?num(row.medium+row.large+row.huge):'—'}}</b></div>
      <div><span>≥20 MiB</span><b class="red">{{num(row?.huge)}}</b></div>
      <div><span>大请求占比</span><b>{{percent===null?'—':percent.toFixed(1)+'%'}}</b></div>
     </div>
     <TrendChart title="大请求 · 次/分钟" :series="sizeSeries.slice(1)" :y-min="0" @select-minute="selectMinute" />
     <div class="small-trend"><TrendChart title="小于 5 MiB · 次/分钟" :series="sizeSeries.slice(0,1)" :y-min="0" @select-minute="selectMinute" /></div>
     <label class="minute-picker">查看分钟 <input v-model.number="index" type="range" min="0" :max="Math.max(0,availablePoints.length-1)" :disabled="availablePoints.length<2" aria-label="查看分钟"><span>{{time(current?.time)}}</span><el-button link :disabled="!availablePoints.length" @click="selectedTime=null">回到最新</el-button></label>
     <div class="bin-counts"><span v-for="bin in bins" :key="bin.key"><i :style="{background:bin.color}"/>{{bin.name}} <b>{{num(row?.[bin.key])}}</b></span><span>大小未知 <b>{{num(row?.unknown)}}</b></span></div>
     <p v-if="!row" class="warning-text">{{availablePoints.length?'所选分钟尚无可展示日志':'当前时间范围暂无可展示日志'}}。</p>
     <p class="muted">数据更新至 {{time(latestAvailable)}}<span v-if="latestAvailable"> 分钟</span>。</p>
    </template>
   </section>

   <section class="monitor-panel">
    <div class="panel-heading"><div><h3>重点关注渠道 <el-tag size="small">最多 5 条</el-tag></h3><p>ALB 对应站点 {{channels?.site||'未配置'}} · 近 {{channels?.rules.window_minutes??'—'}} 分钟 · 任一指标达到阈值</p></div><div class="controls"><el-radio-group v-model="sort" size="small"><el-radio-button value="volume">请求量优先</el-radio-button><el-radio-button value="latency">异常程度优先</el-radio-button></el-radio-group><el-button size="small" @click="rulesOpen=true">判断规则</el-button></div></div>
    <el-alert v-if="channelError || channelsOutdated" :title="channelError || '渠道查询结果已过期，请刷新。'" type="warning" :closable="false"/>
    <div v-if="channels?.items.length" class="channel-grid">
     <article v-for="channel in channels.items" :key="channel.instance_id+channel.key" class="channel-card">
      <h4>{{channel.name||channel.key}}</h4><p>{{channel.instance_name||channel.instance_id}}</p>
      <div class="reason-tags"><el-tag v-for="reason in channel.reasons" :key="reason" type="danger" size="small">{{channelReason[reason]}}</el-tag><el-tag v-if="channel.partial||channel.unknown.length" type="warning" size="small">部分数据</el-tag></div>
      <div class="channel-values">
       <div><span>首响应 P95</span><b :class="{red:channel.reasons.includes('ttft')}">{{measureText(channel.ttft)}}</b></div>
       <div><span>总耗时 P95</span><b :class="{red:channel.reasons.includes('duration')}">{{measureText(channel.duration)}}</b></div>
       <div><span>错误率</span><b :class="{red:channel.reasons.includes('error_rate')}">{{measureText(channel.errors,'%')}}</b></div>
       <div><span>窗口请求</span><b>{{num(channel.count)}}</b></div>
      </div>
      <TrendChart title="延迟 P95 · 秒" :y-min="0" :y-max="channelCeiling" :series="channelSeries(channel,channels!.from)" />
      <TrendChart title="错误率 · %" :y-min="0" :percent="true" :series="channelSeries(channel,channels!.from,true)" />
      <p v-if="channel.partial||channel.unknown.length">有效样本：首响应 {{num(channel.ttft.samples)}} · 总耗时 {{num(channel.duration.samples)}} · 错误率 {{num(channel.errors.samples)}}</p>
      <p>更新至 {{time(channel.latest)}}</p>
     </article>
    </div>
    <el-empty v-else :description="channelsLoading?'正在加载渠道…':channelError?'渠道数据暂不可用':channels?.latest?'ALB 对应站点暂无达到阈值的渠道':'当前窗口暂无渠道指标'"/>
    <details v-if="channels?.pending_count" class="pending-channels"><summary>数据待补充 {{channels.pending_count}} 条</summary><p v-for="item in channels.pending" :key="item.instance_id+item.key">{{item.name||item.key}} · {{num(item.count)}} 次请求 · {{item.unknown.map(k=>({ttft:'首响应',duration:'总耗时',error_rate:'错误率'})[k]).join('、')}}暂不能判断</p><p v-if="channels.pending_count>10">显示请求量最多的 10 条</p></details>
   </section>

   <section v-if="snapshot && ['success','delayed'].includes(snapshot.status)" class="monitor-panel">
    <h3>请求与响应字节趋势</h3>
    <p class="muted">{{time(current?.time)}} · 请求 {{bytes(row?.request_bytes)}} / 响应体 {{bytes(row?.response_bytes)}}</p>
    <TrendChart title="GiB / 分钟" :series="flowSeries" :y-min="0" @select-minute="selectMinute"/>
    <p v-if="row?.response_unknown" class="warning-text">所选分钟有 {{num(row.response_unknown)}} 次请求的响应大小未知。</p>
   </section>
  </div>
  <RequestMonitorRules v-if="rulesOpen" :editable="can(auth.user,'settings.manage')" @close="rulesOpen=false" @saved="channels=undefined;loadChannels()"/>
 </AppShell>
</template>

<style scoped>
:global(body:has(.request-monitor)){min-width:0}
.request-monitor{display:grid;gap:18px;min-width:0;color:var(--el-text-color-primary)}
.monitor-heading,.panel-heading{display:flex;align-items:center;justify-content:space-between;gap:16px;flex-wrap:wrap}
h2,h3,h4,p{margin:0}h2{font-size:23px}h3{font-size:16px}h4{font-size:16px;overflow-wrap:anywhere}
.monitor-heading p,.panel-heading p,.muted,.channel-card p{color:var(--el-text-color-secondary);font-size:12px;line-height:1.8;margin-top:6px}
.controls,.source-line,.bin-counts{display:flex;gap:12px;align-items:center;flex-wrap:wrap}.source-line{font-size:12px;color:var(--el-text-color-secondary)}
.monitor-panel{background:var(--el-bg-color);border:1px solid var(--el-border-color-lighter);border-radius:12px;padding:20px;min-width:0}
.minute-summary{display:grid;grid-template-columns:repeat(5,1fr);gap:14px;padding:24px 0 12px}
.minute-summary span,.channel-values span{display:block;font-size:12px;color:var(--el-text-color-secondary)}
.minute-summary b{display:block;font-size:25px;margin-top:5px;font-variant-numeric:tabular-nums}
.orange{color:#d97706}.red{color:#ef4444}
.small-trend :deep(.trend-chart-canvas){height:140px}
.minute-picker{display:flex;align-items:center;gap:12px;font-size:12px;margin:12px 0}.minute-picker input{flex:1;min-width:50px}
.bin-counts{font-size:12px}.bin-counts i{display:inline-block;width:8px;height:8px;border-radius:2px;margin-right:5px}.bin-counts b{margin-left:6px}
.channel-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(250px,1fr));gap:14px;margin-top:14px}
.channel-card{padding:16px;border:1px solid var(--el-border-color-lighter);border-radius:9px;min-width:0}
.channel-values{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px;margin:16px 0}.channel-values b{font-size:22px}.channel-values small{font-size:12px;font-weight:400}
.channel-card :deep(.trend-chart-canvas){height:140px}.channel-card :deep(.trend-chart){padding:0;border:0;box-shadow:none}
.reason-tags{display:flex;gap:6px;flex-wrap:wrap;margin-top:10px}.pending-channels{font-size:12px;margin-top:14px;color:var(--el-color-warning)}.pending-channels summary{cursor:pointer}.pending-channels p{margin-top:8px}
.warning-text{color:var(--el-color-warning);font-size:12px;margin-top:10px}
@media(max-width:600px){.monitor-panel{padding:14px}.minute-summary{grid-template-columns:repeat(2,1fr)}.minute-summary>div:first-child{grid-column:1/-1}.controls{width:100%}.channel-grid{grid-template-columns:1fr}.minute-picker{flex-wrap:wrap}}
</style>
