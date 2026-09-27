<script setup lang="ts">
import {ref,watch,onUnmounted,computed} from 'vue'
import {ApiError} from '@ct/shared'
import {client} from '../api'
import {beijingDate} from '../utils/logArchive'
import {useArchiveCurrency} from '../composables/useArchiveCurrency'
import {quotaAmount,currencyUnit,moneyContext} from '../utils/archiveMoney'
import ArchiveAnomalyOverview from './ArchiveAnomalyOverview.vue'
const props=defineProps<{siteId:string}>()
const {money,moneyError,moneyBusy,refreshMoney}=useArchiveCurrency(()=>props.siteId)
const unit=computed(()=>currencyUnit(money.value))
const moneyLabel=computed(()=>money.value?.type==='TOKENS'?'额度（Quota）':`金额 ${unit.value}`)
const summaryQuery=ref<Record<string,string>>()
const date=ref(beijingDate()),category=ref('empty_output'),user=ref(''),model=ref(''),channel=ref(''),busy=ref(false),error=ref(''),loaded=ref(false),more=ref(false)
type Log=Record<string,string|null>
const rows=ref<Log[]>([]),selected=ref<Log>(),page=ref(1),cursors=ref([{time:'0',id:'0'}]);let sequence=0
const charged=computed(()=>rows.value.filter(r=>r.quota&&BigInt(r.quota)>0n).length)
function reset(){sequence++;summaryQuery.value=undefined;rows.value=[];selected.value=undefined;error.value='';loaded.value=false;busy.value=false;more.value=false;page.value=1;cursors.value=[{time:'0',id:'0'}]}
async function load(target=1){const ticket=++sequence;busy.value=true;error.value='';loaded.value=false;rows.value=[]
 if(target===1)void refreshMoney()
 if(target===1)summaryQuery.value={site_id:props.siteId,date:date.value,user_id:user.value,model:model.value,channel_id:channel.value}
 const cursor=cursors.value[target-1];if(!cursor){busy.value=false;return}
 try{const query=new URLSearchParams({site_id:props.siteId,date:date.value,category:category.value,user_id:user.value,model:model.value,channel_id:channel.value,limit:'100',after_time:cursor.time,after_id:cursor.id})
 const result=await client.request<{items:Log[];has_more:boolean}>(`/api/dashboard/log-archive-read/logs?${query}`);if(ticket!==sequence)return
 rows.value=result.items;more.value=result.has_more;page.value=target;loaded.value=true;const last=result.items.at(-1)
 if(result.has_more&&last)cursors.value[target]={time:String(last.created_at),id:String(last.id)}
 }catch(e){if(ticket===sequence)error.value=e instanceof ApiError?`归档日志读取失败（${e.status} · ${e.code}）。请检查归档连接、读取接口和月表时间索引。`:'连接失败，请重试'}finally{if(ticket===sequence)busy.value=false}}
watch(()=>props.siteId,reset,{immediate:true});watch([date,category,user,model,channel],reset);onUnmounted(()=>sequence++)
function amount(row:Log){return row.quota==null?'未知':quotaAmount(BigInt(row.quota),money.value)}
function time(raw:string|null){return raw?new Date(Number(raw)*1000).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'未知'}
</script>
<template><section class="anomalies">
 <h3>异常请求分析</h3><details class="muted"><summary>统计口径与币种</summary><p>仅统计已归档记录。空输出不一定代表调用失败。</p><p>{{moneyBusy?'正在读取币种…':moneyContext(money)}}</p></details>
 <div class="filters"><el-date-picker v-model="date" type="date" value-format="YYYY-MM-DD" :clearable="false"/><el-select v-model="category"><el-option label="空输出请求" value="empty_output"/><el-option label="错误日志" value="error"/><el-option label="输出 Token 缺失" value="missing_output"/></el-select><el-input v-model="user" placeholder="用户 ID"/><el-input v-model="model" placeholder="模型（精确匹配）"/><el-input v-model="channel" placeholder="渠道 ID"/><el-button type="primary" :loading="busy" :disabled="!siteId||!date" @click="cursors=[{time:'0',id:'0'}];load(1)">查询异常</el-button></div>
 <el-alert v-if="error" :title="error" type="error" :closable="false"/>

 <el-alert v-if="moneyError" :title="moneyError" type="warning" :closable="false"><el-button link @click="refreshMoney">重试币种配置</el-button></el-alert>
 <ArchiveAnomalyOverview v-if="summaryQuery" :query="summaryQuery"/>
 <template v-if="loaded"><p>本页 {{rows.length}} 条 · 有扣费 {{charged}} 条</p>
 <el-table :data="rows" row-key="id" @row-click="selected=$event"><el-table-column prop="id" label="日志 ID" width="120"/><el-table-column label="北京时间" min-width="180"><template #default="{row}">{{time(row.created_at)}}</template></el-table-column><el-table-column prop="user_id" label="用户 ID"/><el-table-column prop="model_name" label="模型" min-width="160"/><el-table-column prop="channel" label="渠道"/><el-table-column prop="prompt_tokens" label="输入 Token"/><el-table-column label="输出 Token"><template #default="{row}">{{row.completion_tokens??'缺失'}}</template></el-table-column><el-table-column :label="moneyLabel"><template #default="{row}">{{amount(row)}}</template></el-table-column><el-table-column prop="content_preview" label="日志内容" min-width="240" show-overflow-tooltip/><el-table-column width="90"><template #default="{row}"><el-button link type="primary" @click="selected=row">详情</el-button></template></el-table-column></el-table>
 <footer><el-button :disabled="busy||page===1" @click="load(page-1)">上一页</el-button><span>第 {{page}} 页</span><el-button :disabled="busy||!more" @click="load(page+1)">下一页</el-button></footer></template>
 <el-empty v-else-if="!busy&&!error" description="选择日期和异常类型，查询归档记录"/>
 <el-dialog :model-value="!!selected" title="归档日志详情" width="min(800px, 95vw)" @close="selected=undefined"><template v-if="selected"><el-descriptions :column="2" border><el-descriptions-item v-for="key in ['id','user_id','model_name','channel','prompt_tokens','completion_tokens','quota']" :key="key" :label="key">{{selected[key]??'缺失'}}</el-descriptions-item></el-descriptions><p>{{moneyLabel}}：{{amount(selected)}}</p><el-alert v-if="selected.content_truncated==='1'" title="日志内容超过 4096 字符，仅显示前部预览" type="warning" :closable="false"/><pre>{{selected.content_preview||'日志未保存内容'}}</pre></template></el-dialog>
</section></template>
<style scoped>details{margin:12px 0;color:var(--el-text-color-secondary);font-size:13px}summary{cursor:pointer;width:fit-content}.anomalies{background:var(--el-bg-color);border:1px solid var(--el-border-color);border-radius:12px;padding:20px}.filters{display:flex;flex-wrap:wrap;gap:12px;margin:20px 0}.filters .el-input{width:160px}.filters .el-select{width:285px}.muted{color:var(--el-text-color-secondary);line-height:1.8;font-size:13px}footer{display:flex;gap:16px;justify-content:flex-end;align-items:center;margin-top:16px}pre{white-space:pre-wrap;overflow-wrap:anywhere;max-height:400px;overflow:auto}</style>
