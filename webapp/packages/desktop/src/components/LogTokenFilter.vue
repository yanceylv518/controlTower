<script setup lang="ts">
import { computed, nextTick, reactive, ref, useId, watch } from 'vue'
import { ArrowDown } from '@element-plus/icons-vue'
import { emptyLogTokenFilter, hasLogTokenFilter, logTokenFilterFields, logTokenFilterSummary, validateLogTokenFilter, type LogTokenFilterValue } from '../utils/logTokenFilter'

const props = withDefaults(defineProps<{ modelValue: LogTokenFilterValue; disabled?: boolean; teleported?: boolean }>(), { disabled: false, teleported: true })
const emit = defineEmits<{ 'update:modelValue': [value: LogTokenFilterValue] }>()
const open = ref(false), error = ref('')
const trigger = ref<HTMLButtonElement | null>(null), panel = ref<HTMLElement | null>(null)
const panelID = useId()
const draft = reactive({ ...props.modelValue })
const active = computed(() => hasLogTokenFilter(props.modelValue))
const summary = computed(() => logTokenFilterSummary(props.modelValue))
const modeLabels = { input: '输入', output: '输出', both: '输入+输出' }
const visibleFields = computed(() => logTokenFilterFields(draft))
function prepare() {
  Object.assign(draft, props.modelValue)
  error.value = ''
  void nextTick(() => panel.value?.querySelector<HTMLInputElement>('input')?.focus())
}
function close() {
  open.value = false
  void nextTick(() => trigger.value?.focus())
}
function apply() {
  if (props.disabled) return
  error.value = validateLogTokenFilter(draft)
  if (error.value) return
  emit('update:modelValue', { ...draft })
  close()
}
function clear() {
  if (props.disabled) return
  emit('update:modelValue', emptyLogTokenFilter())
  close()
}
function handleCountEnter(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  event.stopPropagation()
  apply()
}
watch(() => [props.modelValue, props.disabled], () => { open.value = false })
watch(() => [draft.mode, draft.input, draft.output], () => { error.value = '' })
</script>

<template>
  <div class="log-token-filter">
    <el-popover v-model:visible="open" trigger="click" placement="bottom-start" :width="340" :teleported="teleported" :disabled="disabled" :persistent="false" popper-class="log-tokens-popover" @show="prepare">
      <template #reference>
        <button ref="trigger" type="button" class="tokens-trigger" :class="{ 'is-active': active }" :disabled="disabled" :title="summary" :aria-label="`Tokens 筛选：${summary}`" :aria-expanded="open" :aria-controls="panelID" aria-haspopup="dialog">
          <span>Tokens<span v-if="active"> · {{ modeLabels[modelValue.mode] }}</span></span>
          <el-icon aria-hidden="true"><ArrowDown /></el-icon>
        </button>
      </template>
      <div :id="panelID" ref="panel" class="tokens-panel" role="dialog" aria-label="Tokens 筛选条件" @keydown.esc.stop.prevent="close">
        <el-radio-group v-model="draft.mode" class="tokens-mode" aria-label="Tokens 筛选方式">
          <el-radio-button value="input">仅输入</el-radio-button>
          <el-radio-button value="output">仅输出</el-radio-button>
          <el-radio-button value="both">输入和输出</el-radio-button>
        </el-radio-group>
        <div v-for="field in visibleFields" :key="field.key" class="tokens-count">
          <label :for="`${panelID}-${field.key}`">{{ field.label }} Tokens</label>
          <el-input :id="`${panelID}-${field.key}`" v-model="draft[field.key]" inputmode="numeric" placeholder="输入准确数量" clearable :aria-invalid="Boolean(error)" :aria-describedby="`${panelID}-hint ${panelID}-error`" @keydown.enter="handleCountEnter" />
        </div>
        <p :id="`${panelID}-hint`" class="tokens-hint">精确匹配填写的数量；留空表示不限。</p>
        <p v-if="error" :id="`${panelID}-error`" class="tokens-error" role="alert">{{ error }}</p>
        <div class="tokens-actions"><el-button @click="clear">清除</el-button><el-button type="primary" @click="apply">确定</el-button></div>
      </div>
    </el-popover>
  </div>
</template>

<style scoped>
.log-token-filter { min-width:0; }
.tokens-trigger { display:flex;align-items:center;justify-content:space-between;gap:8px;width:100%;height:34px;padding:0 11px;border:1px solid var(--rc35-line,var(--ct-line));border-radius:8px;background:var(--rc35-surface-2,var(--ct-surface));color:var(--rc35-ink-3,var(--ct-ink-3));font:inherit;font-size:14px;cursor:pointer; }
.tokens-trigger > span { overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }
.tokens-trigger .el-icon { flex:none; }
.tokens-trigger:hover { border-color:var(--ct-ink-3); }
.tokens-trigger:focus-visible { outline:2px solid var(--ct-accent);outline-offset:2px; }
.tokens-trigger.is-active { border-color:var(--ct-accent);color:var(--ct-accent); }
.tokens-trigger:disabled { cursor:not-allowed;opacity:.6; }
.tokens-panel { display:grid;gap:14px;color:var(--ct-ink); }
.tokens-mode { display:flex;flex-wrap:nowrap; }
.tokens-mode :deep(.el-radio-button) { flex:1; }
.tokens-mode :deep(.el-radio-button__inner) { width:100%;padding:8px; }
.tokens-count label { display:block;margin-bottom:8px;font-weight:600;font-size:13px; }
.tokens-hint,.tokens-error { margin:0;font-size:12px;line-height:1.6; }
.tokens-hint { color:var(--ct-ink-3); }
.tokens-error { color:var(--el-color-danger); }
.tokens-actions { display:flex;justify-content:flex-end;gap:8px; }
.tokens-actions .el-button { margin:0; }
</style>
<style>
.log-tokens-popover.el-popover.el-popper { max-width:calc(100vw - 32px);padding:16px;border:1px solid var(--ct-line);background:var(--ct-surface); }
.log-tokens-popover .el-popper__arrow::before { border-color:var(--ct-line);background:var(--ct-surface); }
</style>
