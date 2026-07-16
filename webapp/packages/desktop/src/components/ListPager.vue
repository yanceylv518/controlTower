<script setup lang="ts">
import { computed } from "vue";
const props = withDefaults(
  defineProps<{ page: number; pageSize: number; itemCount: number }>(),
  { page: 1, pageSize: 20, itemCount: 0 },
);
const emit = defineEmits<{
  "update:page": [number];
  "update:pageSize": [number];
}>();
const total = computed(
  () =>
    (props.page - 1) * props.pageSize +
    props.itemCount +
    (props.itemCount === props.pageSize ? 1 : 0),
);
function size(value: number) {
  emit("update:pageSize", value);
  emit("update:page", 1);
}
</script>
<template>
  <div class="pager">
    <el-pagination
      :current-page="props.page"
      :page-size="props.pageSize"
      :page-sizes="[20, 50, 100]"
      layout="sizes, prev, pager, next"
      :total="total"
      :disabled="props.itemCount === 0"
      @update:current-page="emit('update:page', $event)"
      @update:page-size="size"
    />
  </div>
</template>
