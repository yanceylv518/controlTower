<script setup lang="ts">
import { ref, watch } from 'vue';
import { ElMessage } from 'element-plus';
import { compactCapacity, parseCapacity } from '../utils/tuningCapacity';
const props = defineProps<{ modelValue: number; label: string }>();
const emit = defineEmits<{ 'update:modelValue': [number]; change: [number] }>();
const focused = ref(false), draft = ref('');
watch(() => props.modelValue, value => { if (!focused.value) draft.value = compactCapacity(value, true); }, { immediate: true });
function focus(event: FocusEvent) { focused.value = true; draft.value = String(props.modelValue ?? 0); const input = event.target as HTMLInputElement; requestAnimationFrame(() => input.select()); }
function commit() {
  const value = parseCapacity(draft.value);
  focused.value = false;
  if (value === null) { ElMessage.warning('请输入非负整数，可使用万或亿；0 表示不限'); draft.value = compactCapacity(props.modelValue, true); return; }
  if (value !== props.modelValue) { emit('update:modelValue', value); emit('change', value); }
  draft.value = compactCapacity(value, true);
}
function keydown(event: KeyboardEvent) { if (event.key === 'Enter') (event.target as HTMLInputElement).blur(); if (event.key === 'Escape') { draft.value = String(props.modelValue ?? 0); (event.target as HTMLInputElement).blur(); } }
</script>
<template><el-input size="small" v-model="draft" :aria-label="label" :title="`${modelValue || 0}（0 表示不限）`" @focus="focus" @blur="commit" @keydown="keydown" /></template>
