<script setup lang="ts">
import { computed } from "vue";
const props = withDefaults(
  defineProps<{ page: number; pageSize: number; itemCount: number; total?: number }>(),
  { page: 1, pageSize: 20, itemCount: 0, total: undefined },
);
const emit = defineEmits<{
  "update:page": [number];
  "update:pageSize": [number];
}>();
function size(value: number) {
  emit("update:pageSize", value);
  emit("update:page", 1);
}
const knownTotal = computed(() => typeof props.total === "number" && props.total >= 0);
const paginationTotal = computed(() => knownTotal.value
  ? props.total!
  : (props.page - 1) * props.pageSize + props.itemCount + (props.itemCount >= props.pageSize ? props.pageSize : 0));
const firstItem = computed(() => props.itemCount > 0 ? (props.page - 1) * props.pageSize + 1 : 0);
const lastItem = computed(() => props.itemCount > 0 ? firstItem.value + props.itemCount - 1 : 0);
const totalPages = computed(() => knownTotal.value ? Math.ceil((props.total || 0) / props.pageSize) : undefined);
</script>
<template>
  <div class="ct-pager">
    <span class="ct-pager-info">{{ knownTotal ? `显示第 ${firstItem} 条 - 第 ${lastItem} 条，共 ${props.total} 条` : `显示第 ${firstItem} 条 - 第 ${lastItem} 条` }}</span>
    <div class="ct-pager-controls">
      <span class="ct-pager-pages">总页数：{{ totalPages ?? "--" }}</span>
      <el-pagination background small :current-page="props.page" :page-size="props.pageSize" :total="paginationTotal" layout="prev, pager, next" @update:current-page="emit('update:page', $event)" />
      <el-select :model-value="props.pageSize" class="ct-pager-size" @update:model-value="size">
        <el-option v-for="n in [20, 50, 100]" :key="n" :label="`每页条数：${n}`" :value="n" />
      </el-select>
    </div>
  </div>
</template>
