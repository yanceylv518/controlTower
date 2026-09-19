<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'

type DateRange = [Date, Date]
type PresetKey = 'today' | '7d' | 'week' | '30d' | 'month'

const props = withDefaults(defineProps<{ modelValue: DateRange; resetEnabled?: boolean; compact?: boolean }>(), {
  resetEnabled: false,
})
const emit = defineEmits<{
  'update:modelValue': [value: DateRange]
  reset: []
}>()

const root = ref<HTMLElement | null>(null)
const popover = ref<HTMLElement | null>(null)
const open = ref(false)
const error = ref('')
// 草稿值与 rc35 一致使用两个 datetime-local 字段，确认前不影响实际查询范围。
const draftStart = ref('')
const draftEnd = ref('')
const popoverStyle = ref<Record<string, string>>({})

const presets: readonly { key: PresetKey; label: string }[] = [
  { key: 'today', label: '今天' },
  { key: '7d', label: '近 7 天' },
  { key: 'week', label: '本周' },
  { key: '30d', label: '近 30 天' },
  { key: 'month', label: '本月' },
]

const start = computed(() => props.modelValue?.[0])
const end = computed(() => props.modelValue?.[1])

function two(value: number) {
  return String(value).padStart(2, '0')
}

function validDate(value: Date | undefined): value is Date {
  return value instanceof Date && !Number.isNaN(value.getTime())
}

function inputDate(value: Date | undefined) {
  if (!validDate(value)) return ''
  return value.getFullYear() + '-' + two(value.getMonth() + 1) + '-' + two(value.getDate())
}

function inputTime(value: Date | undefined) {
  if (!validDate(value)) return ''
  return two(value.getHours()) + ':' + two(value.getMinutes())
}

function toInputValue(value: Date | undefined) {
  if (!validDate(value)) return ''
  return inputDate(value) + 'T' + inputTime(value)
}

function triggerText(value: Date | undefined) {
  if (!validDate(value)) return '-'
  return inputDate(value) + ' ' + inputTime(value)
}

// rc35 触发器只显示到分钟，避免长秒级文本挤压其他筛选控件。
const label = computed(() => {
  if (!start.value && !end.value) return '日期范围'
  if (props.compact) {
    const from = inputDate(start.value), to = inputDate(end.value)
    const today = inputDate(new Date())
    if (from === today && to === today) return '今天'
    return from === to ? from.slice(5) : `${from.slice(5)} ~ ${to.slice(5)}`
  }
  return triggerText(start.value) + ' ~ ' + triggerText(end.value)
})

function syncDraft() {
  draftStart.value = toInputValue(start.value)
  draftEnd.value = toInputValue(end.value)
}

// 严格按本地时间解析 datetime-local，避免浏览器对非法日期自动进位。
function parseInput(value: string): Date | undefined {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value)
  if (!match) return undefined
  const [, yearText, monthText, dayText, hourText, minuteText] = match
  const year = Number(yearText)
  const month = Number(monthText)
  const day = Number(dayText)
  const hour = Number(hourText)
  const minute = Number(minuteText)
  const parsed = new Date(year, month - 1, day, hour, minute, 0, 0)
  return parsed.getFullYear() === year && parsed.getMonth() === month - 1 && parsed.getDate() === day && parsed.getHours() === hour && parsed.getMinutes() === minute ? parsed : undefined
}

function updatePosition() {
  if (!open.value || !root.value) return
  const rect = root.value.getBoundingClientRect()
  // 与 rc35 的 calc(100vw - 2rem) 保持一致，窄屏两侧固定 16px 留白。
  const width = Math.min(520, Math.max(280, window.innerWidth - 32))
  const maxLeft = Math.max(16, window.innerWidth - width - 16)
  const left = Math.min(Math.max(16, rect.left), maxLeft)
  let top = rect.bottom + 8
  const height = popover.value?.offsetHeight || 0
  if (height > 0 && top + height > window.innerHeight - 16 && rect.top > height + 8) top = rect.top - height - 8
  if (height > 0) top = Math.max(16, Math.min(top, window.innerHeight - height - 16))
  popoverStyle.value = { top: Math.round(top) + 'px', left: Math.round(left) + 'px', width: Math.round(width) + 'px' }
}

async function setOpen(next: boolean) {
  if (next) {
    syncDraft()
    error.value = ''
    open.value = true
    await nextTick()
    updatePosition()
    return
  }
  open.value = false
  error.value = ''
}

function parseDraft(): DateRange | undefined {
  const nextStart = parseInput(draftStart.value)
  const nextEnd = parseInput(draftEnd.value)
  if (!nextStart || !nextEnd) {
    error.value = '请选择完整的开始和结束时间'
    return undefined
  }
  if (nextEnd <= nextStart) {
    error.value = '结束时间必须晚于开始时间'
    return undefined
  }
  return [nextStart, nextEnd]
}

function applyDraft() {
  const range = parseDraft()
  if (!range) return
  emit('update:modelValue', range)
  void setOpen(false)
}

function startOfDay(value: Date) {
  return new Date(value.getFullYear(), value.getMonth(), value.getDate(), 0, 0, 0, 0)
}

function endOfDay(value: Date) {
  return new Date(value.getFullYear(), value.getMonth(), value.getDate(), 23, 59, 59, 999)
}

function startOfWeek(value: Date) {
  const day = value.getDay()
  const result = startOfDay(value)
  result.setDate(result.getDate() - day)
  return result
}

function endOfMonth(value: Date) {
  return new Date(value.getFullYear(), value.getMonth() + 1, 0, 23, 59, 59, 999)
}

function presetRange(kind: PresetKey): DateRange {
  const now = new Date()
  if (kind === 'today') return [startOfDay(now), endOfDay(now)]
  if (kind === '7d') {
    const from = startOfDay(now)
    from.setDate(from.getDate() - 6)
    return [from, endOfDay(now)]
  }
  if (kind === 'week') {
    const from = startOfWeek(now)
    const to = new Date(from)
    to.setDate(to.getDate() + 6)
    return [from, endOfDay(to)]
  }
  if (kind === '30d') {
    const from = startOfDay(now)
    from.setDate(from.getDate() - 29)
    return [from, endOfDay(now)]
  }
  return [new Date(now.getFullYear(), now.getMonth(), 1, 0, 0, 0, 0), endOfMonth(now)]
}

function applyPreset(kind: PresetKey) {
  const range = presetRange(kind)
  draftStart.value = toInputValue(range[0])
  draftEnd.value = toInputValue(range[1])
  emit('update:modelValue', range)
  void setOpen(false)
}

// 重置图标位于日期控件内部，只触发父页面的时间范围恢复，不改变其他筛选条件。
function resetRange() {
  if (!props.resetEnabled) return
  emit('reset')
  void setOpen(false)
}

function onDocumentPointerDown(event: PointerEvent) {
  const target = event.target
  if (!(target instanceof Node)) return
  if (root.value?.contains(target) || popover.value?.contains(target)) return
  if (open.value) void setOpen(false)
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && open.value) {
    event.preventDefault()
    void setOpen(false)
  }
}

watch(() => props.modelValue, () => {
  if (!open.value) syncDraft()
}, { deep: true })

onMounted(() => {
  syncDraft()
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('keydown', onKeydown)
  window.addEventListener('resize', updatePosition)
  window.addEventListener('scroll', updatePosition, true)
})

onUnmounted(() => {
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('keydown', onKeydown)
  window.removeEventListener('resize', updatePosition)
  window.removeEventListener('scroll', updatePosition, true)
})
</script>

<template>
  <div ref="root" class="compact-date-range">
    <button type="button" class="compact-date-trigger" :aria-expanded="open" aria-haspopup="dialog" aria-label="日期范围" @click="setOpen(!open)">
      <span class="calendar-glyph" aria-hidden="true" />
      <span class="compact-date-label">{{ label }}</span>
    </button>
    <button v-if="!compact" type="button" class="compact-date-reset" :disabled="!props.resetEnabled" title="重置时间" aria-label="重置时间" @click.stop="resetRange">
      <span class="reset-glyph" aria-hidden="true">↻</span>
    </button>

    <Teleport to="body">
      <div v-if="open" ref="popover" class="compact-date-popover" :style="popoverStyle" role="dialog" aria-label="日期范围">
        <div class="compact-date-fields">
          <label class="compact-date-field"><span>开始时间</span><input v-model="draftStart" class="compact-date-input" type="datetime-local" step="60" aria-label="开始时间" /></label>
          <span class="compact-date-separator" aria-hidden="true">~</span>
          <label class="compact-date-field"><span>结束时间</span><input v-model="draftEnd" class="compact-date-input" type="datetime-local" step="60" aria-label="结束时间" /></label>
        </div>
        <p v-if="error" class="compact-date-error" role="alert">{{ error }}</p>
        <div class="compact-date-presets">
          <button v-for="preset in presets" :key="preset.key" type="button" class="compact-date-preset" @click="applyPreset(preset.key)">{{ preset.label }}</button>
        </div>
        <div class="compact-date-footer"><button type="button" class="compact-date-confirm" @click="applyDraft">确认</button></div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
/* rc35 日期组件使用原生控件承载输入，避免 Element Plus 双月面板改变筛选栏高度。 */
.compact-date-range { position: relative; display: block; min-width: 0; }
.compact-date-trigger {
  display: flex;
  width: 100%;
  height: 34px;
  box-sizing: border-box;
  align-items: center;
  gap: 8px;
  padding: 0 38px 0 10px;
  border: 1px solid var(--ct-line-strong);
  border-radius: 6px;
  background: var(--ct-surface-2);
  color: var(--ct-ink);
  cursor: pointer;
  font: inherit;
  font-size: 12px;
  text-align: left;
  transition: border-color .16s ease, box-shadow .16s ease, background .16s ease;
}
.compact-date-trigger:hover { border-color: var(--ct-line-strong); background: var(--ct-surface); }
.compact-date-trigger:focus-visible { outline: 2px solid var(--ct-accent); outline-offset: 2px; }
.compact-date-reset {
  position: absolute;
  top: 4px;
  right: 4px;
  display: grid;
  width: 26px;
  height: 26px;
  padding: 0;
  place-items: center;
  border: 0;
  border-radius: 5px;
  background: transparent;
  color: var(--ct-ink-3);
  cursor: pointer;
  font: inherit;
}
.compact-date-reset:hover:not(:disabled) { background: var(--ct-accent-weak); color: var(--ct-accent); }
.compact-date-reset:focus-visible { outline: 2px solid var(--ct-accent); outline-offset: 1px; }
.compact-date-reset:disabled { cursor: default; opacity: .35; pointer-events: none; }
.reset-glyph { display: block; font-size: 19px; line-height: 24px; }
.calendar-glyph {
  position: relative;
  display: block;
  width: 15px;
  height: 15px;
  flex: 0 0 15px;
  box-sizing: border-box;
  border: 1.5px solid var(--ct-ink-3);
  border-radius: 4px;
}
.calendar-glyph::before { position: absolute; top: 3px; right: 1px; left: 1px; border-top: 1.5px solid var(--ct-ink-3); content: ''; }
.calendar-glyph::after { position: absolute; top: -3px; left: 3px; width: 1.5px; height: 4px; border-radius: 1px; background: var(--ct-ink-3); box-shadow: 5px 0 #8b95a7; content: ''; }
.compact-date-label { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

/* Teleport 后的弹层仍使用 rc35 的单行输入、快捷项和确认栏结构。 */
.compact-date-popover {
  position: fixed;
  z-index: 3000;
  box-sizing: border-box;
  padding: 12px;
  border: 1px solid var(--ct-line);
  border-radius: 8px;
  background: var(--ct-surface);
  color: var(--ct-ink);
  box-shadow: 0 8px 24px rgba(16, 24, 40, .14);
}
.compact-date-fields { display: grid; grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr); gap: 10px; align-items: end; }
.compact-date-field { display: block; min-width: 0; }
.compact-date-field > span { display: block; margin-bottom: 5px; color: var(--ct-ink-3); font-size: 11px; }
.compact-date-input {
  width: 100%;
  height: 32px;
  min-width: 0;
  box-sizing: border-box;
  padding: 0 7px;
  border: 1px solid var(--ct-line-strong);
  border-radius: 6px;
  outline: 0;
  background: var(--ct-surface-2);
  color: var(--ct-ink);
  font: 12px ui-monospace, SFMono-Regular, Consolas, monospace;
  font-variant-numeric: tabular-nums;
}
.compact-date-input:focus { border-color: var(--ct-accent); box-shadow: 0 0 0 1px #2f5fe0 inset; }
.compact-date-separator { padding-bottom: 9px; color: var(--ct-ink-3); font-size: 12px; }
.compact-date-error { margin: 8px 0 0; color: var(--ct-crit); font-size: 11px; }
.compact-date-presets { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 12px; }
.compact-date-preset {
  height: 28px;
  flex: 1 1 84px;
  min-width: 0;
  padding: 0 9px;
  border: 1px solid var(--ct-line);
  border-radius: 6px;
  background: var(--ct-surface-2);
  color: var(--ct-ink-2);
  cursor: pointer;
  font: inherit;
  font-size: 11px;
  white-space: nowrap;
}
.compact-date-preset:hover { border-color: var(--ct-line); background: var(--ct-accent-weak); color: var(--ct-accent); }
.compact-date-preset:focus-visible, .compact-date-confirm:focus-visible { outline: 2px solid var(--ct-accent); outline-offset: 2px; }
.compact-date-footer { display: flex; justify-content: flex-end; margin-top: 12px; padding-top: 10px; border-top: 1px solid var(--ct-line); }
.compact-date-confirm { height: 30px; padding: 0 13px; border: 1px solid var(--ct-accent); border-radius: 6px; background: var(--ct-primary-solid); color: var(--ct-on-solid); cursor: pointer; font: inherit; font-size: 12px; }
.compact-date-confirm:hover { background: var(--ct-primary-solid); }

@media (max-width: 520px) {
  .compact-date-popover { max-height:calc(100dvh - 32px);overflow-y:auto; }
  .compact-date-input { height:44px;font-size:16px; }
  .compact-date-preset,.compact-date-confirm { min-height:44px; }
  .compact-date-fields { grid-template-columns: minmax(0, 1fr); gap: 8px; }
  .compact-date-separator { display: none; }
  .compact-date-presets { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
}
</style>
