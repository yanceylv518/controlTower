<script setup lang="ts">
import { CopyDocument } from '@element-plus/icons-vue';
import { ElMessage } from 'element-plus';
import { copyText } from '../utils/copyText';

const props = defineProps<{ value: string; label: string }>();
async function copy() {
  if (await copyText(props.value)) ElMessage.success('已复制');
  else ElMessage.error('复制失败，请手动复制');
}
</script>

<template>
  <button type="button" class="monitor-copy" :title="label" :aria-label="label" :disabled="!value" @click.stop="copy">
    <CopyDocument aria-hidden="true" />
  </button>
</template>

<style scoped>
.monitor-copy { display: inline-flex; vertical-align: middle; align-items: center; justify-content: center; flex-shrink: 0; width: 26px; height: 26px; padding: 5px; border: 0; border-radius: 4px; background: transparent; color: var(--ct-ink-3); cursor: pointer; }
.monitor-copy:hover { background: var(--ct-surface-2); color: var(--ct-primary); }
.monitor-copy:focus-visible { outline: 2px solid var(--ct-primary); outline-offset: 2px; }
.monitor-copy:disabled { opacity: .4; cursor: default; }
.monitor-copy svg { width: 14px; height: 14px; }
</style>
