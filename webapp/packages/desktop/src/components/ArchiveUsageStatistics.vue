<script setup lang="ts">
import {computed,ref,watch,onUnmounted} from 'vue'
import {archiveReadError} from '../utils/archiveReadError'
import {client} from '../api'
import TrendChart from './TrendChart.vue'
import ArchiveModelPrices from './ArchiveModelPrices.vue'
import {beijingDate} from '../utils/logArchive'
import {totals,groupStats,csvCell,type StatRow} from '../utils/archiveAnalysis'
import {useArchiveCurrency} from '../composables/useArchiveCurrency'
import {quotaAmount,currencyUnit,moneyContext} from '../utils/archiveMoney'
const props=defineProps<{siteId:string}>()
const {money,moneyError,moneyBusy,refreshMoney}=useArchiveCurrency(()=>props.siteId)
const unit=computed(()=>currencyUnit(money.value))
const moneyLabel=computed(()=>money.value?.type==='TOKENS'?'额度（Quota）':`金额 ${unit.value}`)
const amount=(quota:bigint)=>quotaAmount(quota,money.value)
const month=ref(beijingDate().slice(0,7)),user=ref(''),model=ref(''),channel=ref(''),dimension=ref('model_name'),metric=ref<'quota'|'requests'|'completion_tokens'>('quota')
const rows=ref<StatRow[]>([]),days=ref<string[]>([]),versions=ref<Record<string,string>>({}),busy=ref(false),error=ref(''),progress=ref(''),loaded=ref(false)
type Page={items:Record<string,string>[];version?:string;has_more:boolean}
let sequence=0
const total=computed(()=>totals(rows.value)),groups=computed(()=>groupStats(rows.value,dimension.value))
const dailyTotals=computed(()=>new Map(days.value.map(date=>[date,totals(rows.value.filter(r=>r.date===date))])))
const series=computed(()=>[{name:metric.value==='quota'?moneyLabel.value:metric.value==='requests'?'请求次数':'输出 Token',color:'#3284c6',data:calendar().map(date=>{const total=dailyTotals.value.get(date);let value:number|null=null;if(total&&total[`${metric.value}_missing`]===0n){value=metric.value==='quota'?(money.value?Number(amount(total.quota)):null):Number(total[metric.value]);if(value!==null&&!Number.isFinite(value))value=null}return [date,value] as [string,number|null]})}])
function calendar(){if(!month.value)return [];const count=new Date(Number(month.value.slice(0,4)),Number(month.value.slice(5)),0).getDate();return Array.from({length:count},(_,i)=>`${month.value}-${String(i+1).padStart(2,'0')}`)}
function failure(e:unknown){return archiveReadError(e)}
async function read(kind:string,extra:Record<string,string>){return client.request<Page>(`/api/dashboard/log-archive-read/${kind}?${new URLSearchParams({site_id:props.siteId,limit:'200',...extra})}`)}
async function load(){const ticket=++sequence;rows.value=[];days.value=[];versions.value={};loaded.value=false;error.value='';if(!props.siteId||!month.value)return;busy.value=true
 void refreshMoney()
 try{const dayPage=await read('days',{date:month.value});if(ticket!==sequence)return
 const selected=dayPage.items.filter(d=>d.state==='sealed'),out:StatRow[]=[],manifest:Record<string,string>={};let requests=0
 for(const day of selected){let after='';manifest[day.date]=day.version_id
 do{if(ticket!==sequence)return;if(++requests>500)throw new Error('汇总超过本次读取上限，请按用户、模型或渠道缩小范围；未展示部分结果。')
 progress.value=`读取 ${day.date}（${Object.keys(manifest).length}/${selected.length} 天）`
 const page=await read('stats',{date:day.date,version:day.version_id,after_hash:after,user_id:user.value,model:model.value,channel_id:channel.value});if(ticket!==sequence)return
 for(const item of page.items)out.push({date:day.date,dimensions:JSON.parse(item.dimensions),amounts:JSON.parse(item.amounts)})
 if(!page.has_more)break;const next=page.items.at(-1)?.group_hash;if(!next||next===after)throw new Error('分页游标未推进，请重新查询');after=next
 }while(true)}
 totals(out);rows.value=out;days.value=selected.map(d=>d.date);versions.value=manifest;loaded.value=true
 }catch(e){if(ticket===sequence)error.value=failure(e)}finally{if(ticket===sequence){busy.value=false;progress.value=''}}}
function reset(){sequence++;rows.value=[];days.value=[];versions.value={};loaded.value=false;busy.value=false;error.value=''}
watch(()=>props.siteId,()=>{reset();void load()},{immediate:true});watch([month,user,model,channel],reset);onUnmounted(()=>sequence++)
function csvText(){const header=['维度','请求次数','输入 Token','输出 Token','缓存读取 Token（已知部分）','Quota',`${moneyLabel.value}（按查询时配置）`,'缓存缺失条数','请求缺失条数','输入缺失条数','输出缺失条数','额度缺失条数','站点','月份','用户筛选','模型筛选','渠道筛选','日期版本','币种类型','币种符号','QuotaPerUnit','汇率（每USD）','配置读取时间','金额口径'];const manifest=JSON.stringify(versions.value),c=money.value
 return [header,...groups.value.map(g=>[g.name,g.requests,g.prompt_tokens,g.completion_tokens,g.cache_tokens,g.quota,amount(g.quota),g.cache_tokens_missing,g.requests_missing,g.prompt_tokens_missing,g.completion_tokens_missing,g.quota_missing,props.siteId,month.value,user.value,model.value,channel.value,manifest,c?.type??'',c?.symbol??'',c?.raw_quota_per_unit??'',c?.exchange_rate??'',c?.observed_at??'',c?'current_site_settings_at_query;not_historical_fx':'currency_unavailable'])].map(r=>r.map(csvCell).join(',')).join('\r\n')}
function exportCSV(){const url=URL.createObjectURL(new Blob(['\uFEFF'+csvText()],{type:'text/csv;charset=utf-8'}));const a=document.createElement('a');a.href=url;a.download=`archive-${month.value}-${dimension.value}.csv`;a.click();URL.revokeObjectURL(url)}
</script>
<template><section class="analysis">
 <div class="filters"><el-date-picker v-model="month" type="month" value-format="YYYY-MM" :clearable="false"/><el-input v-model="user" placeholder="用户 ID"/><el-input v-model="model" placeholder="模型（精确匹配）"/><el-input v-model="channel" placeholder="渠道 ID"/><el-button type="primary" :loading="busy" @click="load">查询统计</el-button></div>
 <el-alert v-if="error" :title="error" type="error" :closable="false"/>
 <details class="muted"><summary>统计口径与币种</summary><p>北京时间 · 仅已封存日期，缺失数据留空。{{moneyBusy?'正在读取币种…':moneyContext(money)}}</p></details>
 <el-alert v-if="moneyError" :title="moneyError" type="warning" :closable="false"><el-button link @click="refreshMoney">重试币种配置</el-button></el-alert>
 <p v-if="busy">{{progress}}</p>
 <el-empty v-if="loaded&&!days.length" description="本月暂无已封存数据"/>
 <template v-if="loaded&&days.length"><p>已封存 {{days.length}} / {{calendar().length}} 天</p>
 <div class="kpis"><div><span>{{moneyLabel}}</span><strong>{{amount(total.quota)}}</strong><small v-if="total.quota_missing>0n">{{total.quota_missing}} 条金额缺失，仅计已知值</small></div><div v-for="(label,key) in {requests:'消费请求',prompt_tokens:'输入 Token',completion_tokens:'输出 Token',cache_tokens:'缓存读取 Token'}" :key="key"><span>{{label}}</span><strong>{{total[key].toLocaleString()}}</strong><small v-if="total[`${key}_missing`]>0n">{{total[`${key}_missing`].toLocaleString()}} 条缺失，仅计已知值</small></div></div>
 <div class="chart"><el-radio-group v-model="metric"><el-radio-button value="quota">金额</el-radio-button><el-radio-button value="requests">请求</el-radio-button><el-radio-button value="completion_tokens">输出 Token</el-radio-button></el-radio-group><TrendChart title="每日用量趋势" :series="series"/></div>
 <div class="table-head"><el-radio-group v-model="dimension"><el-radio-button value="model_name">模型</el-radio-button><el-radio-button value="user_id">用户</el-radio-button><el-radio-button value="channel_id">渠道</el-radio-button><el-radio-button value="group">分组</el-radio-button><el-radio-button value="token_id">令牌 ID</el-radio-button></el-radio-group><el-button @click="exportCSV">导出 CSV</el-button></div>
 <el-table :data="groups" max-height="480"><el-table-column prop="name" label="维度" min-width="200"/><el-table-column label="请求次数"><template #default="{row}">{{row.requests.toLocaleString()}}</template></el-table-column><el-table-column label="输入 Token"><template #default="{row}">{{row.prompt_tokens.toLocaleString()}}{{row.prompt_tokens_missing>0n?'（部分缺失）':''}}</template></el-table-column><el-table-column label="输出 Token"><template #default="{row}">{{row.completion_tokens.toLocaleString()}}{{row.completion_tokens_missing>0n?'（部分缺失）':''}}</template></el-table-column><el-table-column label="缓存读取 Token"><template #default="{row}">{{row.cache_tokens.toLocaleString()}}{{row.cache_tokens_missing>0n?'（部分缺失）':''}}</template></el-table-column><el-table-column :label="moneyLabel"><template #default="{row}">{{amount(row.quota)}}{{row.quota_missing>0n?'（部分缺失）':''}}</template></el-table-column></el-table>
 <ArchiveModelPrices :rows="rows" :currency="money"/>
 </template><el-empty v-else-if="!loaded&&!busy&&!error" description="选择月份与筛选条件后查询"/>
</section></template>
<style scoped>details{margin:12px 0;color:var(--el-text-color-secondary);font-size:13px}summary{cursor:pointer;width:fit-content}.analysis{background:var(--el-bg-color);border:1px solid var(--el-border-color);border-radius:12px;padding:20px}.filters,.table-head{display:flex;gap:12px;flex-wrap:wrap;align-items:center}.filters .el-input{width:180px}.muted{color:var(--el-text-color-secondary);font-size:13px;line-height:1.8}.kpis{display:grid;grid-template-columns:repeat(5,1fr);gap:16px;margin:20px 0}.kpis div{display:flex;flex-direction:column;gap:8px}.kpis strong{font-size:23px}.kpis small{color:var(--el-color-warning)}.chart{margin:20px 0}.table-head{justify-content:space-between;margin-bottom:16px}@media(max-width:900px){.kpis{grid-template-columns:repeat(2,1fr)}}</style>
