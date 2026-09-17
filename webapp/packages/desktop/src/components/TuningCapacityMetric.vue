<script setup lang="ts">
import { computed, nextTick, ref } from 'vue';
import { EditPen } from '@element-plus/icons-vue';
import { compactCapacity, parseCapacity } from '../utils/tuningCapacity';

const props = defineProps<{ modelValue: number; current?: number; metric: 'TPM' | 'RPM'; channel: string; modified?: boolean }>();
const emit = defineEmits<{ 'update:modelValue': [number]; change: [number] }>();
const open = ref(false), draft = ref(''), error = ref('');
const input = ref<{ focus: () => void; select: () => void }>();
const exceeded = computed(() => props.modelValue > 0 && props.current != null && props.current >= props.modelValue);
const exact = computed(() => `${props.metric} 当前 ${props.current?.toLocaleString('zh-CN') ?? '—'}${props.modelValue > 0 ? ` / 上限 ${props.modelValue.toLocaleString('zh-CN')}` : ''}；点击编辑上限`);
async function start() {
  draft.value = props.modelValue > 0 ? String(props.modelValue) : '';
  error.value = '';
  await nextTick();
  input.value?.focus();
  input.value?.select();
}
function apply(value: number) {
  if (value !== props.modelValue) { emit('update:modelValue', value); emit('change', value); }
  open.value = false;
}
function confirm() {
  const value = draft.value.trim() === '' ? 0 : parseCapacity(draft.value);
  if (value === null) { error.value = '请输入非负整数，可使用万或亿'; return; }
  apply(value);
}
</script>

<template>
  <el-popover v-model:visible="open" trigger="click" placement="bottom-start" :width="270" @show="start">
    <template #reference>
      <button type="button" class="capacity-metric" :class="{ exceeded, modified }" :title="exact" :aria-label="`${channel} ${metric}，编辑上限`" :aria-expanded="open" @keydown.esc="open = false">
        <span>{{ metric }}</span> <span>{{ compactCapacity(current) }}</span><template v-if="modelValue > 0"><span class="slash">/</span><span>{{ compactCapacity(modelValue) }}</span></template><el-icon class="edit-icon"><EditPen /></el-icon>
      </button>
    </template>
    <div class="capacity-editor" @keydown.esc.stop="open = false">
      <b>{{ metric }} 上限</b>
      <el-input ref="input" v-model="draft" :aria-label="`${channel} ${metric}上限`" placeholder="填写上限，可使用万或亿" :maxlength="32" @input="error = ''" @keydown.enter.prevent="confirm" />
      <small v-if="error" class="error" role="alert">{{ error }}</small>
      <small v-else>留空或填 0 可清除。确认后统一点击页面“保存更改”生效。</small>
      <div class="editor-actions"><el-button text :disabled="!modelValue" @click="apply(0)">清除上限</el-button><el-button type="primary" @click="confirm">确认</el-button></div>
    </div>
  </el-popover>
</template>

<style scoped>
.capacity-metric{display:inline-flex;align-items:center;gap:4px;padding:1px 0;border:0;background:transparent;color:#718097;font:inherit;white-space:nowrap;cursor:pointer;border-radius:3px}
.capacity-metric:hover,.capacity-metric:focus-visible{color:#3168e8;background:#f0f5ff}.capacity-metric:focus-visible{outline:2px solid #b9cdfb;outline-offset:2px}
.capacity-metric.exceeded{color:#b98015}.capacity-metric.modified{background:#fff8e6;box-shadow:0 1px #dcac4b}.slash{color:#a0acbb}.edit-icon{font-size:11px;opacity:0}.capacity-metric:hover .edit-icon,.capacity-metric:focus-visible .edit-icon,.capacity-metric[aria-expanded=true] .edit-icon{opacity:1}
.capacity-editor{display:grid;gap:10px;color:#465367}.capacity-editor b{font-size:13px}.capacity-editor small{font-size:11px;line-height:1.6;color:#8491a5}.capacity-editor .error{color:#d24d51}.editor-actions{display:flex;justify-content:space-between;align-items:center}
</style>
