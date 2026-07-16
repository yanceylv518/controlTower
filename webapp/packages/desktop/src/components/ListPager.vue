<script setup lang="ts">
import { ArrowLeft, ArrowRight } from "@element-plus/icons-vue";
const props = withDefaults(
  defineProps<{ page: number; pageSize: number; itemCount: number }>(),
  { page: 1, pageSize: 20, itemCount: 0 },
);
const emit = defineEmits<{
  "update:page": [number];
  "update:pageSize": [number];
}>();
function size(value: number) {
  emit("update:pageSize", value);
  emit("update:page", 1);
}
</script>
<template>
  <div class="ct-pager">
    <span class="ct-pager-info"
      >第 {{ props.page }} 页 · 本页 {{ props.itemCount }} 条</span
    >
    <el-select
      :model-value="props.pageSize"
      size="small"
      class="ct-pager-size"
      @update:model-value="size"
    >
      <el-option
        v-for="n in [20, 50, 100]"
        :key="n"
        :label="`${n} 条/页`"
        :value="n"
      />
    </el-select>
    <el-button
      size="small"
      :icon="ArrowLeft"
      :disabled="props.page <= 1"
      @click="emit('update:page', props.page - 1)"
    />
    <el-button
      size="small"
      :icon="ArrowRight"
      :disabled="props.itemCount < props.pageSize"
      @click="emit('update:page', props.page + 1)"
    />
  </div>
</template>
