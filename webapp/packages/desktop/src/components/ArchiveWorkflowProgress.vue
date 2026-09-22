<script setup lang="ts">
import { computed } from 'vue'
import { archiveWorkflowSteps, archiveOperationActivity, archiveOperationNames, archiveDiagnosticReason, archiveWorkflowLabels, archivePreparationActivity, formatArchiveCount, type ArchiveItem } from '../utils/logArchive'
const props = defineProps<{ item: ArchiveItem; now: number; readFailed: boolean }>()
const steps = computed(() => archiveWorkflowSteps(props.item))
const operation = computed(() => props.item.status.operation)
const diagnostic = computed(() => props.item.status.error ? props.item.status.diagnostic : undefined)
const errorCode = computed(() => props.item.status.error ? diagnostic.value?.code || props.item.status.error : props.item.status.workflow?.error_code)
const explanation = computed(() => errorCode.value ? archiveDiagnosticReason(errorCode.value) : undefined)
const databases: Record<string, string> = {source:'源数据库', archive:'归档数据库', both:'源库与归档库', control:'控制服务'}
const time = (value?: string) => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString('zh-CN', {hour12:false}) : '尚未上报'
const taskPhase = computed(() => props.item.status.workflow?.phase || '')
const preparation = computed(() => archivePreparationActivity(props.item, props.now, props.readFailed))
const sourceUse = computed(() => ['reset_state','reset_daily','reset_monthly','import_target'].includes(taskPhase.value)
  ? '本阶段不扫描源库历史日志。接入过程还要重建统计，完成后才能进入按日归档。'
  : taskPhase.value === 'verify' ? '本阶段比较归档库与本轮已保存的源证据，不重新扫描源日志。'
  : taskPhase.value === 'seal' ? '本阶段在归档库构建和审计日期版本。'
  : '按日归档会读取源日志；当天未结束或仍在延迟窗口内的数据需稍后接续。')
</script>

<template>
  <section class="panel archive-flow" aria-label="归档流程与具体操作">
    <div class="flow-heading"><h3>归档流程</h3><span>最近状态上报：{{ time(item.seen_at) }}</span></div>
    <ol class="flow-steps">
      <li v-for="(step, index) in steps" :key="step.title" :class="{current:step.current, passed:step.passed}" :aria-current="step.current ? 'step' : undefined">
        <span class="step-number">{{ index + 1 }}</span><div><b>{{ step.title }}</b><small>{{ step.detail }}</small><em v-if="step.current">最近上报阶段</em><em v-else-if="step.passed">已越过准备环节</em></div>
      </li>
    </ol>
    <p class="flow-note">前 3 步准备已有归档；之后按日期循环「归档 → 校验 → 封存」。某一天封存不代表全部历史已完成。</p>
    <div class="operation-card" role="status">
      <div class="operation-caption">{{ archiveOperationActivity(item, now, readFailed) }}</div>
      <h4>{{ operation ? archiveOperationNames[operation.code] || `未识别操作（${operation.code}）` : archiveWorkflowLabels[taskPhase] || '等待阶段信息' }}</h4>
      <p v-if="operation" class="operation-context">{{ databases[operation.database] }}<template v-if="operation.table"> · <code>{{ operation.table }}</code></template><template v-if="operation.date"> · 日期 {{ operation.date }}</template></p>
      <p v-else-if="item.status.workflow?.preparation?.table" class="operation-context">最近成功批次处理表：<code>{{ item.status.workflow.preparation.table }}</code></p>
      <p>{{ sourceUse }}</p>
      <div v-if="operation" class="operation-time"><span>操作开始：{{ time(operation.started_at) }}</span><span>{{ operation.finished_at ? `操作返回：${time(operation.finished_at)}` : '尚未收到该操作的返回记录' }}</span></div>
      <small>操作上报与成功提交分开记录；页面刷新不会启动新任务。</small>
    </div>
    <div v-if="preparation" class="preparation-progress">
      <p>{{ preparation.message }}</p>
      <div class="preparation-counters"><span>{{ preparation.progress && preparation.progress.phase !== taskPhase ? '上一阶段' : '本阶段' }}已记录处理 <b>{{ formatArchiveCount(preparation.progress?.processed_rows) }}</b> 条</span><span>最近一批 <b>{{ formatArchiveCount(preparation.progress?.last_batch_rows) }}</b> 条</span><span>已提交 <b>{{ formatArchiveCount(preparation.progress?.committed_batches) }}</b> 批</span></div>
      <p v-if="preparation.progress">最近成功提交：{{ time(preparation.progress.last_committed_at) }} · {{ archiveWorkflowLabels[preparation.progress.phase] }} · <code>{{ preparation.progress.table || '已到表尾' }}</code><template v-if="preparation.progress.phase==='import_target'"> · 已读 ID {{ preparation.progress.after_id }}</template></p>
      <small>计数从 {{ time(preparation.progress?.recorded_since) }} 起记录；只有成功提交才累计，升级前处理量不追溯。没有全量分母，不估算百分比。</small>
    </div>
    <div v-if="explanation" class="diagnostic-card" role="alert">
      <h4>{{ explanation.reason }}</h4>
      <p v-if="diagnostic?.operation">发生位置：{{ archiveWorkflowLabels[diagnostic.operation.phase] || diagnostic.operation.phase }} → {{ archiveOperationNames[diagnostic.operation.code] || diagnostic.operation.code }} · {{ databases[diagnostic.operation.database] }}<code v-if="diagnostic.operation.table"> / {{ diagnostic.operation.table }}</code><template v-if="diagnostic.operation.date"> · {{ diagnostic.operation.date }}</template></p>
      <p>处理建议：{{ explanation.action }}</p>
      <div class="diagnostic-meta"><span>错误代码：{{ errorCode }}</span><span v-if="diagnostic?.mysql_number">MySQL {{ diagnostic.mysql_number }}<template v-if="diagnostic.sql_state"> / SQLSTATE {{ diagnostic.sql_state }}</template></span><span v-if="diagnostic?.row_id && diagnostic.row_id !== '0'">日志 ID {{ diagnostic.row_id }}</span><span v-if="diagnostic?.row_bytes && diagnostic.row_bytes !== '0'">该行 {{ diagnostic.row_bytes }} 字节</span></div>
      <p v-if="diagnostic">发生于 {{ time(diagnostic.occurred_at) }}<template v-if="diagnostic.retry_at">；{{ item.config.running ? '计划重试于' : '暂停前计划重试于' }} {{ time(diagnostic.retry_at) }}（需有效授权，暂停时不执行）</template></p>
      <p v-else>当前上报没有数据库编号和具体操作，无法进一步判断。升级后的 Agent 会保留这些诊断字段。</p>
    </div>
  </section>
</template>

<style scoped>
.archive-flow{padding:22px;border:1px solid var(--el-border-color-light);border-radius:12px;background:var(--el-bg-color);margin-bottom:16px}
.flow-heading{display:flex;justify-content:space-between;gap:12px;flex-wrap:wrap}.flow-heading h3{margin:0}.flow-heading>span,.flow-note{color:var(--el-text-color-secondary);font-size:12px}
.flow-steps{padding:0;list-style:none;display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:8px;margin:20px 0 12px}.flow-steps li{padding:12px 8px;display:flex;gap:8px;border:1px solid var(--el-border-color-light);border-radius:8px;min-width:0}.flow-steps b{font-size:13px}.flow-steps small,.flow-steps em{display:block;font-size:11px;font-style:normal;margin-top:6px;line-height:1.6;color:var(--el-text-color-secondary)}.step-number{flex:0 0 22px;width:22px;height:22px;border-radius:50%;background:var(--el-fill-color);text-align:center;line-height:22px;font-size:12px}.flow-steps .current{background:var(--el-color-primary-light-9);border-color:var(--el-color-primary)}.current .step-number{background:var(--el-color-primary);color:white}.current em{color:var(--el-color-primary)}.passed .step-number{background:var(--el-color-success-light-9);color:var(--el-color-success)}
.operation-card,.diagnostic-card{padding:16px;border-radius:8px;margin-top:14px;background:var(--el-fill-color-lighter);font-size:13px;line-height:1.7;overflow-wrap:anywhere}.operation-caption{font-size:12px;color:var(--el-text-color-secondary)}h4{font-size:16px;margin:4px 0 8px}p{margin:8px 0}.operation-time,.diagnostic-meta{display:flex;gap:8px 24px;flex-wrap:wrap;font-size:12px}.operation-card small{color:var(--el-text-color-secondary)}.diagnostic-card{background:var(--el-color-danger-light-9);border-left:3px solid var(--el-color-danger)}.diagnostic-meta{margin-top:10px}.operation-context code,code{font-size:12px}
.preparation-progress{font-size:12px;color:var(--el-text-color-secondary);line-height:1.8;margin-top:16px}.preparation-counters{display:flex;gap:12px 24px;flex-wrap:wrap}.preparation-counters b{font-size:18px;color:var(--el-text-color-primary)}
@media(max-width:1150px){.flow-steps{grid-template-columns:repeat(3,minmax(0,1fr))}}@media(max-width:650px){.flow-steps{grid-template-columns:repeat(2,minmax(0,1fr))}.archive-flow{padding:14px}}
</style>
