<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ApiError, type ChannelSchedule, type ChannelScheduleConfig, type ChannelScheduleExecution, type ChannelSchedulePreview, type TuningChannel, type TuningContinuousState } from '@ct/shared'
import { dashboard } from '../api'
import { formatBeijingTime } from '../utils/format'
import { effectiveWindows, scheduleFollowup, scheduleResult, scheduleTime, scheduleTone, weekStrip } from '../utils/channelSchedule'

const props = defineProps<{ modelValue: boolean; site: string; channel: TuningChannel | null; state?: TuningContinuousState }>()
const emit = defineEmits<{ 'update:modelValue': [boolean]; saved: [string] }>()
const open = computed({ get: () => props.modelValue, set: (value: boolean) => emit('update:modelValue', value) })
const saving = ref(false), loading = ref(false), ready = ref(false), direct = ref(false), worker = ref(false)
const error = ref(''), runtimeError = ref(''), historyError = ref(''), previewError = ref(''), savedNotice = ref('')
const conflict = ref(false), uncertain = ref(false)
// 窄屏下两栏折成页签；宽屏忽略该值，两栏同时显示。
const pane = ref<'config' | 'status'>('config')
const plan = ref<ChannelSchedule | null>(null), history = ref<ChannelScheduleExecution[]>([])
const preview = ref<ChannelSchedulePreview | null>(null), previewLoading = ref(false)
const form = reactive<ChannelScheduleConfig>({ enabled: true, days: [1, 2, 3, 4, 5, 6, 7], windows: [{ start: '00:00', end: '08:00' }], revision: 0, align_now: false })
const days = ['周一', '周二', '周三', '周四', '周五', '周六', '周日']
const formElement = ref<HTMLElement | null>(null), errors = reactive({ days: '', windows: [] as string[] })
let disposed = false, baseline = '', generation = 0, loadedSite = '', loadedChannel = 0
let contextAbort: AbortController | undefined, historyAbort: AbortController | undefined
let previewAbort: AbortController | undefined, mutationAbort: AbortController | undefined
let poll: ReturnType<typeof setInterval> | undefined, previewTimer: ReturnType<typeof setTimeout> | undefined
let leavePrompt: Promise<boolean> | undefined
const configKey = (value: ChannelScheduleConfig) => JSON.stringify({ enabled: value.enabled, days: [...value.days].sort((a, b) => a - b), windows: value.windows })
const dirty = computed(() => configKey(form) !== baseline || form.enabled && form.align_now)
const hasChanges = computed(() => !plan.value || dirty.value)
const needsAlignment = computed(() => form.enabled && !!preview.value && !preview.value.next_at && !form.align_now)
const currentStatus = computed(() => {
  const status = String(props.channel?.status || '').trim().toLowerCase()
  if (['1', 'enabled', 'enable', 'active', 'normal'].includes(status)) return '已启用'
  if (!['2', '3', 'disabled', 'disable', 'inactive', 'auto_disabled', 'auto-disabled'].includes(status)) return '状态未知'
  if (props.state?.paused_reason === 'manual_disabled') return '手动关闭'
  if (!ready.value || runtimeError.value || error.value) return '关闭来源待确认'
  if (plan.value?.closed_by_schedule) return '定时关闭'
  return props.state?.circuit_disabled && props.state.circuit_status_target !== 2 && props.state.circuit_status_target !== 3 ? '自动关闭' : '手动关闭'
})
const circuit = computed(() => props.state?.circuit_disabled || ['circuit', 'probing'].includes(props.state?.phase || ''))
const effectText = computed(() => {
  if (!form.enabled) return '停用计划，保持当前开关'
  if (!preview.value) return ''
  if (form.align_now) return circuit.value && preview.value.allowed ? '熔断中，暂不强开' : '保存后尝试' + (preview.value.allowed ? '开启' : '关闭')
  return preview.value.next_at ? scheduleTime(preview.value.next_at) + ' 尝试' + (preview.value.next_status === 1 ? '开启' : '关闭') : '无下一时点，请选择同步当前状态'
})
const nextText = computed(() => plan.value?.enabled && plan.value.next_at ? `下次 ${scheduleTime(plan.value.next_at)} ${plan.value.next_status === 1 ? '开启' : '关闭'}` : '')
// 与保存按钮的禁用条件对应，直接说明还差哪一步。
const saveBlocker = computed(() => {
  if (!ready.value || loading.value || saving.value) return ''
  if (conflict.value) return '计划已被更新，请先核对修改'
  if (uncertain.value) return '上次结果未确认，请重新加载'
  if (form.revision > 0 && !form.expected_updated_at) return '正在同步最新计划'
  if (!hasChanges.value) return '没有修改'
  if (!form.enabled) return ''
  if (!direct.value) return '未配置直连控制，无法启用计划'
  if (!form.days.length || form.windows.some(window => !window.start || !window.end || window.start === window.end)) return '请先完善星期和时段'
  if (previewError.value) return '时段预览失败，请重试'
  if (previewLoading.value || !preview.value) return '正在计算时段'
  if (needsAlignment.value) return '没有下一时点，请选择“同步当前状态”'
  return ''
})
const matches = (site: string, channel: number, version = generation) => !disposed && props.modelValue && loadedSite === site && loadedChannel === channel && version === generation
function message(cause: unknown) {
  if (cause instanceof Error && cause.name === 'AbortError') return '请求超时，请重试'
  if (cause instanceof ApiError) {
    const labels: Record<string, string> = {
      schedule_changed: '计划已更新，请核对修改', channel_control_busy: '渠道正在处理其他操作，请稍后再试',
      schedule_requires_direct_control: '本站点未配置直连控制', schedule_limit_reached: '本站点已达到200个计划',
      schedule_channel_not_found: '渠道不存在，请刷新渠道目录', schedule_query_failed: '计划读取失败',
      schedule_history_query_failed: '执行记录读取失败', schedule_update_failed: '保存失败，请稍后重试',
      invalid_schedule: typeof cause.details.message === 'string' ? cause.details.message : '时段无效',
      permission_denied: '没有计划管理权限', forbidden: '没有计划管理权限',
    }
    return labels[cause.code] || '操作失败，请重试'
  }
  return '连接失败，请重试'
}
function applyConfig(value: ChannelSchedule | null) {
  Object.assign(form, { enabled: value?.enabled ?? true, days: [...(value?.days || [1, 2, 3, 4, 5, 6, 7])], windows: (value?.windows || [{ start: '00:00', end: '08:00' }]).map(window => ({ ...window })), revision: value?.revision || 0, align_now: false, expected_updated_at: value?.updated_at })
  baseline = configKey(form); conflict.value = false; uncertain.value = false; error.value = ''
  errors.days = ''; errors.windows = []
}
async function refreshContext(reset = false) {
  if (contextAbort || saving.value || !props.modelValue) return
  const site = loadedSite, channel = loadedChannel, version = generation
  const request = new AbortController(); contextAbort = request
  const timeout = setTimeout(() => request.abort(), 12000)
  if (reset) loading.value = true
  try {
    const result = await dashboard.channelSchedules(site, request.signal)
    if (!matches(site, channel, version) || contextAbort !== request) return
    const incoming = result.items.find(item => item.channel_id === channel) || null
    direct.value = result.direct_configured; worker.value = result.worker_enabled; runtimeError.value = ''
    if (reset) { plan.value = incoming; applyConfig(incoming); ready.value = true }
    else {
      if ((incoming?.revision || 0) !== form.revision || form.expected_updated_at && incoming?.updated_at !== form.expected_updated_at) {
        if (!dirty.value && !uncertain.value) applyConfig(incoming)
        else conflict.value = true
      } else if (incoming && !form.expected_updated_at) {
        if (configKey(incoming) === baseline) form.expected_updated_at = incoming.updated_at
        else conflict.value = true
      }
      plan.value = incoming
    }
  } catch (cause) {
    if (matches(site, channel, version) && contextAbort === request) { if (reset) error.value = message(cause); else runtimeError.value = '状态刷新失败' }
  } finally {
    clearTimeout(timeout)
    if (contextAbort === request) { contextAbort = undefined; if (reset) loading.value = false }
  }
}
async function refreshHistory() {
  if (historyAbort || !props.modelValue) return
  const site = loadedSite, channel = loadedChannel, version = generation
  const request = new AbortController(); historyAbort = request
  const timeout = setTimeout(() => request.abort(), 12000)
  try {
    const result = await dashboard.channelScheduleExecutions(site, channel, request.signal)
    if (matches(site, channel, version) && historyAbort === request) { history.value = result.items; historyError.value = '' }
  } catch { if (matches(site, channel, version) && historyAbort === request) historyError.value = '执行记录读取失败' }
  finally { clearTimeout(timeout); if (historyAbort === request) historyAbort = undefined }
}
function validDraft(report: boolean) {
  const clock = /^(?:[01]\d|2[0-3]):[0-5]\d$/
  const invalid = form.windows.map(window => !clock.test(window.start) || !clock.test(window.end) ? '请选择完整时段' : window.start === window.end ? '开始与结束不能相同' : '')
  if (report) { errors.days = form.days.length ? '' : '请选择星期'; errors.windows = invalid }
  return form.days.length > 0 && form.windows.length > 0 && invalid.every(value => !value)
}
async function refreshPreview() {
  if (previewAbort) return
  if (!ready.value || !validDraft(false) || !props.modelValue) { preview.value = null; previewLoading.value = false; return }
  const request = new AbortController(); previewAbort = request
  const key = configKey(form), site = loadedSite, channel = loadedChannel, version = generation
  const timeout = setTimeout(() => request.abort(), 12000)
  previewLoading.value = true; previewError.value = ''
  try {
    const result = await dashboard.previewChannelSchedule(site, { ...form, days: [...form.days], windows: form.windows.map(window => ({ ...window })), align_now: false }, request.signal)
    if (matches(site, channel, version) && previewAbort === request && key === configKey(form)) preview.value = result
  } catch { if (matches(site, channel, version) && previewAbort === request) { preview.value = null; previewError.value = '时段预览失败' } }
  finally { clearTimeout(timeout); if (previewAbort === request) { previewAbort = undefined; previewLoading.value = false } }
}
watch(() => [configKey(form), ready.value], () => {
  clearTimeout(previewTimer); previewAbort?.abort(); previewAbort = undefined; preview.value = null; previewError.value = ''
  previewLoading.value = ready.value && validDraft(false)
  previewTimer = setTimeout(() => void refreshPreview(), 250)
})
async function confirmLeave() {
  if (saving.value) return false
  if (!props.modelValue || !ready.value || !dirty.value) return true
  if (leavePrompt) return leavePrompt
  const version = generation
  const prompt = ElMessageBox.confirm('放弃未保存的修改？', '未保存修改', { confirmButtonText: '放弃修改', cancelButtonText: '继续编辑', type: 'warning' }).then(() => version === generation && !disposed && !saving.value, () => false)
  leavePrompt = prompt
  try { return await prompt } finally { if (leavePrompt === prompt) leavePrompt = undefined }
}
defineExpose({ confirmLeave })
onBeforeRouteLeave(confirmLeave)
function beforeUnload(event: BeforeUnloadEvent) {
  if (props.modelValue && (ready.value && dirty.value || saving.value)) { event.preventDefault(); event.returnValue = '' }
}
async function close(done?: () => void) {
  const version = generation
  if (await confirmLeave() && version === generation && !disposed) { emit('update:modelValue', false); done?.() }
}
async function reload() {
  const version = generation
  if (!await confirmLeave() || version !== generation || disposed) return
  contextAbort?.abort(); contextAbort = undefined
  await refreshContext(true); void refreshHistory()
}
async function keepDraft() {
  if (saving.value) return
  const revision = plan.value?.revision || 0, updatedAt = plan.value?.updated_at, version = generation
  try { await ElMessageBox.confirm(plan.value ? '用当前修改覆盖最新计划？' : '计划已删除，重新创建？', '核对修改', { confirmButtonText: '采用当前修改', cancelButtonText: '取消', type: 'warning' }) } catch { return }
  if (version !== generation || revision !== (plan.value?.revision || 0) || updatedAt !== plan.value?.updated_at) return
  form.revision = revision; form.expected_updated_at = updatedAt; conflict.value = false
}
function setDays(value: number[]) { form.days = [...value]; errors.days = '' }
function changeWindow() { errors.windows = []; savedNotice.value = '' }
async function focusError() {
  await nextTick()
  formElement.value?.querySelector<HTMLElement>('.is-error input, .is-error button, .is-error [tabindex="0"]')?.focus()
}
async function save() {
  if (saving.value || !ready.value || !hasChanges.value || conflict.value || uncertain.value || needsAlignment.value || form.revision > 0 && !form.expected_updated_at) return
  if (!validDraft(true)) { pane.value = 'config'; await focusError(); return }
  if (form.enabled && (previewLoading.value || !preview.value || !direct.value)) return
  const site = loadedSite, channel = loadedChannel, version = generation, key = configKey(form)
  const value: ChannelScheduleConfig = { ...form, days: [...form.days], windows: form.windows.map(window => ({ ...window })), align_now: form.enabled && form.align_now }
  if (value.align_now) {
    try { await ElMessageBox.confirm(effectText.value + '？', '同步当前状态', { confirmButtonText: '保存并同步', cancelButtonText: '取消', type: 'warning' }) } catch { return }
  }
  if (!matches(site, channel, version) || saving.value || conflict.value || uncertain.value ||
      key !== configKey(form) || value.revision !== form.revision || value.expected_updated_at !== form.expected_updated_at ||
      value.align_now !== (form.enabled && form.align_now) || value.enabled && (!direct.value || previewLoading.value || !preview.value)) return
  saving.value = true; error.value = ''; savedNotice.value = ''
  contextAbort?.abort(); contextAbort = undefined
  const request = new AbortController(); mutationAbort = request
  const timeout = setTimeout(() => request.abort(), 15000)
  try {
    const result = await dashboard.saveChannelSchedule(site, channel, value, request.signal)
    if (!matches(site, channel, version) || mutationAbort !== request) return
    form.revision = result.revision; form.expected_updated_at = undefined; form.align_now = false; baseline = configKey(form)
    savedNotice.value = result.changed ? '计划已保存' : '计划未变更'; emit('saved', site)
  } catch (cause) {
    if (matches(site, channel, version) && mutationAbort === request) {
      error.value = message(cause)
      conflict.value = cause instanceof ApiError && cause.code === 'schedule_changed'
      uncertain.value = !(cause instanceof ApiError)
      if (uncertain.value) error.value = '保存结果未确认，请重新加载'
    }
  } finally {
    clearTimeout(timeout)
    if (mutationAbort === request) {
      mutationAbort = undefined; saving.value = false
      if (matches(site, channel, version)) { void refreshContext(); void refreshHistory() }
    }
  }
}
async function remove() {
  if (saving.value || !plan.value || uncertain.value) return
  const site = loadedSite, channel = loadedChannel, revision = plan.value.revision, updatedAt = plan.value.updated_at, version = generation
  try { await ElMessageBox.confirm('删除计划，保持当前开关？', '删除计划', { type: 'warning', confirmButtonText: '删除计划', cancelButtonText: '取消' }) } catch { return }
  if (!matches(site, channel, version) || saving.value || uncertain.value || revision !== plan.value?.revision || updatedAt !== plan.value?.updated_at) return
  saving.value = true; error.value = ''
  contextAbort?.abort(); contextAbort = undefined
  const request = new AbortController(); mutationAbort = request
  const timeout = setTimeout(() => request.abort(), 15000)
  try {
    await dashboard.deleteChannelSchedule(site, channel, revision, request.signal, updatedAt)
    if (matches(site, channel, version) && mutationAbort === request) { form.align_now = false; baseline = configKey(form); emit('saved', site); emit('update:modelValue', false); ElMessage.success('计划已删除') }
  } catch (cause) {
    if (matches(site, channel, version) && mutationAbort === request) {
      error.value = message(cause); conflict.value = cause instanceof ApiError && cause.code === 'schedule_changed'; uncertain.value = !(cause instanceof ApiError)
      if (uncertain.value) error.value = '删除结果未确认，请重新加载'
    }
  } finally {
    clearTimeout(timeout)
    if (mutationAbort === request) {
      mutationAbort = undefined; saving.value = false
      if (matches(site, channel, version)) { void refreshContext(); void refreshHistory() }
    }
  }
}
function stopRequests() {
  generation++; contextAbort?.abort(); historyAbort?.abort(); previewAbort?.abort(); mutationAbort?.abort()
  contextAbort = undefined; historyAbort = undefined; previewAbort = undefined; mutationAbort = undefined
  clearInterval(poll); clearTimeout(previewTimer); leavePrompt = undefined
}
watch([() => props.modelValue, () => props.site, () => props.channel?.channel_id], () => {
  stopRequests(); ready.value = false; plan.value = null; history.value = []; preview.value = null
  saving.value = false; loading.value = false; previewLoading.value = false; conflict.value = false; uncertain.value = false; direct.value = false; worker.value = false
  errors.days = ''; errors.windows = []; pane.value = 'config'
  error.value = ''; runtimeError.value = ''; historyError.value = ''; previewError.value = ''; savedNotice.value = ''
  loadedSite = props.site; loadedChannel = props.channel?.channel_id || 0
  if (!props.modelValue || !loadedSite || !loadedChannel) return
  void refreshContext(true); void refreshHistory()
  poll = setInterval(() => { if (!document.hidden) { void refreshContext(); void refreshHistory(); void refreshPreview() } }, 10000)
}, { immediate: true })
window.addEventListener('beforeunload', beforeUnload)
onBeforeUnmount(() => { disposed = true; stopRequests(); window.removeEventListener('beforeunload', beforeUnload) })
</script>

<template>
  <el-dialog v-model="open" :title="`定时启停 · ${channel?.channel_name || `#${channel?.channel_id || ''}`}`" width="min(1040px, calc(100vw - 24px))" top="5vh" :before-close="close" :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" class="channel-schedule-dialog" append-to-body>
    <div v-loading="loading" class="schedule-body">
      <div v-if="error" class="schedule-notice is-error" role="alert">{{ error }} <el-button link :disabled="saving" @click="reload">重新加载</el-button></div>
      <div v-if="savedNotice" class="schedule-notice is-success" role="status">{{ savedNotice }}</div>
      <template v-if="ready">
        <div class="schedule-context" aria-live="polite">
          <el-tag>{{ currentStatus }}</el-tag><span v-if="plan?.enabled">{{ plan.allowed ? '开启时段内' : '开启时段外' }}<template v-if="circuit"> · 熔断中</template></span><span v-else>计划{{ plan ? '已停用' : '未设置' }}</span>
          <b v-if="nextText" class="schedule-next-action">{{ nextText }}</b>
          <el-button link :disabled="saving" @click="refreshContext(); refreshHistory()">刷新状态</el-button>
        </div>
        <p v-if="!direct" class="schedule-notice is-warning">未配置直连控制，无法启用计划</p>
        <p v-if="!worker" class="schedule-notice is-warning">当前服务未运行定时调度</p>
        <p v-if="runtimeError" class="schedule-notice is-warning" role="status">{{ runtimeError }}</p>
        <div v-if="conflict" class="schedule-notice is-warning" role="alert">计划{{ plan ? '已更新' : '已删除' }} <el-button link @click="reload">加载最新计划</el-button><el-button link @click="keepDraft">保留当前修改</el-button></div>
      </template>
      <div class="schedule-pane-switch"><el-radio-group v-model="pane" size="small" aria-label="切换计划设置与运行状态"><el-radio-button value="config">计划设置</el-radio-button><el-radio-button value="status">运行状态</el-radio-button></el-radio-group></div>
      <div class="schedule-grid" :data-pane="pane">
        <section class="schedule-config-pane" aria-label="计划设置">
          <div class="schedule-pane-head"><h3>计划设置</h3><template v-if="ready"><span v-if="!form.enabled" class="schedule-muted">当前开关保持</span><label class="schedule-enable"><span>启用计划</span><el-switch v-model="form.enabled" :disabled="saving" aria-label="启用定时计划"/></label></template></div>
          <div v-if="ready" ref="formElement">
            <el-form label-position="top" :disabled="saving" @submit.prevent="save">
              <el-form-item label="星期" :error="errors.days">
                <div class="schedule-days">
                  <div class="schedule-shortcuts"><el-button size="small" @click="setDays([1,2,3,4,5,6,7])">每天</el-button><el-button size="small" @click="setDays([1,2,3,4,5])">周一至周五</el-button><el-button size="small" @click="setDays([6,7])">周末</el-button></div>
                  <el-checkbox-group v-model="form.days" aria-label="执行星期" @change="errors.days = ''"><el-checkbox v-for="(day,index) in days" :key="day" :value="index+1">{{ day }}</el-checkbox></el-checkbox-group>
                </div>
              </el-form-item>
              <el-form-item label="开启时段 · 北京时间">
                <div class="schedule-windows">
                  <div v-for="(window,index) in form.windows" :key="index" class="schedule-window" :class="{'is-error':!!errors.windows[index]}">
                    <div class="schedule-clock">
                      <input v-model="window.start" :disabled="saving" type="time" step="60" :name="`schedule-start-${index}`" :aria-label="`时段${index+1}开始`" :aria-invalid="!!errors.windows[index]" :aria-describedby="errors.windows[index] ? `schedule-window-error-${index}` : undefined" @input="changeWindow" @blur="validDraft(true)"/>
                      <span>—</span>
                      <input v-model="window.end" :disabled="saving" type="time" step="60" :name="`schedule-end-${index}`" :aria-label="`时段${index+1}结束`" :aria-invalid="!!errors.windows[index]" :aria-describedby="errors.windows[index] ? `schedule-window-error-${index}` : undefined" @input="changeWindow" @blur="validDraft(true)"/>
                      <span class="schedule-next-day">{{ window.end && window.start && window.end < window.start ? '次日' : '' }}</span><el-button text :disabled="form.windows.length===1" :aria-label="`移除时段${index+1}`" @click="form.windows.splice(index,1); changeWindow()">移除</el-button>
                    </div>
                    <p v-if="errors.windows[index]" :id="`schedule-window-error-${index}`" class="schedule-field-error" role="alert">{{ errors.windows[index] }}</p>
                  </div>
                  <div><el-button size="small" :disabled="form.windows.length>=8" @click="form.windows.push({start:'00:00',end:'08:00'}); changeWindow()">+ 添加时段</el-button></div>
                </div>
              </el-form-item>
              <div class="schedule-preview" :class="{'is-idle':!form.enabled}" aria-live="polite">
                <span v-if="previewLoading">计算时段…</span><span v-else-if="previewError">{{ previewError }} <el-button link @click="refreshPreview">重试</el-button></span>
                <template v-else-if="preview">
                  <b>有效时段</b><div class="schedule-effective"><span v-for="window in effectiveWindows(preview.effective_windows)" :key="window">{{ window }}</span></div>
                  <div class="schedule-week" aria-hidden="true">
                    <div class="schedule-week-scale"><span>0</span><span>6</span><span>12</span><span>18</span><span>24</span></div>
                    <div v-for="row in weekStrip(preview.effective_windows)" :key="row.label" class="schedule-week-row"><span>{{ row.label }}</span><div class="schedule-week-track"><i v-for="(segment,index) in row.segments" :key="index" :style="{left:segment.left+'%',width:segment.width+'%'}"/></div></div>
                  </div>
                </template>
              </div>
              <el-form-item v-if="form.enabled" label="生效方式"><el-radio-group v-model="form.align_now" aria-label="计划生效方式"><el-radio :value="false">下次时点</el-radio><el-radio :value="true">同步当前状态</el-radio></el-radio-group></el-form-item>
              <p class="schedule-effect" aria-live="polite">{{ effectText }}<span v-if="form.enabled && !worker"> · 当前服务不执行</span></p>
              <p v-if="channel?.weight===0 && form.enabled && currentStatus !== '状态未知'" class="schedule-notice is-warning">当前权重为0，开启后不分流</p>
            </el-form>
          </div>
        </section>
        <section class="schedule-status-pane" aria-label="运行状态">
          <h3>最近处理</h3>
          <div v-if="plan?.execution" class="schedule-result" :class="`is-${scheduleTone(plan.execution)}`" aria-live="polite">
            <b>{{ scheduleResult(plan.execution) }}</b><span>{{ scheduleTime(plan.execution.updated_at) }}</span><p v-if="plan.execution.error">{{ plan.execution.error }}</p><small v-if="scheduleFollowup(plan.execution)">{{ scheduleFollowup(plan.execution) }}</small>
          </div>
          <p v-else class="schedule-empty">{{ plan ? '尚未处理' : '保存计划后显示处理结果' }}</p>
          <div class="schedule-history-heading"><h3>执行记录</h3><el-button link :disabled="saving" @click="refreshHistory">刷新</el-button></div>
          <p v-if="historyError" class="schedule-notice is-warning" role="status">{{ historyError }}</p>
          <el-table :data="history" max-height="380" size="small" empty-text="暂无执行记录">
            <el-table-column label="处理时间" width="104"><template #default="{row}">{{ scheduleTime(row.updated_at) }}</template></el-table-column>
            <el-table-column label="结果" width="96"><template #default="{row}"><el-tag size="small" :type="scheduleTone(row)">{{ scheduleResult(row) }}</el-tag></template></el-table-column>
            <el-table-column label="详情" min-width="150"><template #default="{row}"><span>{{ row.error || '—' }}</span><small v-if="scheduleFollowup(row)" class="schedule-history-note">{{ scheduleFollowup(row) }}</small><span class="schedule-history-note">计划时点 {{ formatBeijingTime(row.boundary) }}</span></template></el-table-column>
          </el-table>
        </section>
      </div>
    </div>
    <template #footer><div class="schedule-footer"><el-button v-if="plan" type="danger" plain :disabled="saving||loading||uncertain" @click="remove">删除计划</el-button><span class="schedule-footer-reason" role="status">{{ saveBlocker }}</span><el-button :disabled="saving" @click="close()">关闭</el-button><el-button type="primary" :loading="saving" :disabled="!ready||loading||!hasChanges||(form.revision>0&&!form.expected_updated_at)||conflict||uncertain||needsAlignment||(form.enabled&&(!direct||previewLoading||!preview))" @click="save">{{ form.enabled && form.align_now ? '保存并同步' : '保存计划' }}</el-button></div></template>
  </el-dialog>
</template>

<style scoped>
.schedule-body{min-height:100px}.schedule-context{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin-bottom:14px;font-size:13px}.schedule-context>.el-button{margin-left:auto}.schedule-next-action{font-weight:500;color:var(--ct-ink)}.schedule-next-action::before{content:"·";margin-right:10px;color:var(--ct-ink-3)}
.schedule-notice{padding:8px 10px;margin:8px 0 14px;border-radius:6px;font-size:13px;line-height:1.6;overflow-wrap:anywhere}.is-error,.is-danger{color:var(--ct-crit);background:var(--ct-crit-weak)}.is-warning{color:var(--ct-warn);background:var(--ct-warn-weak)}.is-success{color:var(--ct-ok);background:var(--ct-ok-weak)}.schedule-muted{margin-left:10px;color:var(--ct-ink-3);font-size:12px}
.schedule-pane-switch{display:none;margin-bottom:12px}
.schedule-grid{display:grid;grid-template-columns:minmax(0,1.1fr) minmax(0,1fr);gap:0 24px;align-items:start}.schedule-grid>section{min-width:0}.schedule-grid h3{font-size:14px;font-weight:500;margin:0 0 12px}.schedule-pane-head{display:flex;align-items:center;gap:12px;margin-bottom:12px}.schedule-pane-head>h3{margin:0 auto 0 0}.schedule-enable{display:inline-flex;align-items:center;gap:8px;font-size:13px;color:var(--ct-ink-2);cursor:pointer}.schedule-pane-head>.schedule-muted{margin-left:0}.schedule-status-pane{padding-left:24px;border-left:1px solid var(--ct-line)}
.schedule-config-pane :deep(.el-form-item){margin-bottom:14px}.schedule-days{width:100%}.schedule-days :deep(.el-checkbox){margin-right:8px}.schedule-shortcuts{display:flex;gap:8px;margin-bottom:8px}.schedule-shortcuts :deep(.el-button+.el-button){margin-left:0}.schedule-windows{width:100%;display:grid;gap:10px}.schedule-clock{display:flex;gap:10px;align-items:center}.schedule-clock input{box-sizing:border-box;min-width:0;width:136px;min-height:36px;padding:6px 10px;border:1px solid var(--ct-line);border-radius:6px;background:var(--ct-surface);color:var(--ct-ink-2);font:inherit;color-scheme:inherit}.schedule-clock input:focus-visible{outline:2px solid var(--ct-accent);outline-offset:2px}.schedule-window.is-error{background:transparent}.schedule-window.is-error input{border-color:var(--ct-crit)}.schedule-next-day{width:28px;font-size:12px;color:var(--ct-ink-3)}.schedule-field-error{margin:4px 0;color:var(--ct-crit);font-size:12px}
.schedule-preview{margin:0 0 12px;padding:10px 12px;background:var(--ct-surface-2);border-radius:8px;font-size:12px;line-height:1.7;color:var(--ct-ink-2)}.schedule-preview b{font-weight:500}.schedule-preview.is-idle .schedule-week-track>i{opacity:.35}.schedule-effective{display:flex;gap:4px 16px;flex-wrap:wrap}
.schedule-week{display:grid;gap:2px;margin-top:8px}.schedule-week-scale,.schedule-week-row{font-size:10px;line-height:11px;color:var(--ct-ink-3)}.schedule-week-scale{display:flex;justify-content:space-between;padding-left:38px}.schedule-week-row{display:grid;grid-template-columns:30px minmax(0,1fr);align-items:center;gap:8px}.schedule-week-track{position:relative;height:6px;border-radius:4px;background:var(--ct-surface);overflow:hidden}.schedule-week-track>i{position:absolute;top:0;bottom:0;background:var(--ct-accent);border-radius:2px}
.schedule-effect{font-size:13px;margin:0 0 8px;color:var(--ct-ink-2)}.schedule-effect>span{color:var(--ct-warn)}.schedule-result{display:flex;gap:8px;align-items:center;flex-wrap:wrap;padding:12px;border-radius:8px;font-size:13px}.schedule-result>span{margin-left:auto;font-size:12px}.schedule-result p{width:100%;margin:0;overflow-wrap:anywhere}.schedule-result small{font-size:12px}.schedule-empty{margin:0;padding:12px;border-radius:8px;background:var(--ct-surface-2);color:var(--ct-ink-3);font-size:13px}
.schedule-history-heading{display:flex;align-items:center;justify-content:space-between;margin-top:18px}.schedule-history-heading h3{margin:8px 0}.schedule-history-note{display:block;color:var(--ct-ink-3);font-size:11px;line-height:1.6}
.schedule-footer{display:flex;gap:8px;align-items:center}.schedule-footer-reason{flex:1;min-width:0;text-align:right;font-size:12px;color:var(--ct-warn);overflow-wrap:anywhere}.schedule-footer :deep(.el-button+.el-button){margin-left:0}
@media(max-width:820px){.schedule-pane-switch{display:block}.schedule-grid{grid-template-columns:minmax(0,1fr)}.schedule-pane-head>h3{display:none}.schedule-pane-head{justify-content:flex-end}.schedule-status-pane{padding-left:0;border-left:0}.schedule-grid[data-pane=config]>.schedule-status-pane,.schedule-grid[data-pane=status]>.schedule-config-pane{display:none}}
@media(max-width:600px){.schedule-clock{gap:6px;flex-wrap:wrap}.schedule-clock input{flex:1;min-width:100px;min-height:44px}.schedule-clock>.el-button{min-height:44px}.schedule-footer{flex-wrap:wrap}.schedule-footer-reason{flex-basis:100%;order:-1;text-align:left}.schedule-shortcuts :deep(.el-button){min-height:36px}}
</style>
<style>
.channel-schedule-dialog{max-height:90dvh;display:flex;flex-direction:column}.channel-schedule-dialog .el-dialog__header,.channel-schedule-dialog .el-dialog__footer{flex-shrink:0}.channel-schedule-dialog .el-dialog__body{min-height:0;overflow:auto;overscroll-behavior:contain}
</style>
