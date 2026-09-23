<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ApiError } from '@ct/shared'
import { ElMessage } from 'element-plus'
import AppShell from '../components/AppShell.vue'
import { client } from '../api'
import { useFiltersStore } from '../stores/filters'
import { useAuthStore } from '../stores/auth'
import { can } from '../permissions'
import { beijingDate, formatArchiveCount } from '../utils/logArchive'

type Tasks={collection:boolean;history:boolean;retry_token?:string}
type Config={version:number;instance_id:string;agent_id:string;running:boolean;batch_size:number;interval_seconds:number;delay_seconds:number;history_immutable:boolean;tasks:Tasks}
type Progress={step:string;date?:string;table?:string;after_id:string;rows:string;updated_at:string;error?:string}
type Day={date:string;state:string;revision:string;version?:string;rows:string;step?:string;error?:string}
type Engine={latest?:{id:string;table:string;log_time:string;observed_at:string};protocol:number;collection:Progress;history:Progress;first_date?:string;first_date_source?:string;frontier?:string;cutoff?:string;counts_date?:string;counts_error?:string}
type Item={site_id:string;config:Config;seen_at?:string;targets:{agent_id:string;instance_id:string;configured:boolean}[];status:{engine?:Engine;applied_version:number;state:string;error:string}}
const filters=useFiltersStore(),auth=useAuthStore(),tab=ref('overview'),mode=ref('calendar'),month=ref(beijingDate().slice(0,7))
const item=ref<Item>(),reports=ref<Day[]>([]),loading=ref(false),saving=ref(false),error=ref(''),selected=ref<Day>(),initialized=ref(false)
const form=ref<Config>(),now=ref(Date.now());let sequence=0,disposed=false,timer:ReturnType<typeof setInterval>|undefined
const unavailable=ref(false)
const emptyTitle=computed(()=>loading.value?'正在读取归档状态':unavailable.value?'新版归档接口尚未就绪':error.value?error.value:'等待归档数据')
const emptyDescription=computed(()=>unavailable.value?'当前 Server 未提供新版归档接口，请部署新版 Server，并确认 Agent 已升级。连接成功后会显示真实进度。':error.value?'请检查服务连接后刷新重试。未读取到数据不代表没有日志，也不代表任务已停止。':'等待 Server 提供任务配置及 Agent 上报，日志数量和处理进度暂不可用。')
const engine=computed(()=>item.value?.status.engine)
const online=computed(()=>!!item.value?.seen_at&&now.value-Date.parse(item.value.seen_at)<90000)
const writable=computed(()=>can(auth.user,'archive.manage')&&!loading.value&&!saving.value&&!error.value&&online.value&&item.value?.config.version===item.value?.status.applied_version)
const labels:Record<string,string>={collecting:'采集中',pending:'待处理',processing:'处理中',sealed:'已封存',failed:'处理失败',unknown:'状态待上报',future:'未来日期'}
const steps:Record<string,string>={idle:'等待采集',collect:'采集源日志',verify_source:'校验并补齐源日志',verify_archive:'核对归档完整性',summarize:'整理多维日统计',seal:'发布封存结果',sealed:'当日处理完成',failed:'当日处理失败'}
const reasons:Record<string,string>={created_at_index_required:'日志时间字段缺少索引，无法安全读取日期边界',source_archive_content_mismatch:'源库与归档库的日志数量或内容不一致',source_history_retention_unconfirmed:'尚未确认源库历史日志完整保留且稳定，不能封存',invalid_billing_other_json:'日志 other 字段不是有效 JSON，无法整理计费数据',invalid_billing_integer:'计费用量或额度不是有效的非负整数',archive_identity_or_schema_mismatch:'新归档数据集身份或结构版本不匹配',database_timeout:'数据库查询超时，批次未完成',archive_writer_busy:'另一个归档执行器持有写入锁'}
const apiReasons:Record<string,string>={archive_schema_missing:'归档控制表缺失，请检查 Server 数据库迁移是否完成',archive_schema_mismatch:'归档控制表结构不匹配，请更新 Server 并完成数据库迁移',archive_database_permission_denied:'Server 数据库账号无权读取归档控制数据',archive_unavailable:'Server 读取归档任务配置或状态失败',archive_days_unavailable:'Server 读取每日归档状态失败'}
const countError=computed(()=>{const code=engine.value?.counts_error;if(!code)return '';if(code==='archive_count_created_at_index_required'||code==='archive_count_time_id_index_required')return '归档月表缺少适合计数的时间索引，请检查归档库索引';if(code==='database_timeout'||code.startsWith('mysql_3024_'))return '归档数量查询超时，将自动重试';return reason(code)})
function apiFailure(e:unknown){if(e instanceof ApiError){return `${apiReasons[e.code]||(e.status===403?'当前账号没有查看归档数据的权限':'归档接口请求失败')}（HTTP ${e.status} · ${e.code}）`}return '无法连接归档接口，请检查服务和网络后重试'}
function reason(code?:string){return code?(reasons[code]||`执行失败：${code}`):''}
function time(value?:string){return value&&!value.startsWith('0001-')?new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'尚未上报'}
const days=computed(()=>{
 if(!engine.value?.first_date)return []
 const out:Day[]=[],last=new Date(`${month.value}-01T12:00:00Z`);last.setUTCMonth(last.getUTCMonth()+1);last.setUTCDate(0)
 for(let n=1;n<=last.getUTCDate();n++){const date=`${month.value}-${String(n).padStart(2,'0')}`;if(date<engine.value.first_date)continue;out.push(reports.value.find(d=>d.date===date)||{date,state:date>beijingDate(now.value)?'future':'unknown',revision:'0',rows:''})}return out
})
const offset=computed(()=>days.value.length?(new Date(`${days.value[0].date}T12:00:00Z`).getUTCDay()+6)%7:0)
function disabledMonth(date:Date){return !!engine.value?.first_date&&`${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}`<engine.value.first_date.slice(0,7)}
async function load(){
 const ticket=++sequence,site=filters.site_id,requested=month.value;if(!site)return;loading.value=true
 try{const res=await client.request<{protocol:number;items:Item[];days:Day[]}>(`/api/dashboard/log-archive-jobs?site_id=${encodeURIComponent(site)}&month=${requested}`)
 if(disposed||ticket!==sequence||site!==filters.site_id)return
 if(res.protocol!==1)throw new ApiError(404,'archive_protocol_unavailable')
 item.value=res.items[0];reports.value=res.days;error.value='';unavailable.value=false
 if(selected.value)selected.value=reports.value.find(d=>d.date===selected.value?.date)||days.value.find(d=>d.date===selected.value?.date)
 if(!initialized.value&&engine.value?.first_date){initialized.value=true;const first=engine.value.first_date.slice(0,7);if(first!==month.value){month.value=first;void load()}}
 }catch(e){if(!disposed&&ticket===sequence&&site===filters.site_id){unavailable.value=e instanceof ApiError&&(e.status===404||e.status===410);error.value=unavailable.value?'新版归档接口尚未就绪':apiFailure(e)}}finally{if(ticket===sequence)loading.value=false}
}
async function save(config:Config){const site=filters.site_id;saving.value=true
 try{await client.request(`/api/dashboard/log-archive-jobs/${encodeURIComponent(site)}`,{method:'PUT',body:JSON.stringify(config)});if(site!==filters.site_id)return;form.value=undefined;ElMessage.success('已提交，等待 Agent 应用');await load()}catch(e){ElMessage.error(e instanceof Error?e.message:'保存失败')}finally{saving.value=false}}
function toggle(task:keyof Pick<Tasks,'collection'|'history'>){if(!item.value||!writable.value)return;const c=item.value.config;const tasks={...c.tasks};if(!c.running){tasks.collection=false;tasks.history=false}tasks[task]=!c.running||!c.tasks[task];void save({...c,tasks,running:tasks.collection||tasks.history})}
function retryToken(){return globalThis.crypto.randomUUID()}
function edit(){if(item.value)form.value=JSON.parse(JSON.stringify(item.value.config))}
watch(()=>filters.site_id,()=>{sequence++;error.value='';unavailable.value=false;item.value=undefined;reports.value=[];selected.value=undefined;form.value=undefined;initialized.value=false;month.value=beijingDate().slice(0,7);void load()})
onMounted(async()=>{await filters.loadInstances();await load();timer=setInterval(()=>{now.value=Date.now();if(!document.hidden&&!loading.value&&!saving.value)void load()},15000)})
onUnmounted(()=>{disposed=true;sequence++;if(timer)clearInterval(timer)})
</script>
<template>
 <AppShell title="日志归档"><div class="archive-jobs">
 <header class="archive-toolbar">
  <el-tabs v-model="tab" class="archive-tabs"><el-tab-pane label="运行总览" name="overview"/><el-tab-pane label="每日数据" name="daily"/><el-tab-pane label="设置" name="settings"/></el-tabs>
  <div v-if="item" class="executor-status" aria-label="执行 Agent 状态">
   <div class="executor-identity"><span class="status-label">执行 Agent</span><span class="executor-name" :title="item.config.agent_id">{{item.config.agent_id||'未配置'}}</span></div>
   <span class="connection-status" :class="{'is-online':online}"><i aria-hidden="true"/>{{online?'在线':'离线 / 状态待确认'}}</span>
   <span class="config-status" :class="{'is-pending':item.config.version!==item.status.applied_version}">{{item.config.version===item.status.applied_version?'配置已应用':'等待配置确认'}}</span>
   <span class="report-time"><span class="status-label">最近上报</span><time>{{time(item.seen_at)}}</time></span>
  </div>
  <el-button class="refresh-status" :loading="loading" @click="load">刷新状态</el-button>
 </header>
 <el-alert v-if="error||!item" :title="item?error:emptyTitle" :description="item?'当前展示最近成功读取的数据，实时状态待确认，操作暂不可用。':emptyDescription" :type="unavailable||!error?'warning':'error'" show-icon :closable="false"/>
 <el-alert v-if="item?.status.error" :title="reason(item.status.error)" type="warning" :closable="false"/>
 <el-alert v-if="countError" :title="`每日数量统计${engine?.counts_date?'（'+engine.counts_date+'）':''}：${countError}`" type="warning" show-icon :closable="false"/>
 <div v-if="tab==='overview'" class="task-grid">
 <section v-for="task in (['collection','history'] as const)" :key="task"><header><h3>{{task==='collection'?'日志采集':'历史处理'}}</h3><span>{{!item?'等待连接':!online||error?'状态待确认':item.config.running&&item.config.tasks[task]?'已启用':'已暂停'}}</span></header>
 <h2>{{task==='collection'?(engine?.latest ? time(engine.latest.log_time) : '最新归档位置尚未上报'):engine?.history.date||(item?'等待历史日期':'历史处理进度待上报')}}</h2>
 <p v-if="task==='collection'&&engine?.latest">{{engine.latest.table}} · 最大日志 ID {{engine.latest.id}}<br>采集游标 ID {{engine.collection.after_id}} · 月表位置不代表历史完整</p><p>{{steps[engine?.[task].step||'']||(item?'等待执行':'尚未获取任务状态')}}</p><p v-if="engine?.[task].table">处理表：{{engine[task].table}}</p>
 <p v-if="task==='collection'">按 ID 持续采集，无需日期范围。首次从已有月表最大 ID 开始，历史缺口由历史处理补齐。</p><p v-else>逐日校验补齐 → 整理日统计 → 封存。处理到昨天；某天失败记录原因，继续下一天。</p>
 <p v-if="engine?.[task].error" class="failure">{{reason(engine[task].error)}}</p>
 <p class="muted">最近提交：{{time(engine?.[task].updated_at)}} · 已处理 {{formatArchiveCount(engine?.[task].rows)}} 条</p>
 <el-button :disabled="!writable" @click="toggle(task)">{{item?.config.running&&item.config.tasks[task]?'暂停':'启动'}}{{task==='collection'?'日志采集':'历史处理'}}</el-button></section>
 </div>
 <section v-if="tab==='daily'" class="daily-panel"><header class="daily-heading"><h3>每日数据</h3><div class="daily-controls"><el-radio-group v-model="mode" aria-label="每日数据视图"><el-radio-button value="calendar">日历</el-radio-button><el-radio-button value="list">列表</el-radio-button></el-radio-group><el-date-picker v-model="month" class="month-picker" type="month" value-format="YYYY-MM" :clearable="false" :disabled="!engine?.first_date" :disabled-date="disabledMonth" @change="load"/></div></header>
 <div v-if="engine?.first_date" class="daily-caption"><p>起始日期 <strong>{{engine.first_date}}</strong><span class="origin-note">{{engine.first_date_source==='source'?'归档月表为空，使用源库起点':'以现有归档月表为准'}}</span></p><span class="seal-note">已封存：校验、日统计和发布均已完成</span></div>
 <p v-if="engine?.first_date" class="count-caption">日志数为归档库中实际已有的条数，暂停采集时仍会更新。数量待统计不代表 0 条。<span v-if="engine.counts_date&&!engine.counts_error"> 正在统计 {{engine.counts_date}}。</span></p>
 <el-empty v-if="!engine?.first_date" description="尚未获取日志起始日期和每日数量"><p class="muted">连接新版 Server 并收到 Agent 上报后，按实际日志日期展示日历和列表。数量未知，不代表 0 条。</p></el-empty>
 <div v-else-if="mode==='calendar'" class="calendar-scroll"><div class="day-calendar">
  <span v-for="w in ['一','二','三','四','五','六','日']" :key="w" class="weekday">周{{w}}</span><span v-for="n in offset" :key="`blank${n}`" aria-hidden="true"/>
  <button v-for="d in days" :key="d.date" class="day-cell" :class="[d.state,{'is-selected':selected?.date===d.date,'is-today':d.date===beijingDate(now)}]" :disabled="d.state==='future'" :aria-pressed="selected?.date===d.date" :aria-label="`${d.date}，${labels[d.state]}，${d.rows!==''?formatArchiveCount(d.rows)+' 条':'数量待统计'}`" @click="selected=d">
   <span class="day-heading"><b class="day-number">{{Number(d.date.slice(8))}}</b><span class="day-status" :class="d.state"><i aria-hidden="true"/>{{labels[d.state]}}</span></span>
   <span v-if="d.rows!==''" class="day-count"><strong>{{formatArchiveCount(d.rows)}}</strong><span>条</span></span>
   <span v-else class="day-count count-pending"><strong>—</strong><span>{{d.state==='future'?'尚未开始':'数量待统计'}}</span></span>
   <span v-if="d.error||d.step" class="day-note" :title="reason(d.error)||steps[d.step||'']">{{reason(d.error)||steps[d.step||'']}}</span>
  </button>
 </div></div>
 <el-table v-else :data="days" class="daily-table" row-key="date" highlight-current-row @row-click="selected=$event"><el-table-column prop="date" label="日期" min-width="130"/><el-table-column label="状态" min-width="150"><template #default="{row}"><span class="day-status" :class="row.state"><i aria-hidden="true"/>{{labels[row.state]}}</span></template></el-table-column><el-table-column label="日志数" min-width="130" align="right"><template #default="{row}"><span :class="row.rows!==''?'list-count':'muted'">{{row.rows!==''?formatArchiveCount(row.rows):'数量待统计'}}</span></template></el-table-column><el-table-column label="原因" min-width="220" show-overflow-tooltip><template #default="{row}"><span class="muted">{{reason(row.error)||'—'}}</span></template></el-table-column></el-table>
 <div v-if="selected" class="day-detail"><header><div class="detail-title"><h3>{{selected.date}}</h3><span class="day-status" :class="selected.state"><i aria-hidden="true"/>{{labels[selected.state]}}</span></div><el-button text size="small" @click="selected=undefined">收起详情</el-button></header><p v-if="selected.step" class="muted">{{steps[selected.step]}}</p><p v-if="selected.error" class="failure">{{reason(selected.error)}}</p><dl class="detail-values"><div><dt>日志数</dt><dd>{{selected.rows!==''?formatArchiveCount(selected.rows)+' 条':'数量待统计'}}</dd></div><div><dt>数据版本</dt><dd>{{selected.revision}}</dd></div><div><dt>封存版本</dt><dd>{{selected.version||'尚未封存'}}</dd></div></dl></div>
 </section>
 <section v-if="tab==='settings'"><header><h3>执行设置</h3><el-button :disabled="!writable" @click="edit">编辑设置</el-button></header><p v-if="item">每批 {{item.config.batch_size}} 条 · 间隔 {{item.config.interval_seconds}} 秒 · 采集延迟 {{item.config.delay_seconds}} 秒</p><p>两个任务按批次交接源库读取权。采集优先，每四个采集批次提供一次历史处理机会；只启动一个任务时独立推进。</p><p v-if="!item" class="muted">执行 Agent、每批条数、间隔及采集延迟等待 Server 提供配置，连接就绪后可编辑。</p><p v-if="item">源历史完整保留声明：{{item.config.history_immutable?'已确认':'尚未确认，历史处理不能封存'}}</p><el-button :disabled="!writable" @click="item&&save({...item.config,tasks:{...item.config.tasks,retry_token:retryToken()}})">重试失败日期</el-button></section>
 <el-dialog :model-value="!!form" title="执行设置" @close="form=undefined"><el-form v-if="form" label-width="120px"><el-form-item label="执行 Agent"><el-select v-model="form.agent_id"><el-option v-for="t in item?.targets" :key="t.agent_id" :value="t.agent_id" :label="t.agent_id" @click="form.instance_id=t.instance_id"/></el-select></el-form-item><el-form-item label="每批条数"><el-input-number v-model="form.batch_size" :min="1" :max="5000"/></el-form-item><el-form-item label="间隔（秒）"><el-input-number v-model="form.interval_seconds" :min="2" :max="3600"/></el-form-item><el-form-item label="延迟（秒）"><el-input-number v-model="form.delay_seconds" :min="60" :max="86400"/></el-form-item><el-checkbox v-model="form.history_immutable">确认源历史日志完整保留、已稳定，可用于完整性校验</el-checkbox><p>已有月表最大 ID 不代表此前日志完整。此声明应符合源库实际保留策略。</p></el-form><template #footer><el-button :loading="saving" @click="form&&save(form)">保存</el-button></template></el-dialog>
 </div></AppShell>
</template>
<style scoped>
.archive-toolbar{display:grid;grid-template-columns:auto minmax(0,1fr) auto;column-gap:20px;row-gap:8px;padding-bottom:12px;border-bottom:1px solid var(--el-border-color-light)}
.archive-tabs{min-width:0}
.archive-tabs :deep(.el-tabs__header){margin:0}
.archive-tabs :deep(.el-tabs__nav-wrap::after){display:none}
.executor-status{display:flex;align-items:center;justify-content:flex-end;flex-wrap:wrap;gap:6px 12px;min-width:0;font-size:12px;line-height:22px}
.executor-identity,.report-time{display:flex;align-items:center;gap:6px;min-width:0}
.status-label{color:var(--el-text-color-secondary);white-space:nowrap}
.executor-name{max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--el-text-color-primary);font-weight:500}
.connection-status{display:inline-flex;align-items:center;gap:5px;padding:0 7px;border-radius:4px;background:var(--el-fill-color);color:var(--el-text-color-secondary);white-space:nowrap}
.connection-status i{width:6px;height:6px;flex-shrink:0;border-radius:50%;background:currentColor}
.connection-status.is-online{background:var(--el-color-success-light-9);color:var(--el-color-success)}
.config-status,.report-time{color:var(--el-text-color-secondary);white-space:nowrap}
.config-status.is-pending{color:var(--el-color-warning)}
.report-time time{font-variant-numeric:tabular-nums}
.refresh-status{grid-column:3;grid-row:1;margin-left:0}
.daily-panel .daily-heading{gap:12px}
.daily-controls{display:flex;align-items:center;gap:10px;flex-wrap:wrap}
.daily-controls :deep(.month-picker){width:180px;height:32px}
.daily-controls :deep(.el-radio-button__inner){display:flex;align-items:center;justify-content:center;height:32px;box-sizing:border-box;padding:0 14px}
.daily-caption{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:4px 20px;margin:12px 0 16px;color:var(--el-text-color-secondary);font-size:12px;line-height:20px}
.daily-caption p{margin:0;font-size:12px}
.daily-caption strong{margin-left:6px;color:var(--el-text-color-regular);font-weight:500;font-variant-numeric:tabular-nums}
.origin-note{margin-left:12px}
.daily-panel .count-caption{margin:-8px 0 14px;font-size:12px;line-height:20px;color:var(--el-text-color-secondary)}
.calendar-scroll{overflow-x:auto;padding:2px}
.day-calendar{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:8px;min-width:1050px}
.weekday{text-align:center;font-size:12px;line-height:20px;padding:0 0 6px;color:var(--el-text-color-secondary)}
.day-cell{display:flex;flex-direction:column;gap:16px;min-width:0;min-height:104px;padding:12px;text-align:left;font:inherit;background:var(--el-bg-color);border:1px solid var(--el-border-color-lighter);border-radius:8px;color:var(--el-text-color-primary);cursor:pointer;transition:border-color .15s,background-color .15s,box-shadow .15s}
.day-cell:hover:not(:disabled){border-color:var(--el-color-primary-light-5);background:var(--el-fill-color-light)}
.day-cell:focus-visible{outline:2px solid var(--el-color-primary);outline-offset:2px}
.day-cell.is-selected{border-color:var(--el-color-primary);box-shadow:inset 0 0 0 1px var(--el-color-primary);background:var(--el-color-primary-light-9)}
.day-cell:disabled{cursor:default;background:var(--el-fill-color-lighter);color:var(--el-text-color-secondary)}
.day-heading{display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:6px;min-height:22px}
.day-number{font-size:15px;font-weight:600;font-variant-numeric:tabular-nums}
.day-cell.is-today .day-number{color:var(--el-color-primary)}
.day-status{display:inline-flex;align-items:center;gap:5px;width:fit-content;font-size:11px;line-height:20px;white-space:nowrap;color:var(--el-text-color-secondary)}
.day-status i{width:5px;height:5px;flex-shrink:0;background:currentColor;border-radius:50%}
.day-status.processing,.day-status.collecting{color:var(--el-color-primary)}
.day-status.sealed{color:var(--el-color-success)}
.day-status.failed{color:var(--el-color-danger)}
.day-status.pending{color:var(--el-color-warning)}
.day-count{display:flex;align-items:baseline;gap:6px;min-width:0;font-variant-numeric:tabular-nums}
.day-count strong{font-size:21px;line-height:26px;font-weight:600;overflow-wrap:anywhere}
.day-count>span{font-size:12px;color:var(--el-text-color-secondary);white-space:nowrap}
.count-pending strong{font-size:19px;font-weight:400;color:var(--el-text-color-placeholder)}
.day-note{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:12px;line-height:18px;color:var(--el-text-color-secondary);margin-top:-8px}
.day-cell.failed .day-note{color:var(--el-color-danger)}
.list-count{font-variant-numeric:tabular-nums;font-weight:500}
.daily-table{--el-table-header-bg-color:var(--el-fill-color-light);--el-table-border-color:var(--el-border-color-lighter)}
.day-detail{margin-top:16px;padding:14px 16px;border:1px solid var(--el-border-color-light);border-radius:8px;background:var(--el-fill-color-lighter)}
.detail-title{display:flex;align-items:center;gap:12px}
.detail-values{display:grid;grid-template-columns:1fr 1fr 2fr;gap:12px;margin:12px 0 0;font-size:12px;line-height:20px}
.detail-values dt{color:var(--el-text-color-secondary)}
.detail-values dd{margin:4px 0 0;color:var(--el-text-color-primary);font-variant-numeric:tabular-nums;overflow-wrap:anywhere}
@media(max-width:1280px){.archive-toolbar{grid-template-columns:minmax(0,1fr) auto;column-gap:12px}.executor-status{grid-column:1/-1;grid-row:2;justify-content:flex-start}.refresh-status{grid-column:2}.executor-name{max-width:260px}}
@media(max-width:700px){.archive-toolbar{column-gap:8px}.archive-tabs :deep(.el-tabs__item){padding:0 12px}.executor-status{gap:4px 10px}.executor-identity{flex:1 1 auto}.executor-name{max-width:180px}.report-time{flex-basis:100%}}
@media(max-width:700px){.daily-controls{width:100%;justify-content:space-between;gap:8px}.daily-controls :deep(.month-picker){width:150px;height:36px}.daily-controls :deep(.el-radio-button__inner){height:36px}.daily-caption{margin:10px 0 12px}.origin-note{display:block;margin-left:0}.seal-note{font-size:11px}.day-count strong{font-size:18px}.detail-values{grid-template-columns:1fr 1fr}.detail-values>div:last-child{grid-column:1/-1}.day-detail{padding:12px}}
.archive-jobs{display:grid;gap:16px}header{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}h3{margin:0;font-size:16px}h2{font-size:21px}.task-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}section{padding:20px;border:1px solid var(--el-border-color-light);border-radius:12px;background:var(--el-bg-color);min-width:0;overflow-wrap:anywhere}p{font-size:13px;line-height:1.8}.muted{color:var(--el-text-color-secondary);font-size:12px}.failure{color:var(--el-color-danger)}@media(max-width:700px){.task-grid{grid-template-columns:1fr}section{padding:12px}}
</style>
