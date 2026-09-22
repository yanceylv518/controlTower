<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { archiveDiagnosticReason, archiveOperationNames, formatArchiveCount, type ArchiveItem, type ArchiveTask, type ArchivePipelineSettings } from '../utils/logArchive'
const props = defineProps<{item: ArchiveItem; supported: boolean; writable: boolean; stale: boolean; mode?: 'overview' | 'manage'}>()
const emit = defineEmits<{save: [ArchivePipelineSettings]; 'master-toggle': []}>()
const tasks: {key: ArchiveTask; title: string; description: string; next: string}[] = [
  {key:'migration', title:'1 · 版本迁移', description:'准备归档结构、清理旧统计台账。已有月表日志保留，完成后不重复清理。', next:'完成后开放历史整理；日志采集可同时进行。'},
  {key:'organization', title:'2 · 历史整理', description:'只读归档月表，逐日重建贡献记录与日、月统计。', next:'一天整理完成后交给校验，记录结果后再处理下一天。'},
  {key:'verification', title:'3 · 校验与封存', description:'补齐并核对当天日志，生成封存版本，或记录具体受阻原因。', next:'整个任务执行期间，日志采集等待；日期受阻会继续下一天。'},
  {key:'collection', title:'4 · 日志采集', description:'使用独立游标读取源日志，写入对应月表，不等待历史逐日整理。', next:'可与迁移、整理并行；迟到写入自动使日期结果失效，等待重整。'},
]
const managing = computed(() => props.mode !== 'overview')
const status = computed(()=>props.item.status.pipeline)
const settings = computed(()=>props.item.config.pipeline)
const activationReason = computed(()=> !props.supported ? '需先升级 Server 和 Agent，才能使用新的归档流程。' : props.item.config.running || props.item.status.state!=='paused' ? '请先暂停归档并等待 Agent 确认，再升级归档流程。' : ['backfill','verify','seal'].includes(props.item.status.workflow?.phase || '') ? '当前日期正在补齐、校验或封存；请先让当前日期处理结束，再暂停并升级归档流程。' : '')
const range=ref<string[]>([]), newest=ref(false)
watch(()=>props.item.config.pipeline, value=> {range.value=value?.collection_from ? [value.collection_from,value.collection_through || ''] : [];newest.value=!!value?.collection_newest_first},{immediate:true})
function changeRange() {if(settings.value) emit('save',{...settings.value, collection_from:range.value?.[0] || '',collection_through:range.value?.[1] || '',collection_newest_first:!!range.value?.length && newest.value})}
function retryFailed() {if(settings.value) emit('save',{...settings.value,retry_token:crypto.randomUUID().replaceAll('-','')})}
function toggle(task: ArchiveTask) { if (settings.value) emit('save',{...settings.value,[task]:!settings.value[task]}) }
function label(task: ArchiveTask) {
  if (props.stale) return '上报过期 · 当前执行未知'
  if (props.item.config.version!==props.item.status.applied_version) return '等待 Agent 应用配置'
  if (task==='collection' && status.value?.collection_done) return '所选范围已采集完成'
  if (task==='migration' && status.value?.migration_done) return '已完成 · 不重复执行'
  if (!props.item.config.running) return '整体已暂停 / 等待暂停确认'
  if (!settings.value?.[task]) return '已停用'
  if (status.value?.errors?.[task]) return '异常 · 等待重试'
  if (status.value?.active?.[task]) return `处理中${status.value.active[task]?.date ? ' · '+status.value.active[task]?.date : ''}`
  if (task==='collection' && status.value?.active?.verification) return '等待校验任务释放源库'
  if ((task==='organization'||task==='verification') && !status.value?.migration_done) return '等待版本迁移'
  return '已启用 · 等待可处理批次'
}
function liveOperation(task: ArchiveTask) {
 const op=props.item.status.operation
 const phases:Record<ArchiveTask,string[]>={migration:['migration','reset_state','reset_daily','reset_monthly'],organization:['organization'],verification:['backfill','verify','seal','verification'],collection:['collection']}
 return !props.stale && props.item.config.running && settings.value?.[task] && op?.state==='executing' && phases[task].includes(op.phase) ? op : undefined
}
function display(value?:string) {return value ? new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}) : '尚未上报'}
</script>

<template>
  <section class="pipeline-panel" :class="{ managing }" aria-label="日志归档任务">
    <header><div><h3>{{ managing ? '任务控制' : '任务运行情况' }}</h3><p>采集独立推进；历史整理与校验按天衔接。本轮历史截止：{{ status?.cutoff || '启动时确定为昨天' }}。</p></div><el-button v-if="settings && managing" :disabled="!writable" @click="emit('master-toggle')">{{ item.config.running ? '整体暂停' : '继续已启用任务' }}</el-button></header>
    <div v-if="!managing && item.status.diagnostic" class="task-error"><b>{{ archiveDiagnosticReason(item.status.diagnostic.code).reason }}</b><p>{{ archiveDiagnosticReason(item.status.diagnostic.code).action }}</p><code>{{ item.status.diagnostic.code }} · {{ item.status.diagnostic.operation?.table || '无表信息' }} · MySQL {{ item.status.diagnostic.mysql_number || '—' }} / {{ item.status.diagnostic.sql_state || '—' }}</code><p v-if="item.status.diagnostic.retry_at">计划重试：{{ display(item.status.diagnostic.retry_at) }}</p></div>
    <template v-if="!settings && managing"><p>{{ activationReason || '升级后保留已提交进度和原始日志，可分别管理版本迁移、历史整理、校验与封存、日志采集。' }}</p><el-button type="primary" :disabled="!writable || !!activationReason" @click="emit('save',{migration:true,organization:true,verification:true,collection:true})">升级归档流程</el-button></template>
    <div v-else-if="settings">
      <div v-if="managing" class="range-controls"><b>采集范围</b><p>不选择日期时，按独立 ID 游标持续采集全部日志。指定范围使用单独进度，不改变持续采集的游标；更改范围需先整体暂停并等待确认。</p><el-date-picker v-model="range" type="daterange" value-format="YYYY-MM-DD" start-placeholder="开始日期" end-placeholder="结束日期" :disabled="!writable || item.config.running" /><el-checkbox v-model="newest" :disabled="!writable || item.config.running || !range?.length">范围内先采集较新日期</el-checkbox><el-button :disabled="!writable || item.config.running" @click="changeRange">保存范围并启动</el-button><p v-if="status?.collection_date">采集当前日期：{{ status.collection_date }}</p><el-button :disabled="!writable" @click="retryFailed">重新排队受阻日期</el-button></div>
      <div class="task-grid">
      <article v-for="task in tasks" :key="task.key">
        <div class="task-heading"><h4>{{ task.title }}</h4><el-button v-if="managing" size="small" :disabled="!writable || task.key==='migration' && status?.migration_done" @click="toggle(task.key)">{{ settings[task.key] ? '停用' : '启用' }}</el-button></div>
        <b class="task-state">{{ label(task.key) }}</b><template v-if="managing"><p>{{ task.description }}</p><p>{{ task.next }}</p></template>
        <template v-else>
        <p v-if="liveOperation(task.key)"><b>最近上报正在执行：</b>{{ archiveOperationNames[liveOperation(task.key)!.code] || liveOperation(task.key)!.code }}<span v-if="liveOperation(task.key)?.table">（{{ liveOperation(task.key)?.table }}）</span> · {{ display(liveOperation(task.key)?.started_at) }}</p>
        <p v-if="task.key==='migration' && !status?.migration_done && item.status.workflow?.preparation">已提交清理 {{ formatArchiveCount(item.status.workflow.preparation.processed_rows) }} 条 · {{ formatArchiveCount(item.status.workflow.preparation.committed_batches) }} 批；最近批次 {{ display(item.status.workflow.preparation.last_committed_at) }}</p>
        <template v-if="status?.progress?.[task.key]">
          <p>最近操作：{{ archiveOperationNames[status.progress[task.key]?.operation?.code || ''] || status.progress[task.key]?.operation?.code || '等待记录' }}<span v-if="status.progress[task.key]?.operation?.table">（{{ status.progress[task.key]?.operation?.table }}）</span></p>
          <p>批次记录：{{ display(status.progress[task.key]?.updated_at) }}<template v-if="task.key==='collection'"> · 已提交 ID {{ status.progress[task.key]?.after_id }} · 本模式累计 {{ formatArchiveCount(status.progress[task.key]?.rows) }} 条</template></p>
          <div v-if="status.progress[task.key]?.diagnostic" class="task-error"><b>{{ archiveDiagnosticReason(status.progress[task.key]!.diagnostic!.code).reason }}</b><p>{{ archiveDiagnosticReason(status.progress[task.key]!.diagnostic!.code).action }}</p><code>{{ status.progress[task.key]?.diagnostic?.code }} · MySQL {{ status.progress[task.key]?.diagnostic?.mysql_number || '—' }} · 日志 ID {{ status.progress[task.key]?.diagnostic?.row_id || '—' }}</code></div>
        </template>
        </template>
      </article>
    </div>
    </div>
    <p v-if="settings && managing" class="footnote">开关在当前批次结束后生效；已发起的在线建索引可能继续完成，但不会因此启动日志数据批次。“处理中”表示任务持有处理位置，不表示这一刻正在执行 SQL。已有月表数量显示在日历中，与整理中的统计分开。封存需要源历史保留声明，迟到日志会触发新一轮处理。</p>
  </section>
</template>

<style scoped>
.managing .task-grid{grid-template-columns:minmax(0,1fr)}.managing .task-grid article{padding:14px 16px}.managing .task-state{margin-top:6px}.pipeline-panel{padding:20px;background:var(--el-bg-color);border:1px solid var(--el-border-color-light);border-radius:12px;margin-bottom:16px}header{display:flex;justify-content:space-between;align-items:center;gap:12px;flex-wrap:wrap}h3,h4{margin:0}p{font-size:13px;color:var(--el-text-color-secondary);line-height:1.7;margin:9px 0}.task-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px;margin-top:16px}.task-grid article{padding:16px;border:1px solid var(--el-border-color-light);border-radius:8px;min-width:0;overflow-wrap:anywhere}.task-heading{display:flex;justify-content:space-between;align-items:center;gap:12px}.task-state{display:block;margin-top:12px;font-size:13px;color:var(--el-color-primary)}.task-error{background:var(--el-color-danger-light-9);padding:12px;border-radius:6px;font-size:12px;color:var(--el-color-danger)}.range-controls{margin-top:14px;padding:14px;background:var(--el-fill-color-lighter);border-radius:8px}.range-controls :deep(.el-date-editor){max-width:100%}.footnote{margin-bottom:0}@media(max-width:700px){.task-grid{grid-template-columns:minmax(0,1fr)}.pipeline-panel{padding:14px}}
</style>
