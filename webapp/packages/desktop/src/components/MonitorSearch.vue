<script setup lang="ts">
import { nextTick, ref, watch } from "vue";
import { Search } from "@element-plus/icons-vue";
import type { AutocompleteFetchSuggestionsCallback, AutocompleteInstance } from "element-plus";
import { filterMonitorOptions, type MonitorSearchOption } from "../utils/monitorSearch";

const props = defineProps<{
  modelValue: string;
  selectedKey: string;
  options: MonitorSearchOption[];
  placeholder: string;
  loading: boolean;
}>();
const emit = defineEmits<{
  "update:modelValue": [value: string];
  "update:selectedKey": [value: string];
  select: [key: string];
}>();
const autocomplete = ref<AutocompleteInstance>();

async function suggestions(query: string, callback: AutocompleteFetchSuggestionsCallback) {
  // 输入事件先清除精确选择，等待父组件同步后再匹配候选。
  await nextTick();
  const matches = filterMonitorOptions(props.options, props.selectedKey ? "" : query);
  callback(matches.length ? matches : [{ key: "", value: query, detail: props.loading ? "监控数据加载中…" : "没有匹配的监控项" }]);
}
function input(value: string) {
  emit("update:selectedKey", "");
  emit("update:modelValue", value);
}
function select(option: Record<string, unknown>) {
  if (typeof option.key !== "string" || !option.key || typeof option.value !== "string") return;
  emit("update:modelValue", option.value);
  emit("update:selectedKey", option.key);
  emit("select", option.key);
}
watch([() => props.options, () => props.loading], () => {
  if (autocomplete.value?.activated) void autocomplete.value.getData(props.modelValue);
});
</script>

<template>
  <el-autocomplete
    ref="autocomplete"
    class="monitor-search"
    :model-value="modelValue"
    :fetch-suggestions="suggestions"
    :prefix-icon="Search"
    :placeholder="placeholder"
    :aria-label="placeholder"
    :debounce="0"
    :trigger-on-focus="true"
    popper-class="monitor-search-dropdown"
    size="small"
    clearable
    @input="input"
    @clear="input('')"
    @select="select"
    @click="autocomplete?.getData(modelValue)"
  >
    <template #default="{ item }">
      <div v-if="item.key" class="monitor-search-option" :title="`${item.value} · ${item.detail}`">
        <span class="monitor-search-name">{{ item.value }}</span>
        <span class="monitor-search-detail">{{ item.detail }}</span>
      </div>
      <span v-else class="monitor-search-detail" aria-disabled="true" role="status">{{ item.detail }}</span>
    </template>
  </el-autocomplete>
</template>

<style scoped>
.monitor-search { min-width: 0; max-width: 100%; }
</style>

<style>
.monitor-search-dropdown { max-width: calc(100vw - 24px); min-width: min(280px, calc(100vw - 24px)); }
.monitor-search-dropdown li { padding: 6px 12px; line-height: 20px; }
.monitor-search-option { display: flex; min-width: 0; flex-direction: column; }
.monitor-search-name, .monitor-search-detail { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.monitor-search-name { color: var(--ct-ink); font-size: 13px; }
.monitor-search-detail { color: var(--ct-ink-3); font-size: 11px; }
</style>
