<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { EditPen } from '@element-plus/icons-vue';
import { compactCapacity, capacityInWan, parseCapacityWan } from '../utils/tuningCapacity';

const props = defineProps<{ modelValue: number; current?: number; metric: 'TPM' | 'RPM'; channel: string; modified?: boolean; disabled?: boolean; persist: (value: number) => Promise<void> }>();
const submitting = ref(false);
const open = ref(false), draft = ref(''), error = ref('');
const input = ref<{ focus: () => void; select: () => void }>();
const exceeded = computed(() => props.modelValue > 0 && props.current != null && props.current >= props.modelValue);
const exact = computed(() => `${props.metric} 当前 ${props.current?.toLocaleString('zh-CN') ?? '—'}${props.modelValue > 0 ? ` / 上限 ${props.modelValue.toLocaleString('zh-CN')}` : ''}；点击编辑上限`);
async function start() {
  draft.value = props.modelValue > 0 ? capacityInWan(props.modelValue) : '';
  error.value = '';
  await nextTick();
  input.value?.focus();
  input.value?.select();
}
async function apply(value: number) {
  if (submitting.value || props.disabled) return;
  submitting.value = true;
  error.value = '';
  try {
    await props.persist(value);
    open.value = false;
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '保存失败，请重试';
  } finally { submitting.value = false; }
}
function confirm() {
  const value = parseCapacityWan(draft.value);
  if (value === null) { error.value = '请输入非负数，最多 4 位小数，单位为万'; return; }
  apply(value);
}
</script>

<template>
  <el-popover v-model:visible="open" trigger="click" :disabled="disabled || submitting" placement="bottom-start" :width="270" @show="start">
    <template #reference>
      <button type="button" class="capacity-metric" :disabled="disabled || submitting" :class="{ exceeded, modified }" :title="exact" :aria-label="`${channel} ${metric}，编辑上限`" :aria-expanded="open" @keydown.esc="open = false">
        <span>{{ metric }}</span> <span>{{ compactCapacity(current) }}</span><template v-if="modelValue > 0"><span class="slash">/</span><span>{{ compactCapacity(modelValue) }}</span></template><el-icon class="edit-icon"><EditPen /></el-icon>
      </button>
    </template>
    <div class="capacity-editor" @keydown.esc.stop="open = false">
      <b>{{ metric }} 上限（万）</b>
      <el-input ref="input" v-model="draft" :disabled="disabled || submitting" :aria-label="`${channel} ${metric}上限`" placeholder="例如 20 表示 20 万" :maxlength="32" @input="error = ''" @keydown.enter.prevent="confirm"><template #append>万</template></el-input>
      <small v-if="error" class="error" role="alert">{{ error }}</small>
      <small v-else>留空或填 0 可清除。点击确认直接保存生效。</small>
      <div class="editor-actions"><el-button text :disabled="!modelValue || disabled || submitting" @click="apply(0)">清除上限</el-button><el-button type="primary" :loading="submitting" :disabled="disabled" @click="confirm">确认</el-button></div>
    </div>
  </el-popover>
</template>

<style scoped>
.capacity-metric{display:inline-flex;align-items:center;gap:4px;padding:1px 0;border:0;background:transparent;color:var(--ct-ink-3);font:inherit;white-space:nowrap;cursor:pointer;border-radius:3px}
.capacity-metric:hover,.capacity-metric:focus-visible{color:var(--ct-accent);background:var(--ct-accent-weak)}.capacity-metric:focus-visible{outline:2px solid var(--ct-line);outline-offset:2px}
.capacity-metric.exceeded{color:var(--ct-warn)}.capacity-metric.modified{background:var(--ct-warn-weak);box-shadow:0 1px #dcac4b}.slash{color:var(--ct-ink-3)}.edit-icon{font-size:11px;opacity:0}.capacity-metric:hover .edit-icon,.capacity-metric:focus-visible .edit-icon,.capacity-metric[aria-expanded=true] .edit-icon{opacity:1}
.capacity-editor{display:grid;gap:10px;color:var(--ct-ink-2)}.capacity-editor b{font-size:13px}.capacity-editor small{font-size:11px;line-height:1.6;color:var(--ct-ink-3)}.capacity-editor .error{color:var(--ct-crit)}.editor-actions{display:flex;justify-content:space-between;align-items:center}
</style>
