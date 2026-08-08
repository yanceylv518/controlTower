<script setup lang="ts">
import { ref, watch } from "vue";
import { ElMessage } from "element-plus";

const props = defineProps<{ modelValue: boolean; token: string; graceUntil?: string }>();
const emit = defineEmits<{ (e: "update:modelValue", v: boolean): void }>();
const saved = ref(false);

watch(() => props.modelValue, (value) => {
  if (value) saved.value = false;
});

function legacyCopy(value: string) {
  const input = document.createElement("textarea");
  input.value = value;
  input.setAttribute("readonly", "");
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.select();
  const copied = document.execCommand("copy");
  input.remove();
  if (!copied) throw new Error("copy command failed");
}

async function copy() {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(props.token);
    } else {
      legacyCopy(props.token);
    }
    ElMessage.success("Token 已复制");
  } catch {
    ElMessage.error("复制失败，请手动选中 Token 后复制");
  }
}
</script>
<template><el-dialog :model-value="modelValue" title="保存 Agent Token" width="620px" :close-on-click-modal="false" :close-on-press-escape="false" :show-close="false"><el-alert title="Token 仅此一次显示，关闭后无法找回" type="warning" show-icon :closable="false"/><p v-if="graceUntil">旧 Token 宽限至：{{new Date(graceUntil).toLocaleString()}}</p><div class="token-box"><code>{{token}}</code><el-button @click="copy">复制</el-button></div><el-checkbox v-model="saved">我已保存</el-checkbox><template #footer><el-button type="primary" :disabled="!saved" @click="emit('update:modelValue',false)">我已保存并关闭</el-button></template></el-dialog></template>
