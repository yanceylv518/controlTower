<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue';
import { FullScreen } from '@element-plus/icons-vue';
import { client } from '../api';
import TrendChart, {type TrendSeries} from './TrendChart.vue';
import {errorTrend,type ErrorCodeResult} from '../utils/monitorErrorTrend';
const props=defineProps<{site:string;dimensionType:string;value:string;hours:number;active:boolean;series:TrendSeries[];bucket:'1m'|'5m'}>();
const mode=ref('overview');const expanded=ref(false);const selected=ref<string|null>(null);
const data=ref<ErrorCodeResult>();const loading=ref(false);const error=ref('');
const title=computed(()=>`请求与错误（${props.bucket} 桶）`);
const windowRange=computed(()=>{
 const times=props.series[0]?.data.map(([t])=>Date.parse(t)).filter(Number.isFinite)||[];
 if(!times.length)return null;return {start:new Date(Math.min(...times)).toISOString(),end:new Date(Math.max(...times)+(props.bucket==='5m'?300000:60000)).toISOString()};
});
const chartSeries=computed(()=>mode.value==='overview'?props.series:data.value?.configured?errorTrend(data.value,selected.value):[]);
const contextSeries=computed(()=>mode.value==='overview'?undefined:[...props.series.slice(0,1).map(s=>({...s,name:'请求量（监控）'})),{name:'错误总数（源日志）',color:'#ce3b44',data:data.value?.configured?(errorTrend(data.value)[0]?.data||[]).map(([time])=>[time,Object.values(data.value!.buckets.find(b=>Date.parse(b.time)===Date.parse(time))?.counts||{}).reduce((a,b)=>a+b,0)] as [string,number]):[]}]);
let controller:AbortController|undefined;let generation=0;let loadedKey='';
async function load(force=false){
 const range=windowRange.value;
 const key=JSON.stringify([props.site,props.dimensionType,props.value,range,props.bucket]);
 if(!force&&key===loadedKey&&data.value)return;
 controller?.abort();const token=++generation;data.value=undefined;error.value='';loading.value=false;
 if(!props.active||!props.site||!props.value||!range||(mode.value==='overview'&&!expanded.value))return;
 controller=new AbortController();loading.value=true;
 const query=new URLSearchParams({site:props.site,dimension_type:props.dimensionType,value:props.value,start_time:range.start,end_time:range.end,bucket:props.bucket});
 try{const result=await client.request<ErrorCodeResult>(`/api/dashboard/monitor-error-codes?${query}`,{signal:controller.signal});if(token===generation){data.value=result;loadedKey=key;}}
 catch(e){if(token===generation)error.value=(e as {code?:string}).code==='error_statistics_limit'?'错误日志较多，请缩短时间范围。':'错误码统计读取失败，请重试。';}
 finally{if(token===generation)loading.value=false;}
}
watch(()=>[props.site,props.dimensionType,props.value,props.hours,props.bucket],()=>{selected.value=null;loadedKey='';});
watch(()=>[props.site,props.dimensionType,props.value,props.hours,props.active,windowRange.value?.start,windowRange.value?.end,mode.value,expanded.value],()=>void load(),{immediate:true});
function focus(code:string){selected.value=selected.value===code?null:code;mode.value='codes';}
onBeforeUnmount(()=>{generation++;controller?.abort();});
</script>
<template>
 <div class="request-error-card" v-loading="loading && mode==='codes'">
  <TrendChart :title="title" :series="chartSeries" :context-series="contextSeries">
   <template #actions><el-radio-group v-model="mode" size="small"><el-radio-button value="overview">总览</el-radio-button><el-radio-button value="codes">按错误码</el-radio-button></el-radio-group><el-button text :icon="FullScreen" aria-label="展开请求与错误" @click="expanded=true" /></template>
  </TrendChart>
  <div v-if="mode==='codes'" class="error-status"><span v-if="error">{{ error }} <el-button link type="primary" @click="load(true)">重试</el-button></span><span v-else-if="data && !data.configured">尚未配置站点只读连接</span><span v-else-if="!windowRange">暂无可对齐的监控时间桶</span><span v-else>Top 5 + 其他 · 源错误日志 <el-button v-if="selected!==null" link @click="selected=null">查看全部</el-button></span></div>
 </div>
 <el-dialog v-model="expanded" title="请求与错误" width="min(1100px, 94vw)" destroy-on-close>
  <div class="expanded-toolbar"><el-radio-group v-model="mode" size="small"><el-radio-button value="overview">总览</el-radio-button><el-radio-button value="codes">按错误码</el-radio-button></el-radio-group><el-button size="small" :disabled="loading" @click="load(true)">刷新</el-button></div>
  <TrendChart :title="title" :series="chartSeries" :context-series="contextSeries" />
  <el-alert v-if="error" :title="error" type="error" :closable="false" />
  <el-empty v-else-if="data && !data.configured" description="尚未配置站点只读连接" />
  <div v-else v-loading="loading"><header class="distribution-header"><b>错误码分布</b><span v-if="data">总计 {{ data.total.toLocaleString() }} 条 <el-button v-if="selected!==null" link @click="selected=null">查看全部</el-button></span></header>
   <el-table v-if="data" :data="data.items" empty-text="所选时段暂无错误日志" :max-height="300">
    <el-table-column label="错误码" min-width="200"><template #default="{row}">{{ row.code || '未知' }}</template></el-table-column>
    <el-table-column label="次数" width="120" align="right"><template #default="{row}">{{ row.count.toLocaleString() }}</template></el-table-column>
    <el-table-column label="错误占比" width="120" align="right"><template #default="{row}">{{ data.total?(row.count/data.total*100).toFixed(2):'0.00' }}%</template></el-table-column>
    <el-table-column label="操作" width="120"><template #default="{row}"><el-button link type="primary" @click="focus(row.code)">{{ selected===row.code?'查看全部':'查看曲线' }}</el-button></template></el-table-column>
   </el-table>
  </div>
 </el-dialog>
</template>
<style scoped>.request-error-card{min-width:0;position:relative}.error-status{padding:0 12px 6px;font-size:12px;color:var(--el-text-color-secondary)}.expanded-toolbar,.distribution-header{display:flex;justify-content:space-between;align-items:center;margin-bottom:12px}.request-error-card :deep(.trend-header){flex-wrap:nowrap}.request-error-card :deep(.trend-header h3){white-space:nowrap;font-size:12px}.request-error-card :deep(.el-radio-button__inner){padding:5px 7px}.request-error-card :deep(.trend-chart){height:100%;box-sizing:border-box}</style>
