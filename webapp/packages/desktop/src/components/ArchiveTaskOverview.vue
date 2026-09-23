<script setup lang="ts">
import { computed } from 'vue'
import { archiveDiagnosticReason, archiveOperationNames, formatArchiveCount, type ArchiveItem, type ArchiveTask, type ArchiveTaskGroup, type ArchiveCalendarDay } from '../utils/logArchive'
const props = defineProps<{item: ArchiveItem; days: ArchiveCalendarDay[]; month: string; writable: boolean; stale: boolean}>()
const emit = defineEmits<{'toggle-group':[ArchiveTaskGroup]; daily:[]; select:[ArchiveCalendarDay]}>()
const pipeline = computed(() => props.item.status.pipeline)
const groups: {key: ArchiveTaskGroup; title: string; tasks: ArchiveTask[]}[] = [
  {key:'collection',title:'日志采集',tasks:['collection']},
  {key:'history',title:'历史处理',tasks:['migration','organization','verification']},
]
const applied = computed(() => props.item.config.version === props.item.status.applied_version)
function enabled(group: ArchiveTaskGroup) {
  const p=props.item.config.pipeline
  return !!p && (group==='collection' ? p.collection : p.organization || p.verification)
}
function active(tasks: ArchiveTask[]) { return tasks.map(t=>pipeline.value?.active?.[t]).find(Boolean) }
function progress(tasks: ArchiveTask[]) {
  return tasks.map(t=>pipeline.value?.progress?.[t]).filter(p=>!!p).sort((a,b)=>Date.parse(b!.updated_at)-Date.parse(a!.updated_at))[0]
}
function error(tasks: ArchiveTask[]) {
  for(const t of tasks) if(pipeline.value?.errors?.[t]) return pipeline.value.errors[t]
  return undefined
}
function state(group: ArchiveTaskGroup, tasks: ArchiveTask[]) {
  if(props.stale) return '状态待确认'
  if(!applied.value) return '等待配置确认'
  if(error(tasks)) return '异常'
  if(!props.item.config.running) return props.item.status.state==='paused' ? '已暂停' : '等待暂停确认'
  if(!enabled(group)) return '已暂停'
  if(group==='collection' && pipeline.value?.collection_done) return '范围已完成'
  return '运行中'
}
function action(group: ArchiveTaskGroup, tasks: ArchiveTask[]) {
  if(props.stale) return '上报已过期，以下保留最近处理记录'
  if(!applied.value) return '配置已提交，等待 Agent 应用'
  if(!props.item.config.running || !enabled(group)) return '保留已提交进度，恢复后继续'
  if(group==='collection' && pipeline.value?.active?.verification) return '等待历史校验释放源库'
  const a=active(tasks)
  if(a) return a.task==='organization' ? '正在整理已有归档' : a.task==='verification' ? '正在校验与封存' : a.task==='migration' ? '正在准备归档结构' : '正在推进采集批次'
  return '等待下一批处理；以成功提交记录判断进度'
}
const issues=computed(()=>props.days.filter(d=>['blocked','failed','mismatched'].includes(d.kind)))
const sealed=computed(()=>props.days.filter(d=>d.kind==='sealed').length)
const events=computed(()=>groups.flatMap(g=>g.tasks.map(t=>({task:t,title:g.title,...pipeline.value?.progress?.[t]}))).filter(p=>p.updated_at).sort((a,b)=>Date.parse(b.updated_at!)-Date.parse(a.updated_at!)))
function time(value?:string){return value?new Date(value).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai',hour12:false}):'尚未上报'}
</script>
<template>
  <div class="task-overview">
    <div class="task-pair">
      <section v-for="group in groups" :key="group.key" class="task-card" :aria-label="group.title">
        <header><h3>{{ group.title }}</h3><span class="task-badge" :class="{danger:!!error(group.tasks)}">{{ state(group.key,group.tasks) }}</span></header>
        <h4>{{ group.key==='collection' ? (item.status.raw_position?.table ? time(item.status.raw_position.log_time) : item.status.raw_position ? '归档月表暂无日志' : '最新归档位置尚未上报') : active(group.tasks)?.date || '等待可处理日期' }}</h4>
        <p>{{ action(group.key,group.tasks) }}</p>
        <p v-if="group.key==='history'" class="muted">本轮截止：{{ pipeline?.cutoff || '尚未上报' }}</p>
        <template v-else><p v-if="item.status.raw_position?.table" class="muted">归档库最大日志 ID：{{ item.status.raw_position.id }} · {{ item.status.raw_position.table }}</p><p class="muted">{{ item.status.raw_position ? '位置查询时间：' + time(item.status.raw_position.observed_at) : '等待 Agent 上报归档月表实际位置' }}<span v-if="item.status.raw_position_error"> · 查询失败，保留上次结果</span></p></template>
        <div v-if="error(group.tasks)" class="task-error"><b>{{ archiveDiagnosticReason(error(group.tasks)!).reason }}</b><p>{{ archiveDiagnosticReason(error(group.tasks)!).action }}</p></div>
        <footer><span class="muted">{{ group.key==='collection' ? `本模式累计处理 ${formatArchiveCount(progress(group.tasks)?.rows)} 条` : '逐日推进，保留每一天的处理结果' }}</span><el-button :disabled="!writable || !applied" @click="emit('toggle-group',group.key)">{{ item.config.running && enabled(group.key) ? '暂停' : '继续' }}{{ group.title }}</el-button></footer>
        <details><summary>进度与技术详情</summary><p v-if="group.key==='collection'">持续采集进度请结合当前模式查看。已提交游标 ID：{{ progress(group.tasks)?.after_id || '尚未上报' }}。月表位置包含旧流程已归档日志，不代表前面数据完整，也不等于当前采集游标。最近批次提交：{{ time(progress(group.tasks)?.updated_at) }}。累计处理量不等于新增量；当前接口未提供追平边界和新增 / 已存在拆分。</p><p v-else>当前执行器仍使用整理、校验与封存内部阶段；此处合并展示和控制，不代表调度或处理顺序已升级。</p><template v-for="task in group.tasks" :key="task"><p v-if="pipeline?.progress?.[task]">{{ archiveOperationNames[pipeline.progress[task]?.operation?.code || ''] || '等待操作记录' }} · {{ time(pipeline.progress[task]?.updated_at) }}<br>处理表：{{ pipeline.progress[task]?.operation?.table || '—' }}</p><p v-if="pipeline?.progress?.[task]?.diagnostic">错误：{{ pipeline.progress[task]?.diagnostic?.code }} · MySQL {{ pipeline.progress[task]?.diagnostic?.mysql_number || '—' }} / {{ pipeline.progress[task]?.diagnostic?.sql_state || '—' }} · 日志 ID {{ pipeline.progress[task]?.diagnostic?.row_id || '—' }}</p></template></details>
      </section>
    </div>
    <section class="task-card"><header><h3>历史完成情况</h3><el-button link type="primary" @click="emit('daily')">查看每日数据 →</el-button></header><div class="completion"><span>{{ month }} 已封存 <b>{{ sealed }}</b> 天</span><span>处理失败 <b>{{ issues.length }}</b> 天</span><span class="muted">连续封存边界尚未上报</span></div><div v-for="day in issues.slice(0,3)" :key="day.date" class="issue-row"><div><b>{{ day.date }} · 处理失败</b><p>{{ day.reason }}</p></div><el-button @click="emit('select',day)">查看原因</el-button></div><p v-if="!issues.length" class="muted">所选月份的已上报记录中未发现处理失败；未上报日期不代表完成。</p></section>
    <section class="task-card"><header><h3>最近操作</h3><span class="muted">各阶段最近记录 · 北京时间</span></header><div v-for="event in events" :key="event.task" class="event-row"><time>{{ time(event.updated_at) }}</time><div>{{ event.title }} · {{ archiveOperationNames[event.operation?.code || ''] || '已保存批次进度' }}<p v-if="event.operation?.date" class="muted">处理日期：{{ event.operation.date }}</p></div></div><p v-if="!events.length" class="muted">尚未收到批次操作记录。</p></section>
  </div>
</template>
<style scoped>
.task-badge{font-size:12px;padding:3px 8px;border-radius:4px;color:var(--el-color-primary);background:var(--el-color-primary-light-9)}.task-badge.danger{color:var(--el-color-danger);background:var(--el-color-danger-light-9)}.task-overview{display:grid;gap:14px}.task-pair{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.task-card{background:var(--el-bg-color);border:1px solid var(--el-border-color-light);border-radius:10px;padding:18px;min-width:0;overflow-wrap:anywhere}header,footer,.completion,.issue-row{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap}h3{font-size:15px;margin:0}h4{font-size:22px;margin:16px 0 8px}p{font-size:13px;line-height:1.7;margin:8px 0}.muted,time,details{font-size:12px;color:var(--el-text-color-secondary)}footer{border-top:1px solid var(--el-border-color-light);padding-top:14px;margin-top:16px}details{margin-top:12px}summary{cursor:pointer;color:var(--el-color-primary)}.completion{justify-content:flex-start;margin:14px 0}.issue-row{padding:12px;background:var(--el-color-danger-light-9);border-radius:6px;margin-top:8px}.issue-row>div{flex:1;min-width:180px}.issue-row b,.task-error{color:var(--el-color-danger)}.event-row{display:grid;grid-template-columns:170px 1fr;gap:12px;padding:12px 0;border-bottom:1px solid var(--el-border-color-light);font-size:13px}.event-row:last-child{border:0}@media(max-width:700px){.task-pair{grid-template-columns:1fr}.task-card{padding:14px}.event-row{grid-template-columns:1fr;gap:4px}}
</style>
