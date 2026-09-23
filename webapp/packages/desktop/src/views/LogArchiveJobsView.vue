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
type Engine={latest?:{id:string;table:string;log_time:string;observed_at:string};protocol:number;collection:Progress;history:Progress;first_date?:string;first_date_source?:string;frontier?:string;cutoff?:string}
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
 <header><el-tabs v-model="tab"><el-tab-pane label="运行总览" name="overview"/><el-tab-pane label="每日数据" name="daily"/><el-tab-pane label="设置" name="settings"/></el-tabs><el-button :loading="loading" @click="load">刷新状态</el-button></header>
 <el-alert v-if="error||!item" :title="item?error:emptyTitle" :description="item?'当前展示最近成功读取的数据，实时状态待确认，操作暂不可用。':emptyDescription" :type="unavailable||!error?'warning':'error'" show-icon :closable="false"/>
 <p v-if="item" class="muted">执行 Agent：{{item.config.agent_id}} · {{online?'在线':'离线 / 状态待确认'}} · 最近上报 {{time(item.seen_at)}} · {{item.config.version===item.status.applied_version?'配置已应用':'等待配置确认'}}</p>
 <el-alert v-if="item?.status.error" :title="reason(item.status.error)" type="warning" :closable="false"/>
 <div v-if="tab==='overview'" class="task-grid">
 <section v-for="task in (['collection','history'] as const)" :key="task"><header><h3>{{task==='collection'?'日志采集':'历史处理'}}</h3><span>{{!item?'等待连接':!online||error?'状态待确认':item.config.running&&item.config.tasks[task]?'已启用':'已暂停'}}</span></header>
 <h2>{{task==='collection'?(engine?.latest ? time(engine.latest.log_time) : '最新归档位置尚未上报'):engine?.history.date||(item?'等待历史日期':'历史处理进度待上报')}}</h2>
 <p v-if="task==='collection'&&engine?.latest">{{engine.latest.table}} · 最大日志 ID {{engine.latest.id}}<br>采集游标 ID {{engine.collection.after_id}} · 月表位置不代表历史完整</p><p>{{steps[engine?.[task].step||'']||(item?'等待执行':'尚未获取任务状态')}}</p><p v-if="engine?.[task].table">处理表：{{engine[task].table}}</p>
 <p v-if="task==='collection'">按 ID 持续采集，无需日期范围。首次从已有月表最大 ID 开始，历史缺口由历史处理补齐。</p><p v-else>逐日校验补齐 → 整理日统计 → 封存。处理到昨天；某天失败记录原因，继续下一天。</p>
 <p v-if="engine?.[task].error" class="failure">{{reason(engine[task].error)}}</p>
 <p class="muted">最近提交：{{time(engine?.[task].updated_at)}} · 已处理 {{formatArchiveCount(engine?.[task].rows)}} 条</p>
 <el-button :disabled="!writable" @click="toggle(task)">{{item?.config.running&&item.config.tasks[task]?'暂停':'启动'}}{{task==='collection'?'日志采集':'历史处理'}}</el-button></section>
 </div>
 <section v-if="tab==='daily'"><header><h3>每日数据</h3><div><el-radio-group v-model="mode"><el-radio-button value="calendar">日历</el-radio-button><el-radio-button value="list">列表</el-radio-button></el-radio-group><el-date-picker v-model="month" type="month" value-format="YYYY-MM" :clearable="false" :disabled="!engine?.first_date" :disabled-date="disabledMonth" @change="load"/></div></header>
 <p v-if="engine?.first_date" class="muted">起始日期：{{engine?.first_date||'尚未确定'}} · {{engine?.first_date_source==='source'?'归档月表为空，使用源库起点':'以现有归档月表为准'}}。已封存表示校验、日统计和发布均已完成。</p>
 <el-empty v-if="!engine?.first_date" description="尚未获取日志起始日期和每日数量"><p class="muted">连接新版 Server 并收到 Agent 上报后，按实际日志日期展示日历和列表。数量未知，不代表 0 条。</p></el-empty>
 <div v-else-if="mode==='calendar'" class="calendar"><span v-for="w in ['一','二','三','四','五','六','日']" :key="w">{{w}}</span><span v-for="n in offset" :key="`blank${n}`"/><button v-for="d in days" :key="d.date" :class="d.state" :disabled="d.state==='future'" @click="selected=d"><b>{{Number(d.date.slice(8))}}</b><span>{{labels[d.state]}}</span><small>{{d.rows!==''?formatArchiveCount(d.rows)+' 条':'数量待统计'}}</small></button></div>
 <el-table v-else :data="days" @row-click="selected=$event"><el-table-column prop="date" label="日期"/><el-table-column label="状态"><template #default="{row}">{{labels[row.state]}}</template></el-table-column><el-table-column prop="rows" label="日志数"/><el-table-column label="原因"><template #default="{row}">{{reason(row.error)}}</template></el-table-column></el-table>
 <div v-if="selected" class="detail"><h3>{{selected.date}} · {{labels[selected.state]}}</h3><p>{{steps[selected.step||'']}}</p><p>{{reason(selected.error)}}</p><p>数据版本 {{selected.revision}} · 封存版本 {{selected.version||'尚未封存'}}</p></div>
 </section>
 <section v-if="tab==='settings'"><header><h3>执行设置</h3><el-button :disabled="!writable" @click="edit">编辑设置</el-button></header><p v-if="item">每批 {{item.config.batch_size}} 条 · 间隔 {{item.config.interval_seconds}} 秒 · 采集延迟 {{item.config.delay_seconds}} 秒</p><p>两个任务按批次交接源库读取权。采集优先，每四个采集批次提供一次历史处理机会；只启动一个任务时独立推进。</p><p v-if="!item" class="muted">执行 Agent、每批条数、间隔及采集延迟等待 Server 提供配置，连接就绪后可编辑。</p><p v-if="item">源历史完整保留声明：{{item.config.history_immutable?'已确认':'尚未确认，历史处理不能封存'}}</p><el-button :disabled="!writable" @click="item&&save({...item.config,tasks:{...item.config.tasks,retry_token:retryToken()}})">重试失败日期</el-button></section>
 <el-dialog :model-value="!!form" title="执行设置" @close="form=undefined"><el-form v-if="form" label-width="120px"><el-form-item label="执行 Agent"><el-select v-model="form.agent_id"><el-option v-for="t in item?.targets" :key="t.agent_id" :value="t.agent_id" :label="t.agent_id" @click="form.instance_id=t.instance_id"/></el-select></el-form-item><el-form-item label="每批条数"><el-input-number v-model="form.batch_size" :min="1" :max="5000"/></el-form-item><el-form-item label="间隔（秒）"><el-input-number v-model="form.interval_seconds" :min="2" :max="3600"/></el-form-item><el-form-item label="延迟（秒）"><el-input-number v-model="form.delay_seconds" :min="60" :max="86400"/></el-form-item><el-checkbox v-model="form.history_immutable">确认源历史日志完整保留、已稳定，可用于完整性校验</el-checkbox><p>已有月表最大 ID 不代表此前日志完整。此声明应符合源库实际保留策略。</p></el-form><template #footer><el-button :loading="saving" @click="form&&save(form)">保存</el-button></template></el-dialog>
 </div></AppShell>
</template>
<style scoped>
.archive-jobs{display:grid;gap:16px}header{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}h3{margin:0;font-size:16px}h2{font-size:21px}.task-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px}section{padding:20px;border:1px solid var(--el-border-color-light);border-radius:12px;background:var(--el-bg-color);min-width:0;overflow-wrap:anywhere}p{font-size:13px;line-height:1.8}.muted{color:var(--el-text-color-secondary);font-size:12px}.failure{color:var(--el-color-danger)}.calendar{display:grid;grid-template-columns:repeat(7,minmax(0,1fr));gap:8px}.calendar>span{text-align:center}.calendar button{display:flex;flex-direction:column;align-items:flex-start;gap:8px;padding:12px;min-height:92px;background:var(--el-fill-color-lighter);color:var(--el-text-color-primary);border:1px solid var(--el-border-color);border-radius:8px;cursor:pointer}.calendar .sealed{background:var(--el-color-success-light-9)}.calendar .failed{background:var(--el-color-danger-light-9)}.calendar .processing{background:var(--el-color-primary-light-9)}.detail{margin-top:18px;border-top:1px solid var(--el-border-color);padding-top:16px}@media(max-width:700px){.task-grid{grid-template-columns:1fr}.calendar{gap:4px}.calendar button{padding:6px;font-size:11px}section{padding:12px}}
</style>
