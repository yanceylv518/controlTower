<script setup lang="ts">
import {computed,ref,watch,onUnmounted} from 'vue'
import {Document,Coin,WarningFilled} from '@element-plus/icons-vue'
import {client} from '../api'
import {overviewCache} from '../utils/archiveOverviewCache'
import {useAuthStore} from '../stores/auth'
import {beijingDate} from '../utils/logArchive'
import {archiveReadError} from '../utils/archiveReadError'
import {totals,groupStats,csvCell,type StatRow} from '../utils/archiveAnalysis'
import {useArchiveCurrency} from '../composables/useArchiveCurrency'
import {quotaAmount,currencyUnit} from '../utils/archiveMoney'
import ArchiveDailyChart from './ArchiveDailyChart.vue'
import ArchiveAnomalyAnalysis from './ArchiveAnomalyAnalysis.vue'
const props=defineProps<{siteId:string;active?:boolean}>()
const auth=useAuthStore(),month=ref(beijingDate().slice(0,7)),user=ref(''),model=ref(''),channel=ref(''),dimension=ref('model_name')
const range=ref<[string,string]>([month.value+'-01',`${month.value}-${new Date(Number(month.value.slice(0,4)),Number(month.value.slice(5)),0).getDate()}`]),preset=ref('month')
function quick(value:string){preset.value=value;const today=beijingDate();if(value==='month'){month.value=today.slice(0,7);range.value=[month.value+'-01',`${month.value}-${new Date(Number(month.value.slice(0,4)),Number(month.value.slice(5)),0).getDate()}`]}else if(value==='recent'){const from=new Date(today+'T12:00:00Z');from.setUTCDate(from.getUTCDate()-29);range.value=[from.toISOString().slice(0,10),today]}}
const selected=ref(''),detail=ref(false),view=ref('trend'),metric=ref('requests'),busy=ref(false),error=ref('')
type Day={date:string;state:string;ready:boolean;version:string;error:string;updated_at:string}
type Overview={days:Day[];rows:StatRow[];observed_at:string;options?:Record<string,Record<string,boolean>>;option_names?:Record<string,Record<string,string>>}
const data=ref<Overview>(),cache=overviewCache<Overview>(auth.user)
const loadedScope=ref(''),loadedQuery=ref('')
let sequence=0,debounce:ReturnType<typeof setTimeout>|undefined
const {money,moneyError,refreshMoney}=useArchiveCurrency(()=>props.siteId)
const amount=(quota:bigint)=>quotaAmount(quota,money.value)
const moneyLabel=computed(()=>money.value?.type==='TOKENS'?'额度':`金额 ${currencyUnit(money.value)}`)
const query=computed(()=>new URLSearchParams({site_id:props.siteId,date:month.value,from:range.value?.[0]??'',through:range.value?.[1]??'',user_id:user.value??'',model:model.value??'',channel_id:channel.value??'',dimension:dimension.value,limit:'200'}).toString())
const scope=computed(()=>{const params=new URLSearchParams(query.value);params.delete('dimension');return params.toString()})
const dimensionPending=computed(()=>!!data.value&&loadedQuery.value!==query.value)
const hasStats=computed(()=>!!data.value&&(data.value.rows.length>0||data.value.days.some(d=>d.ready)))
const rows=computed(()=>data.value?.rows??[]),total=computed(()=>totals(rows.value))
const scoped=computed(()=>selected.value?rows.value.filter(r=>r.date===selected.value):rows.value)
const groups=computed(()=>dimensionPending.value?[]:groupStats(scoped.value,dimension.value))
const days=computed(()=>data.value?.days??[]),complete=computed(()=>days.value.filter(d=>d.ready&&!d.error).length)
const daily=computed(()=>new Map(days.value.map(d=>[d.date,totals(rows.value.filter(r=>r.date===d.date))])))
function anomalies(items:StatRow[],key:string){let covered=0n,count=0n,all=0n;for(const r of items){all+=BigInt(String(r.amounts.log_rows??0));covered+=BigInt(String(r.amounts.anomaly_rows??0));count+=BigInt(String(r.amounts[key]??0))}return hasStats.value&&covered===all?count.toLocaleString():'—'}
const points=computed(()=>days.value.map(d=>{const t=daily.value.get(d.date),items=rows.value.filter(r=>r.date===d.date);let value:number|null=null
 if(d.version&&t&&(d.ready||items.length)) {if(metric.value==='quota')value=money.value&&t.quota_missing===0n?Number(amount(t.quota)):null;else if(metric.value==='tokens')value=t.prompt_tokens_missing+t.completion_tokens_missing===0n?Number(t.prompt_tokens+t.completion_tokens):null;else if(metric.value==='anomalies'){const values=['empty_output','missing_output','error_logs'].map(k=>anomalies(items,k));value=values.includes('—')?null:values.reduce((sum,v)=>sum+Number(v.replaceAll(',','')),0)}else value=Number(t.requests)}
 return {date:d.date,value:value!==null&&Number.isFinite(value)?value:null,sealed:d.state==='sealed',partial:!d.ready}}))
const selectedDay=computed(()=>days.value.find(d=>d.date===selected.value))
function options(key:string){return Object.keys(data.value?.options?.[key]??{})}
function optionLabel(key:string,id:string){const name=data.value?.option_names?.[key]?.[id];return name?`${name}（ID ${id}）`:`${key==='user_id'?'用户':'渠道'} ${id}`}
function groupEmpty(name:string){const items=scoped.value.filter(r=>String(r.dimensions[dimension.value]??(dimension.value==='channel_id'?r.dimensions.channel:undefined)??'未知')===name);const count=anomalies(items,'empty_output'),requests=totals(items).requests;if(count==='—'||requests===0n)return '—';return (Number(BigInt(count.replaceAll(',',''))*10000n/requests)/100).toFixed(2)+'%'}

function state(d:Day){return d.error?'统计失败':!d.version?'待统计':!d.ready?'统计中':d.state==='sealed'?'已校验':'待校验'}
function restoreData(){const saved=cache.get(query.value);if(saved){data.value=saved.data;loadedScope.value=scope.value;loadedQuery.value=query.value}else if(loadedScope.value!==scope.value){data.value=undefined;loadedQuery.value=''}return saved}
async function load(force=false){const ticket=++sequence,key=query.value;if(!props.siteId||!month.value)return
 const saved=restoreData();error.value='';busy.value=false
 if(saved&&!force&&Date.now()-saved.time<30000)return
 busy.value=true
 try{const result=await client.request<{items:Overview[]}>(`/api/dashboard/log-archive-read/overview?${key}`);if(ticket!==sequence||key!==query.value)return
 const next=result.items[0];if(!next)throw new Error('统计响应为空');totals(next.rows)
 data.value=next;loadedScope.value=scope.value;loadedQuery.value=key;cache.delete(key);cache.set(key,{data:next,time:Date.now()});if(cache.size>8)cache.delete(cache.keys().next().value!)
 }catch(e){if(ticket===sequence)error.value=archiveReadError(e)}finally{if(ticket===sequence)busy.value=false}}
watch(()=>props.siteId,()=>{void refreshMoney()},{immediate:true})
watch(query,()=>{sequence++;restoreData();error.value='';busy.value=false;if(debounce)clearTimeout(debounce);debounce=setTimeout(()=>void load(),350)},{immediate:true})
watch([month,range,user,model,channel,()=>props.siteId],()=>{selected.value='';detail.value=false})
watch(()=>auth.user,()=>{cache.clear();data.value=undefined;sequence++;if(auth.user)void load()})
watch(()=>props.active,active=>{if(active)void load()})
const timer=setInterval(()=>{if(props.active!==false&&!document.hidden&&!busy.value)void load(true)},60000)
onUnmounted(()=>{sequence++;clearInterval(timer);if(debounce)clearTimeout(debounce)})
function select(date:string){selected.value=selected.value===date?'':date;detail.value=false}
function exportCSV(){const text=[['日期范围','维度','请求次数','输入 Token','输出 Token','Quota',moneyLabel.value,'统计覆盖','数据版本','用户筛选','模型筛选','渠道筛选','金额缺失条数','输入缺失条数','输出缺失条数','缓存缺失条数','币种配置（查询时）'],...groups.value.map(g=>[selected.value||range.value.join(' ~ '),g.name,g.requests,g.prompt_tokens,g.completion_tokens,g.quota,amount(g.quota),`${complete.value}/${days.value.length}`,JSON.stringify(days.value.map(d=>({date:d.date,version:d.version,state:state(d),updated_at:d.updated_at}))),user.value,model.value,channel.value,g.quota_missing,g.prompt_tokens_missing,g.completion_tokens_missing,g.cache_tokens_missing,JSON.stringify(money.value??null)])].map(r=>r.map(csvCell).join(',')).join('\r\n');const url=URL.createObjectURL(new Blob(['\uFEFF'+text],{type:'text/csv;charset=utf-8'}));const a=document.createElement('a');a.href=url;a.download=`archive-${month.value}.csv`;a.click();URL.revokeObjectURL(url)}
</script>
<template><div class="archive-data">
 <h2>归档数据</h2>
 <section class="filter-panel"><div class="filters">
  <el-date-picker v-model="range" type="daterange" value-format="YYYY-MM-DD" range-separator="至" start-placeholder="开始日期" end-placeholder="结束日期" :clearable="false" @change="preset='custom'"/>
  <el-radio-group v-model="preset" @change="quick(String($event))"><el-radio-button value="month">本月</el-radio-button><el-radio-button value="recent">近30天</el-radio-button><el-radio-button value="custom">自定义</el-radio-button></el-radio-group>
  <label>用户<el-select v-model="user" filterable allow-create default-first-option clearable value-on-clear="" placeholder="全部"><el-option v-for="v in options('user_id')" :key="v" :label="optionLabel('user_id',v)" :value="v"/></el-select></label>
  <label>模型<el-select v-model="model" filterable allow-create default-first-option clearable value-on-clear="" placeholder="全部"><el-option v-for="v in options('model_name')" :key="v" :label="v" :value="v"/></el-select></label>
  <label>渠道<el-select v-model="channel" filterable allow-create default-first-option clearable value-on-clear="" placeholder="全部"><el-option v-for="v in options('channel_id')" :key="v" :label="optionLabel('channel_id',v)" :value="v"/></el-select></label>
  <el-button :loading="busy" @click="load(true)">刷新</el-button>
 </div><div class="caption"><span>更新至 {{data?new Date(data.observed_at).toLocaleTimeString('zh-CN',{hour12:false}):'—'}}<span v-if="busy"> · 更新中</span></span><span>已统计 {{complete}} / {{days.length}} 天</span><span class="verified-dot">已校验 {{days.filter(d=>d.state==='sealed').length}} 天</span><span class="pending-dot">待校验 {{days.filter(d=>d.state!=='sealed').length}} 天</span><span v-if="complete<days.length">部分统计</span></div></section>
 <el-alert v-if="error&&!dimensionPending" :title="error" type="error" :closable="false"/>
 <el-alert v-if="moneyError" title="币种配置暂不可用" type="warning" :closable="false"><el-button link @click="refreshMoney">重试</el-button></el-alert>
 <el-skeleton v-if="!data&&busy" :rows="7" animated/>
 <template v-if="data"><div class="kpis">
  <div v-for="(label,key) in {requests:'请求次数',prompt_tokens:'输入 Token',completion_tokens:'输出 Token'}" :key="key"><span><i :class="key"><el-icon><Document v-if="key==='requests'"/><Coin v-else/></el-icon></i>{{label}}</span><strong>{{hasStats?total[key].toLocaleString():'—'}}</strong><small v-if="total[`${key}_missing`]>0n">含缺失值</small></div>
  <div><span><i class="money">¥</i>{{moneyLabel}}</span><strong>{{hasStats?amount(total.quota):'—'}}</strong><small v-if="total.quota_missing>0n">部分金额缺失</small></div>
  <div><span><i><el-icon><Document/></el-icon></i>空输出请求</span><strong>{{anomalies(rows,'empty_output')}}</strong></div><div><span><i class="danger"><el-icon><WarningFilled/></el-icon></i>错误日志</span><strong>{{anomalies(rows,'error_logs')}}</strong></div>
 </div>
 <section class="daily-panel"><div class="section-head"><h3>每日数据</h3><el-radio-group v-model="view"><el-radio-button value="trend">趋势</el-radio-button><el-radio-button value="calendar">日历</el-radio-button></el-radio-group></div>
 <el-radio-group v-model="metric" size="small"><el-radio-button value="requests">请求</el-radio-button><el-radio-button value="tokens">Token</el-radio-button><el-radio-button value="quota">金额</el-radio-button><el-radio-button value="anomalies">异常</el-radio-button></el-radio-group>
 <ArchiveDailyChart v-if="view==='trend'" :points="points" :selected="selected" :unit="metric==='quota'?moneyLabel:metric==='tokens'?'Token':'次'" @select="select"/>
 <template v-else><div class="weekdays"><span v-for="d in ['一','二','三','四','五','六','日']" :key="d">周{{d}}</span></div><div class="day-grid"><button v-for="(d,index) in days" :key="d.date" :style="index===0?{gridColumnStart:(new Date(d.date+'T12:00:00Z').getUTCDay()+6)%7+1}:undefined" :class="{selected:selected===d.date,verified:d.state==='sealed'}" @click="select(d.date)"><span>{{d.date.slice(5).replace('-','/')}}<small>{{state(d)}}</small></span><strong>{{points[index]?.value===null?'—':points[index]?.value?.toLocaleString()}}</strong></button></div></template>
 <el-empty v-if="!days.length" description="所选日期暂无归档数据"/>
 <div class="selection-strip"><el-button v-if="selected" type="primary" plain size="small" @click="selected='';detail=false">当前明细：{{selected.slice(5).replace('-','/')}} ×</el-button><span v-else>当前明细：整个日期范围</span><span v-if="selectedDay" :class="selectedDay.state==='sealed'?'verified-dot':'pending-dot'">{{selected.slice(5).replace('-','/')}} {{state(selectedDay)}}</span><span>请求 <b>{{hasStats?totals(scoped).requests.toLocaleString():'—'}}</b></span><span>空输出 <b>{{anomalies(scoped,'empty_output')}}</b></span><span>错误日志 <b>{{anomalies(scoped,'error_logs')}}</b></span></div>
 </section>
 <div class="detail-grid"><section class="dimension-panel"><div class="section-head"><h3>多维统计</h3><el-button link :disabled="dimensionPending" @click="exportCSV">导出</el-button></div>
 <el-radio-group v-model="dimension" size="small"><el-radio-button value="model_name">模型</el-radio-button><el-radio-button value="user_id">用户</el-radio-button><el-radio-button value="channel_id">渠道</el-radio-button><el-radio-button value="group">分组</el-radio-button><el-radio-button value="token_id">令牌</el-radio-button></el-radio-group>
 <el-alert v-if="error&&dimensionPending" :title="error" type="error" :closable="false"><el-button link @click="load(true)">重试</el-button></el-alert><el-table v-loading="dimensionPending&&!error" :data="groups" max-height="380" size="small"><el-table-column type="expand" width="32"><template #default="{row}"><div class="token-detail">输入 {{row.prompt_tokens.toLocaleString()}} · 输出 {{row.completion_tokens.toLocaleString()}} · 缓存 {{row.cache_tokens.toLocaleString()}}<span v-if="row.prompt_tokens_missing+row.completion_tokens_missing+row.cache_tokens_missing>0n">（部分缺失）</span></div></template></el-table-column><el-table-column prop="name" label="维度" min-width="100" show-overflow-tooltip/><el-table-column label="请求次数" min-width="80"><template #default="{row}">{{row.requests.toLocaleString()}}</template></el-table-column><el-table-column :label="moneyLabel" min-width="100"><template #default="{row}">{{amount(row.quota)}}{{row.quota_missing>0n?' *':''}}</template></el-table-column><el-table-column label="空输出率" width="75"><template #default="{row}">{{groupEmpty(row.name)}}</template></el-table-column></el-table>
 </section><section class="request-panel"><div class="section-head"><h3>请求明细</h3><small>{{selected||'点击上方日期查看'}}</small></div><ArchiveAnomalyAnalysis v-if="selected" :key="selected+user+model+channel" :site-id="siteId" :initial-date="selected" :initial-user="user" :initial-model="model" :initial-channel="channel" embedded/><el-empty v-else :image-size="70" description="选择日期查看请求与异常记录"/></section></div>
 </template></div></template>
<style scoped>
.archive-data{min-width:0;--panel:var(--el-bg-color);--line:var(--el-border-color-lighter);display:flex;flex-direction:column;gap:14px}.archive-data h2{font-size:22px;margin:6px 0 0;letter-spacing:-.3px}.filter-panel,.daily-panel,.dimension-panel,.request-panel,.kpis>div{background:var(--panel);border:1px solid var(--line);border-radius:5px}.filter-panel{overflow:hidden}.filters{display:flex;align-items:center;gap:12px;flex-wrap:wrap;padding:14px}.filters :deep(.el-date-editor){flex:0 1 260px;width:260px}.filters>label{display:flex;align-items:center;gap:8px;font-size:13px;flex:1;min-width:110px}.filters .el-select{min-width:70px;flex:1}.caption{display:flex;align-items:center;gap:20px;flex-wrap:wrap;border-top:1px solid var(--line);padding:10px 14px;background:var(--el-fill-color-light);font-size:12px;color:var(--el-text-color-secondary)}.verified-dot:before,.pending-dot:before{content:'';display:inline-block;width:8px;height:8px;border-radius:50%;background:#20b987;margin-right:7px}.pending-dot:before{background:#ffb329}.kpis{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:10px}.kpis>div{padding:16px 14px;min-height:104px;box-sizing:border-box;display:flex;flex-direction:column;gap:10px}.kpis span{display:flex;align-items:center;gap:8px;font-size:12px;color:var(--el-text-color-regular)}.kpis span>i{width:28px;height:28px;display:inline-flex;align-items:center;justify-content:center;background:#edf4ff;color:#2875ef;border-radius:50%;font-style:normal;font-weight:700}.kpis .completion_tokens{color:#14a77c;background:#e7f8f1}.kpis .money{color:#d7910a;background:#fff6df}.kpis .danger{color:#dc525d;background:#ffeded}.kpis strong{font-size:clamp(17px,1.45vw,23px);font-weight:650;letter-spacing:-.5px;font-variant-numeric:tabular-nums;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.kpis small,small{font-size:12px;color:var(--el-text-color-secondary)}.daily-panel,.dimension-panel,.request-panel{padding:14px}.section-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:12px}h3{font-size:15px;margin:0}.selection-strip{display:flex;align-items:center;gap:24px;flex-wrap:wrap;margin-top:18px;border:1px solid var(--line);border-radius:4px;padding:10px 12px;font-size:12px;color:var(--el-text-color-secondary)}.selection-strip b{font-size:14px;color:var(--el-text-color-primary);margin-left:6px}.detail-grid{display:grid;grid-template-columns:minmax(0,42fr) minmax(0,58fr);gap:12px}.detail-grid>section{min-width:0}.request-panel :deep(.anomalies){padding:0;border:0;border-radius:0}.request-panel :deep(.filters){margin:0 0 12px;gap:8px}.request-panel :deep(.el-table){font-size:12px}.token-detail{padding:12px;color:var(--el-text-color-secondary);font-size:12px}.el-table{margin-top:12px}.day-grid,.weekdays{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:8px;margin-top:14px}.weekdays{font-size:12px;text-align:center;color:var(--el-text-color-secondary)}.day-grid button{background:var(--panel);border:1px solid var(--line);border-radius:5px;padding:12px;text-align:left;color:var(--el-text-color-primary);cursor:pointer}.day-grid button>span{display:flex;justify-content:space-between;gap:4px;font-size:12px}.day-grid strong{display:block;margin-top:14px;font-size:18px}.day-grid .verified small{color:var(--el-color-success)}.day-grid .selected{border-color:var(--el-color-primary);background:var(--el-color-primary-light-9)}@media(max-width:1200px){.kpis{grid-template-columns:repeat(3,minmax(0,1fr))}.detail-grid{grid-template-columns:1fr}}@media(max-width:650px){.kpis{grid-template-columns:repeat(2,minmax(0,1fr))}.kpis strong{font-size:20px}.filters>label{min-width:100%}.caption,.selection-strip{gap:12px}.day-grid button{padding:6px}.day-grid button>span{display:block}.day-grid small{display:block}.day-grid strong{font-size:12px}}
</style>
