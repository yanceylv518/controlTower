<script setup lang="ts">
import { reactive } from 'vue'
import type { ChannelBaseValue } from '@ct/shared'
const props = defineProps<{ row:ChannelBaseValue; site:string; priority:number; priorityLocked:boolean }>()
const emit = defineEmits<{ close:[]; apply:[value:{base_weight:number;priority:number;max_rpm:number;max_tpm:number}] }>()
const draft = reactive({base_weight:props.row.base_weight,priority:props.priority,max_rpm:props.row.max_rpm,max_tpm:props.row.max_tpm})
const valid = () => Object.values(draft).every(value => Number.isSafeInteger(value) && value >= 0)
</script>
<template>
  <el-dialog :model-value="true" title="编辑权重与优先级" class="mobile-tuning-dialog" append-to-body :close-on-click-modal="false" @close="emit('close')">
    <p class="scope">{{ site }} / {{ row.channel_name }} #{{ row.channel_id }}<br>{{ row.model_name }}</p>
    <div class="online-values"><span>线上权重 <b>{{ row.current_weight }}</b></span><span>线上优先级 <b>{{ row.current_priority }}</b></span></div>
    <el-form label-position="top">
      <el-form-item label="基础权重"><el-input-number v-model="draft.base_weight" aria-label="基础权重" :min="0" :precision="0" /></el-form-item>
      <el-form-item label="优先级（保存后同步线上）"><el-input-number v-model="draft.priority" aria-label="线上优先级" :disabled="priorityLocked" :min="0" :precision="0" /><small v-if="priorityLocked">熔断或探测中，暂不可修改优先级</small></el-form-item>
      <el-form-item label="RPM 上限（0 表示不限）"><el-input-number v-model="draft.max_rpm" aria-label="RPM 上限" :min="0" :precision="0" /></el-form-item>
      <el-form-item label="TPM 上限（0 表示不限）"><el-input-number v-model="draft.max_tpm" aria-label="TPM 上限" :min="0" :precision="0" /></el-form-item>
    </el-form>
    <p class="explanation">基础权重用于后续计算，不等于当前线上权重。优先级保存会同步线上；分组独立提交。</p>
    <div class="groups"><b>当前分组</b><p>{{ row.group_name || '未设置' }}</p><small>分组独立保存，可返回渠道卡片点击分组编辑。</small></div>
    <template #footer><el-button @click="emit('close')">取消</el-button><el-button type="primary" :disabled="!valid()" @click="emit('apply', { ...draft })">预览变更</el-button></template>
  </el-dialog>
</template>
<style scoped>
.scope { padding:12px;background:var(--ct-surface-2);border-radius:8px;overflow-wrap:anywhere;line-height:1.7; }
.online-values { display:flex;gap:16px;justify-content:space-between;margin:16px 0; }
.online-values b { display:block;margin-top:8px;font-size:24px; }
.el-input-number { width:100%; }
.explanation,small { color:var(--ct-ink-3);font-size:12px;line-height:1.7; }
.groups { border-top:1px solid var(--ct-line);padding:16px 0;overflow-wrap:anywhere; }
</style>
